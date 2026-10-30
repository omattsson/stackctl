//go:build !windows

package cmd

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

const (
	pfSigtermHelperEnv = "STACKCTL_TEST_PF_SIGTERM_HELPER"
	pfSigtermURLEnv    = "STACKCTL_TEST_PF_SIGTERM_URL"
)

// firstLineWriter sends the first line written to it.
type firstLineWriter struct {
	buf  bytes.Buffer
	line chan<- string
	sent bool
}

func (w *firstLineWriter) Write(p []byte) (int, error) {
	if !w.sent {
		w.buf.Write(p)
		if line, _, ok := strings.Cut(w.buf.String(), "\n"); ok {
			w.sent = true
			w.line <- strings.TrimSpace(line)
		}
	}
	return len(p), nil
}

// TestPortForwardSigtermHelper runs the real command in the subprocess of
// TestPortForwardCmd_SigtermClosesTunnels.
func TestPortForwardSigtermHelper(t *testing.T) {
	if os.Getenv(pfSigtermHelperEnv) != "1" {
		t.Skip("helper for TestPortForwardCmd_SigtermClosesTunnels")
	}
	SetArgs([]string{"stack", "port-forward", "42", "mysql", "0:3306", "-q",
		"--address", "127.0.0.1", "--api-url", os.Getenv(pfSigtermURLEnv)})
	if err := Execute(); err != nil {
		os.Exit(3)
	}
}

func TestPortForwardCmd_SigtermClosesTunnels(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	const timeout = 10 * time.Second

	closeCodes := make(chan int, 1)
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
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				code := 0
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				closeCodes <- code
				return
			}
			if err := ws.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "STACKCTL_") {
			env = append(env, kv)
		}
	}
	env = append(env, pfSigtermHelperEnv+"=1", pfSigtermURLEnv+"="+srv.URL, "STACKCTL_CONFIG_DIR="+t.TempDir())

	sub := exec.Command(os.Args[0], "-test.run=^TestPortForwardSigtermHelper$", "-test.count=1")
	sub.Env = env
	var subErr lockedBuffer
	sub.Stderr = &subErr
	addrCh := make(chan string, 1)
	sub.Stdout = &firstLineWriter{line: addrCh}
	require.NoError(t, sub.Start())
	exited := make(chan error, 1)
	waitStarted := false
	defer func() {
		if !waitStarted {
			_ = sub.Process.Kill()
			_ = sub.Wait()
		}
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(timeout):
		t.Fatal("no host:port on stdout")
	}
	require.Regexp(t, `^127\.0\.0\.1:\d+$`, addr, "stderr: %s", subErr.String())

	conn, err := net.DialTimeout("tcp", addr, timeout)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("x"))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	got := make([]byte, 1)
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err, "the tunnel is open")
	assert.Equal(t, "x", string(got))

	require.NoError(t, sub.Process.Signal(syscall.SIGTERM))
	waitStarted = true
	go func() { exited <- sub.Wait() }()
	select {
	case err := <-exited:
		require.NoError(t, err, "exit code 0; stderr: %s", subErr.String())
	case <-time.After(timeout):
		_ = sub.Process.Kill()
		<-exited
		t.Fatalf("the subprocess did not exit after SIGTERM; stderr: %s", subErr.String())
	}
	select {
	case code := <-closeCodes:
		assert.Equal(t, websocket.CloseNormalClosure, code)
	case <-time.After(timeout):
		t.Fatal("the server got no close")
	}

	_ = conn.Close()
	srv.Close()
	goleak.VerifyNone(t, ignore)
}
