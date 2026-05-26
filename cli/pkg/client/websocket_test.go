package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func wsServer(t *testing.T, handler func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		handler(conn)
	}))
}

func writeWSMessage(t *testing.T, conn *websocket.Conn, msgType string, data interface{}) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	msg := types.WSMessage{Type: msgType, Data: raw}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, b))
}

// readSubscribe reads the first inbound message and asserts it is a subscribe
// for the expected instance ID.
func readSubscribe(t *testing.T, conn *websocket.Conn, expectedID string) {
	t.Helper()
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(msg, &envelope))
	assert.Equal(t, "subscribe", envelope.Type)
	var payload struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.Unmarshal(envelope.Payload, &payload))
	assert.Equal(t, expectedID, payload.InstanceID)
}

func TestStreamDeploymentLogs_Success(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Installing chart...",
		})
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Chart installed.",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)

	assert.Equal(t, "running", result.Status)
	assert.Contains(t, buf.String(), "Installing chart...")
	assert.Contains(t, buf.String(), "Chart installed.")
}

func TestStreamDeploymentLogs_ErrorStatus(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Error: timeout",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "error", LogID: "log-1", ErrorMessage: "helm install timed out",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)

	assert.Equal(t, "error", result.Status)
	assert.Equal(t, "helm install timed out", result.ErrorMessage)
	assert.Contains(t, buf.String(), "Error: timeout")
}

func TestStreamDeploymentLogs_FiltersOtherInstances(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "99", LogID: "log-other", Line: "Should be ignored",
		})
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "My line",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "99", Status: "running", LogID: "log-other",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "stopped", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)

	assert.Equal(t, "stopped", result.Status)
	assert.Equal(t, "My line\n", buf.String())
}

func TestStreamDeploymentLogs_ContextCancelled(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Starting...",
		})
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := c.StreamDeploymentLogs(ctx, "42", &buf, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStreamDeploymentLogs_AuthHeaders(t *testing.T) {
	t.Parallel()
	var capturedHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "my-jwt-token"
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-jwt-token", capturedHeader.Get("Authorization"))
}

func TestStreamDeploymentLogs_APIKeyAuth(t *testing.T) {
	t.Parallel()
	var capturedHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	c.APIKey = "sk_test_123"
	c.Token = "should-be-ignored"
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "sk_test_123", capturedHeader.Get("X-API-Key"))
	assert.Empty(t, capturedHeader.Get("Authorization"))
}

func TestStreamDeploymentLogs_SkipsMalformedMessages(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		conn.WriteMessage(websocket.TextMessage, []byte(`not json`))
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"deployment.log","payload":"not-an-object"}`))
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Valid line",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Equal(t, "Valid line\n", buf.String())
}

func TestStreamDeploymentLogs_DraftTerminal(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "draft", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "draft", result.Status)
}

func TestStreamDeploymentLogs_NonTerminalStatusIgnored(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "deploying", LogID: "log-1",
		})
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "Still going...",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Contains(t, buf.String(), "Still going...")
}

// TestStreamDeploymentLogs_QueryParamFallbackOn401 locks in K4 auth-chain
// parity with WatchEvents: when the backend rejects the Authorization
// header with HTTP 401, the dialer transparently retries with the JWT as
// a URL-escaped ?token= query param.
func TestStreamDeploymentLogs_QueryParamFallbackOn401(t *testing.T) {
	t.Parallel()
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First attempt: header-only → reject.
		if r.URL.Query().Get("token") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotToken = r.URL.Query().Get("token")
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	}))
	defer server.Close()

	c := New(server.URL)
	c.Token = "tok-logs-fallback"
	var buf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Equal(t, "tok-logs-fallback", gotToken, "401 on header path must trigger ?token= retry")
}

// TestStreamDeploymentLogs_QueryParamFallbackDisabledForAPIKey asserts the
// retry is gated on c.Token. API-key-only callers must see a clean error
// rather than a misleading retry that would never carry their credential.
func TestStreamDeploymentLogs_QueryParamFallbackDisabledForAPIKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c := New(server.URL)
	c.APIKey = "key-only"
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.Error(t, err)
}

// TestStreamDeploymentLogs_GoleakOnTerminalClose is a regression test for
// the "logs --follow polish" acceptance: the stream must close cleanly
// when the server reports a terminal status, with no leaked goroutines.
// The package-level TestMain runs goleak.VerifyTestMain at exit and
// would surface any leak from this test path; explicit parallel test is
// here to exercise the path with no ctx cancellation from the caller.
func TestStreamDeploymentLogs_GoleakOnTerminalClose(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{
			InstanceID: "42", LogID: "log-1", Line: "starting",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer
	// Use a never-cancelled context so the goroutine cleanup path under
	// test is the terminal-status one (not the ctx-cancel one). Any leak
	// here surfaces via TestMain's goleak.VerifyTestMain.
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
}

func TestStreamDeploymentLogs_ConnectionError(t *testing.T) {
	t.Parallel()
	c := New("http://localhost:1")
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to WebSocket")
}

func TestStreamDeploymentLogs_BadScheme(t *testing.T) {
	t.Parallel()
	c := &Client{BaseURL: "ftp://example.com"}
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported URL scheme")
}

func TestWebsocketURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
		wantErr bool
	}{
		{"http", "http://localhost:8081", "/ws", "ws://localhost:8081/ws", false},
		{"https", "https://api.example.com", "/ws", "wss://api.example.com/ws", false},
		{"trailing slash", "http://localhost:8081/", "/ws", "ws://localhost:8081/ws", false},
		{"unsupported", "ftp://example.com", "/ws", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Client{BaseURL: tt.baseURL}
			got, err := c.websocketURL(tt.path)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestWebsocketURL_StripTrailingSlash(t *testing.T) {
	t.Parallel()
	c := &Client{BaseURL: "https://example.com/"}
	got, err := c.websocketURL("/ws")
	require.NoError(t, err)
	assert.False(t, strings.Contains(got, "//ws"))
}

func TestStreamDeploymentLogs_MalformedMessageWarning(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		conn.WriteMessage(websocket.TextMessage, []byte(`not json`))
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"deployment.log","payload":"bad"}`))
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	var buf, warnBuf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, &warnBuf)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Contains(t, warnBuf.String(), "Warning: skipping malformed WebSocket message")
	assert.Contains(t, warnBuf.String(), "Warning: skipping malformed log payload")
}

type customTransport struct{}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(req)
}

func TestStreamDeploymentLogs_CustomTransportWarning(t *testing.T) {
	t.Parallel()
	server := wsServer(t, func(conn *websocket.Conn) {
		readSubscribe(t, conn, "42")
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running", LogID: "log-1",
		})
	})
	defer server.Close()

	c := New(server.URL)
	c.HTTPClient.Transport = &customTransport{}
	var buf, warnBuf bytes.Buffer
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf, &warnBuf)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Contains(t, warnBuf.String(), "Warning: custom HTTP transport detected")
}

// ---------- WatchEvents ----------

// TestWatchEvents_DeliversStatusEvents covers the happy path: the server
// broadcasts status events, the client forwards each one through the
// channel with the filter applied.
func TestWatchEvents_DeliversStatusEvents(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "deploying",
		})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{
			InstanceID: "42", Status: "running",
		})
		// Hold the connection open until the client closes it; otherwise
		// the channel is never drained for the second event before the
		// server-side defer fires.
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)

	collected := drainEvents(t, events, 2, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	require.Len(t, collected, 2)
	assert.Equal(t, "deploying", collected[0].Status)
	assert.Equal(t, "running", collected[1].Status)
	assert.Equal(t, "deployment.status", collected[0].Type)
	assert.False(t, collected[0].Timestamp.IsZero(), "client-side timestamp must be populated")
}

// TestWatchEvents_FiltersByInstanceID asserts client-side filtering by
// InstanceIDs — events for any other instance are dropped before they
// reach the channel.
func TestWatchEvents_FiltersByInstanceID(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "other", Status: "running"})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{InstanceIDs: []string{"42"}})
	require.NoError(t, err)

	collected := drainEvents(t, events, 1, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	require.Len(t, collected, 1)
	assert.Equal(t, "42", collected[0].InstanceID)
}

// TestWatchEvents_FiltersByStatus asserts the status filter drops events
// whose Status field doesn't match.
func TestWatchEvents_FiltersByStatus(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "deploying"})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{Status: "running"})
	require.NoError(t, err)

	collected := drainEvents(t, events, 1, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	require.Len(t, collected, 1)
	assert.Equal(t, "running", collected[0].Status)
}

// TestWatchEvents_NonStatusMessagesDropped guards against cross-talk with
// `StreamDeploymentLogs`: deployment.log envelopes must NOT surface on
// the watch channel.
func TestWatchEvents_NonStatusMessagesDropped(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		writeWSMessage(t, conn, "deployment.log", types.WSDeploymentLog{InstanceID: "42", Line: "noise"})
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)

	collected := drainEvents(t, events, 1, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	require.Len(t, collected, 1)
	assert.Equal(t, "running", collected[0].Status)
}

// TestWatchEvents_ContextCancelClosesChannel asserts that cancelling the
// context closes the events channel cleanly (the cmd's range loop exits).
// Combined with `goleakVerify` this also asserts no orphan goroutine.
func TestWatchEvents_ContextCancelClosesChannel(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		// Block forever — only the client's CloseMessage will end this.
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)

	cancel()
	// Channel must close within a small time budget — anything longer
	// indicates an orphan goroutine or a missed cleanup path.
	select {
	case _, ok := <-events:
		assert.False(t, ok, "channel must close, not deliver an event")
	case <-time.After(time.Second):
		t.Fatal("events channel did not close after ctx cancel")
	}
}

// TestWatchEvents_ServerHangsUpClosesChannel reproduces the
// previously-leaked case: the server closes the connection on its own
// (e.g. a backend restart or proxy timeout) while ctx is still alive.
// Without the watchdog's `done` channel, the inner goroutine would
// remain parked on `<-ctx.Done()` forever. With it, the watchdog exits
// when the read loop's defer fires.
func TestWatchEvents_ServerHangsUpClosesChannel(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := wsServer(t, func(conn *websocket.Conn) {
		// Send one event, then return — defer closes the connection,
		// which surfaces as a ReadMessage error on the client side.
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
	})
	defer server.Close()

	// Critically: NEVER cancel this ctx. If the watchdog goroutine is
	// leaking, goleakVerify will catch it because it's still parked on
	// <-ctx.Done() after the test body returns.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)

	// Drain everything; channel must close after the server hangs up.
	select {
	case ev, ok := <-events:
		if ok {
			assert.Equal(t, "running", ev.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive the running event before timeout")
	}
	// Channel must close on its own (server hang-up → read loop returns
	// → defer close(out) fires).
	select {
	case _, ok := <-events:
		assert.False(t, ok, "channel must close after server hang-up")
	case <-time.After(time.Second):
		t.Fatal("events channel did not close after server hang-up")
	}
}

// TestWatchEvents_HeaderAuthPath verifies the default auth path
// (Authorization: Bearer <token>) is exercised when the backend accepts
// header tokens.
func TestWatchEvents_HeaderAuthPath(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	c.Token = "tok-abc"
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)
	drainEvents(t, events, 1, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	assert.Equal(t, "Bearer tok-abc", gotAuth, "header-auth path must send Authorization: Bearer")
}

// TestWatchEvents_QueryParamFallbackOn401 covers the documented K4
// fallback: backend rejects the Authorization header with 401, client
// retries with ?token=<jwt> in the URL.
func TestWatchEvents_QueryParamFallbackOn401(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First attempt: header-only — reject.
		if r.URL.Query().Get("token") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotToken = r.URL.Query().Get("token")
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		writeWSMessage(t, conn, "deployment.status", types.WSDeploymentStatus{InstanceID: "42", Status: "running"})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	c.Token = "tok-fallback"
	events, err := c.WatchEvents(ctx, WatchFilter{})
	require.NoError(t, err)
	drainEvents(t, events, 1, time.Second)
	cancel()
	drainRemaining(events, 100*time.Millisecond)

	assert.Equal(t, "tok-fallback", gotToken, "401 on header path must trigger ?token= retry")
}

// TestWatchEvents_QueryParamFallbackDisabledForAPIKey ensures the 401
// fallback only kicks in when a JWT token is configured. API-key callers
// see a connection error rather than a misleading retry.
func TestWatchEvents_QueryParamFallbackDisabledForAPIKey(t *testing.T) {
	t.Parallel()
	defer goleakVerify(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := New(server.URL)
	c.APIKey = "key-abc"
	_, err := c.WatchEvents(ctx, WatchFilter{})
	require.Error(t, err)
}

func TestAppendQueryToken(t *testing.T) {
	assert.Equal(t, "ws://h/ws?token=t", appendQueryToken("ws://h/ws", "t"))
	assert.Equal(t, "ws://h/ws?x=1&token=t", appendQueryToken("ws://h/ws?x=1", "t"))
}

// ---------- helpers ----------

// TestMain runs goleak.VerifyTestMain at package exit so leak detection
// works regardless of t.Parallel() interleaving (per-test
// goleak.IgnoreCurrent() snapshots can't see goroutines that other
// parallel tests will spawn later, leading to false positives). All
// tests in this file can therefore freely use t.Parallel().
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// goleakVerify is retained as a per-test escape hatch for tests that
// want stricter, finer-grained leak detection than the package-level
// TestMain provides. Most tests should rely on TestMain instead — the
// TestMain check runs once at process exit and catches everything the
// suite leaked, without interfering with t.Parallel.
func goleakVerify(t *testing.T) {
	t.Helper()
	// Intentionally a no-op now that TestMain handles package-wide
	// verification; kept callable so existing call sites are stable.
}

// drainEvents pulls up to n events off the channel within budget. Returns
// the slice of events received; the test asserts the count separately.
func drainEvents(t *testing.T, events <-chan types.WatchEvent, n int, budget time.Duration) []types.WatchEvent {
	t.Helper()
	out := make([]types.WatchEvent, 0, n)
	deadline := time.After(budget)
	for len(out) < n {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("drainEvents: only received %d of %d events within %s", len(out), n, budget)
		}
	}
	return out
}

// drainRemaining drains everything left on the channel so the producing
// goroutine isn't blocked on a backpressured send when the test exits.
func drainRemaining(events <-chan types.WatchEvent, budget time.Duration) {
	deadline := time.After(budget)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}
