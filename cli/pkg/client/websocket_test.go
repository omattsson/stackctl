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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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

	_, err := c.StreamDeploymentLogs(ctx, "42", &buf)
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
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
	result, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
	require.NoError(t, err)
	assert.Equal(t, "running", result.Status)
	assert.Contains(t, buf.String(), "Still going...")
}

func TestStreamDeploymentLogs_ConnectionError(t *testing.T) {
	t.Parallel()
	c := New("http://localhost:1")
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to WebSocket")
}

func TestStreamDeploymentLogs_BadScheme(t *testing.T) {
	t.Parallel()
	c := &Client{BaseURL: "ftp://example.com"}
	var buf bytes.Buffer
	_, err := c.StreamDeploymentLogs(context.Background(), "42", &buf)
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
