package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

// Close reasons that k8s-stack-manager sends with close code 1008 (policy
// violation) when it ends a WebSocket connection.
const (
	// CloseReasonSessionRevoked: the session of the user ended (delete,
	// disable, password reset, role change, logout or logout-all).
	CloseReasonSessionRevoked = "session revoked"
	// CloseReasonTokenExpired: the access token of the connection expired.
	CloseReasonTokenExpired = "token expired"
)

// WSClosedError reports that the server closed the WebSocket connection
// with close code 1008 (policy violation). Reason is the close reason of the
// server, for example CloseReasonSessionRevoked or CloseReasonTokenExpired.
type WSClosedError struct {
	Code   int
	Reason string
}

// Error returns a message for the user, for example "Connection closed by
// the server: session revoked. Log in again."
func (e *WSClosedError) Error() string {
	if e.TokenExpired() {
		// The session can still be valid (only the access token expired),
		// so a new command can renew it. An SSO token cannot be renewed.
		return "Connection closed by the server: token expired. Run the command again. If it fails, run 'stackctl login'."
	}
	reason := sanitizeServerMessage(e.Reason)
	if reason == "" {
		reason = "policy violation"
	}
	return fmt.Sprintf("Connection closed by the server: %s. Log in again.", reason)
}

// TokenExpired reports whether the server closed the connection because the
// access token expired. A renewed token can open a new connection.
func (e *WSClosedError) TokenExpired() bool {
	return e.Code == websocket.ClosePolicyViolation && e.Reason == CloseReasonTokenExpired
}

// SessionRevoked reports whether the server closed the connection because
// the session ended. Only a new login helps.
func (e *WSClosedError) SessionRevoked() bool {
	return e.Code == websocket.ClosePolicyViolation && e.Reason == CloseReasonSessionRevoked
}

// policyCloseError returns a *WSClosedError when err is a close frame with
// code 1008 (policy violation), else nil.
func policyCloseError(err error) *WSClosedError {
	var ce *websocket.CloseError
	if errors.As(err, &ce) && ce.Code == websocket.ClosePolicyViolation {
		return &WSClosedError{Code: ce.Code, Reason: ce.Text}
	}
	return nil
}

// terminalStatuses are the stack instance statuses that end an operation
// (deploy, stop, clean, rollback). k8s-stack-manager sets "running" after a
// deploy, "stopped" after a stop, "draft" after a clean, "error" after a
// failure and "partial" when some charts deployed and others failed.
// "failed" is not a server status; stackctl accepts it for compatibility.
var terminalStatuses = map[string]bool{
	"running": true,
	"stopped": true,
	"draft":   true,
	"error":   true,
	"partial": true,
	"failed":  true,
}

// failedStatuses are the terminal statuses of a failed operation.
var failedStatuses = map[string]bool{
	"error":   true,
	"partial": true,
	"failed":  true,
}

// IsTerminalStatus reports whether status ends an operation on a stack
// instance (running, stopped, draft, error, partial, failed).
func IsTerminalStatus(status string) bool {
	return terminalStatuses[status]
}

// IsFailedStatus reports whether status is a terminal status of a failed
// operation (error, partial, failed).
func IsFailedStatus(status string) bool {
	return failedStatuses[status]
}

// StreamOptions changes the behaviour of StreamDeploymentLogsWithOptions.
type StreamOptions struct {
	// AfterSubscribe runs after the connection is open and the subscribe
	// message is sent, before the first read. A non-nil result ends the
	// stream with that result. Use it to read the instance status after a
	// reconnect, so a terminal status during the reconnect gap counts.
	AfterSubscribe func(ctx context.Context) (*types.StreamResult, error)
}

// StreamDeploymentLogs connects to the backend WebSocket and streams deployment
// log lines for the given instance to w. It blocks until a terminal status is
// received, the context is cancelled, or the connection drops.
func (c *Client) StreamDeploymentLogs(ctx context.Context, instanceID string, w io.Writer, warnWriter io.Writer) (*types.StreamResult, error) {
	return c.StreamDeploymentLogsWithOptions(ctx, instanceID, w, warnWriter, StreamOptions{})
}

// StreamDeploymentLogsWithOptions is StreamDeploymentLogs with options.
func (c *Client) StreamDeploymentLogsWithOptions(ctx context.Context, instanceID string, w io.Writer, warnWriter io.Writer, opts StreamOptions) (*types.StreamResult, error) {
	conn, err := c.dialWS(ctx, "/ws", warnWriter)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Subscribe to this instance so we receive deployment.log events
	// (the hub only sends log lines to subscribed clients).
	sub, _ := json.Marshal(map[string]interface{}{
		"type":    "subscribe",
		"payload": map[string]string{"instance_id": instanceID},
	})
	if err := conn.WriteMessage(websocket.TextMessage, sub); err != nil {
		return nil, fmt.Errorf("subscribing to instance: %w", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Send a close frame AND tear down the TCP connection. Use
			// WriteControl with an explicit deadline rather than the
			// blocking WriteMessage — a stalled connection could pin the
			// goroutine on WriteMessage forever, preventing conn.Close
			// from ever running. With WriteControl + 1s deadline + Close,
			// ReadMessage in the read loop unwinds within ctx-cancel
			// latency regardless of network state. Mirrors WatchEvents.
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(time.Second))
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	if opts.AfterSubscribe != nil {
		result, err := opts.AfterSubscribe(ctx)
		if err != nil || result != nil {
			return result, err
		}
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return &types.StreamResult{Status: "unknown"}, nil
			}
			if closeErr := policyCloseError(err); closeErr != nil {
				return nil, closeErr
			}
			return nil, fmt.Errorf("reading WebSocket message: %w", err)
		}

		var msg types.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			if warnWriter != nil {
				fmt.Fprintf(warnWriter, "Warning: skipping malformed WebSocket message: %v\n", err)
			}
			continue
		}

		switch msg.Type {
		case "deployment.log":
			var logLine types.WSDeploymentLog
			if err := json.Unmarshal(msg.Data, &logLine); err != nil {
				if warnWriter != nil {
					fmt.Fprintf(warnWriter, "Warning: skipping malformed log payload: %v\n", err)
				}
				continue
			}
			if logLine.InstanceID != instanceID {
				continue
			}
			fmt.Fprintln(w, logLine.Line)

		case "deployment.status":
			var status types.WSDeploymentStatus
			if err := json.Unmarshal(msg.Data, &status); err != nil {
				if warnWriter != nil {
					fmt.Fprintf(warnWriter, "Warning: skipping malformed status payload: %v\n", err)
				}
				continue
			}
			if status.InstanceID != instanceID {
				continue
			}
			if terminalStatuses[status.Status] {
				return &types.StreamResult{
					Status:       status.Status,
					ErrorMessage: status.ErrorMessage,
				}, nil
			}
		}
	}
}

// dialWS opens a WebSocket to the given path on the client's BaseURL with
// the standard stackctl auth chain:
//
//   - X-API-Key when c.APIKey is set;
//   - Authorization: Bearer when c.Token is set;
//   - On HTTP 401 from the header path AND c.Token != "", a single retry
//     with the JWT as a URL-escaped ?token= query param (the backend
//     handler reads from either location — see handlers/websocket.go).
//
// Subprotocol auth is NOT attempted; the backend doesn't support it yet
// (tracked as k8s-stack-manager K4).
//
// Custom HTTP transports have their TLS config copied to the dialer so
// --insecure works for the WS upgrade. A non-*http.Transport custom
// transport triggers a warning via warnWriter (when non-nil) because we
// can't extract TLS settings safely.
func (c *Client) dialWS(ctx context.Context, path string, warnWriter io.Writer) (*websocket.Conn, error) {
	wsURL, err := c.websocketURL(path)
	if err != nil {
		return nil, err
	}

	// Renew a login session first: the upgrade request has no 401 renewal.
	token, err := c.EnsureFreshToken()
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	if c.APIKey != "" {
		header.Set("X-API-Key", c.APIKey)
	} else if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}

	dialer := websocket.DefaultDialer
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		if t, ok := c.HTTPClient.Transport.(*http.Transport); ok {
			d := *websocket.DefaultDialer
			d.TLSClientConfig = t.TLSClientConfig
			dialer = &d
		} else if warnWriter != nil {
			fmt.Fprintln(warnWriter, "Warning: custom HTTP transport detected; WebSocket TLS config may not be applied")
		}
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err == nil {
		return conn, nil
	}
	// 401 → fall back to ?token= query param when a JWT is configured.
	// The retry is gated on c.Token because an API-key-only caller has no
	// token to fall back to. When BOTH APIKey and Token are set, this
	// transparently switches to the JWT path (documented precedence).
	if resp != nil && resp.StatusCode == http.StatusUnauthorized && token != "" {
		fallbackURL := appendQueryToken(wsURL, token)
		conn2, _, err2 := dialer.DialContext(ctx, fallbackURL, nil)
		if err2 != nil {
			return nil, fmt.Errorf("connecting to WebSocket (header auth failed with 401, query-param fallback also failed): %w", err2)
		}
		return conn2, nil
	}
	return nil, fmt.Errorf("connecting to WebSocket: %w", err)
}

func (c *Client) websocketURL(path string) (string, error) {
	base := c.BaseURL
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return "", fmt.Errorf("unsupported URL scheme in %q", c.BaseURL)
	}
	return strings.TrimRight(base, "/") + path, nil
}

// WatchFilter narrows the event stream returned by WatchEvents. All fields
// are optional; an empty filter passes every "deployment.status" event the
// backend broadcasts on /ws.
//
// Filtering is performed client-side because the backend hub broadcasts
// status events to every connected client (per
// backend/internal/deployer/broadcast.go broadcastStatus → hub.Broadcast).
// The hub's subscribe protocol only targets per-instance log streams.
type WatchFilter struct {
	// InstanceIDs limits events to the listed instance UUIDs. Nil/empty
	// matches every instance.
	InstanceIDs []string
	// Status limits events to a single status string (e.g. "running",
	// "failed"). Empty matches every status.
	Status string
}

// matches returns true when the supplied status payload satisfies the filter.
func (f WatchFilter) matches(s types.WSDeploymentStatus) bool {
	if f.Status != "" && s.Status != f.Status {
		return false
	}
	if len(f.InstanceIDs) > 0 {
		for _, id := range f.InstanceIDs {
			if id == s.InstanceID {
				return true
			}
		}
		return false
	}
	return true
}

// WatchEvents subscribes to the backend /ws stream and pushes each
// "deployment.status" event that matches filter onto the returned channel.
//
// Lifecycle:
//   - The returned channel is closed when ctx is cancelled, when the
//     connection drops, or when the read loop exits. Receivers should
//     range over the channel until it closes. Use Watch to learn why the
//     stream ended (for example a server close with code 1008).
//   - Spawns ONE background goroutine that reads from the WS, decodes
//     payloads, applies the filter, and forwards matches. Goroutine exits
//     when ctx is Done or the read loop returns; goleak verified.
//   - Caller is responsible for cancelling ctx when done; the function
//     does not buffer beyond the returned channel's capacity (8).
//
// Auth:
//   - X-API-Key is sent when c.APIKey is set (the backend WS handler
//     currently ignores it; documented limitation).
//   - Authorization: Bearer <token> is sent when c.Token is set.
//   - If the upgrade is rejected with HTTP 401 AND c.Token is set, the
//     dialer retries with the JWT as a ?token= query param (the backend
//     reads from either location). The retry is gated on c.Token because
//     an API-key-only caller has no token to fall back to.
//   - When BOTH c.APIKey and c.Token are set, a 401 on the API-key path
//     transparently falls back to the JWT query-param path. This is
//     intentional — operators that configure both expect the JWT to be
//     the operational credential and the API key the headless one.
//   - The Sec-WebSocket-Protocol subprotocol fallback documented in
//     stackctl#75 is NOT yet supported by the backend (tracked as
//     k8s-stack-manager K4); the dialer does not attempt it.
//
// Non-status messages (deployment.log) are silently dropped so this can
// be safely connected to the same /ws endpoint as StreamDeploymentLogs
// without cross-talk.
func (c *Client) WatchEvents(ctx context.Context, filter WatchFilter) (<-chan types.WatchEvent, error) {
	stream, err := c.Watch(ctx, filter)
	if err != nil {
		return nil, err
	}
	return stream.Events, nil
}

// WatchStream is an open event stream of Watch.
type WatchStream struct {
	// Events receives the matching events. It is closed when the stream
	// ends.
	Events <-chan types.WatchEvent

	// err is written before Events is closed, so a read after the close
	// is safe.
	err error
}

// Err returns why the stream ended: a *WSClosedError when the server closed
// the connection with close code 1008 (session revoked, token expired),
// else nil (context cancelled, normal close, connection lost). Call it only
// after Events is closed.
func (s *WatchStream) Err() error {
	return s.err
}

// Watch is WatchEvents with access to the end reason of the stream (see
// WatchStream.Err).
func (c *Client) Watch(ctx context.Context, filter WatchFilter) (*WatchStream, error) {
	conn, err := c.dialWS(ctx, "/ws", nil)
	if err != nil {
		return nil, err
	}

	// Buffer of 8 is roughly 2s of backpressure tolerance at the backend's
	// typical broadcast rate (a few status events per deploy across multiple
	// instances). Bumping this only matters if the receiver is slower than
	// the broadcaster — for an interactive CLI that's unlikely.
	out := make(chan types.WatchEvent, 8)
	stream := &WatchStream{Events: out}

	// `done` is closed by the outer goroutine's defer (line 253) — it tells
	// the watchdog goroutine to exit when the read loop returns on its own
	// (e.g. the server hung up). Without it, a server-side close while ctx
	// is still alive would leave the watchdog parked on `<-ctx.Done()`
	// forever.
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(out)
		defer conn.Close()

		// Watchdog: closes the connection when ctx is cancelled so the
		// blocking ReadMessage below returns and the read loop exits;
		// returns on its own when `done` closes so a server-initiated
		// hang-up doesn't leak this goroutine. WriteControl with a write
		// deadline is used instead of WriteMessage so a stalled TCP
		// connection can't pin the goroutine forever and block conn.Close.
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second))
				_ = conn.Close()
			case <-done:
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() == nil {
					if closeErr := policyCloseError(err); closeErr != nil {
						stream.err = closeErr
					}
				}
				return
			}
			var msg types.WSMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}
			if msg.Type != "deployment.status" {
				continue
			}
			var status types.WSDeploymentStatus
			if err := json.Unmarshal(msg.Data, &status); err != nil {
				continue
			}
			if !filter.matches(status) {
				continue
			}
			event := types.WatchEvent{
				Type:         msg.Type,
				InstanceID:   status.InstanceID,
				Status:       status.Status,
				LogID:        status.LogID,
				ErrorMessage: status.ErrorMessage,
				Timestamp:    time.Now().UTC(),
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return stream, nil
}

// appendQueryToken returns a URL with ?token=<jwt> appended (or merged
// into an existing query string). The token is URL-escaped to survive
// future token formats containing characters that would otherwise be
// interpreted as query delimiters (`+`, `=`, `&`, `?`, whitespace).
// Extracted so tests can verify the fallback URL shape without spinning
// up a real connection.
func appendQueryToken(rawURL, token string) string {
	escaped := url.QueryEscape(token)
	if strings.Contains(rawURL, "?") {
		return rawURL + "&token=" + escaped
	}
	return rawURL + "?token=" + escaped
}
