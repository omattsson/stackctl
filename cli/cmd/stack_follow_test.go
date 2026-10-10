package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd.

// readSubscribe reads the subscribe message that --follow sends first.
func readSubscribe(conn *websocket.Conn) {
	_, _, _ = conn.ReadMessage()
}

// instanceRoute answers GET /api/v1/stack-instances/42 with the statuses in
// order (the last one repeats) and counts the calls.
func instanceRoute(calls *atomic.Int32, statuses ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(statuses) {
			n = len(statuses) - 1
		}
		_ = json.NewEncoder(w).Encode(types.StackInstance{Status: statuses[n], ErrorMessage: "image pull failed"})
	}
}

// runFollow runs followLogsCtx with a 5 s limit, so a missing terminal
// status fails the test instead of hanging.
func runFollow(t *testing.T, op string) (string, error) {
	t.Helper()
	c, err := newClient()
	require.NoError(t, err)
	var out, warn bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- followLogsCtx(context.Background(), c, "42", op, &out, &warn) }()
	select {
	case err = <-done:
		return warn.String(), err
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not exit")
		return "", nil
	}
}

func TestFollowLogsCtx_TerminalStatuses(t *testing.T) {
	tests := []struct {
		op      string
		status  string
		wantErr string
	}{
		{followOpDeploy, "running", ""},
		{followOpStop, "stopped", ""},
		{followOpClean, "draft", ""},
		{followOpDeploy, "error", "deploy failed (status error): chart api failed"},
		{followOpStop, "error", "stop failed (status error): chart api failed"},
		{followOpClean, "error", "clean failed (status error): chart api failed"},
		{followOpRollback, "error", "rollback failed (status error): chart api failed"},
		{followOpLogs, "error", "operation failed (status error): chart api failed"},
		{followOpDeploy, "partial", "deploy partially failed (status partial): some charts did not deploy: chart api failed. Run 'stackctl stack status 42' to see the charts"},
		{followOpRollback, "partial", "rollback partially failed (status partial)"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.op+"/"+tt.status, func(t *testing.T) {
			var getCalls atomic.Int32
			server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
				readSubscribe(conn)
				writeWatchStatus(t, conn, "42", "deploying")
				payload, _ := json.Marshal(types.WSDeploymentStatus{InstanceID: "42", Status: tt.status, ErrorMessage: "chart api failed"})
				msg, _ := json.Marshal(types.WSMessage{Type: "deployment.status", Data: payload})
				_ = conn.WriteMessage(websocket.TextMessage, msg)
				_, _, _ = conn.ReadMessage()
			}, map[string]http.HandlerFunc{
				"/api/v1/stack-instances/42": instanceRoute(&getCalls, "deploying"),
			})
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)

			_, err := runFollow(t, tt.op)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestFollowLogsCtx_FirstConnectCatchUp: the operation ends between the 202
// and the subscribe. The read after the first connect finds the status.
// `stack logs -f` starts no operation and does not read the instance.
func TestFollowLogsCtx_FirstConnectCatchUp(t *testing.T) {
	tests := []struct {
		name     string
		op       string
		status   string
		wantErr  string
		wantGets int32
	}{
		{"deploy ended running", followOpDeploy, "running", "", 1},
		{"deploy ended error", followOpDeploy, "error", "deploy failed (status error): image pull failed", 1},
		{"deploy ended partial", followOpDeploy, "partial", "deploy partially failed (status partial)", 1},
		{"stop ended stopped", followOpStop, "stopped", "", 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var getCalls atomic.Int32
			server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
				// No status event: it was sent before the subscribe.
				readSubscribe(conn)
				_, _, _ = conn.ReadMessage()
			}, map[string]http.HandlerFunc{
				"/api/v1/stack-instances/42": instanceRoute(&getCalls, tt.status),
			})
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)

			_, err := runFollow(t, tt.op)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			assert.Equal(t, tt.wantGets, getCalls.Load())
			assert.Equal(t, int32(1), conns.Load())
		})
	}
}

func TestFollowLogsCtx_LogsDoesNotReadInstance(t *testing.T) {
	var getCalls atomic.Int32
	server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		readSubscribe(conn)
		writeWatchStatus(t, conn, "42", "running")
		_, _, _ = conn.ReadMessage()
	}, map[string]http.HandlerFunc{
		"/api/v1/stack-instances/42": instanceRoute(&getCalls, "error"),
	})
	defer server.Close()
	_ = setupStackTestCmd(t, server.URL)

	_, err := runFollow(t, followOpLogs)
	require.NoError(t, err)
	assert.Equal(t, int32(0), getCalls.Load())
}

func TestFollowLogsCtx_TokenExpiredReconnects(t *testing.T) {
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	var refreshCalls, getCalls atomic.Int32
	var secondAuth atomic.Value
	server, conns := watchCloseServer(t, func(n int32, r *http.Request, conn *websocket.Conn) {
		readSubscribe(conn)
		if n == 1 {
			writeWatchStatus(t, conn, "42", "deploying")
			closePolicy(conn, "token expired")
			return
		}
		secondAuth.Store(r.Header.Get("Authorization"))
		writeWatchStatus(t, conn, "42", "running")
		_, _, _ = conn.ReadMessage()
	}, map[string]http.HandlerFunc{
		"/api/v1/auth/refresh": func(w http.ResponseWriter, _ *http.Request) {
			refreshCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-2", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
		},
		"/api/v1/stack-instances/42": instanceRoute(&getCalls, "deploying"),
	})
	defer server.Close()
	_ = setupStackTestCmd(t, server.URL)
	saveWatchSession(t)

	warn, err := runFollow(t, followOpDeploy)
	require.NoError(t, err)
	assert.Equal(t, int32(2), conns.Load())
	assert.Equal(t, int32(1), refreshCalls.Load())
	assert.Equal(t, int32(2), getCalls.Load(), "one read after each connect")
	assert.Equal(t, "Bearer "+newJWT, secondAuth.Load(), "reconnect uses the renewed token")
	assert.Contains(t, warn, "access token expired. Reconnecting with a renewed token")
}

// TestFollowLogsCtx_TerminalDuringReconnectGap: the final status comes while
// the command renews the token. The read of the instance after the
// reconnect finds it.
func TestFollowLogsCtx_TerminalDuringReconnectGap(t *testing.T) {
	tests := []struct {
		status  string
		wantErr string
	}{
		{"running", ""},
		{"error", "deploy failed (status error): image pull failed"},
		{"partial", "deploy partially failed (status partial)"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.status, func(t *testing.T) {
			var refreshCalls, getCalls atomic.Int32
			routes := refreshRoute(t, &refreshCalls)
			// First connect: still deploying. After the reconnect: final.
			routes["/api/v1/stack-instances/42"] = instanceRoute(&getCalls, "deploying", tt.status)
			server, conns := watchCloseServer(t, func(n int32, _ *http.Request, conn *websocket.Conn) {
				readSubscribe(conn)
				if n == 1 {
					closePolicy(conn, "token expired")
					return
				}
				// No event on the second connection: the final status
				// was in the gap.
				_, _, _ = conn.ReadMessage()
			}, routes)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			saveWatchSession(t)

			_, err := runFollow(t, followOpDeploy)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			assert.Equal(t, int32(2), conns.Load())
			assert.Equal(t, int32(2), getCalls.Load())
		})
	}
}

// TestFollowLogsCtx_StopsWithoutRestartHint: when --follow cannot follow
// the operation to the end, the error says that the operation continues on
// the server and points to watch/status. It never says to run the command
// again.
func TestFollowLogsCtx_StopsWithoutRestartHint(t *testing.T) {
	tests := []struct {
		name       string
		op         string
		reason     string
		session    bool
		refresh    http.HandlerFunc
		wantConns  int32
		wantDetail string
	}{
		{
			name: "session revoked", op: followOpDeploy, reason: "session revoked", session: true,
			wantConns: 1, wantDetail: "Connection closed by the server: session revoked. Log in again.",
		},
		{
			name: "SSO login without refresh token", op: followOpRollback, reason: "token expired",
			wantConns: 1, wantDetail: "token expired; stackctl could not renew the session (the login has no refresh token, for example an SSO login)",
		},
		{
			name: "refresh 401", op: followOpDeploy, reason: "token expired", session: true,
			refresh: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"Session expired"}`))
			},
			wantConns: 1, wantDetail: "stackctl could not renew the session (session renewal failed:",
		},
		{
			name: "token expired twice", op: followOpDeploy, reason: "token expired", session: true,
			wantConns: 2, wantDetail: "the server closed the renewed connection again",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var refreshCalls, getCalls atomic.Int32
			routes := refreshRoute(t, &refreshCalls)
			if tt.refresh != nil {
				routes["/api/v1/auth/refresh"] = tt.refresh
			}
			routes["/api/v1/stack-instances/42"] = instanceRoute(&getCalls, "deploying")
			server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
				readSubscribe(conn)
				closePolicy(conn, tt.reason)
			}, routes)
			defer server.Close()
			_ = setupStackTestCmd(t, server.URL)
			if tt.session {
				saveWatchSession(t)
			}

			_, err := runFollow(t, tt.op)
			require.Error(t, err)
			msg := err.Error()
			assert.Contains(t, msg, tt.wantDetail)
			assert.Contains(t, msg, "The "+tt.op+" continues on the server. Do not start it again.")
			assert.Contains(t, msg, "'stackctl stack watch --id 42' or 'stackctl stack status 42'")
			assert.NotContains(t, msg, "Run the command again")
			assert.Equal(t, tt.wantConns, conns.Load())
		})
	}
}

func TestFollowLogsCtx_ConnectionLostPointsToWatch(t *testing.T) {
	var getCalls atomic.Int32
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		readSubscribe(conn)
		writeWatchStatus(t, conn, "42", "deploying")
		// Return without a close frame: the connection is lost.
	}, map[string]http.HandlerFunc{
		"/api/v1/stack-instances/42": instanceRoute(&getCalls, "deploying"),
	})
	defer server.Close()
	_ = setupStackTestCmd(t, server.URL)

	_, err := runFollow(t, followOpClean)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "The clean continues on the server")
	assert.Equal(t, int32(1), conns.Load(), "no reconnect")
}

// TestStackDeployCmd_FollowPartialExitsNonZero runs `stack deploy -f`
// end to end: a deploy that ends "partial" must not hang and must fail.
func TestStackDeployCmd_FollowPartialExitsNonZero(t *testing.T) {
	var getCalls atomic.Int32
	server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		readSubscribe(conn)
		writeWatchStatus(t, conn, "42", "partial")
		_, _, _ = conn.ReadMessage()
	}, map[string]http.HandlerFunc{
		"/api/v1/stack-instances/42/deploy": func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"log_id": "log-1", "message": "Deployment started"})
		},
		"/api/v1/stack-instances/42": instanceRoute(&getCalls, "deploying"),
	})
	defer server.Close()
	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, stackDeployCmd.Flags().Set("follow", "true"))
	t.Cleanup(func() { _ = stackDeployCmd.Flags().Set("follow", "false") })

	done := make(chan error, 1)
	go func() { done <- stackDeployCmd.RunE(stackDeployCmd, []string{"42"}) }()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deploy partially failed (status partial)")
	case <-time.After(5 * time.Second):
		t.Fatal("stack deploy -f did not exit on status partial")
	}
}

func TestStackWatchCmd_PartialIsFailedTerminal(t *testing.T) {
	server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		writeWatchStatus(t, conn, "42", "deploying")
		writeWatchStatus(t, conn, "42", "partial")
		_, _, _ = conn.ReadMessage()
	}, nil)
	defer server.Close()
	out, _ := setupWatchCloseTest(t, server.URL)

	done := make(chan error, 1)
	go func() { done <- stackWatchCmd.RunE(stackWatchCmd, []string{}) }()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed terminal status: 42 (partial)")
	case <-time.After(5 * time.Second):
		t.Fatal("stack watch --id did not exit on status partial")
	}
	assert.Contains(t, out.String(), "42 partial")
}
