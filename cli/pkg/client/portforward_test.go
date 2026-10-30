package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pfPath = "/api/v1/stack-instances/42/port-forward"

// pfServer serves the port-forward route with handler after the upgrade.
func pfServer(t *testing.T, handler func(r *http.Request, ws *websocket.Conn)) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pfPath {
			http.NotFound(w, r)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		handler(r, ws)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// echo returns binary frames until the client closes. It records the largest
// frame.
func echo(maxFrame *atomic.Int64) func(*http.Request, *websocket.Conn) {
	return func(_ *http.Request, ws *websocket.Conn) {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if maxFrame != nil && int64(len(data)) > maxFrame.Load() {
				maxFrame.Store(int64(len(data)))
			}
			if err := ws.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
}

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair(t *testing.T) (tunnelSide, clientSide *net.TCPConn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	cs, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	ts := <-accepted
	require.NotNil(t, ts)
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ts.Close()
	})
	return ts.(*net.TCPConn), cs.(*net.TCPConn)
}

type tunnelResult struct {
	stats TunnelStats
	err   error
}

// startTunnel dials the server and runs Tunnel in a goroutine.
func startTunnel(t *testing.T, ctx context.Context, c *Client, opts TunnelOptions) (*net.TCPConn, <-chan tunnelResult) {
	t.Helper()
	ws, err := c.DialPortForward(ctx, "42", "mysql", 3306)
	require.NoError(t, err)
	tunnelSide, clientSide := tcpPair(t)
	done := make(chan tunnelResult, 1)
	go func() {
		st, err := c.Tunnel(ctx, ws, tunnelSide, opts)
		done <- tunnelResult{st, err}
	}()
	return clientSide, done
}

func waitTunnel(t *testing.T, done <-chan tunnelResult) tunnelResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel did not end")
		return tunnelResult{}
	}
}

func TestPortForward_RoundTrip1MiB(t *testing.T) {
	t.Parallel()
	var maxFrame atomic.Int64
	srv := pfServer(t, echo(&maxFrame))
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{})

	payload := make([]byte, 1<<20)
	_, err := rand.Read(payload)
	require.NoError(t, err)
	readErr := make(chan error, 1)
	got := make([]byte, len(payload))
	go func() {
		_, err := io.ReadFull(clientSide, got)
		readErr <- err
	}()
	_, err = clientSide.Write(payload)
	require.NoError(t, err)
	require.NoError(t, <-readErr)
	assert.True(t, bytes.Equal(payload, got), "echoed bytes differ")

	require.NoError(t, clientSide.CloseWrite())
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, int64(1<<20), r.stats.BytesOut)
	assert.Equal(t, int64(1<<20), r.stats.BytesIn)
	assert.Equal(t, CloseNormal, r.stats.CloseCode)
	assert.Equal(t, TunnelReasonLocalClosed, r.stats.CloseReason)
	assert.LessOrEqual(t, maxFrame.Load(), int64(32<<10), "frames must stay below the server read limit")
}

func TestDialPortForward_AuthHeadersAndQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		token      string
		apiKey     string
		wantHeader string
		wantValue  string
	}{
		{"bearer", "jwt-1", "", "Authorization", "Bearer jwt-1"},
		{"api key", "", "sk_test", "X-API-Key", "sk_test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reqs := make(chan *http.Request, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reqs <- r.Clone(context.Background())
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_, _, _ = ws.ReadMessage()
			}))
			defer srv.Close()

			c := New(srv.URL)
			c.Token = tt.token
			c.APIKey = tt.apiKey
			ws, err := c.DialPortForward(context.Background(), "42", "my svc&x=1", 8080)
			require.NoError(t, err)
			_ = ws.Close()

			r := <-reqs
			assert.Equal(t, pfPath, r.URL.Path)
			assert.Equal(t, "my svc&x=1", r.URL.Query().Get("service"))
			assert.Equal(t, "8080", r.URL.Query().Get("port"))
			assert.Empty(t, r.URL.Query().Get("x"), "service must be escaped")
			assert.Empty(t, r.URL.Query().Get("token"))
			assert.Equal(t, tt.wantValue, r.Header.Get(tt.wantHeader))
			assert.Empty(t, r.Header.Get("Origin"))
			assert.True(t, strings.HasPrefix(r.Header.Get("User-Agent"), "stackctl/"))
		})
	}
}

func TestDialPortForward_NoQueryTokenRetryAfter401(t *testing.T) {
	t.Parallel()
	var hits, withToken atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Get("token") != "" {
			withToken.Add(1)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.Token = "jwt-1"
	_, err := c.DialPortForward(context.Background(), "42", "mysql", 3306)
	var hs *WSHandshakeError
	require.ErrorAs(t, err, &hs)
	assert.Equal(t, http.StatusUnauthorized, hs.StatusCode)
	assert.True(t, hs.Fatal())
	assert.Contains(t, err.Error(), "stackctl login")
	assert.Equal(t, int32(1), hits.Load(), "no retry")
	assert.Equal(t, int32(0), withToken.Load(), "the JWT never goes into the URL")
}

func TestDialPortForward_RenewsSessionAfter401(t *testing.T) {
	t.Parallel()
	renewed := makeJWT(t, "alice", time.Now().Add(time.Hour))
	var refreshCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			refreshCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "r2", Path: "/api/v1/auth"})
			writeJSON(w, http.StatusOK, map[string]string{"token": renewed})
		case pfPath:
			if r.Header.Get("Authorization") != "Bearer "+renewed {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
				return
			}
			ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			_, _, _ = ws.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	old := makeJWT(t, "alice-old", time.Now().Add(time.Hour))
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: old, RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour), APIURL: srv.URL})
	ws, err := c.DialPortForward(context.Background(), "42", "mysql", 3306)
	require.NoError(t, err)
	_ = ws.Close()
	assert.Equal(t, int32(1), refreshCalls.Load())
}

func TestDialPortForward_HandshakeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		body      string
		apiKey    bool
		check     func(t *testing.T, hs *WSHandshakeError)
		wantFatal bool
		wantMsg   []string
	}{
		{name: "api key not allowed", status: 403, apiKey: true,
			body:      `{"error":"API keys cannot open this stream","code":"api_key_not_allowed"}`,
			wantFatal: true, wantMsg: []string{"HTTP 403", "API keys are not allowed for this command", "stackctl login"}},
		{name: "forbidden", status: 403,
			body:      `{"error":"You do not have permission","code":"forbidden"}`,
			wantFatal: true, wantMsg: []string{"port-forward refused by the server (HTTP 403): You do not have permission"}},
		{name: "feature disabled", status: 404,
			body: `{"error":"This feature is disabled","code":"feature_disabled"}`,
			check: func(t *testing.T, hs *WSHandshakeError) {
				assert.True(t, hs.FeatureDisabled())
				assert.False(t, hs.NoRoute())
			},
			wantFatal: true, wantMsg: []string{"This feature is disabled", "turned off"}},
		{name: "service not found", status: 404,
			body:      `{"error":"Service not found","code":"service_not_found"}`,
			wantFatal: true, wantMsg: []string{"Service not found", "service port"}},
		{name: "plain 404", status: 404, body: "404 page not found",
			check:     func(t *testing.T, hs *WSHandshakeError) { assert.True(t, hs.NoRoute()) },
			wantFatal: true, wantMsg: []string{"no port-forward route", "upgrade k8s-stack-manager"}},
		{name: "not deployed", status: 409,
			body:      `{"error":"Instance is not deployed","code":"instance_not_deployed"}`,
			wantFatal: true, wantMsg: []string{"Deploy the stack first"}},
		{name: "tunnel limit", status: 429,
			body:      `{"error":"Too many tunnels","code":"tunnel_limit"}`,
			wantFatal: false, wantMsg: []string{"HTTP 429", "Too many tunnels"}},
		{name: "no ready pod", status: 503,
			body:      `{"error":"No ready pod","code":"no_ready_pod"}`,
			wantFatal: false, wantMsg: []string{"HTTP 503", "No ready pod"}},
		{name: "dial failed", status: 502,
			body:      `{"error":"Dial to the pod failed","code":"dial_failed"}`,
			wantFatal: false, wantMsg: []string{"HTTP 502"}},
		{name: "200 without upgrade", status: 200, body: "<html>ok</html>",
			check:     func(t *testing.T, hs *WSHandshakeError) { assert.True(t, hs.UpgradeDropped()) },
			wantFatal: true, wantMsg: []string{"HTTP 200", "proxy"}},
		{name: "upgrade required", status: 400,
			body:      `{"error":"WebSocket upgrade required","code":"upgrade_required"}`,
			check:     func(t *testing.T, hs *WSHandshakeError) { assert.True(t, hs.UpgradeDropped()) },
			wantFatal: true, wantMsg: []string{"proxy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			c := New(srv.URL)
			if tt.apiKey {
				c.APIKey = "sk_test"
			}
			_, err := c.DialPortForward(context.Background(), "42", "mysql", 3306)
			var hs *WSHandshakeError
			require.ErrorAs(t, err, &hs)
			assert.Equal(t, tt.status, hs.StatusCode)
			assert.Equal(t, tt.wantFatal, hs.Fatal())
			for _, m := range tt.wantMsg {
				assert.Contains(t, err.Error(), m)
			}
			assert.NotContains(t, err.Error(), "sk_test")
			if tt.check != nil {
				tt.check(t, hs)
			}
		})
	}
}

func TestTunnel_ServerCloseCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		code      int
		reason    string
		wantMsg   string
		wantFatal bool
	}{
		{"max duration", CloseMaxDuration, "maximum duration reached", "tunnel closed: server time limit reached", false},
		{"idle timeout", CloseIdleTimeout, "idle timeout", "tunnel closed: idle timeout", false},
		{"pod stream error", ClosePodStreamError, "connection refused", "tunnel closed: pod stream error: connection refused", false},
		{"server shutdown", CloseGoingAway, "server shutdown", "tunnel closed: the server is shutting down", false},
		{"session revoked", ClosePolicy, "session revoked", "tunnel closed: access revoked (token, session or permissions); run 'stackctl login'", true},
		{"token expired", ClosePolicy, "token expired", "run 'stackctl login'", true},
		{"stack stopped or access lost", ClosePolicy, "access revoked", "tunnel closed: access revoked: the stack was stopped, deleted or cleaned, or your access to it changed", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
				mt, data, err := ws.ReadMessage()
				if err != nil {
					return
				}
				_ = ws.WriteMessage(mt, data)
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(tt.code, tt.reason), time.Now().Add(time.Second))
				_, _, _ = ws.ReadMessage()
			})
			clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{})
			_, err := clientSide.Write([]byte("hello"))
			require.NoError(t, err)
			got, _ := io.ReadAll(clientSide)
			assert.Equal(t, "hello", string(got))

			r := waitTunnel(t, done)
			var sc *StreamClosedError
			require.ErrorAs(t, r.err, &sc)
			assert.Contains(t, r.err.Error(), tt.wantMsg)
			assert.Equal(t, tt.wantFatal, sc.Fatal())
			assert.Equal(t, tt.code, r.stats.CloseCode)
			assert.Equal(t, int64(5), r.stats.BytesIn)
			assert.Equal(t, int64(5), r.stats.BytesOut)
			assert.Positive(t, r.stats.Duration)
			var wsClosed *WSClosedError
			assert.Equal(t, tt.code == ClosePolicy, errors.As(r.err, &wsClosed), "1008 also gives a WSClosedError")
		})
	}
}

func TestTunnel_ServerNormalCloseEndsLocalStream(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("bye"))
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(CloseNormal, ""), time.Now().Add(time.Second))
		_, _, _ = ws.ReadMessage()
	})
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{})
	got, err := io.ReadAll(clientSide)
	require.NoError(t, err, "the local client gets an EOF")
	assert.Equal(t, "bye", string(got))
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, TunnelReasonServerClosed, r.stats.CloseReason)
}

// podstreamLike serves like the server: the default close handler echoes
// the close of the client, and nothing is sent after it. send runs once
// localEOF is closed, unless the client closed first.
func podstreamLike(localEOF <-chan struct{}, gotClose chan<- int, send func(ws *websocket.Conn, closed <-chan struct{})) func(*http.Request, *websocket.Conn) {
	return func(_ *http.Request, ws *websocket.Conn) {
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			for {
				_, _, err := ws.ReadMessage()
				if err != nil {
					var ce *websocket.CloseError
					if errors.As(err, &ce) {
						gotClose <- ce.Code
					}
					return
				}
			}
		}()
		select {
		case <-localEOF:
		case <-closed:
			return
		}
		send(ws, closed)
		<-closed
	}
}

func TestTunnel_LocalEOFSendsCloseAndDrains(t *testing.T) {
	t.Parallel()
	localEOF := make(chan struct{})
	gotClose := make(chan int, 1)
	srv := pfServer(t, podstreamLike(localEOF, gotClose, func(ws *websocket.Conn, closed <-chan struct{}) {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-closed:
			return
		default:
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("late"))
	}))
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{DrainTimeout: 500 * time.Millisecond})
	_, err := clientSide.Write([]byte("query"))
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, clientSide.CloseWrite())
	close(localEOF)

	got, _ := io.ReadAll(clientSide)
	assert.Equal(t, "late", string(got), "data after the local EOF still reaches the local side")
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	select {
	case code := <-gotClose:
		assert.Equal(t, CloseNormal, code)
	case <-time.After(2 * time.Second):
		t.Fatal("the server got no close frame")
	}
	assert.Equal(t, TunnelReasonLocalClosed, r.stats.CloseReason)
	assert.Equal(t, int64(4), r.stats.BytesIn)
	assert.Equal(t, int64(5), r.stats.BytesOut)
}

func TestTunnel_DrainWaitRestartsOnData(t *testing.T) {
	t.Parallel()
	localEOF := make(chan struct{})
	gotClose := make(chan int, 1)
	srv := pfServer(t, podstreamLike(localEOF, gotClose, func(ws *websocket.Conn, closed <-chan struct{}) {
		for i := 0; i < 6; i++ {
			select {
			case <-closed:
				return
			case <-time.After(100 * time.Millisecond):
			}
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte("x"))
		}
	}))
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{DrainTimeout: 250 * time.Millisecond})
	require.NoError(t, clientSide.CloseWrite())
	close(localEOF)

	got, _ := io.ReadAll(clientSide)
	assert.Equal(t, "xxxxxx", string(got), "600 ms of data with a 250 ms drain timeout")
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, CloseNormal, <-gotClose)
}

func TestTunnel_ServerCloseDuringDrainEndsAtOnce(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("reply"))
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(CloseNormal, ""), time.Now().Add(time.Second))
		_, _, _ = ws.ReadMessage()
	})
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{DrainTimeout: 10 * time.Second})
	start := time.Now()
	_, err := clientSide.Write([]byte("q"))
	require.NoError(t, err)
	require.NoError(t, clientSide.CloseWrite())
	got, _ := io.ReadAll(clientSide)
	assert.Equal(t, "reply", string(got))
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Less(t, time.Since(start), 3*time.Second, "no wait for the drain timeout")
}

func TestTunnel_StalledLocalReaderEndsTunnel(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		chunk := make([]byte, 32<<10)
		for {
			_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := ws.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return
			}
		}
	})
	opts := TunnelOptions{Keepalive: KeepaliveTimings{ReadTimeout: 300 * time.Millisecond}}
	_, done := startTunnel(t, context.Background(), New(srv.URL), opts)
	// The local client never reads.
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, TunnelReasonLocalStalled, r.stats.CloseReason)
	assert.Positive(t, r.stats.BytesIn)
}

func TestTunnel_CancelWhileDataWriterBlocked(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		// Read nothing, so the data writer of the client blocks.
		<-release
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := TunnelOptions{Keepalive: KeepaliveTimings{PingInterval: 10 * time.Millisecond}}
	clientSide, done := startTunnel(t, ctx, New(srv.URL), opts)
	go func() {
		buf := make([]byte, 256<<10)
		for {
			if _, err := clientSide.Write(buf); err != nil {
				return
			}
		}
	}()
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	cancel()
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, TunnelReasonInterrupted, r.stats.CloseReason)
	assert.Less(t, time.Since(start), 2*time.Second, "a blocked ping must not delay the cancel")
}

func TestTunnel_DrainTimeout(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		ws.SetCloseHandler(func(int, string) error { return nil })
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				break
			}
		}
		// Never answer the close: hold the connection until the client drops it.
		_, _ = io.Copy(io.Discard, ws.UnderlyingConn())
	})
	clientSide, done := startTunnel(t, context.Background(), New(srv.URL), TunnelOptions{DrainTimeout: 100 * time.Millisecond})
	require.NoError(t, clientSide.CloseWrite())
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, TunnelReasonLocalClosed, r.stats.CloseReason)
}

func TestTunnel_SilentServerTripsReadDeadline(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		// No pong replies and no data.
		ws.SetPingHandler(func(string) error { return nil })
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	opts := TunnelOptions{Keepalive: KeepaliveTimings{PingInterval: 50 * time.Millisecond, ReadTimeout: 200 * time.Millisecond}}
	_, done := startTunnel(t, context.Background(), New(srv.URL), opts)
	r := waitTunnel(t, done)
	require.ErrorIs(t, r.err, ErrTunnelTimeout)
	assert.Contains(t, r.err.Error(), "no response from the server")
}

func TestTunnel_ServerPingsKeepAliveUntilCancel(t *testing.T) {
	t.Parallel()
	gotClose := make(chan int, 1)
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		ws.SetCloseHandler(func(code int, _ string) error {
			gotClose <- code
			return nil
		})
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			tk := time.NewTicker(40 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					_ = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
				}
			}
		}()
		// The client pings too; the default handler answers them.
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	opts := TunnelOptions{Keepalive: KeepaliveTimings{PingInterval: time.Hour, ReadTimeout: 150 * time.Millisecond}}
	_, done := startTunnel(t, ctx, New(srv.URL), opts)
	select {
	case r := <-done:
		t.Fatalf("tunnel ended early: %v", r.err)
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, TunnelReasonInterrupted, r.stats.CloseReason)
	select {
	case code := <-gotClose:
		assert.Equal(t, CloseNormal, code)
	case <-time.After(2 * time.Second):
		t.Fatal("the server got no close frame")
	}
}

func TestTunnel_TextFramesIgnored(t *testing.T) {
	t.Parallel()
	srv := pfServer(t, func(_ *http.Request, ws *websocket.Conn) {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"secret":"x"}`))
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("data"))
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(CloseNormal, ""), time.Now().Add(time.Second))
		_, _, _ = ws.ReadMessage()
	})
	c := New(srv.URL)
	var debug bytes.Buffer
	c.Debug = true
	c.DebugWriter = &debug
	clientSide, done := startTunnel(t, context.Background(), c, TunnelOptions{})
	got, _ := io.ReadAll(clientSide)
	assert.Equal(t, "data", string(got))
	r := waitTunnel(t, done)
	require.NoError(t, r.err)
	assert.Contains(t, debug.String(), "ignored a text frame")
	assert.NotContains(t, debug.String(), "secret", "--debug never logs frame content")
}

func TestCloseCodeText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "access revoked (API key or permissions); check the key with 'stackctl apikey list'", CloseCodeText(ClosePolicy, "", true))
	assert.Equal(t, "internal server error", CloseCodeText(CloseInternalError, "", false))
	assert.Equal(t, "closed by the server (code 4999): odd", CloseCodeText(4999, "odd", false))
	assert.Equal(t, "pod stream error", CloseCodeText(ClosePodStreamError, "", false))

	tests := []struct {
		name    string
		reason  string
		apiKey  bool
		want    []string
		notWant string
	}{
		{"session revoked", "session revoked", false, []string{"stackctl login"}, "stopped"},
		{"token expired", "token expired", false, []string{"stackctl login"}, "stopped"},
		{"session revoked with api key", "session revoked", true, []string{"stackctl apikey list"}, "stopped"},
		{"stack stopped or access lost", "access revoked", false, []string{"stopped, deleted or cleaned", "access to it changed"}, "stackctl login"},
		{"stack stopped with api key", "access revoked", true, []string{"stopped, deleted or cleaned"}, "stackctl login"},
		{"api key expired", "api key expired", true, []string{"stackctl apikey create"}, "stackctl login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CloseCodeText(ClosePolicy, tt.reason, tt.apiKey)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			assert.NotContains(t, got, tt.notWant)
			assert.True(t, (&StreamClosedError{Code: ClosePolicy, Reason: tt.reason}).Fatal(), "1008 stays fatal")
		})
	}
}

func TestDialPortForward_Parallel401sRenewOnce(t *testing.T) {
	t.Parallel()
	renewed := makeJWT(t, "alice", time.Now().Add(time.Hour))
	var refreshCalls, stale atomic.Int32
	bothStale, renewedSeen := make(chan struct{}), make(chan struct{})
	var renewedOnce sync.Once
	wait := func(ch <-chan struct{}) {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			refreshCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "r2", Path: "/api/v1/auth"})
			writeJSON(w, http.StatusOK, map[string]string{"token": renewed})
		case pfPath:
			if r.Header.Get("Authorization") != "Bearer "+renewed {
				// Both dials hold the old token. The second 401 comes after
				// the first dial has renewed and dialed again.
				n := stale.Add(1)
				if n == 2 {
					close(bothStale)
				}
				wait(bothStale)
				if n == 2 {
					wait(renewedSeen)
				}
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
				return
			}
			renewedOnce.Do(func() { close(renewedSeen) })
			ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			_, _, _ = ws.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	old := makeJWT(t, "alice-old", time.Now().Add(time.Hour))
	c, _ := newSessionClient(t, srv.URL, &config.StoredToken{Token: old, RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour), APIURL: srv.URL})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ws, err := c.DialPortForward(context.Background(), "42", "mysql", 3306)
			if err == nil {
				_ = ws.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	assert.Equal(t, int32(2), stale.Load())
	assert.Equal(t, int32(1), refreshCalls.Load(), "the second 401 reuses the renewed token")
}
