package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
}

func newPFTestServer(t *testing.T, refuse func(w http.ResponseWriter) bool) *pfTestServer {
	t.Helper()
	s := &pfTestServer{}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
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

func TestPortForwardCmd_NonLoopbackWarning(t *testing.T) {
	srv := newPFTestServer(t, nil)
	_, stderr := setupPortForwardTest(t, srv.URL)
	// 192.0.2.1 (TEST-NET-1) is not a local address: the bind fails after
	// the warning, so no port opens to the network.
	require.NoError(t, stackPortForwardCmd.Flags().Set("address", "127.0.0.1,192.0.2.1"))
	err := waitDone(t, startPortForward(context.Background(), "42", "mysql", "0:3306"))
	require.Error(t, err)
	assert.Contains(t, stderr.String(), "Warning: 192.0.2.1 is not a loopback address")
	assert.NotContains(t, stderr.String(), "Warning: 127.0.0.1")
	assert.Equal(t, int32(0), srv.upgrades.Load())
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
