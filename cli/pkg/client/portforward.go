package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// maxTunnelFrame is the largest data frame that a tunnel sends; the
	// server read limit is 64 KiB.
	maxTunnelFrame = 32 << 10
	// tunnelReadLimit bounds a server frame (the server sends at most 32 KiB).
	tunnelReadLimit = 1 << 20
	// DefaultDrainTimeout is how long a tunnel reads after the local client
	// closed its side.
	DefaultDrainTimeout = 2 * time.Second
	// interruptCloseTimeout bounds the close frame after a cancel.
	interruptCloseTimeout = time.Second
)

// Close reasons in TunnelStats.CloseReason for a normal close.
const (
	TunnelReasonLocalClosed  = "local connection closed"
	TunnelReasonServerClosed = "closed by the server"
	TunnelReasonInterrupted  = "interrupted"
)

// ErrTunnelTimeout reports that the server sent no data, ping or pong within
// the read timeout.
var ErrTunnelTimeout = errors.New("tunnel closed: no response from the server")

// TunnelOptions changes the timings of Tunnel. A zero field takes the
// default. Tests inject short values.
type TunnelOptions struct {
	Keepalive KeepaliveTimings
	// DrainTimeout is how long the tunnel still reads server data after the
	// local client closed its side (default 2 s).
	DrainTimeout time.Duration
}

func (o TunnelOptions) withDefaults() TunnelOptions {
	o.Keepalive = o.Keepalive.withDefaults()
	if o.DrainTimeout <= 0 {
		o.DrainTimeout = DefaultDrainTimeout
	}
	return o
}

// TunnelStats describes a finished tunnel.
type TunnelStats struct {
	// BytesIn counts the bytes from the server to the local connection.
	BytesIn int64
	// BytesOut counts the bytes from the local connection to the server.
	BytesOut int64
	Duration time.Duration
	// CloseCode is the close code of the server, or CloseNormal for a close
	// that the client started; 0 when the connection was lost.
	CloseCode int
	// CloseReason is a short text for the user, for example "server time
	// limit reached" or TunnelReasonLocalClosed.
	CloseReason string
}

// DialPortForward opens the port-forward WebSocket of a stack instance
// (GET /api/v1/stack-instances/:id/port-forward?service=&port=). The route
// accepts only header auth; a refused upgrade gives a *WSHandshakeError with
// the error code of the server.
func (c *Client) DialPortForward(ctx context.Context, instanceID, service string, port int) (*websocket.Conn, error) {
	q := url.Values{}
	q.Set("service", service)
	q.Set("port", strconv.Itoa(port))
	path := fmt.Sprintf("/api/v1/stack-instances/%s/port-forward", url.PathEscape(instanceID)) + "?" + q.Encode()
	ws, err := c.dialWSOpts(ctx, path, c.WarnWriter, wsDialOptions{stream: true, op: "port-forward"})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(tunnelReadLimit)
	return ws, nil
}

// localWriteError is a failed write to the local connection.
type localWriteError struct{ err error }

func (e *localWriteError) Error() string { return "writing to the local connection: " + e.err.Error() }
func (e *localWriteError) Unwrap() error { return e.err }

// localPumpResult is the end of the local-to-server copy.
type localPumpResult struct {
	// wsErr is set when the write to the server failed.
	wsErr error
}

// Tunnel copies bytes between ws (a port-forward WebSocket) and local until
// one side closes, ctx ends or the server stops answering. It closes ws and
// local before it returns and leaves no goroutine behind.
//
// A server close 1000, a local close and a cancelled ctx give a nil error. A
// server close with another code gives a *StreamClosedError (Fatal for 1008);
// a silent server gives ErrTunnelTimeout.
func (c *Client) Tunnel(ctx context.Context, ws *websocket.Conn, local net.Conn, opts TunnelOptions) (TunnelStats, error) {
	opts = opts.withDefaults()
	start := time.Now()
	var in, out atomic.Int64
	stats := func(code int, reason string) TunnelStats {
		return TunnelStats{
			BytesIn: in.Load(), BytesOut: out.Load(), Duration: time.Since(start),
			CloseCode: code, CloseReason: reason,
		}
	}

	extend := setKeepalive(ws, opts.Keepalive)
	readDone := make(chan error, 1)
	writeDone := make(chan localPumpResult, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readDone <- c.serverToLocal(ws, local, extend, &in)
	}()
	go func() {
		defer wg.Done()
		writeDone <- localToServer(ws, local, opts.Keepalive.ReadTimeout, &out)
	}()
	teardown := func() {
		_ = ws.Close()
		_ = local.Close()
		wg.Wait()
	}

	ticker := time.NewTicker(opts.Keepalive.PingInterval)
	defer ticker.Stop()
	var drain <-chan time.Time
	var wsWriteErr error
	localDone := false
	for {
		select {
		case <-ctx.Done():
			_ = sendClose(ws, CloseNormal, interruptCloseTimeout)
			teardown()
			return stats(CloseNormal, TunnelReasonInterrupted), nil

		case <-ticker.C:
			_ = sendPing(ws, opts.Keepalive)

		case r := <-writeDone:
			writeDone = nil
			if r.wsErr != nil {
				// The reader usually holds the close frame that explains it.
				wsWriteErr = r.wsErr
				drain = time.After(time.Second)
				continue
			}
			localDone = true
			_ = sendClose(ws, CloseNormal, opts.Keepalive.WriteTimeout)
			drain = time.After(opts.DrainTimeout)

		case <-drain:
			teardown()
			if wsWriteErr != nil {
				return stats(0, "connection lost"), fmt.Errorf("tunnel connection lost: %w", wsWriteErr)
			}
			return stats(CloseNormal, TunnelReasonLocalClosed), nil

		case err := <-readDone:
			code, reason, tErr := c.tunnelEnd(ctx, err, localDone, opts)
			switch {
			case reason == TunnelReasonServerClosed:
				// Pass the EOF of the server to the local client.
				if cw, ok := local.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				}
			case reason == TunnelReasonLocalClosed && !localDone:
				_ = sendClose(ws, CloseNormal, opts.Keepalive.WriteTimeout)
			}
			teardown()
			return stats(code, reason), tErr
		}
	}
}

// tunnelEnd classifies the end of the server-to-local copy.
func (c *Client) tunnelEnd(ctx context.Context, err error, localDone bool, opts TunnelOptions) (code int, reason string, tErr error) {
	var ce *websocket.CloseError
	var lw *localWriteError
	var ne net.Error
	// gorilla reports a lost connection as close code 1006; no frame came.
	closeFrame := errors.As(err, &ce) && ce.Code != websocket.CloseAbnormalClosure
	switch {
	case ctx.Err() != nil:
		return CloseNormal, TunnelReasonInterrupted, nil
	case closeFrame:
		if ce.Code == CloseNormal || ce.Code == closeNoStatus {
			if localDone {
				return CloseNormal, TunnelReasonLocalClosed, nil
			}
			return CloseNormal, TunnelReasonServerClosed, nil
		}
		reason := sanitizeServerMessage(ce.Text)
		return ce.Code, CloseCodeText(ce.Code, reason, c.APIKey != ""),
			&StreamClosedError{Subject: "tunnel", Code: ce.Code, Reason: reason, APIKey: c.APIKey != ""}
	case errors.As(err, &lw):
		return CloseNormal, TunnelReasonLocalClosed, nil
	case localDone:
		// The server dropped the connection after our close frame.
		return CloseNormal, TunnelReasonLocalClosed, nil
	case errors.As(err, &ne) && ne.Timeout():
		return 0, "no response from the server", fmt.Errorf("%w for %s", ErrTunnelTimeout, opts.Keepalive.ReadTimeout)
	default:
		return 0, "connection lost", fmt.Errorf("tunnel connection lost: %w", err)
	}
}

// serverToLocal copies the binary frames of ws to local. Each read moves the
// read deadline. Text frames are ignored (their content is never logged).
func (c *Client) serverToLocal(ws *websocket.Conn, local net.Conn, extend func(), n *atomic.Int64) error {
	buf := make([]byte, maxTunnelFrame)
	for {
		extend()
		mt, r, err := ws.NextReader()
		if err != nil {
			return err
		}
		if mt != websocket.BinaryMessage {
			c.debugf("port-forward: ignored a text frame from the server\n")
			if _, err := io.Copy(io.Discard, r); err != nil {
				return err
			}
			continue
		}
		for {
			k, rerr := r.Read(buf)
			if k > 0 {
				extend()
				if _, werr := local.Write(buf[:k]); werr != nil {
					return &localWriteError{err: werr}
				}
				n.Add(int64(k))
			}
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
	}
}

// localToServer copies local to ws in binary frames of at most 32 KiB. It is
// the only data writer of ws. It returns at the local EOF or read error, or
// at a failed write to the server.
func localToServer(ws *websocket.Conn, local net.Conn, writeTimeout time.Duration, n *atomic.Int64) localPumpResult {
	buf := make([]byte, maxTunnelFrame)
	for {
		k, err := local.Read(buf)
		if k > 0 {
			_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:k]); werr != nil {
				return localPumpResult{wsErr: werr}
			}
			n.Add(int64(k))
		}
		if err != nil {
			return localPumpResult{}
		}
	}
}
