package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// Tests in this file are NOT parallelized: they mutate package globals
// (cfg, printer, flag variables) through setupStackTestCmd.

const pfTestPath = "/api/v1/stack-instances/42/port-forward"

// lockedBuffer is a bytes.Buffer that the connection goroutines and the
// test can use at the same time.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// pfTestServer counts all requests and the upgrades of the port-forward
// route. refuse, when set, answers the route without an upgrade.
type pfTestServer struct {
	*httptest.Server
	requests atomic.Int32
	upgrades atomic.Int32
	// routes answer other paths (for example /ui-config); set by
	// newPFTestServerWith before the server starts.
	routes map[string]http.HandlerFunc
	// requestLog records every request (path, query, headers).
	requestLog
}

func newPFTestServer(t *testing.T, refuse func(w http.ResponseWriter) bool) *pfTestServer {
	t.Helper()
	return newPFTestServerWith(t, nil, refuse)
}

// newPFTestServerWith is newPFTestServer with extra routes by path.
func newPFTestServerWith(t *testing.T, routes map[string]http.HandlerFunc, refuse func(w http.ResponseWriter) bool) *pfTestServer {
	t.Helper()
	s := &pfTestServer{routes: routes}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.record(r)
		if h, ok := s.routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		if r.URL.Path != pfTestPath {
			http.NotFound(w, r)
			return
		}
		if refuse != nil && refuse(w) {
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.upgrades.Add(1)
		defer ws.Close()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func jsonRefusal(status int, code, msg string) func(http.ResponseWriter) bool {
	return func(w http.ResponseWriter) bool {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
		return true
	}
}

// setupPortForwardTest prepares the globals and the flags of the command.
func setupPortForwardTest(t *testing.T, apiURL string) (stdout, stderr *lockedBuffer) {
	t.Helper()
	setupStackTestCmd(t, apiURL)
	stdout, stderr = &lockedBuffer{}, &lockedBuffer{}
	printer.Writer = stdout
	printer.ErrWriter = stderr
	stackPortForwardCmd.SetErr(stderr)
	t.Cleanup(func() {
		stackPortForwardCmd.SetErr(nil)
		_ = stackPortForwardCmd.Flags().Set("address", "127.0.0.1")
		_ = stackPortForwardCmd.Flags().Set("allow-insecure-stream", "false")
	})
	return stdout, stderr
}

// startPortForward runs the command in a goroutine.
func startPortForward(ctx context.Context, args ...string) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- runPortForward(ctx, stackPortForwardCmd, args)
	}()
	return done
}

var forwardingLine = regexp.MustCompile(`Forwarding from (\S+) -> `)

// waitForwarding returns the local address from stdout.
func waitForwarding(t *testing.T, stdout *lockedBuffer, done <-chan error) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := forwardingLine.FindStringSubmatch(stdout.String()); m != nil {
			return m[1]
		}
		select {
		case err := <-done:
			t.Fatalf("command ended before it listened: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("no Forwarding line; stdout: %q", stdout.String())
	return ""
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("command did not end")
		return nil
	}
}

func portOf(t *testing.T, hostPort string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(hostPort)
	require.NoError(t, err)
	n, err := strconv.Atoi(p)
	require.NoError(t, err)
	return n
}

// holdPorts listens on n consecutive free ports of 127.0.0.1 above 1024 and
// returns the first one.
func holdPorts(t *testing.T, n int) int {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		base := probe.Addr().(*net.TCPAddr).Port
		_ = probe.Close()
		if base < portForwardMinLocal || base+n+portForwardTries > 65535 {
			continue
		}
		var held []net.Listener
		ok := true
		for p := base; p < base+n; p++ {
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
			if err != nil {
				ok = false
				break
			}
			held = append(held, l)
		}
		if ok {
			t.Cleanup(func() { closeListeners(held) })
			return base
		}
		closeListeners(held)
	}
	t.Fatal("no free port range")
	return 0
}

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		in       string
		want     portSpec
		wantErr  string
		explicit bool
	}{
		{in: "3306", want: portSpec{local: 3306, remote: 3306}},
		{in: "80", want: portSpec{local: 10080, remote: 80}},
		{in: "1024", want: portSpec{local: 11024, remote: 1024}},
		{in: "1025", want: portSpec{local: 1025, remote: 1025}},
		{in: "0:3306", want: portSpec{local: 0, remote: 3306, explicit: true}},
		{in: "1025:80", want: portSpec{local: 1025, remote: 80, explicit: true}},
		{in: "65535:1", want: portSpec{local: 65535, remote: 1, explicit: true}},
		{in: "1024:3306", wantErr: "LOCAL must be 0"},
		{in: "65536:3306", wantErr: "LOCAL must be 0"},
		{in: "-1:3306", wantErr: "LOCAL must be 0"},
		{in: ":3306", wantErr: "LOCAL must be 0"},
		{in: "0", wantErr: "REMOTE must be"},
		{in: "65536", wantErr: "REMOTE must be"},
		{in: "3306:", wantErr: "REMOTE must be"},
		{in: "abc", wantErr: "REMOTE must be"},
		{in: "1:2:3", wantErr: "REMOTE must be"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parsePortSpec(tt.in)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPortForwardCmd_TwoParallelConnections(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)
	assert.NotEqual(t, 0, portOf(t, addr))
	assert.Contains(t, stdout.String(), "-> mysql:3306")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			msg := []byte(fmt.Sprintf("connection-%d-payload", i))
			if _, err := conn.Write(msg); err != nil {
				errs <- err
				return
			}
			got := make([]byte, len(msg))
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(conn, got); err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(msg, got) {
				errs <- fmt.Errorf("connection %d got %q", i, got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), srv.upgrades.Load(), "one WebSocket per local connection")

	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Contains(t, stderr.String(), "Connection 1 from")
	assert.Contains(t, stderr.String(), "Connection 2 from")
	assert.Contains(t, stderr.String(), "bytes in")
}

func TestPortForwardCmd_QuietPrintsHostPort(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, _ := setupPortForwardTest(t, srv.URL)
	printer.Quiet = true
	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	deadline := time.Now().Add(5 * time.Second)
	for stdout.String() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Regexp(t, `^127\.0\.0\.1:\d+\n$`, stdout.String())
}

func TestPortForwardCmd_ForbiddenStopsCommand(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		code    string
		apiKey  string
		wantMsg string
	}{
		{"forbidden", http.StatusForbidden, "forbidden", "", "HTTP 403"},
		{"api key not allowed", http.StatusForbidden, "api_key_not_allowed", "sk_test", "API keys are not allowed for this command"},
		{"feature disabled", http.StatusNotFound, "feature_disabled", "", "turned off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newPFTestServer(t, jsonRefusal(tt.status, tt.code, "refused"))
			stdout, _ := setupPortForwardTest(t, srv.URL)
			flagAPIKey = tt.apiKey
			done := startPortForward(context.Background(), "42", "mysql", "0:3306")
			addr := waitForwarding(t, stdout, done)

			conn, err := net.Dial("tcp", addr)
			require.NoError(t, err)
			defer conn.Close()
			err = waitDone(t, done)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			_, err = net.DialTimeout("tcp", addr, time.Second)
			assert.Error(t, err, "the listener is closed")
		})
	}
}

func TestPortForwardCmd_PerConnectionErrorKeepsListening(t *testing.T) {
	var first atomic.Bool
	first.Store(true)
	srv := newPFTestServer(t, func(w http.ResponseWriter) bool {
		if first.Swap(false) {
			return jsonRefusal(http.StatusServiceUnavailable, "no_ready_pod", "No ready pod")(w)
		}
		return false
	})
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	c1, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_ = c1.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = c1.Read(make([]byte, 1))
	assert.Error(t, err, "the refused connection is closed")
	_ = c1.Close()

	c2, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c2.Close()
	_, err = c2.Write([]byte("ok"))
	require.NoError(t, err)
	got := make([]byte, 2)
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadFull(c2, got)
	require.NoError(t, err)
	assert.Equal(t, "ok", string(got))

	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Contains(t, stderr.String(), "Connection 1 from")
	assert.Contains(t, stderr.String(), "HTTP 503")
}

func TestPortForwardCmd_CancelReturnsNilWithoutLeaks(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ignore := goleak.IgnoreCurrent()

	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("x"))
	require.NoError(t, err)
	_, err = io.ReadFull(conn, make([]byte, 1))
	require.NoError(t, err)

	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Contains(t, stderr.String(), "(interrupted)")
	_ = conn.Close()
	srv.Close()
	goleak.VerifyNone(t, ignore)
}

func TestPortForwardCmd_BindFailureOpensNothing(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	// 192.0.2.1 (TEST-NET-1) is not a local address: the bind fails, so no
	// port opens to the network.
	require.NoError(t, stackPortForwardCmd.Flags().Set("address", "127.0.0.1,192.0.2.1"))
	err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "0:3306"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "192.0.2.1")
	assert.Empty(t, stdout.String())
	assert.NotContains(t, stderr.String(), "Warning")
	assert.Equal(t, int32(0), srv.upgrades.Load())
}

func TestPortForwardCmd_LocalhostBindsLoopbackWithoutWarning(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	require.NoError(t, stackPortForwardCmd.Flags().Set("address", "localhost"))
	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	waitForwarding(t, stdout, done)
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.NotContains(t, stderr.String(), "not a loopback address")
}

// addrListener is a net.Listener that only reports an address.
type addrListener struct {
	net.Listener
	addr net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr }

func TestWarnNonLoopback(t *testing.T) {
	t.Parallel()
	listeners := []net.Listener{
		addrListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3306}},
		addrListener{addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 3306}},
		addrListener{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 3306}},
		addrListener{addr: &net.TCPAddr{IP: net.IPv4zero, Port: 3306}},
	}
	var buf bytes.Buffer
	warnNonLoopback(&buf, listeners)
	assert.Equal(t, "Warning: 192.0.2.1 is not a loopback address: anyone who can reach it uses your access to the stack\n"+
		"Warning: 0.0.0.0 is not a loopback address: anyone who can reach it uses your access to the stack\n", buf.String())
}

func TestPlaintextRemoteURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want bool
	}{
		{"http://localhost:8081", false},
		{"http://LOCALHOST:8081", false},
		{"http://127.0.0.1:8081", false},
		{"http://127.0.0.2", false},
		{"http://[::1]:8081", false},
		{"https://stacks.example.com", false},
		{"http://stacks.example.com", true},
		{"HTTP://stacks.example.com", true},
		{"http://192.0.2.1:8081", true},
		{"http://localhost.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, plaintextRemoteURL(tt.url))
		})
	}
}

func TestPortForwardCmd_PlainHTTPRemoteNeedsAllowFlag(t *testing.T) {
	// Refused before any request: stacks.example.com is never contacted.
	stdout, _ := setupPortForwardTest(t, "http://stacks.example.com")
	err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "0:3306"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refuses an http:// API URL")
	assert.Contains(t, err.Error(), "--allow-insecure-stream")
	assert.Empty(t, stdout.String())
}

func TestPortForwardCmd_ConnectionLimit(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	old := portForwardConnLimit
	portForwardConnLimit = 2
	t.Cleanup(func() { portForwardConnLimit = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	roundTrip := func(conn net.Conn) error {
		if _, err := conn.Write([]byte("ping")); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err := io.ReadFull(conn, make([]byte, 4))
		return err
	}
	var open []net.Conn
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, roundTrip(conn))
		open = append(open, conn)
	}

	over, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer over.Close()
	_ = over.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = over.Read(make([]byte, 1))
	assert.Error(t, err, "the connection over the limit is closed")
	assert.Contains(t, stderr.String(), "refused: 2 tunnels are open (the limit)")
	assert.Equal(t, int32(2), srv.upgrades.Load(), "no WebSocket for the refused connection")

	// A closed tunnel frees its slot.
	_ = open[0].Close()
	require.Eventually(t, func() bool { return strings.Contains(stderr.String(), "Connection 1 closed") }, 5*time.Second, 10*time.Millisecond)
	next, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer next.Close()
	require.NoError(t, roundTrip(next))

	cancel()
	require.NoError(t, waitDone(t, done))
}

func TestPortForwardCmd_FatalCloseNotPrintedTwice(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pfTestPath {
			http.NotFound(w, r)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "access revoked"), time.Now().Add(time.Second))
		_, _, _ = ws.ReadMessage()
	}))
	defer srv.Close()
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	done := startPortForward(context.Background(), "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	err = waitDone(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped, deleted or cleaned")
	assert.Contains(t, stderr.String(), "Connection 1 closed")
	assert.Contains(t, stderr.String(), "(the command stops)")
	assert.NotContains(t, stderr.String(), "stopped, deleted or cleaned", "the error prints once, as the command error")
}

func TestPortForwardCmd_DefaultPortInUseTakesNext(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, _ := setupPortForwardTest(t, srv.URL)
	held := holdPorts(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", strconv.Itoa(held))
	got := portOf(t, waitForwarding(t, stdout, done))
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Greater(t, got, held)
	assert.LessOrEqual(t, got, held+portForwardTries)
	assert.Contains(t, stdout.String(), fmt.Sprintf("-> mysql:%d", held))
}

func TestPortForwardCmd_AllTriedPortsInUseFallsBack(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, _ := setupPortForwardTest(t, srv.URL)
	held := holdPorts(t, portForwardTries+1)
	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", strconv.Itoa(held))
	got := portOf(t, waitForwarding(t, stdout, done))
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.False(t, got >= held && got <= held+portForwardTries, "port %d is in the held range", got)
}

func TestPortForwardCmd_ExplicitPortInUse(t *testing.T) {
	srv := newPFTestServer(t, nil)
	_, _ = setupPortForwardTest(t, srv.URL)
	held := holdPorts(t, 1)
	err := waitDone(t, startPortForward(context.Background(), "42", "mysql", fmt.Sprintf("%d:3306", held)))
	require.Error(t, err)
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%d is in use; choose another LOCAL port, or leave it out to let stackctl choose one", held), err.Error())
	assert.Equal(t, int32(0), srv.upgrades.Load(), "no WebSocket")
}

func TestPortForwardCmd_InsecureNeedsAllowFlag(t *testing.T) {
	srv := newPFTestServer(t, nil)
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	flagInsecure = true

	err := waitDone(t, startPortForward(context.Background(), "my-stack", "mysql", "0:3306"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--allow-insecure-stream")
	assert.Equal(t, int32(0), srv.requests.Load(), "refused before any request")
	assert.Empty(t, stdout.String())

	require.NoError(t, stackPortForwardCmd.Flags().Set("allow-insecure-stream", "true"))
	ctx, cancel := context.WithCancel(context.Background())
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	waitForwarding(t, stdout, done)
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Contains(t, stderr.String(), "Warning: --allow-insecure-stream")
}

func TestPortForwardCmd_InvalidArgs(t *testing.T) {
	srv := newPFTestServer(t, nil)
	_, _ = setupPortForwardTest(t, srv.URL)
	err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "80:3306"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOCAL must be 0")

	require.NoError(t, stackPortForwardCmd.Flags().Set("address", "not-an-ip"))
	err = waitDone(t, startPortForward(context.Background(), "42", "mysql", "3306"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --address")
	assert.Equal(t, int32(0), srv.requests.Load())
}

func TestPortForwardCmd_Registered(t *testing.T) {
	found, _, err := rootCmd.Find([]string{"stack", "port-forward"})
	require.NoError(t, err)
	assert.Equal(t, stackPortForwardCmd, found)
	assert.True(t, strings.Contains(found.Long, "mysql -h 127.0.0.1 -P 3306"))
	assert.Contains(t, found.Long, "DNS rebinding")
}

func TestPortForwardSignals(t *testing.T) {
	t.Parallel()
	assert.Contains(t, portForwardSignals, os.Interrupt)
	assert.Contains(t, portForwardSignals, syscall.SIGTERM)
}

// dialUntilClosed opens a local connection and waits until the command
// closes it.
func dialUntilClosed(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
	var ne net.Error
	require.False(t, errors.As(err, &ne) && ne.Timeout(), "the refused connection is closed")
}

// fast429Retries shortens the dial retries after a 429.
func fast429Retries(t *testing.T) {
	t.Helper()
	old := portForward429Delays
	portForward429Delays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { portForward429Delays = old })
}

// pfRoundTrip writes "ok" on conn and reads the echo.
func pfRoundTrip(t *testing.T, conn net.Conn) {
	t.Helper()
	_, err := conn.Write([]byte("ok"))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadFull(conn, make([]byte, 2))
	require.NoError(t, err)
}

// refuse429Unless returns a refusal function for newPFTestServer: 429 for
// every dial while allow is false.
func refuse429Unless(dials *atomic.Int32, allow *atomic.Bool) func(http.ResponseWriter) bool {
	return func(w http.ResponseWriter) bool {
		dials.Add(1)
		if allow.Load() {
			return false
		}
		return jsonRefusal(http.StatusTooManyRequests, "tunnel_limit", "Too many tunnels")(w)
	}
}

func TestPortForwardCmd_429RetrySucceeds(t *testing.T) {
	fast429Retries(t)
	var dials atomic.Int32
	srv := newPFTestServer(t, func(w http.ResponseWriter) bool {
		if dials.Add(1) <= 2 {
			return jsonRefusal(http.StatusTooManyRequests, "tunnel_limit", "Too many tunnels")(w)
		}
		return false
	})
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	pfRoundTrip(t, conn)
	_ = conn.Close()

	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Equal(t, int32(3), dials.Load(), "two retries, then the upgrade")
	assert.Equal(t, int32(1), srv.upgrades.Load())
	assert.NotContains(t, stderr.String(), "HTTP 429", "a retried refusal is not a failed connection")
}

func TestPortForwardCmd_Repeated429StopsCommand(t *testing.T) {
	fast429Retries(t)
	var dials atomic.Int32
	var allow atomic.Bool
	srv := newPFTestServer(t, refuse429Unless(&dials, &allow))
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	done := startPortForward(context.Background(), "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	for i := 0; i < portForwardMax429; i++ {
		dialUntilClosed(t, addr)
	}
	err := waitDone(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the server refused 3 connections in a row with 429 (rate limit or tunnel limit) while no other connection was open; wait and try again")
	assert.Equal(t, int32(3*(1+len(portForward429Delays))), dials.Load(), "each connection retries the dial")
	assert.Equal(t, int32(0), srv.upgrades.Load())
	assert.Equal(t, 2, strings.Count(stderr.String(), "HTTP 429"), "one line for each refused connection below the limit")

	_, err = net.DialTimeout("tcp", addr, time.Second)
	assert.Error(t, err, "the listener is closed")
}

func TestPortForwardCmd_SuccessResets429Count(t *testing.T) {
	// refused, refused, upgrade, refused, refused: never 3 in a row.
	fast429Retries(t)
	var dials atomic.Int32
	var allow atomic.Bool
	srv := newPFTestServer(t, refuse429Unless(&dials, &allow))
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	dialUntilClosed(t, addr)
	dialUntilClosed(t, addr)
	allow.Store(true)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	pfRoundTrip(t, conn)
	_ = conn.Close()
	allow.Store(false)
	require.Eventually(t, func() bool { return strings.Contains(stderr.String(), "Connection 3 closed") }, 5*time.Second, 10*time.Millisecond)
	dialUntilClosed(t, addr)
	dialUntilClosed(t, addr)

	select {
	case err := <-done:
		t.Fatalf("command ended: %v", err)
	default:
	}
	cancel()
	require.NoError(t, waitDone(t, done))
	assert.Equal(t, int32(1), srv.upgrades.Load())
	assert.Equal(t, 4, strings.Count(stderr.String(), "HTTP 429"))
}

// The server tunnel limit is full with tunnels of this command: further
// connections are refused, the open tunnel keeps working and the command
// keeps running.
func TestPortForwardCmd_429WithOpenTunnelKeepsCommand(t *testing.T) {
	fast429Retries(t)
	var dials atomic.Int32
	var allow atomic.Bool
	allow.Store(true)
	srv := newPFTestServer(t, refuse429Unless(&dials, &allow))
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	open, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer open.Close()
	pfRoundTrip(t, open)
	allow.Store(false)

	for i := 0; i < portForwardMax429+1; i++ {
		dialUntilClosed(t, addr)
	}
	select {
	case err := <-done:
		t.Fatalf("command ended: %v", err)
	default:
	}
	pfRoundTrip(t, open)
	assert.Equal(t, portForwardMax429+1, strings.Count(stderr.String(), "HTTP 429"))

	// The refusals while the tunnel was open do not count: after it
	// closes, one refusal does not stop the command; 3 in a row do.
	_ = open.Close()
	require.Eventually(t, func() bool { return strings.Contains(stderr.String(), "Connection 1 closed") }, 5*time.Second, 10*time.Millisecond)
	dialUntilClosed(t, addr)
	select {
	case err := <-done:
		t.Fatalf("command ended after one refusal without a tunnel: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	dialUntilClosed(t, addr)
	dialUntilClosed(t, addr)
	err = waitDone(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refused 3 connections in a row")
}

// A 429 of the API rate limiter (no tunnel_limit code) is not retried: the
// retries would only add requests to the rate limit window.
func TestPortForwardCmd_RateLimiter429NotRetried(t *testing.T) {
	fast429Retries(t)
	var dials atomic.Int32
	srv := newPFTestServer(t, func(w http.ResponseWriter) bool {
		dials.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Rate limit exceeded"}`))
		return true
	})
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "42", "mysql", "0:3306")
	addr := waitForwarding(t, stdout, done)

	dialUntilClosed(t, addr)
	require.Eventually(t, func() bool { return strings.Contains(stderr.String(), "HTTP 429") }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), dials.Load())
	cancel()
	require.NoError(t, waitDone(t, done))
}

func TestPortForwardCmd_InsecureTextsUnchanged(t *testing.T) {
	// The check moved to checkStreamTransport; the port-forward texts stay.
	t.Run("--insecure", func(t *testing.T) {
		srv := newPFTestServer(t, nil)
		_, _ = setupPortForwardTest(t, srv.URL)
		flagInsecure = true
		err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "0:3306"))
		require.Error(t, err)
		assert.Equal(t, "port-forward refuses --insecure: without TLS verification a man in the middle can read the tunnel data; add --allow-insecure-stream to accept that risk", err.Error())
	})
	t.Run("http remote", func(t *testing.T) {
		_, _ = setupPortForwardTest(t, "http://stacks.invalid")
		err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "0:3306"))
		require.Error(t, err)
		assert.Equal(t, "port-forward refuses an http:// API URL to a host that is not loopback: a man in the middle can read the tunnel data; use https, or add --allow-insecure-stream to accept that risk", err.Error())
	})
	t.Run("warning", func(t *testing.T) {
		srv := newPFTestServer(t, nil)
		stdout, stderr := setupPortForwardTest(t, srv.URL)
		flagInsecure = true
		require.NoError(t, stackPortForwardCmd.Flags().Set("allow-insecure-stream", "true"))
		ctx, cancel := context.WithCancel(context.Background())
		done := startPortForward(ctx, "42", "mysql", "0:3306")
		waitForwarding(t, stdout, done)
		cancel()
		require.NoError(t, waitDone(t, done))
		assert.Contains(t, stderr.String(), "Warning: --allow-insecure-stream: the tunnel data is not protected against a man in the middle\n")
	})
}
