package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// Tests in this file are NOT parallelized: they mutate package globals
// (cfg, printer, flag variables, execSignals, execForceExit) and run
// rootCmd.

const execCmdTestPath = "/api/v1/stack-instances/42/exec"

// execCmdServer serves the exec route of instance 42 and the stack list
// (name resolution: "my-stack" is 42, every other name has no match). It
// counts all requests and the upgrades, and records the query and the
// command of the last exec. refuse, when set, answers the exec route
// without an upgrade; handler runs after the start message.
type execCmdServer struct {
	*httptest.Server
	requests atomic.Int32
	upgrades atomic.Int32
	lists    atomic.Int32
	mu       sync.Mutex
	query    url.Values
	command  []string
	// listDelay delays the stack list answer.
	listDelay time.Duration
}

func (s *execCmdServer) lastQuery() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query
}

func (s *execCmdServer) lastCommand() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.command
}

func newExecCmdServer(t *testing.T, refuse func(w http.ResponseWriter) bool, handler func(ws *websocket.Conn)) *execCmdServer {
	t.Helper()
	s := &execCmdServer{}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		switch r.URL.Path {
		case "/api/v1/stack-instances":
			s.lists.Add(1)
			if s.listDelay > 0 {
				time.Sleep(s.listDelay)
			}
			data := []map[string]any{}
			if r.URL.Query().Get("name") == "my-stack" {
				data = append(data, map[string]any{"id": "42", "name": "my-stack", "status": "running"})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": len(data), "page": 1, "pageSize": 25})
			return
		case execCmdTestPath:
		default:
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.query = r.URL.Query()
		s.mu.Unlock()
		if refuse != nil && refuse(w) {
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.upgrades.Add(1)
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Time{})
		var start types.ExecStartMessage
		if json.Unmarshal(raw, &start) != nil || start.Type != "start" {
			return
		}
		s.mu.Lock()
		s.command = start.Command
		s.mu.Unlock()
		if handler != nil {
			handler(ws)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func wsJSON(ws *websocket.Conn, v any) {
	data, _ := json.Marshal(v)
	_ = ws.WriteMessage(websocket.TextMessage, data)
}

func wsStream(ws *websocket.Conn, stream byte, data string) {
	_ = ws.WriteMessage(websocket.BinaryMessage, append([]byte{stream}, data...))
}

func wsCloseAndDrain(ws *websocket.Conn, code int, reason string) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
	}
}

// exitWith sends started, the output and the exit code.
func exitWith(code int, stdout, stderr string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		wsJSON(ws, map[string]any{"type": "started", "pod": "web-0", "container": "php"})
		if stdout != "" {
			wsStream(ws, client.ExecStreamStdout, stdout)
		}
		if stderr != "" {
			wsStream(ws, client.ExecStreamStderr, stderr)
		}
		wsJSON(ws, map[string]any{"type": "exit", "exit_code": code})
		wsCloseAndDrain(ws, websocket.CloseNormalClosure, "")
	}
}

// closeWith sends started, then the close code without an exit frame.
func closeWith(code int, reason string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		wsJSON(ws, map[string]any{"type": "started", "pod": "web-0", "container": "php"})
		wsCloseAndDrain(ws, code, reason)
	}
}

// waitClientClose sends started and "x", then waits for the close of the
// client without answering it, and sends the close code to gotClose.
// afterClose runs after the close; then the server waits until the client
// drops the connection.
func waitClientClose(gotClose chan<- int, afterClose func(ws *websocket.Conn)) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		ws.SetCloseHandler(func(int, string) error { return nil })
		wsJSON(ws, map[string]any{"type": "started", "pod": "web-0", "container": "php"})
		wsStream(ws, client.ExecStreamStdout, "x")
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _, err := ws.ReadMessage()
		code := 0
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			code = ce.Code
		}
		gotClose <- code
		if afterClose != nil {
			afterClose(ws)
		}
		_ = ws.NetConn().SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _ = io.Copy(io.Discard, ws.NetConn())
	}
}

// execTest is a run of stackctl stack exec through rootCmd.
type execTest struct {
	stdout, stderr *lockedBuffer
	signals        chan os.Signal
	forced         chan int
}

// setupExecTest prepares the globals for rootCmd runs of stack exec: the
// config directory, the API URL, the writers, an injected signal channel
// and a recording execForceExit. Everything is restored at the end.
func setupExecTest(t *testing.T, apiURL string) *execTest {
	t.Helper()
	setupStackTestCmd(t, apiURL)
	e := &execTest{
		stdout: &lockedBuffer{}, stderr: &lockedBuffer{},
		signals: make(chan os.Signal, 2), forced: make(chan int, 2),
	}
	prevSignals, prevForce := execSignals, execForceExit
	prevKeepalive, prevWait := execKeepalive, execCancelWait
	execSignals = func() (<-chan os.Signal, func()) { return e.signals, func() {} }
	execForceExit = func(code int) { e.forced <- code }
	execCancelWait = 300 * time.Millisecond
	t.Cleanup(func() {
		execSignals, execForceExit = prevSignals, prevForce
		execKeepalive, execCancelWait = prevKeepalive, prevWait
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		// pflag keeps the values, Changed and the "--" position of the last
		// parse; reset them for the next Execute in this binary.
		resetParsedFlags(stackExecCmd)
		resetParsedFlags(rootCmd)
	})
	rootCmd.SetOut(e.stdout)
	rootCmd.SetErr(e.stderr)
	return e
}

// start runs "stackctl stack exec args..." in a goroutine.
func (e *execTest) start(args ...string) <-chan error {
	rootCmd.SetArgs(append([]string{"stack", "exec"}, args...))
	done := make(chan error, 1)
	go func() { done <- rootCmd.Execute() }()
	return done
}

func (e *execTest) run(t *testing.T, args ...string) error {
	t.Helper()
	return waitDone(t, e.start(args...))
}

// requireExit checks that err is an *ExitError with code and returns it.
func requireExit(t *testing.T, err error, code int) *ExitError {
	t.Helper()
	var ee *ExitError
	require.ErrorAs(t, err, &ee, "error %v", err)
	assert.Equal(t, code, ee.Code, "exit code; error %v", err)
	return ee
}

func TestStackExecCmd_UsageErrorsExit1WithoutDial(t *testing.T) {
	many := []string{"42", "web", "--"}
	for i := 0; i <= client.ExecMaxArgs; i++ {
		many = append(many, "x")
	}
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"missing --", []string{"42", "web", "ls"}, "missing '--' before the command"},
		{"missing -- without a command", []string{"42", "web"}, "missing '--' before the command"},
		{"-- after the stack", []string{"42", "--", "web", "ls"}, "expected <name|id> and <chart> before '--', got 1 argument(s)"},
		{"-- first", []string{"--", "42", "web", "ls"}, "expected <name|id> and <chart> before '--', got 0 argument(s)"},
		{"-- after three words", []string{"42", "web", "extra", "--", "ls"}, "expected <name|id> and <chart> before '--', got 3 argument(s)"},
		{"no command after --", []string{"42", "web", "--"}, "missing command after '--'"},
		{"empty stack", []string{" ", "web", "--", "ls"}, "stack name or ID must not be empty"},
		{"empty chart", []string{"42", "", "--", "ls"}, "chart name must not be empty"},
		{"empty program", []string{"42", "web", "--", "", "x"}, "invalid command: the first argument of the command (the program) is empty"},
		{"NUL byte", []string{"42", "web", "--", "ls", "a\x00b"}, "invalid command: argument 2 of the command contains a NUL byte"},
		{"too many arguments", many, "invalid command: the command has 257 arguments; the server allows at most 256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newExecCmdServer(t, nil, exitWith(0, "", ""))
			e := setupExecTest(t, srv.URL)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			var ee *ExitError
			assert.False(t, errors.As(err, &ee), "a usage error exits 1, not with an ExitError")
			assert.Equal(t, int32(0), srv.requests.Load(), "no request")
			assert.Empty(t, e.stdout.String())
		})
	}
}

func TestStackExecArgs_ResetBetweenParses(t *testing.T) {
	// The "--" position of one parse must not leak into the next one.
	require.NoError(t, stackExecCmd.ParseFlags([]string{"42", "web", "--", "ls"}))
	require.NoError(t, stackExecArgs(stackExecCmd, stackExecCmd.Flags().Args()))
	resetParsedFlags(stackExecCmd)
	require.NoError(t, stackExecCmd.ParseFlags([]string{"42", "web", "ls"}))
	t.Cleanup(func() { resetParsedFlags(stackExecCmd) })
	err := stackExecArgs(stackExecCmd, stackExecCmd.Flags().Args())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing '--'")
}

func TestStackExecCmd_ContainerAndPassThrough(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantContainer string
		wantCommand   []string
	}{
		{"no -c leaves out container", []string{"42", "web", "--", "ls"}, "", []string{"ls"}},
		{"-c sends container", []string{"42", "web", "-c", "php", "--", "ls"}, "php", []string{"ls"}},
		{"--container before the stack", []string{"--container=php", "42", "web", "--", "ls"}, "php", []string{"ls"}},
		{"flags after -- go to the remote command", []string{"42", "web", "--", "ls", "-la", "--help", "-c", "x"}, "",
			[]string{"ls", "-la", "--help", "-c", "x"}},
		{"global flags after -- are not parsed", []string{"42", "web", "-c", "php", "--", "sh", "-c", "echo hi", "-o", "json", "--debug", "--", "-q"}, "php",
			[]string{"sh", "-c", "echo hi", "-o", "json", "--debug", "--", "-q"}},
		{"name resolution", []string{"my-stack", "web", "--", "true"}, "", []string{"true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newExecCmdServer(t, nil, exitWith(0, "ok\n", ""))
			e := setupExecTest(t, srv.URL)
			require.NoError(t, e.run(t, tt.args...))
			assert.Equal(t, "ok\n", e.stdout.String(), "stdout is only the remote stdout")
			assert.Empty(t, e.stderr.String())
			assert.Equal(t, tt.wantCommand, srv.lastCommand())
			q := srv.lastQuery()
			assert.Equal(t, "web", q.Get("chart"))
			_, has := q["container"]
			assert.Equal(t, tt.wantContainer != "", has, "container in the query")
			assert.Equal(t, tt.wantContainer, q.Get("container"))
			assert.Len(t, q, map[bool]int{true: 2, false: 1}[has], "only chart and container in the query")
			assert.Equal(t, int32(1), srv.upgrades.Load())
			assert.False(t, flagDebug, "--debug after -- is not a stackctl flag")
		})
	}
}

func TestStackExecCmd_ExitMapping(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		refuse     func(w http.ResponseWriter) bool
		handler    func(ws *websocket.Conn)
		wantNil    bool
		wantCode   int
		wantErrNil bool
		wantMsg    []string
		wantDial   bool
		wantStdout string
		check      func(t *testing.T, err error)
	}{
		{name: "exit 0", handler: exitWith(0, "out", "err"), wantStdout: "out", wantNil: true, wantDial: true},
		{name: "exit 7 passes through without a message", handler: exitWith(7, "out", "err"), wantStdout: "out", wantCode: 7, wantErrNil: true, wantDial: true},
		{name: "exit 1", handler: exitWith(1, "out", "err"), wantStdout: "out", wantCode: 1, wantErrNil: true, wantDial: true},
		{name: "exit 255", handler: exitWith(255, "out", "err"), wantStdout: "out", wantCode: 255, wantErrNil: true, wantDial: true},
		{name: "exit 256 is clamped to 255", handler: exitWith(256, "out", "err"), wantStdout: "out", wantCode: 255, wantDial: true,
			wantMsg: []string{"the command exited with code 256, which is outside 0 to 255"}},
		{name: "exit -1 is clamped to 255", handler: exitWith(-1, "out", "err"), wantStdout: "out", wantCode: 255, wantDial: true,
			wantMsg: []string{"the command exited with code -1, which is outside 0 to 255"}},
		{name: "time limit gives 124", handler: closeWith(client.CloseMaxDuration, "maximum duration reached"), wantCode: 124, wantDial: true,
			wantMsg: []string{"server time limit reached"},
			check:   func(t *testing.T, err error) { assert.ErrorIs(t, err, client.ErrExecTimeLimit) }},
		{name: "close without an exit frame gives 255", handler: closeWith(websocket.CloseNormalClosure, ""), wantCode: 255, wantDial: true,
			wantMsg: []string{"connection lost; the command can still run on the server"}},
		{name: "refused command gives 255", handler: closeWith(client.CloseBadStart, "command too long"), wantCode: 255, wantDial: true,
			wantMsg: []string{"the server refused the command (too long or not valid): command too long"}},
		{name: "revoked session gives 255", handler: closeWith(websocket.ClosePolicyViolation, "session revoked"), wantCode: 255, wantDial: true,
			wantMsg: []string{"exec closed: session revoked (close 1008); log in again with 'stackctl login'; the command can still run on the server"}},
		{name: "a close before started says so", wantCode: 255, wantDial: true,
			handler: func(ws *websocket.Conn) { wsCloseAndDrain(ws, websocket.CloseGoingAway, "server shutdown") },
			wantMsg: []string{"exec closed: the server is shutting down; the server did not start the command"}},
		{name: "403 container not allowed", args: []string{"42", "web", "-c", "php", "--", "ls"}, wantCode: 255,
			refuse:  jsonRefusal(http.StatusForbidden, "container_not_allowed", "The container is not allowed for exec"),
			wantMsg: []string{`exec refused by the server (HTTP 403): The container is not allowed for exec: "php". Use -c with a container that allows exec, or ask an administrator which containers do.`}},
		{name: "404 feature disabled", wantCode: 255,
			refuse:  jsonRefusal(http.StatusNotFound, "feature_disabled", "This feature is disabled"),
			wantMsg: []string{"The server has exec turned off"}},
		{name: "plain 404", wantCode: 255,
			refuse: func(w http.ResponseWriter) bool {
				http.NotFound(w, nil)
				return true
			},
			wantMsg: []string{"the server has no exec route (HTTP 404); upgrade the server to " + client.ExecServerVersion}},
		{name: "503 no ready pod", wantCode: 255,
			refuse:  jsonRefusal(http.StatusServiceUnavailable, "no_ready_pod", "No ready pod for the chart"),
			wantMsg: []string{"exec refused by the server (HTTP 503): No ready pod for the chart. Check 'stackctl stack pods', then try again."}},
		{name: "unknown stack name", args: []string{"nope", "web", "--", "ls"}, wantCode: 255,
			handler: exitWith(0, "", ""),
			wantMsg: []string{`no stack found with name "nope"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newExecCmdServer(t, tt.refuse, tt.handler)
			e := setupExecTest(t, srv.URL)
			args := tt.args
			if args == nil {
				args = []string{"42", "web", "--", "ls"}
			}
			err := e.run(t, args...)
			if tt.wantNil {
				require.NoError(t, err)
			} else {
				ee := requireExit(t, err, tt.wantCode)
				if tt.wantErrNil {
					assert.NoError(t, ee.Err, "no message for a remote exit code")
					assert.Equal(t, fmt.Sprintf("exit status %d", tt.wantCode), ee.Error())
				} else {
					require.Error(t, ee.Err)
				}
				for _, m := range tt.wantMsg {
					assert.Contains(t, err.Error(), m)
				}
			}
			assert.Equal(t, tt.wantStdout, e.stdout.String(), "stdout holds only the remote stdout")
			if tt.check != nil {
				tt.check(t, err)
			}
			if tt.wantDial {
				assert.Equal(t, int32(1), srv.upgrades.Load())
			} else {
				assert.Equal(t, int32(0), srv.upgrades.Load())
			}
		})
	}
}

func TestStackExecCmd_RemoteExitPrintsNothingExtra(t *testing.T) {
	srv := newExecCmdServer(t, nil, exitWith(7, "remote out\n", "remote err\n"))
	e := setupExecTest(t, srv.URL)
	err := e.run(t, "42", "web", "--", "false")
	ee := requireExit(t, err, 7)
	assert.Nil(t, ee.Err)
	assert.Equal(t, "remote out\n", e.stdout.String())
	assert.Equal(t, "remote err\n", e.stderr.String(), "stderr holds only the remote stderr")
}

func TestStackExecCmd_OutputFlagsDoNotChangeStdout(t *testing.T) {
	const remote = "\x00binary\xff\n{\"not\": \"json\"}\n"
	for _, flags := range [][]string{nil, {"-o", "json"}, {"-o", "yaml"}, {"-q"}, {"-o", "json", "-q"}} {
		t.Run(strings.Join(append([]string{"default"}, flags...), " "), func(t *testing.T) {
			srv := newExecCmdServer(t, nil, exitWith(0, remote, "remote err"))
			e := setupExecTest(t, srv.URL)
			args := append(append([]string{}, flags...), "42", "web", "--", "cat", "x")
			require.NoError(t, e.run(t, args...))
			assert.Equal(t, remote, e.stdout.String(), "stdout is exactly the remote stdout")
			assert.Equal(t, "remote err", e.stderr.String())
		})
	}
}

func TestStackExecCmd_DebugShowsPodButNeverOutputOrCommand(t *testing.T) {
	srv := newExecCmdServer(t, nil, exitWith(0, "SECRET-STDOUT", "remote-stderr"))
	e := setupExecTest(t, srv.URL)
	require.NoError(t, e.run(t, "--debug", "my-stack", "web", "--", "printenv", "SECRET-ARG"))
	stderr := e.stderr.String()
	assert.Contains(t, stderr, "exec: started in pod web-0, container php")
	assert.NotContains(t, stderr, "SECRET-STDOUT", "remote stdout never on stderr")
	assert.NotContains(t, stderr, "SECRET-ARG", "the argv is never logged")
	assert.NotContains(t, stderr, "printenv")
	assert.Equal(t, 1, strings.Count(stderr, "remote-stderr"), "remote stderr once, not logged again")
	assert.Equal(t, "SECRET-STDOUT", e.stdout.String())
	assert.Equal(t, int32(1), srv.lists.Load(), "the name was resolved")
}

func TestStackExecCmd_InsecureNeedsAllowFlag(t *testing.T) {
	t.Run("--insecure is refused before any request", func(t *testing.T) {
		srv := newExecCmdServer(t, nil, exitWith(0, "out", ""))
		e := setupExecTest(t, srv.URL)
		err := e.run(t, "--insecure", "my-stack", "web", "--", "ls")
		requireExit(t, err, 255)
		assert.Equal(t, "exec refuses --insecure: without TLS verification a man in the middle can read the commands and their output; add --allow-insecure-stream to accept that risk", err.Error())
		assert.Equal(t, int32(0), srv.requests.Load(), "refused before any request")
		assert.Empty(t, e.stdout.String())
	})
	t.Run("insecure context is refused", func(t *testing.T) {
		srv := newExecCmdServer(t, nil, exitWith(0, "out", ""))
		e := setupExecTest(t, srv.URL)
		dir := os.Getenv("STACKCTL_CONFIG_DIR")
		require.NotEmpty(t, dir)
		cfgYAML := "current-context: test\ncontexts:\n  test:\n    api-url: " + srv.URL + "\n    insecure: true\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o600))
		err := e.run(t, "42", "web", "--", "ls")
		requireExit(t, err, 255)
		assert.Contains(t, err.Error(), "exec refuses --insecure")
		assert.Equal(t, int32(0), srv.requests.Load())
	})
	t.Run("http to a remote host is refused", func(t *testing.T) {
		// .invalid never resolves (RFC 2606): a dial would fail differently.
		e := setupExecTest(t, "http://stacks.invalid")
		err := e.run(t, "42", "web", "--", "ls")
		requireExit(t, err, 255)
		assert.Equal(t, "exec refuses an http:// API URL to a host that is not loopback: a man in the middle can read the commands and their output; use https, or add --allow-insecure-stream to accept that risk", err.Error())
		assert.Empty(t, e.stdout.String())
	})
	t.Run("--allow-insecure-stream warns and runs", func(t *testing.T) {
		srv := newExecCmdServer(t, nil, exitWith(0, "out", ""))
		e := setupExecTest(t, srv.URL)
		require.NoError(t, e.run(t, "--insecure", "--allow-insecure-stream", "42", "web", "--", "ls"))
		assert.Contains(t, e.stderr.String(), "Warning: --allow-insecure-stream: the commands and their output are not protected against a man in the middle\n")
		assert.Equal(t, "out", e.stdout.String())
		assert.Equal(t, int32(1), srv.upgrades.Load())
	})
}

// waitStdout waits until the buffer holds want.
func waitStdout(t *testing.T, b *lockedBuffer, want string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.String() != want {
		select {
		case err := <-done:
			t.Fatalf("command ended early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdout %q, want %q", b.String(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitCloseCode(t *testing.T, gotClose <-chan int) int {
	t.Helper()
	select {
	case code := <-gotClose:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no close")
		return 0
	}
}

func TestStackExecCmd_InterruptExits130(t *testing.T) {
	t.Run("no exit frame", func(t *testing.T) {
		gotClose := make(chan int, 1)
		srv := newExecCmdServer(t, nil, waitClientClose(gotClose, nil))
		e := setupExecTest(t, srv.URL)
		ignore := goleak.IgnoreCurrent()

		done := e.start("42", "web", "--", "sleep", "100")
		waitStdout(t, e.stdout, "x", done)
		e.signals <- os.Interrupt
		assert.Equal(t, websocket.CloseNormalClosure, waitCloseCode(t, gotClose), "Ctrl-C sends close 1000")
		err := waitDone(t, done)
		ee := requireExit(t, err, 130)
		assert.Equal(t, "interrupted; the command can still run on the server", ee.Err.Error())
		select {
		case code := <-e.forced:
			t.Fatalf("execForceExit(%d) after one signal", code)
		default:
		}
		srv.Close()
		goleak.VerifyNone(t, ignore)
	})
	t.Run("exit frame inside the wait", func(t *testing.T) {
		gotClose := make(chan int, 1)
		srv := newExecCmdServer(t, nil, waitClientClose(gotClose, func(ws *websocket.Conn) {
			wsJSON(ws, map[string]any{"type": "exit", "exit_code": 143})
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		}))
		e := setupExecTest(t, srv.URL)
		execCancelWait = 5 * time.Second
		done := e.start("42", "web", "--", "sleep", "100")
		waitStdout(t, e.stdout, "x", done)
		start := time.Now()
		e.signals <- syscall.SIGTERM
		err := waitDone(t, done)
		ee := requireExit(t, err, 130)
		assert.Equal(t, "interrupted; the command ended with exit code 143", ee.Err.Error())
		assert.Less(t, time.Since(start), 3*time.Second, "returns at the exit frame")
		assert.Equal(t, websocket.CloseNormalClosure, waitCloseCode(t, gotClose))
	})
	t.Run("during the dial", func(t *testing.T) {
		// The server holds the upgrade until the end of the test.
		dialing, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		srv := newExecCmdServer(t, func(http.ResponseWriter) bool {
			once.Do(func() { close(dialing) })
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			return true
		}, exitWith(0, "out", ""))
		t.Cleanup(func() { close(release) }) // before srv.Close
		e := setupExecTest(t, srv.URL)
		done := e.start("42", "web", "--", "rm", "-rf", "/tmp/x")
		select {
		case <-dialing:
		case <-time.After(5 * time.Second):
			t.Fatal("the dial did not reach the server")
		}
		start := time.Now()
		e.signals <- os.Interrupt
		ee := requireExit(t, waitDone(t, done), 130)
		assert.Equal(t, "interrupted before the command started", ee.Err.Error())
		assert.Less(t, time.Since(start), 3*time.Second, "Ctrl-C ends the dial")
		assert.Nil(t, srv.lastCommand(), "no start message")
	})
	t.Run("after the dial, before the started frame", func(t *testing.T) {
		gotStart := make(chan struct{})
		srv := newExecCmdServer(t, nil, func(ws *websocket.Conn) {
			close(gotStart)
			// No started frame; the default close handler answers the close
			// of the client.
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = ws.ReadMessage()
		})
		e := setupExecTest(t, srv.URL)
		execCancelWait = 5 * time.Second
		done := e.start("42", "web", "--", "rm", "-rf", "/tmp/x")
		select {
		case <-gotStart:
		case <-time.After(5 * time.Second):
			t.Fatal("the server got no start message")
		}
		start := time.Now()
		e.signals <- os.Interrupt
		ee := requireExit(t, waitDone(t, done), 130)
		assert.Equal(t, "interrupted before the command started", ee.Err.Error())
		assert.Less(t, time.Since(start), 3*time.Second, "the close of the server ends the wait")
		assert.Equal(t, int32(1), srv.upgrades.Load())
	})
	t.Run("before the command started", func(t *testing.T) {
		srv := newExecCmdServer(t, nil, exitWith(0, "out", ""))
		srv.listDelay = 500 * time.Millisecond // the signal is handled during the name resolution
		e := setupExecTest(t, srv.URL)
		e.signals <- os.Interrupt
		err := e.run(t, "my-stack", "web", "--", "ls")
		ee := requireExit(t, err, 130)
		assert.Equal(t, "interrupted before the command started", ee.Err.Error())
		assert.Equal(t, int32(0), srv.upgrades.Load(), "no exec")
		assert.Empty(t, e.stdout.String())
	})
}

func TestStackExecCmd_SecondInterruptForcesExit(t *testing.T) {
	gotClose := make(chan int, 1)
	srv := newExecCmdServer(t, nil, waitClientClose(gotClose, nil))
	e := setupExecTest(t, srv.URL)
	const cancelWait = 3 * time.Second
	execCancelWait = cancelWait
	done := e.start("42", "web", "--", "sleep", "100")
	waitStdout(t, e.stdout, "x", done)

	e.signals <- os.Interrupt
	require.Equal(t, websocket.CloseNormalClosure, waitCloseCode(t, gotClose))
	second := time.Now()
	e.signals <- os.Interrupt
	select {
	case code := <-e.forced:
		assert.Equal(t, 130, code)
		assert.Less(t, time.Since(second), cancelWait/2, "the second signal exits at once, not after the wait")
	case <-time.After(cancelWait / 2):
		t.Fatal("execForceExit was not called after the second signal")
	}
	assert.Contains(t, e.stderr.String(), "interrupted\n")
	// The replaced execForceExit returns: the command still ends after the
	// wait, with 130.
	requireExit(t, waitDone(t, done), 130)
}

// blockingWriter passes writes to buf until a write contains block; from
// then on every write waits for release (a paused 2>&1 | less). blocked is
// closed at the first such write.
type blockingWriter struct {
	buf     *lockedBuffer
	block   string
	on      atomic.Bool
	once    sync.Once
	blocked chan struct{}
	release chan struct{}
	freed   sync.Once
}

func newBlockingWriter(buf *lockedBuffer, block string) *blockingWriter {
	return &blockingWriter{buf: buf, block: block, blocked: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.block) {
		w.on.Store(true)
	}
	if w.on.Load() {
		w.once.Do(func() { close(w.blocked) })
		<-w.release
	}
	return w.buf.Write(p)
}

func (w *blockingWriter) free() { w.freed.Do(func() { close(w.release) }) }

func TestStackExecCmd_SecondInterruptWithABlockedStderr(t *testing.T) {
	// The reader holds the lock of the stderr writer in a blocked write of
	// the remote stderr. The second signal must still reach execForceExit.
	gotClose := make(chan int, 1)
	srv := newExecCmdServer(t, nil, func(ws *websocket.Conn) {
		ws.SetCloseHandler(func(int, string) error { return nil })
		wsJSON(ws, map[string]any{"type": "started", "pod": "web-0", "container": "php"})
		wsStream(ws, client.ExecStreamStderr, "BLOCK\n")
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _, err := ws.ReadMessage()
		code := 0
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			code = ce.Code
		}
		gotClose <- code
		_ = ws.NetConn().SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _ = io.Copy(io.Discard, ws.NetConn())
	})
	e := setupExecTest(t, srv.URL)
	stderr := newBlockingWriter(e.stderr, "BLOCK")
	var done <-chan error
	ended := false
	// Runs before the restore of setupExecTest: free the writer and wait for
	// the command, so a failed run never calls the real execForceExit.
	t.Cleanup(func() {
		stderr.free()
		if done != nil && !ended {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
		}
	})
	rootCmd.SetErr(stderr)
	ignore := goleak.IgnoreCurrent()

	done = e.start("42", "web", "--", "sleep", "100")
	select {
	case <-stderr.blocked:
	case err := <-done:
		t.Fatalf("command ended early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the remote stderr write did not block")
	}
	e.signals <- os.Interrupt
	require.Equal(t, websocket.CloseNormalClosure, waitCloseCode(t, gotClose), "the first signal sends close 1000")
	second := time.Now()
	e.signals <- os.Interrupt
	select {
	case code := <-e.forced:
		assert.Equal(t, 130, code)
		assert.Less(t, time.Since(second), time.Second, "the note waits at most execNoteWait")
	case <-time.After(3 * time.Second):
		t.Fatal("execForceExit was not called: the second signal blocked on stderr")
	}
	select {
	case err := <-done:
		t.Fatalf("command ended while stderr was blocked: %v", err)
	default:
	}

	// The real execForceExit ends the process; the test one returns, so free
	// the writer and let the command end.
	stderr.free()
	err := waitDone(t, done)
	ended = true
	ee := requireExit(t, err, 130)
	assert.Equal(t, "interrupted; the command can still run on the server", ee.Err.Error())
	// Both blocked writes complete after free, in either order.
	assert.Eventually(t, func() bool {
		out := e.stderr.String()
		return strings.Contains(out, "BLOCK\n") && strings.Contains(out, "interrupted\n")
	}, 5*time.Second, 5*time.Millisecond, "stderr %q", e.stderr.String())
	srv.Close()
	goleak.VerifyNone(t, ignore)
}

func TestWriteNoteNoWait(t *testing.T) {
	t.Run("a free writer gets the note", func(t *testing.T) {
		var b lockedBuffer
		writeNoteNoWait(&b, "interrupted", time.Second)
		assert.Equal(t, "interrupted\n", b.String())
	})
	t.Run("a blocked writer delays at most wait", func(t *testing.T) {
		var b lockedBuffer
		w := newBlockingWriter(&b, "")
		w.on.Store(true)
		defer w.free()
		start := time.Now()
		writeNoteNoWait(w, "interrupted", 50*time.Millisecond)
		assert.Less(t, time.Since(start), time.Second)
		assert.Empty(t, b.String())
	})
}

func TestStackExecCmd_Registered(t *testing.T) {
	found, _, err := rootCmd.Find([]string{"stack", "exec"})
	require.NoError(t, err)
	assert.Equal(t, stackExecCmd, found)
	for _, s := range []string{"124", "130", "255", "--allow-insecure-stream", "sh -c", "secrets", "stackctl login", "escape", "OSC 52", "cat -v"} {
		assert.Contains(t, found.Long, s)
	}
	f := found.Flags().ShorthandLookup("c")
	require.NotNil(t, f)
	assert.Equal(t, "container", f.Name)
}

func TestRemoteExit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code     int
		wantNil  bool
		wantCode int
		wantMsg  bool
	}{
		{0, true, 0, false},
		{1, false, 1, false},
		{7, false, 7, false},
		{124, false, 124, false},
		{255, false, 255, false},
		{256, false, 255, true},
		{-1, false, 255, true},
		{1 << 20, false, 255, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.code), func(t *testing.T) {
			t.Parallel()
			err := remoteExit(tt.code)
			if tt.wantNil {
				assert.NoError(t, err)
				return
			}
			var ee *ExitError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, tt.wantCode, ee.Code)
			assert.Equal(t, tt.wantMsg, ee.Err != nil)
		})
	}
}

// TestInterruptedExit covers the exit mapping of an interrupted exec. A
// close frame of the server can win the race with the cancel; without
// started or output before it the command did not run.
func TestInterruptedExit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  *client.ExecResult
		err  error
		want string
	}{
		{name: "exit frame", res: &client.ExecResult{ExitCode: 143},
			want: "interrupted; the command ended with exit code 143"},
		{name: "before the dial", err: errExecNotStarted,
			want: "interrupted before the command started"},
		{name: "canceled before started", err: &client.ExecCanceledError{Err: context.Canceled},
			want: "interrupted before the command started"},
		{name: "canceled after started", err: &client.ExecCanceledError{Started: true, Err: context.Canceled},
			want: "interrupted; the command can still run on the server"},
		{name: "server close before started keeps the server text",
			err:  &client.ExecClosedError{Code: client.CloseGoingAway, Reason: "server shutdown"},
			want: "interrupted before the command started: exec closed: the server is shutting down; the server did not start the command"},
		{name: "server refused the command",
			err:  &client.ExecClosedError{Code: client.CloseBadStart, Reason: "command too long"},
			want: "interrupted before the command started: exec failed: the server refused the command (too long or not valid): command too long"},
		{name: "server close after started", err: &client.ExecClosedError{Code: client.CloseGoingAway, Reason: "server shutdown", Started: true},
			want: "interrupted; the command can still run on the server"},
		{name: "lost connection (no close frame)", err: &client.ExecClosedError{},
			want: "interrupted; the command can still run on the server"},
		{name: "time limit", err: client.ErrExecTimeLimit,
			want: "interrupted; the command can still run on the server"},
		{name: "other error", err: errors.New("boom"),
			want: "interrupted; the command can still run on the server"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := interruptedExit(tt.res, tt.err)
			require.NotNil(t, ee)
			assert.Equal(t, exitInterrupted, ee.Code)
			assert.Equal(t, tt.want, ee.Err.Error())
			var ce *client.ExecClosedError
			if errors.As(tt.err, &ce) && ce.Code != 0 && !ce.Started {
				assert.ErrorAs(t, ee, &ce, "the close error stays in the chain")
			}
		})
	}
}

func TestExitError(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "exit status 7", (&ExitError{Code: 7}).Error())
	assert.Nil(t, (&ExitError{Code: 7}).Unwrap())
	cause := errors.New("boom")
	wrapped := fmt.Errorf("outer: %w", &ExitError{Code: 255, Err: cause})
	var ee *ExitError
	require.ErrorAs(t, wrapped, &ee)
	assert.Equal(t, 255, ee.Code)
	assert.Equal(t, "boom", ee.Error())
	assert.ErrorIs(t, wrapped, cause)
}
