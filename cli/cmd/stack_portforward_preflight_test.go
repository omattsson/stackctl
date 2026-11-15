package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeServer is a port-forward server for portForwardPreflight: it records
// the requests and the close code of every tunnel that it upgraded.
type probeServer struct {
	*httptest.Server
	requestLog
	requests atomic.Int32
	// closeCodes gets the close code of each tunnel that the client closed.
	closeCodes chan int
}

// newProbeServer starts a server. refuse, when not nil, answers a request
// instead of the upgrade and returns true.
func newProbeServer(t *testing.T, refuse func(w http.ResponseWriter, r *http.Request) bool) *probeServer {
	t.Helper()
	s := &probeServer{closeCodes: make(chan int, 8)}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.record(r)
		if refuse != nil && refuse(w, r) {
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				code := 0
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				s.closeCodes <- code
				return
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *probeServer) client() *client.Client {
	c := client.New(s.URL)
	c.APIKey = "sk_probe"
	return c
}

func refusal(status int, code, msg string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, _ *http.Request) bool {
		return jsonRefusal(status, code, msg)(w)
	}
}

func TestPortForwardPreflight_Success(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		service string
		port    int
	}{
		{"plain", "mysql", 3306},
		{"highest port", "web", 65535},
		{"lowest port", "dns", 1},
		{"name with characters that need escaping", "my svc&x=1", 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newProbeServer(t, nil)
			require.NoError(t, portForwardPreflight(context.Background(), srv.client(), "42", tt.service, tt.port))

			select {
			case code := <-srv.closeCodes:
				assert.Equal(t, client.CloseNormal, code, "the probe tunnel is closed with 1000")
			case <-time.After(5 * time.Second):
				t.Fatal("the server did not see the tunnel close")
			}
			reqs := srv.requestsTo(pfTestPath)
			require.Len(t, reqs, 1, "one probe, no retry")
			assert.Equal(t, tt.service, reqs[0].Query.Get("service"))
			assert.Equal(t, fmt.Sprint(tt.port), reqs[0].Query.Get("port"))
			assert.Empty(t, reqs[0].Query.Get("x"), "the service name is one query value")
			assert.Equal(t, "sk_probe", reqs[0].Header.Get("X-API-Key"), "the probe uses the credentials of the client")
			assert.Equal(t, int32(1), srv.requests.Load())
		})
	}
}

func TestPortForwardPreflight_FatalRefusalIsReturned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		refuse   func(http.ResponseWriter, *http.Request) bool
		wantCode string
	}{
		{"unknown service", refusal(http.StatusNotFound, client.StreamCodeServiceNotFound, "Service not found"), client.StreamCodeServiceNotFound},
		{"unknown port", refusal(http.StatusNotFound, client.StreamCodePortNotFound, "Port not found"), client.StreamCodePortNotFound},
		{"invalid service", refusal(http.StatusBadRequest, client.StreamCodeInvalidService, "Invalid service"), client.StreamCodeInvalidService},
		{"invalid port", refusal(http.StatusBadRequest, client.StreamCodeInvalidPort, "Invalid port"), client.StreamCodeInvalidPort},
		{"stack not found", refusal(http.StatusNotFound, client.StreamCodeInstanceNotFound, "Stack not found"), client.StreamCodeInstanceNotFound},
		{"stack not deployed", refusal(http.StatusConflict, client.StreamCodeInstanceNotDeployed, "Not deployed"), client.StreamCodeInstanceNotDeployed},
		{"external name service", refusal(http.StatusConflict, client.StreamCodeServiceExternalName, "ExternalName"), client.StreamCodeServiceExternalName},
		{"no selector", refusal(http.StatusConflict, client.StreamCodeServiceNoSelector, "No selector"), client.StreamCodeServiceNoSelector},
		{"forbidden", refusal(http.StatusForbidden, client.StreamCodeForbidden, "Forbidden"), client.StreamCodeForbidden},
		{"namespace not allowed", refusal(http.StatusForbidden, client.StreamCodeNamespaceNotAllowed, "Namespace"), client.StreamCodeNamespaceNotAllowed},
		{"cluster switch off", refusal(http.StatusForbidden, client.StreamCodeClusterDisabled, "Cluster"), client.StreamCodeClusterDisabled},
		{"feature disabled", refusal(http.StatusNotFound, client.StreamCodeFeatureDisabled, "Disabled"), client.StreamCodeFeatureDisabled},
		{"unauthorized", refusal(http.StatusUnauthorized, "", "Unauthorized"), ""},
		{"older server without the route", func(w http.ResponseWriter, _ *http.Request) bool {
			http.NotFound(w, nil)
			return true
		}, ""},
		{"proxy answers without the upgrade", func(w http.ResponseWriter, _ *http.Request) bool {
			_, _ = io.WriteString(w, "ok")
			return true
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newProbeServer(t, tt.refuse)
			err := portForwardPreflight(context.Background(), srv.client(), "42", "svc", 80)
			require.Error(t, err)
			var hs *client.WSHandshakeError
			require.True(t, errors.As(err, &hs), "the error of a refused connection: %v", err)
			assert.True(t, hs.Fatal())
			assert.True(t, portForwardFatal(err))
			assert.Equal(t, tt.wantCode, hs.Code)
			assert.Equal(t, int32(1), srv.requests.Load(), "no retry")
			assert.Empty(t, srv.closeCodes, "no tunnel was opened")
		})
	}
}

func TestPortForwardPreflight_NonFatalFailureIsIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		refuse func(http.ResponseWriter, *http.Request) bool
	}{
		{"tunnel limit", refusal(http.StatusTooManyRequests, client.StreamCodeTunnelLimit, "Too many tunnels")},
		{"rate limit", refusal(http.StatusTooManyRequests, "", "Rate limit exceeded")},
		{"no ready pod", refusal(http.StatusServiceUnavailable, client.StreamCodeNoReadyPod, "No ready pod")},
		{"kube timeout", refusal(http.StatusGatewayTimeout, client.StreamCodeKubeTimeout, "Timeout")},
		{"shutting down", refusal(http.StatusServiceUnavailable, client.StreamCodeShuttingDown, "Shutting down")},
		{"server error", refusal(http.StatusInternalServerError, "", "boom")},
		{"bad gateway", refusal(http.StatusBadGateway, "", "bad gateway")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newProbeServer(t, tt.refuse)
			require.NoError(t, portForwardPreflight(context.Background(), srv.client(), "42", "svc", 80))
			assert.Equal(t, int32(1), srv.requests.Load(), "the probe is not retried, not even after a 429 tunnel_limit")
		})
	}
}

func TestPortForwardPreflight_NetworkErrorIsIgnored(t *testing.T) {
	t.Parallel()
	srv := newProbeServer(t, nil)
	c := srv.client()
	srv.Close()
	require.NoError(t, portForwardPreflight(context.Background(), c, "42", "svc", 80))
}

func TestPortForwardPreflight_Canceled(t *testing.T) {
	t.Parallel()
	t.Run("before the dial", func(t *testing.T) {
		t.Parallel()
		srv := newProbeServer(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.NoError(t, portForwardPreflight(ctx, srv.client(), "42", "svc", 80))
		assert.Equal(t, int32(0), srv.requests.Load(), "no request after a cancel")
	})
	t.Run("while the server holds the upgrade", func(t *testing.T) {
		t.Parallel()
		reached := make(chan struct{})
		srv := newProbeServer(t, func(_ http.ResponseWriter, r *http.Request) bool {
			close(reached)
			<-r.Context().Done()
			return true
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- portForwardPreflight(ctx, srv.client(), "42", "svc", 80) }()
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
			t.Fatal("the probe did not reach the server")
		}
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err, "a cancel is not an error of the probe")
		case <-time.After(5 * time.Second):
			t.Fatal("the probe did not return after the cancel")
		}
	})
	t.Run("a fatal refusal that arrives with the cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		srv := newProbeServer(t, func(w http.ResponseWriter, _ *http.Request) bool {
			cancel()
			return jsonRefusal(http.StatusNotFound, client.StreamCodeServiceNotFound, "Service not found")(w)
		})
		defer cancel()
		require.NoError(t, portForwardPreflight(ctx, srv.client(), "42", "svc", 80))
	})
}

// freePort returns a local port that nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return portOf(t, l.Addr().String())
}

// holdProbe returns a refusal function for newPFTestServer that holds the
// first request (the probe) until release is closed, then lets answer decide.
func holdProbe(entered chan<- struct{}, release <-chan struct{}, answer func(http.ResponseWriter) bool) func(http.ResponseWriter) bool {
	var first atomic.Bool
	first.Store(true)
	return func(w http.ResponseWriter) bool {
		if first.Swap(false) {
			close(entered)
			<-release
			return answer(w)
		}
		return false
	}
}

func TestPortForwardCmd_ClientWaitingDuringProbeIsServed(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newPFTestServer(t, holdProbe(entered, release, func(http.ResponseWriter) bool { return false }))
	t.Cleanup(func() { close(release) }) // before srv.Close
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", fmt.Sprintf("%d:3306", port))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not reach the server")
	}
	// The port is bound while the probe runs, but nothing is printed yet.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err, "the kernel queues a client that connects during the probe")
	defer conn.Close()
	assert.Empty(t, stdout.String(), "no address before the probe has passed")
	_, err = conn.Write([]byte("hi"))
	require.NoError(t, err)

	release <- struct{}{}
	waitForwarding(t, stdout, done)
	got := make([]byte, 2)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	assert.Equal(t, "hi", string(got), "the queued client is served once the probe passed")
	assert.Equal(t, int32(2), srv.upgrades.Load(), "the probe and the tunnel of the queued client")

	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Contains(t, stderr.String(), "Connection 1 from")
}

func TestPortForwardCmd_ClientWaitingDuringFailedProbeIsReset(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newPFTestServer(t, holdProbe(entered, release, jsonRefusal(http.StatusNotFound, "service_not_found", "Service not found")))
	t.Cleanup(func() { close(release) })
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	port := freePort(t)
	done := startPortForward(context.Background(), "42", "no-such-svc", fmt.Sprintf("%d:3306", port))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not reach the server")
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer conn.Close()

	release <- struct{}{}
	err = waitDone(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Service not found")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
	var ne net.Error
	assert.False(t, errors.As(err, &ne) && ne.Timeout(), "the waiting client is closed, not left hanging")
	assert.Empty(t, stdout.String())
	assert.NotContains(t, stderr.String(), "Connection", "the probe is not a connection")
	assert.Equal(t, int32(0), srv.upgrades.Load(), "no tunnel was opened")
}

func TestPortForwardCmd_PreflightErrorTextIsThatOfAConnection(t *testing.T) {
	const msg = "Service not found. Check the service name and the service port (not the container port)."
	srv := newPFTestServer(t, jsonRefusal(http.StatusNotFound, "service_not_found", msg))
	setupPortForwardTest(t, srv.URL)

	cmdErr := waitDone(t, startPortForward(context.Background(), "42", "no-such-svc", "0:3306"))
	require.Error(t, cmdErr)

	c, err := newClient()
	require.NoError(t, err)
	_, dialErr := c.DialPortForward(context.Background(), "42", "no-such-svc", 3306)
	require.Error(t, dialErr)
	assert.Equal(t, dialErr.Error(), cmdErr.Error(), "the same message as the first connection gave before")
	assert.Contains(t, cmdErr.Error(), msg)
}

func TestPortForwardCmd_PreflightRefusalHints(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"older server without the route", http.StatusNotFound, "", "v0.15.0"},
		{"feature disabled", http.StatusNotFound, "feature_disabled", "turned off"},
		{"cluster switch off", http.StatusForbidden, "cluster_port_forward_disabled", "HTTP 403"},
		{"stack not found", http.StatusNotFound, "instance_not_found", "HTTP 404"},
		{"unauthorized", http.StatusUnauthorized, "", "HTTP 401"},
		{"invalid port", http.StatusBadRequest, "invalid_port", "HTTP 400"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newPFTestServer(t, func(w http.ResponseWriter) bool {
				if tt.code == "" && tt.status == http.StatusNotFound {
					http.NotFound(w, nil) // no error body, no code: an older server
					return true
				}
				return jsonRefusal(tt.status, tt.code, "refused")(w)
			})
			stdout, _ := setupPortForwardTest(t, srv.URL)
			err := waitDone(t, startPortForward(context.Background(), "42", "svc", "0:3306"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, stdout.String())
			assert.Equal(t, int32(1), srv.requests.Load(), "one probe, nothing else")
		})
	}
}

func TestPortForwardCmd_PreflightNetworkErrorStillListens(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	// Point the command at a server that is gone: the probe fails with a
	// network error, which does not stop the command.
	dead := httptest.NewServer(http.NotFoundHandler())
	flagAPIURL = dead.URL
	dead.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err, "the connection fails on its own dial")
	require.Eventually(t, func() bool { return len(stderr.String()) > 0 }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, stderr.String(), "Connection 1 from")
	assert.Contains(t, stderr.String(), "failed")

	cancel()
	require.NoError(t, waitDone(t, done))
}

func TestPortForwardCmd_PreflightQuietHappyFlow(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, _ := setupPortForwardTest(t, srv.URL)
	printer.Quiet = true
	t.Cleanup(func() { printer.Quiet = false })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")

	require.Eventually(t, func() bool { return stdout.String() != "" }, 5*time.Second, 10*time.Millisecond)
	line := stdout.String()
	_, port, err := net.SplitHostPort(line[:len(line)-1])
	require.NoError(t, err, "-q prints only host:port: %q", line)
	assert.NotEqual(t, "0", port)
	assert.Equal(t, int32(1), srv.upgrades.Load(), "the probe ran before the address was printed")

	cancel()
	require.NoError(t, waitDone(t, done))
}
