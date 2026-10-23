package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wantUserAgent(v string) string {
	return "stackctl/" + v + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

func TestFormatUserAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "empty version means dev", version: "", want: wantUserAgent("dev")},
		{name: "dev", version: "dev", want: wantUserAgent("dev")},
		{name: "release", version: "1.4.0", want: wantUserAgent("1.4.0")},
		{name: "git describe", version: "v1.4.0-3-gabc123-dirty", want: wantUserAgent("v1.4.0-3-gabc123-dirty")},
		{name: "all tchar symbols kept", version: "1!#$%&'*+-.^_`|~", want: wantUserAgent("1!#$%&'*+-.^_`|~")},
		{name: "space", version: "1.4.0 beta", want: wantUserAgent("1.4.0_beta")},
		{name: "newline", version: "1.4.0\r\nX-Evil: 1", want: wantUserAgent("1.4.0__X-Evil__1")},
		{name: "separators", version: "1/2(3)", want: wantUserAgent("1_2_3_")},
		{name: "non-ASCII bytes", version: "1.0é", want: wantUserAgent("1.0__")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatUserAgent(tt.version))
		})
	}
}

// TestUserAgent_Requests checks that every request kind sends the
// User-Agent of SetVersion. It changes the package version, so it does not
// run in parallel: Go runs the parallel tests after it, and it restores the
// version before it returns.
func TestUserAgent_Requests(t *testing.T) {
	prev, _ := version.Load().(string)
	t.Cleanup(func() { SetVersion(prev) })

	SetVersion("")
	assert.Equal(t, wantUserAgent("dev"), UserAgent(), "no version means dev")

	SetVersion("v9.8.7")
	want := wantUserAgent("v9.8.7")
	require.Equal(t, want, UserAgent())

	tests := []struct {
		name string
		// run sends the request to the server URL.
		run func(t *testing.T, c *Client) error
		// handler answers the request (nil: 200 with an empty JSON object).
		handler func(w http.ResponseWriter, r *http.Request)
		// wantCalls is the number of requests that must carry the header.
		wantCalls int
		// setup sets the credentials (nil: Token "jwt-1").
		setup func(c *Client)
	}{
		{
			name:      "API request",
			run:       func(_ *testing.T, c *Client) error { return c.Get("/api/v1/stack-instances", nil) },
			wantCalls: 1,
		},
		{
			name:      "session request",
			run:       func(_ *testing.T, c *Client) error { return c.Logout("r1") },
			wantCalls: 1,
		},
		{
			name: "WebSocket upgrade",
			run: func(_ *testing.T, c *Client) error {
				conn, err := c.dialWS(context.Background(), "/ws", nil)
				if err == nil {
					conn.Close()
				}
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					conn.Close()
				}
			},
			wantCalls: 1,
		},
		{
			name: "WebSocket query-param fallback after 401",
			run: func(_ *testing.T, c *Client) error {
				conn, err := c.dialWS(context.Background(), "/ws", nil)
				if err == nil {
					conn.Close()
				}
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("token") == "" {
					assert.Equal(t, "Bearer jwt-1", r.Header.Get("Authorization"), "first dial uses the header")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				assert.Empty(t, r.Header.Get("Authorization"), "fallback sends the token in the URL only")
				assert.Empty(t, r.Header.Get("X-API-Key"), "fallback sends no API key")
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					conn.Close()
				}
			},
			wantCalls: 2,
		},
		{
			name:  "WebSocket upgrade with API key",
			setup: func(c *Client) { c.APIKey = "key-1" },
			run: func(_ *testing.T, c *Client) error {
				conn, err := c.dialWS(context.Background(), "/ws", nil)
				if err == nil {
					conn.Close()
				}
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "key-1", r.Header.Get("X-API-Key"))
				assert.Empty(t, r.Header.Get("Authorization"))
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					conn.Close()
				}
			},
			wantCalls: 1,
		},
		{
			name:  "WebSocket API key refused, JWT fallback",
			setup: func(c *Client) { c.APIKey = "key-1"; c.Token = "jwt-1" },
			run: func(_ *testing.T, c *Client) error {
				conn, err := c.dialWS(context.Background(), "/ws", nil)
				if err == nil {
					conn.Close()
				}
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("token") == "" {
					assert.Equal(t, "key-1", r.Header.Get("X-API-Key"), "first dial uses the API key")
					assert.Empty(t, r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				assert.Equal(t, "jwt-1", r.URL.Query().Get("token"))
				assert.Empty(t, r.Header.Get("X-API-Key"), "fallback sends no API key")
				assert.Empty(t, r.Header.Get("Authorization"))
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					conn.Close()
				}
			},
			wantCalls: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if assert.Equal(t, want, r.Header.Get("User-Agent"), "%s %s", r.Method, r.URL) {
					calls.Add(1)
				}
				if tt.handler != nil {
					tt.handler(w, r)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{})
			}))
			defer srv.Close()

			c := New(srv.URL)
			if tt.setup != nil {
				tt.setup(c)
			} else {
				c.Token = "jwt-1"
			}
			require.NoError(t, tt.run(t, c))
			srv.Close() // wait for the handlers before reading calls
			assert.Equal(t, int32(tt.wantCalls), calls.Load())
		})
	}
}
