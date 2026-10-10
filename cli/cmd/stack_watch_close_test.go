package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// writeWatchStatus sends one "deployment.status" envelope.
func writeWatchStatus(t *testing.T, conn *websocket.Conn, id, status string) {
	t.Helper()
	payload, _ := json.Marshal(types.WSDeploymentStatus{InstanceID: id, Status: status})
	msg, _ := json.Marshal(types.WSMessage{Type: "deployment.status", Data: payload})
	_ = conn.WriteMessage(websocket.TextMessage, msg)
}

// closePolicy closes the socket with close code 1008 and reason, like the
// k8s-stack-manager hub, and waits for the close reply of the client.
func closePolicy(conn *websocket.Conn, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
		time.Now().Add(time.Second))
	_, _, _ = conn.ReadMessage()
}

// watchCloseServer serves /ws with onWS and the other paths with routes
// (404 for a path without a route).
func watchCloseServer(t *testing.T, onWS func(n int32, r *http.Request, conn *websocket.Conn), routes map[string]http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			onWS(conns.Add(1), r, conn)
			return
		}
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	return server, &conns
}

// refreshRoute answers /api/v1/auth/refresh with a new access token.
func refreshRoute(t *testing.T, calls *atomic.Int32) map[string]http.HandlerFunc {
	t.Helper()
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	return map[string]http.HandlerFunc{
		"/api/v1/auth/refresh": func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-next", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
		},
	}
}

func setupWatchCloseTest(t *testing.T, apiURL string) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := setupStackTestCmd(t, apiURL)
	var errBuf bytes.Buffer
	stackWatchCmd.SetErr(&errBuf)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() {
		stackWatchCmd.SetErr(nil)
		_ = stackWatchCmd.Flags().Set("id", "")
	})
	return out, &errBuf
}

// saveWatchSession stores a username/password session with a refresh token.
func saveWatchSession(t *testing.T) {
	t.Helper()
	exp := time.Now().Add(10 * time.Minute)
	require.NoError(t, saveSession(testJWT(t, "alice", exp), "refresh-1", "alice", exp))
}

func TestStackWatchCmd_PolicyCloseExitsNonZero(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{"session revoked", "session revoked", "Connection closed by the server: session revoked. Log in again."},
		{"token expired without session", "token expired", "Connection closed by the server: token expired. Run the command again. If it fails, run 'stackctl login'."},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
				writeWatchStatus(t, conn, "42", "deploying")
				closePolicy(conn, tt.reason)
			}, nil)
			defer server.Close()
			out, _ := setupWatchCloseTest(t, server.URL)

			err := stackWatchCmd.RunE(stackWatchCmd, []string{})
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			assert.Contains(t, out.String(), "deploying", "events before the close are printed")
			assert.Equal(t, int32(1), conns.Load(), "no reconnect")
		})
	}
}

func TestStackWatchCmd_SessionRevokedDoesNotRenew(t *testing.T) {
	var refreshCalls atomic.Int32
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		closePolicy(conn, "session revoked")
	}, map[string]http.HandlerFunc{"/api/v1/auth/refresh": func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}})
	defer server.Close()
	_, _ = setupWatchCloseTest(t, server.URL)
	saveWatchSession(t)

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session revoked. Log in again.")
	assert.Equal(t, int32(1), conns.Load())
	assert.Equal(t, int32(0), refreshCalls.Load())
}

func TestStackWatchCmd_TokenExpiredReconnects(t *testing.T) {
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	var refreshCalls atomic.Int32
	var secondAuth atomic.Value
	server, conns := watchCloseServer(t, func(n int32, r *http.Request, conn *websocket.Conn) {
		if n == 1 {
			writeWatchStatus(t, conn, "42", "deploying")
			closePolicy(conn, "token expired")
			return
		}
		secondAuth.Store(r.Header.Get("Authorization"))
		writeWatchStatus(t, conn, "42", "running")
		_, _, _ = conn.ReadMessage()
	}, map[string]http.HandlerFunc{
		"/api/v1/auth/refresh": func(w http.ResponseWriter, r *http.Request) {
			refreshCalls.Add(1)
			ck, err := r.Cookie("refresh_token")
			if err != nil || ck.Value != "refresh-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-2", Path: "/api/v1/auth"})
			_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
		},
		"/api/v1/stack-instances/42": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(types.StackInstance{Status: "deploying"})
		},
	})
	defer server.Close()
	out, errBuf := setupWatchCloseTest(t, server.URL)
	saveWatchSession(t)

	require.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))
	assert.Equal(t, int32(2), conns.Load())
	assert.Equal(t, int32(1), refreshCalls.Load())
	assert.Equal(t, "Bearer "+newJWT, secondAuth.Load(), "reconnect uses the renewed token")
	assert.Contains(t, errBuf.String(), "access token expired. Reconnecting with a renewed token")
	assert.Contains(t, out.String(), "running")
	assert.Equal(t, "refresh-2", readStored(t).RefreshToken)
}

func TestStackWatchCmd_TokenExpiredTwiceStops(t *testing.T) {
	var refreshCalls atomic.Int32
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		closePolicy(conn, "token expired")
	}, refreshRoute(t, &refreshCalls))
	defer server.Close()
	_, _ = setupWatchCloseTest(t, server.URL)
	saveWatchSession(t)

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Equal(t, "Connection closed by the server: token expired. Run the command again. If it fails, run 'stackctl login'.", err.Error())
	assert.Equal(t, int32(2), conns.Load(), "one reconnect, then stop")
	assert.Equal(t, int32(1), refreshCalls.Load())
}

func TestStackWatchCmd_TokenExpiredRenewalFails(t *testing.T) {
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		closePolicy(conn, "token expired")
	}, map[string]http.HandlerFunc{"/api/v1/auth/refresh": func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Session expired"}`))
	}})
	defer server.Close()
	_, _ = setupWatchCloseTest(t, server.URL)
	saveWatchSession(t)

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Connection closed by the server: token expired. Run the command again. If it fails, run 'stackctl login'.")
	assert.Contains(t, err.Error(), "session renewal failed")
	assert.Equal(t, int32(1), conns.Load())
}

func TestStackWatchCmd_ConnectionLostWhilePending(t *testing.T) {
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		// One instance finishes, the other is pending; then the server
		// drops the connection without a close frame.
		writeWatchStatus(t, conn, "1", "running")
		writeWatchStatus(t, conn, "2", "deploying")
	}, nil)
	defer server.Close()
	out, _ := setupWatchCloseTest(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "1,2"))

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Equal(t, "connection lost before all instances reached a terminal status (pending: 2)", err.Error())
	assert.Contains(t, out.String(), "deploying")
	assert.Equal(t, int32(1), conns.Load(), "no reconnect")
}

func TestStackWatchCmd_ConnectionLostWithoutIDIsClean(t *testing.T) {
	server, _ := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		writeWatchStatus(t, conn, "1", "deploying")
	}, nil)
	defer server.Close()
	_, errBuf := setupWatchCloseTest(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", ""))

	assert.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))
	assert.Contains(t, errBuf.String(), "Note: the connection to the server ended.")
}

// TestStackWatchCmd_TerminalDuringReconnectGap: the terminal event comes
// while the watch renews the token and dials again. The status read after
// the reconnect finds it.
func TestStackWatchCmd_TerminalDuringReconnectGap(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{"running", "running", false},
		{"error", "error", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var refreshCalls, getCalls atomic.Int32
			routes := refreshRoute(t, &refreshCalls)
			routes["/api/v1/stack-instances/42"] = func(w http.ResponseWriter, _ *http.Request) {
				getCalls.Add(1)
				_ = json.NewEncoder(w).Encode(types.StackInstance{Status: tt.status, ErrorMessage: "image pull failed"})
			}
			server, conns := watchCloseServer(t, func(n int32, _ *http.Request, conn *websocket.Conn) {
				if n == 1 {
					writeWatchStatus(t, conn, "42", "deploying")
					closePolicy(conn, "token expired")
					return
				}
				// The second connection sends no event: the terminal
				// event was in the gap.
				_, _, _ = conn.ReadMessage()
			}, routes)
			defer server.Close()
			out, _ := setupWatchCloseTest(t, server.URL)
			saveWatchSession(t)

			err := stackWatchCmd.RunE(stackWatchCmd, []string{})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "failed terminal status: 42")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, int32(2), conns.Load())
			assert.Equal(t, int32(1), getCalls.Load())
			assert.Contains(t, out.String(), "42 "+tt.status)
		})
	}
}

func TestStackWatchCmd_CtrlCDuringRenewalIsClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newJWT := testJWT(t, "renewed", time.Now().Add(15*time.Minute))
	server, conns := watchCloseServer(t, func(_ int32, _ *http.Request, conn *websocket.Conn) {
		closePolicy(conn, "token expired")
	}, map[string]http.HandlerFunc{"/api/v1/auth/refresh": func(w http.ResponseWriter, _ *http.Request) {
		cancel() // Ctrl-C while the renewal runs
		http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "refresh-2", Path: "/api/v1/auth"})
		_ = json.NewEncoder(w).Encode(map[string]string{"token": newJWT})
	}})
	defer server.Close()
	_, _ = setupWatchCloseTest(t, server.URL)
	saveWatchSession(t)
	stackWatchCmd.SetContext(ctx)
	t.Cleanup(func() { stackWatchCmd.SetContext(context.Background()) })

	assert.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))
	assert.Equal(t, int32(1), conns.Load(), "no reconnect after Ctrl-C")
}
