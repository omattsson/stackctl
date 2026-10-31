package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Leaks: the package TestMain runs goleak.VerifyTestMain, which covers the
// goroutines of Exec (reader, pinger) in every test of this file. The tests
// run in parallel, so a per-test goleak.VerifyNone would see the goroutines
// of other tests.

const execPath = "/api/v1/stack-instances/42/exec"

// Secrets of the test clients: no error text may contain them.
const (
	execTestToken  = "eyJ.exec-secret-token.sig"
	execTestAPIKey = "sk_exec_secret_key"
)

// execBuf is a bytes.Buffer that the reader goroutine of Exec and the test
// can use at the same time. notify, when set, gets a signal after each write.
type execBuf struct {
	mu     sync.Mutex
	b      bytes.Buffer
	notify chan struct{}
}

func (e *execBuf) Write(p []byte) (int, error) {
	e.mu.Lock()
	n, err := e.b.Write(p)
	e.mu.Unlock()
	if e.notify != nil {
		select {
		case e.notify <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (e *execBuf) Bytes() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return bytes.Clone(e.b.Bytes())
}

func (e *execBuf) String() string { return string(e.Bytes()) }

// execSrv serves the exec route with handler after the upgrade. It counts
// all requests and keeps the last request of the route.
type execSrv struct {
	*httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	last     *http.Request
}

func (s *execSrv) lastRequest() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func newExecSrv(t *testing.T, handler func(r *http.Request, ws *websocket.Conn)) *execSrv {
	t.Helper()
	s := &execSrv{}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if r.URL.Path != execPath {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.last = r.Clone(context.Background())
		s.mu.Unlock()
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		handler(r, ws)
	}))
	t.Cleanup(s.Close)
	return s
}

// refusingSrv answers every request with status and body, without an
// upgrade, and counts the requests.
func refusingSrv(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// Server side helpers. They run on the handler goroutine and never call
// t.FailNow.

// readStart reads the first client message.
func readStart(ws *websocket.Conn) (mt int, raw []byte, err error) {
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	mt, raw, err = ws.ReadMessage()
	_ = ws.SetReadDeadline(time.Time{})
	return mt, raw, err
}

func sendJSON(ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.WriteMessage(websocket.TextMessage, data)
}

func sendStarted(ws *websocket.Conn, pod, container string) error {
	return sendJSON(ws, map[string]any{"type": "started", "pod": pod, "container": container})
}

// sendStream writes a binary frame: the stream byte, then data.
func sendStream(ws *websocket.Conn, stream byte, data []byte) error {
	return ws.WriteMessage(websocket.BinaryMessage, append([]byte{stream}, data...))
}

// closeAndDrain writes a close frame and reads until the client answers or
// drops the connection.
func closeAndDrain(ws *websocket.Conn, code int, reason string) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	drain(ws)
}

// drain reads until the first read error.
func drain(ws *websocket.Conn) {
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
	}
}

// sendExit writes the exit frame and close 1000, like the server.
func sendExit(ws *websocket.Conn, code int) {
	_ = sendJSON(ws, map[string]any{"type": "exit", "exit_code": code})
	closeAndDrain(ws, websocket.CloseNormalClosure, "")
}

// serveFrames reads the start message, sends started, runs frames and sends
// exit code.
func serveFrames(code int, frames func(ws *websocket.Conn)) func(*http.Request, *websocket.Conn) {
	return func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStarted(ws, "web-0", "php")
		if frames != nil {
			frames(ws)
		}
		sendExit(ws, code)
	}
}

type execRun struct {
	res            *ExecResult
	err            error
	stdout, stderr *execBuf
}

// runExec runs Exec with a 10 s guard, so a hang fails the test.
func runExec(t *testing.T, ctx context.Context, c *Client, req ExecRequest) execRun {
	t.Helper()
	return waitExec(t, startExec(ctx, c, req, &execBuf{}, &execBuf{}))
}

func startExec(ctx context.Context, c *Client, req ExecRequest, stdout, stderr *execBuf) <-chan execRun {
	done := make(chan execRun, 1)
	go func() {
		var so, se io.Writer
		if stdout != nil {
			so = stdout
		}
		if stderr != nil {
			se = stderr
		}
		res, err := c.Exec(ctx, "42", req, so, se)
		done <- execRun{res: res, err: err, stdout: stdout, stderr: stderr}
	}()
	return done
}

func waitExec(t *testing.T, done <-chan execRun) execRun {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("Exec did not return")
		return execRun{}
	}
}

func execReq(command ...string) ExecRequest {
	if len(command) == 0 {
		command = []string{"bin/console", "cache:clear"}
	}
	return ExecRequest{Chart: "backend", Command: command}
}

// assertNoSecret checks that an error text holds no credential.
func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	assert.NotContains(t, err.Error(), execTestToken)
	assert.NotContains(t, err.Error(), execTestAPIKey)
}

// terminalControls are texts that must never reach the terminal from the
// server: ESC (CSI), a C1 CSI, bidi overrides and isolates.
var terminalControls = []string{"\x1b", "\u009b", "\u202e", "\u2066", "\u200f"}

func assertNoTerminalControls(t *testing.T, s string) {
	t.Helper()
	for _, c := range terminalControls {
		assert.NotContains(t, s, c, "terminal control %q in %q", c, s)
	}
}

func TestExec_QueryStartMessageAndAuth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		container     string
		token, apiKey string
		wantHeader    string
		wantValue     string
		noHeader      string
		wantQueryKeys []string
	}{
		{name: "bearer without container", token: execTestToken,
			wantHeader: "Authorization", wantValue: "Bearer " + execTestToken, noHeader: "X-API-Key",
			wantQueryKeys: []string{"chart"}},
		{name: "api key with container", container: "php", apiKey: execTestAPIKey,
			wantHeader: "X-API-Key", wantValue: execTestAPIKey, noHeader: "Authorization",
			wantQueryKeys: []string{"chart", "container"}},
		{name: "api key wins over a token", container: "php", token: execTestToken, apiKey: execTestAPIKey,
			wantHeader: "X-API-Key", wantValue: execTestAPIKey, noHeader: "Authorization",
			wantQueryKeys: []string{"chart", "container"}},
	}
	command := []string{"bin/console", "cache:clear", "--env=prod", "a b&c=d"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			starts := make(chan []byte, 1)
			frameTypes := make(chan int, 1)
			srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
				mt, raw, err := readStart(ws)
				if err != nil {
					return
				}
				frameTypes <- mt
				starts <- raw
				sendExit(ws, 0)
			})
			c := New(srv.URL)
			c.Token, c.APIKey = tt.token, tt.apiKey
			req := ExecRequest{Chart: "back end&x=1", Container: tt.container, Command: command}
			r := runExec(t, context.Background(), c, req)
			require.NoError(t, r.err)
			assert.Equal(t, 0, r.res.ExitCode)

			hr := srv.lastRequest()
			require.NotNil(t, hr)
			assert.Equal(t, http.MethodGet, hr.Method)
			assert.Equal(t, execPath, hr.URL.Path)
			q := hr.URL.Query()
			var keys []string
			for k := range q {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			assert.Equal(t, tt.wantQueryKeys, keys, "only chart and container in the query")
			assert.Equal(t, "back end&x=1", q.Get("chart"), "chart is escaped")
			if tt.container != "" {
				assert.Equal(t, tt.container, q.Get("container"))
			}
			for _, a := range command {
				assert.NotContains(t, hr.URL.RawQuery, strings.ReplaceAll(a, " ", "+"), "the command is not in the URL")
			}
			assert.NotContains(t, hr.URL.String(), "cache")
			assert.Empty(t, q.Get("token"), "no ?token=")
			assert.Equal(t, tt.wantValue, hr.Header.Get(tt.wantHeader))
			assert.Empty(t, hr.Header.Get(tt.noHeader))
			assert.True(t, strings.HasPrefix(hr.Header.Get("User-Agent"), "stackctl/"))

			assert.Equal(t, websocket.TextMessage, <-frameTypes, "the start message is a text frame")
			raw := <-starts
			assert.JSONEq(t, `{"type":"start","command":["bin/console","cache:clear","--env=prod","a b&c=d"]}`, string(raw))
		})
	}
}

func TestExec_NoQueryTokenAfter401(t *testing.T) {
	t.Parallel()
	var hits, withToken atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Has("token") || strings.Contains(r.URL.RawQuery, execTestToken) {
			withToken.Add(1)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.Token = execTestToken
	r := runExec(t, context.Background(), c, execReq())
	var hs *WSHandshakeError
	require.ErrorAs(t, r.err, &hs)
	assert.Equal(t, http.StatusUnauthorized, hs.StatusCode)
	assert.Equal(t, "exec", hs.Op)
	assert.Contains(t, r.err.Error(), "exec refused by the server (HTTP 401)")
	assert.Contains(t, r.err.Error(), "stackctl login")
	assert.Equal(t, int32(1), hits.Load(), "exactly one dial, no ?token= fallback")
	assert.Equal(t, int32(0), withToken.Load(), "the JWT never goes into the URL")
	assertNoSecret(t, r.err)
}

func TestExec_StreamsRawBytesToStdoutAndStderr(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("0123456789abcdef"), 6<<10) // 96 KiB, more than one read chunk
	srv := newExecSrv(t, serveFrames(0, func(ws *websocket.Conn) {
		_ = sendStream(ws, ExecStreamStdout, []byte{0x00, 'a', 0x00})
		_ = sendStream(ws, ExecStreamStderr, []byte{0xff, 0xfe, 'e', 0x80})
		_ = sendStream(ws, ExecStreamStdout, []byte("\xc3\x28 bad utf-8 \r\n"))
		_ = sendStream(ws, ExecStreamStdout, big)
		_ = sendStream(ws, ExecStreamStderr, []byte("\x1b[31mred\x1b[0m\n"))
	}))
	r := runExec(t, context.Background(), New(srv.URL), execReq())
	require.NoError(t, r.err)
	wantOut := append([]byte{0x00, 'a', 0x00}, []byte("\xc3\x28 bad utf-8 \r\n")...)
	wantOut = append(wantOut, big...)
	assert.True(t, bytes.Equal(wantOut, r.stdout.Bytes()), "stdout bytes differ")
	assert.Equal(t, []byte("\xff\xfee\x80\x1b[31mred\x1b[0m\n"), r.stderr.Bytes(), "stderr bytes unchanged")
}

func TestExec_NilWritersDiscard(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, serveFrames(4, func(ws *websocket.Conn) {
		_ = sendStream(ws, ExecStreamStdout, []byte("out"))
		_ = sendStream(ws, ExecStreamStderr, []byte("err"))
	}))
	r := waitExec(t, startExec(context.Background(), New(srv.URL), execReq(), nil, nil))
	require.NoError(t, r.err)
	assert.Equal(t, 4, r.res.ExitCode)
}

func TestExec_StreamsBeforeExit(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStarted(ws, "web-0", "php")
		_ = sendStream(ws, ExecStreamStdout, []byte("first line\n"))
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			return // the client never showed the frame: the test fails below
		}
		_ = sendStream(ws, ExecStreamStdout, []byte("second line\n"))
		sendExit(ws, 0)
	})
	stdout := &execBuf{notify: make(chan struct{}, 1)}
	done := startExec(context.Background(), New(srv.URL), execReq(), stdout, &execBuf{})

	deadline := time.After(5 * time.Second)
	for stdout.String() != "first line\n" {
		select {
		case <-stdout.notify:
		case <-deadline:
			t.Fatalf("the first frame did not reach stdout before the exit frame; stdout %q", stdout.String())
		}
	}
	select {
	case <-done:
		t.Fatal("Exec returned before the exit frame")
	default:
	}
	close(release)
	r := waitExec(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, "first line\nsecond line\n", stdout.String())
}

func TestExec_ExitCodes(t *testing.T) {
	t.Parallel()
	// The client reports the code of the server as is; cmd maps a code
	// outside 0 to 255.
	for _, code := range []int{0, 3, 124, 255, 256, -1} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Parallel()
			srv := newExecSrv(t, serveFrames(code, nil))
			r := runExec(t, context.Background(), New(srv.URL), execReq())
			require.NoError(t, r.err)
			require.NotNil(t, r.res)
			assert.Equal(t, code, r.res.ExitCode)
			assert.Equal(t, "web-0", r.res.Pod)
			assert.Equal(t, "php", r.res.Container)
		})
	}
}

func TestExec_ExitWithoutStartedFrame(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		sendExit(ws, 2)
	})
	r := runExec(t, context.Background(), New(srv.URL), execReq())
	require.NoError(t, r.err)
	assert.Equal(t, &ExecResult{ExitCode: 2}, r.res)
}

func TestExec_HandshakeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		body      string
		container string
		apiKey    bool
		wantMsg   []string
		notMsg    []string
		check     func(t *testing.T, hs *WSHandshakeError)
	}{
		{name: "container not allowed names the container", status: 403, container: "php",
			body:    `{"error":"The container is not allowed for exec","code":"container_not_allowed"}`,
			wantMsg: []string{`exec refused by the server (HTTP 403): The container is not allowed for exec: "php". Use -c with a container that allows exec, or ask an administrator which containers do.`}},
		{name: "container not allowed without -c", status: 403,
			body:    `{"error":"No container allows exec","code":"container_not_allowed"}`,
			wantMsg: []string{"No container of the chart allows exec"}},
		{name: "api key not allowed", status: 403, apiKey: true,
			body:    `{"error":"API keys cannot open this stream","code":"api_key_not_allowed"}`,
			wantMsg: []string{"HTTP 403", "API keys are not allowed for this command", "stackctl login"}},
		{name: "forbidden", status: 403,
			body:    `{"error":"You do not have permission","code":"forbidden"}`,
			wantMsg: []string{"exec refused by the server (HTTP 403): You do not have permission"}},
		{name: "service account token", status: 403,
			body:    `{"error":"The container holds a service-account token","code":"service_account_token"}`,
			wantMsg: []string{"exec refused by the server (HTTP 403): The container holds a service-account token. The server refuses exec into such a container; ask an administrator."}},
		{name: "cluster exec disabled", status: 403,
			body:    `{"error":"This cluster does not allow exec","code":"cluster_exec_disabled"}`,
			wantMsg: []string{"exec refused by the server (HTTP 403): This cluster does not allow exec. Ask an administrator."}},
		{name: "cluster exec disabled without a server text", status: 403,
			body:    `{"code":"cluster_exec_disabled"}`,
			wantMsg: []string{"exec refused by the server (HTTP 403): Forbidden. The cluster of the stack does not allow exec. Ask an administrator."}},
		{name: "exec limit without a server text", status: 429,
			body:    `{"code":"exec_limit"}`,
			wantMsg: []string{"exec refused by the server (HTTP 429): Too Many Requests. Too many of your exec sessions are open. Wait until one of your exec sessions ends, then try again."}},
		{name: "feature disabled", status: 404,
			body:    `{"error":"This feature is disabled","code":"feature_disabled"}`,
			wantMsg: []string{"This feature is disabled", "The server has exec turned off"},
			check: func(t *testing.T, hs *WSHandshakeError) {
				assert.True(t, hs.FeatureDisabled())
				assert.False(t, hs.NoRoute())
			}},
		{name: "plain 404 gives the upgrade hint", status: 404, body: "404 page not found",
			wantMsg: []string{"exec failed: the server has no exec route (HTTP 404); upgrade the server to " + ExecServerVersion},
			notMsg:  []string{"port-forward"},
			check:   func(t *testing.T, hs *WSHandshakeError) { assert.True(t, hs.NoRoute()) }},
		{name: "chart not found", status: 404,
			body:    `{"error":"Chart not found","code":"chart_not_found"}`,
			wantMsg: []string{"Chart not found", "CHART column of 'stackctl stack pods'"}},
		{name: "container not found", status: 404, container: "nope",
			body:    `{"error":"No ready pod runs the container","code":"container_not_found"}`,
			wantMsg: []string{`exec refused by the server (HTTP 404): No ready pod runs the container: "nope". Check the container name and 'stackctl stack pods'.`}},
		{name: "container not found without -c", status: 404,
			body:    `{"error":"No ready pod runs the container","code":"container_not_found"}`,
			wantMsg: []string{"exec refused by the server (HTTP 404): No ready pod runs the container. Check 'stackctl stack pods'."},
			notMsg:  []string{`container ""`}},
		{name: "invalid chart", status: 400,
			body:    `{"error":"chart must be a Helm release name (a DNS-1123 name of at most 53 characters)","code":"invalid_chart"}`,
			wantMsg: []string{"exec refused by the server (HTTP 400): chart must be a Helm release name (a DNS-1123 name of at most 53 characters). The CHART column of 'stackctl stack pods' shows the chart names."},
			check: func(t *testing.T, hs *WSHandshakeError) {
				assert.False(t, hs.UpgradeDropped(), "a 400 with a code is a refusal")
			}},
		{name: "invalid container", status: 400, container: "PHP_",
			body:    `{"error":"Invalid container name","code":"invalid_container"}`,
			wantMsg: []string{"exec refused by the server (HTTP 400): Invalid container name. Check the container name."}},
		{name: "token check forbidden", status: 403,
			body:    `{"error":"The backend may not read the service account or secrets of the pod","code":"token_check_forbidden"}`,
			wantMsg: []string{"exec refused by the server (HTTP 403): The backend may not read the service account or secrets of the pod. Without that right the server cannot check the container for a service-account token and refuses exec; ask an administrator."}},
		{name: "no ready pod", status: 503,
			body:    `{"error":"No ready pod for the chart","code":"no_ready_pod"}`,
			wantMsg: []string{"exec refused by the server (HTTP 503): No ready pod for the chart. Check 'stackctl stack pods', then try again."}},
		{name: "exec limit", status: 429,
			body:    `{"error":"Too many open exec sessions","code":"exec_limit"}`,
			wantMsg: []string{"exec refused by the server (HTTP 429): Too many open exec sessions. Wait until one of your exec sessions ends, then try again."}},
		{name: "401 with an api key", status: 401, apiKey: true,
			body:    `{"error":"Invalid API key"}`,
			wantMsg: []string{"HTTP 401", "Check the API key"}},
		{name: "terminal escapes in the JSON body are sanitized", status: 403, container: "php\x1b[2J\u202e",
			body:    `{"error":"bad \u001b[31mthing\u009b2J\u202eevil\u2066","code":"container_not_allowed"}`,
			wantMsg: []string{"HTTP 403", "bad", "evil"}},
		{name: "200 without upgrade", status: 200, body: "<html>ok</html>",
			wantMsg: []string{"exec failed: the server answered HTTP 200 without a WebSocket upgrade"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, hits := refusingSrv(t, tt.status, tt.body)
			c := New(srv.URL)
			c.Token = execTestToken
			if tt.apiKey {
				c.APIKey = execTestAPIKey
			}
			req := execReq()
			req.Container = tt.container
			r := runExec(t, context.Background(), c, req)
			require.Nil(t, r.res)
			var hs *WSHandshakeError
			require.ErrorAs(t, r.err, &hs)
			assert.Equal(t, tt.status, hs.StatusCode)
			assert.Equal(t, "exec", hs.Op)
			assert.Equal(t, tt.container, hs.Container)
			for _, m := range tt.wantMsg {
				assert.Contains(t, r.err.Error(), m)
			}
			for _, m := range tt.notMsg {
				assert.NotContains(t, r.err.Error(), m)
			}
			assertNoTerminalControls(t, r.err.Error())
			assertNoSecret(t, r.err)
			assert.Equal(t, int32(1), hits.Load(), "one dial")
			if tt.check != nil {
				tt.check(t, hs)
			}
		})
	}
}

func TestStreamRoutes_PlainNotFoundNamesTheirOwnServerVersion(t *testing.T) {
	t.Parallel()
	srv, _ := refusingSrv(t, http.StatusNotFound, "404 page not found")
	c := New(srv.URL)

	_, err := c.DialPortForward(context.Background(), "42", "mysql", 3306)
	require.Error(t, err)
	assert.Equal(t, "port-forward failed: the server has no port-forward route (HTTP 404); upgrade the server to "+PortForwardServerVersion, err.Error())

	r := runExec(t, context.Background(), c, execReq())
	require.Error(t, r.err)
	assert.Equal(t, "exec failed: the server has no exec route (HTTP 404); upgrade the server to "+ExecServerVersion, r.err.Error())
}

func TestExec_ServerCloses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		code         int
		reason       string
		errFrame     string
		started      bool
		apiKey       bool
		wantMsg      string
		wantTimeout  bool
		wantWSClosed bool
	}{
		{name: "4400 bad start with reason", code: CloseBadStart, reason: "command is longer than 4096 bytes",
			wantMsg: "exec failed: the server refused the command (too long or not valid): command is longer than 4096 bytes"},
		{name: "4400 bad start without reason", code: CloseBadStart,
			wantMsg: "exec failed: the server refused the command (too long or not valid)"},
		{name: "4408 start timeout", code: CloseStartTimeout,
			wantMsg: "exec failed: the server received no command in time (close 4408); try again"},
		{name: "4001 time limit", code: CloseMaxDuration, reason: "maximum duration reached", started: true, wantTimeout: true},
		{name: "1008 session revoked", code: ClosePolicy, reason: CloseReasonSessionRevoked, started: true, wantWSClosed: true,
			wantMsg: "exec closed: session revoked (close 1008); log in again with 'stackctl login'; the command can still run on the server"},
		{name: "1008 session revoked before start", code: ClosePolicy, reason: CloseReasonSessionRevoked, wantWSClosed: true,
			wantMsg: "exec closed: session revoked (close 1008); log in again with 'stackctl login'; the server did not start the command"},
		{name: "1008 token expired", code: ClosePolicy, reason: CloseReasonTokenExpired, started: true, wantWSClosed: true,
			wantMsg: "exec closed: token expired (close 1008); check the effect of the command before you run it again, and run 'stackctl login' if a new command fails; the command can still run on the server"},
		{name: "1008 token expired before start", code: ClosePolicy, reason: CloseReasonTokenExpired, wantWSClosed: true,
			wantMsg: "exec closed: token expired (close 1008); run 'stackctl login' if a new command fails; the server did not start the command"},
		{name: "1008 api key", code: ClosePolicy, reason: "api key revoked", apiKey: true, started: true, wantWSClosed: true,
			wantMsg: "exec closed: api key revoked (close 1008); the API key was deleted or its user changed; check it with 'stackctl apikey list'; the command can still run on the server"},
		{name: "1008 api key expired", code: ClosePolicy, reason: CloseReasonAPIKeyExpired, apiKey: true, started: true, wantWSClosed: true,
			wantMsg: "exec closed: the API key expired; create a new key with 'stackctl apikey create'; the command can still run on the server"},
		{name: "1008 without reason", code: ClosePolicy, started: true, wantWSClosed: true,
			wantMsg: "exec closed: policy violation (close 1008); log in again with 'stackctl login'; the command can still run on the server"},
		{name: "1008 access revoked", code: ClosePolicy, reason: CloseReasonAccessLost, started: true, wantWSClosed: true,
			wantMsg: "exec closed: access revoked: the stack was stopped, deleted or cleaned, or your access to it changed; the command can still run on the server"},
		{name: "1008 access revoked before start", code: ClosePolicy, reason: CloseReasonAccessLost, wantWSClosed: true,
			wantMsg: "exec closed: access revoked: the stack was stopped, deleted or cleaned, or your access to it changed; the server did not start the command"},
		{name: "4502 with an error frame after start", code: ClosePodStreamError, reason: "stream error", errFrame: "container exited unexpectedly", started: true,
			wantMsg: "exec failed: pod stream error: container exited unexpectedly; the command can still run on the server"},
		{name: "4502 reason only before start", code: ClosePodStreamError, reason: "exec not found",
			wantMsg: "exec failed: pod stream error: exec not found; the server did not start the command"},
		{name: "1011 with an error frame", code: CloseInternalError, errFrame: "audit unavailable",
			wantMsg: "exec failed: internal server error: audit unavailable; the server did not start the command"},
		{name: "1011 with the generic message", code: CloseInternalError, errFrame: "Internal server error",
			wantMsg: "exec failed: internal server error; the server did not start the command"},
		{name: "1011 after start", code: CloseInternalError, errFrame: "Internal server error", started: true,
			wantMsg: "exec failed: internal server error; the command can still run on the server"},
		{name: "1001 server shutdown", code: CloseGoingAway, reason: "server shutdown", started: true,
			wantMsg: "exec closed: the server is shutting down; the command can still run on the server"},
		{name: "1001 server shutdown before start", code: CloseGoingAway, reason: "server shutdown",
			wantMsg: "exec closed: the server is shutting down; the server did not start the command"},
		{name: "1000 without an exit frame", code: CloseNormal, started: true,
			wantMsg: "exec: connection lost; the command can still run on the server (the server closed the connection without an exit code)"},
		{name: "1000 before start", code: CloseNormal,
			wantMsg: "exec: the server closed the connection without an exit code; the server did not start the command"},
		{name: "1005 close without a code", code: closeNoStatus, started: true,
			wantMsg: "exec: connection lost; the command can still run on the server (the server closed the connection without an exit code)"},
		{name: "1009 message too big", code: CloseMessageTooBig,
			wantMsg: "exec failed: the command is too long for the server (close 1009)"},
		{name: "4002 idle timeout", code: CloseIdleTimeout, started: true,
			wantMsg: "exec closed: idle timeout (no data in either direction); the command can still run on the server"},
		{name: "unknown code", code: 4999, reason: "odd", started: true,
			wantMsg: "exec closed: closed by the server (code 4999): odd; the command can still run on the server"},
		{name: "unknown code before start", code: 4999, reason: "odd",
			wantMsg: "exec closed: closed by the server (code 4999): odd; the server did not start the command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
				if _, _, err := readStart(ws); err != nil {
					return
				}
				if tt.started {
					_ = sendStarted(ws, "web-0", "php")
					_ = sendStream(ws, ExecStreamStdout, []byte("partial"))
				}
				if tt.errFrame != "" {
					_ = sendJSON(ws, map[string]any{"type": "error", "message": tt.errFrame})
				}
				if tt.code == closeNoStatus {
					_ = ws.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(time.Second))
					drain(ws)
					return
				}
				closeAndDrain(ws, tt.code, tt.reason)
			})
			c := New(srv.URL)
			c.Token = execTestToken
			if tt.apiKey {
				c.APIKey = execTestAPIKey
			}
			r := runExec(t, context.Background(), c, execReq())
			require.Error(t, r.err)
			assert.Nil(t, r.res)
			assertNoSecret(t, r.err)
			if tt.started {
				assert.Equal(t, "partial", r.stdout.String(), "output before the close is kept")
			}
			if tt.wantTimeout {
				assert.ErrorIs(t, r.err, ErrExecTimeLimit)
				assert.Equal(t, "exec closed: server time limit reached; the command can still run on the server", r.err.Error())
				return
			}
			assert.NotErrorIs(t, r.err, ErrExecTimeLimit)
			var ce *ExecClosedError
			require.ErrorAs(t, r.err, &ce)
			wantCode := tt.code
			assert.Equal(t, wantCode, ce.Code)
			assert.Equal(t, tt.started, ce.Started)
			assert.Equal(t, tt.apiKey, ce.APIKey)
			assert.Equal(t, tt.wantMsg, r.err.Error())
			var wsClosed *WSClosedError
			assert.Equal(t, tt.wantWSClosed, errors.As(r.err, &wsClosed), "1008 also gives a WSClosedError")
		})
	}
}

func TestExec_ConnectionDropWithoutCloseFrame(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStarted(ws, "web-0", "php")
		_ = sendStream(ws, ExecStreamStdout, []byte("before the drop"))
		_ = ws.NetConn().Close() // abrupt TCP close, no close frame
	})
	c := New(srv.URL)
	c.Token = execTestToken
	r := runExec(t, context.Background(), c, execReq())
	var ce *ExecClosedError
	require.ErrorAs(t, r.err, &ce)
	assert.Equal(t, 0, ce.Code)
	assert.True(t, ce.Started)
	// gorilla reports the drop as close 1006; its text is not shown.
	assert.Equal(t, "exec: connection lost; the command can still run on the server", r.err.Error())
	assert.NotContains(t, r.err.Error(), "1006")
	assert.Equal(t, "before the drop", r.stdout.String())
	assertNoSecret(t, r.err)
}

func TestExec_SilentServerTripsReadDeadline(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStarted(ws, "web-0", "php")
		// No reads: the pings of the client get no pong.
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	})
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	req := execReq()
	req.Keepalive = KeepaliveTimings{PingInterval: 50 * time.Millisecond, ReadTimeout: 300 * time.Millisecond, WriteTimeout: time.Second}
	r := runExec(t, context.Background(), New(srv.URL), req)
	var ce *ExecClosedError
	require.ErrorAs(t, r.err, &ce)
	assert.Equal(t, 0, ce.Code)
	assert.Equal(t, "exec: connection lost; the command can still run on the server (no response from the server)", r.err.Error())
}

func TestExec_PingsKeepASilentCommandAlive(t *testing.T) {
	t.Parallel()
	var pings atomic.Int32
	// Generous for a loaded -race runner: 10 pings per read timeout.
	const readTimeout = 500 * time.Millisecond
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		ws.SetPingHandler(func(data string) error {
			pings.Add(1)
			return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()
		_ = sendStarted(ws, "web-0", "php")
		time.Sleep(3 * readTimeout) // silent, longer than the read timeout
		_ = sendJSON(ws, map[string]any{"type": "exit", "exit_code": 0})
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(CloseNormal, ""), time.Now().Add(time.Second))
		select {
		case <-readerDone:
		case <-time.After(5 * time.Second):
		}
	})
	req := execReq()
	req.Keepalive = KeepaliveTimings{PingInterval: 50 * time.Millisecond, ReadTimeout: readTimeout, WriteTimeout: time.Second}
	r := runExec(t, context.Background(), New(srv.URL), req)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.res.ExitCode)
	assert.Greater(t, pings.Load(), int32(1), "the client pings")
}

// cancelServer reads the start message, sends started and "x", then waits
// for the close of the client without answering it. afterClose runs after
// the close frame; then the server waits until the client drops the TCP
// connection.
func cancelServer(gotClose chan<- int, afterClose func(ws *websocket.Conn)) func(*http.Request, *websocket.Conn) {
	return func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		ws.SetCloseHandler(func(int, string) error { return nil }) // no echo
		_ = sendStarted(ws, "web-0", "php")
		_ = sendStream(ws, ExecStreamStdout, []byte("x"))
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
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
		_ = ws.NetConn().SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, ws.NetConn())
	}
}

// waitOutput waits until w holds want.
func waitOutput(t *testing.T, w *execBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.String() != want {
		if time.Now().After(deadline) {
			t.Fatalf("output %q, want %q", w.String(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestExec_CancelWithoutExitFrameWaitsCancelWait(t *testing.T) {
	t.Parallel()
	gotClose := make(chan int, 1)
	srv := newExecSrv(t, cancelServer(gotClose, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := execReq()
	const wait = 300 * time.Millisecond
	req.CancelWait = wait
	stdout := &execBuf{}
	done := startExec(ctx, New(srv.URL), req, stdout, &execBuf{})
	waitOutput(t, stdout, "x")

	start := time.Now()
	cancel()
	select {
	case code := <-gotClose:
		assert.Equal(t, websocket.CloseNormalClosure, code, "the client sends close 1000")
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no close")
	}
	r := waitExec(t, done)
	elapsed := time.Since(start)
	assert.ErrorIs(t, r.err, context.Canceled)
	assertCanceled(t, r.err, true)
	assert.Equal(t, "exec canceled; the command can still run on the server", r.err.Error())
	assert.Nil(t, r.res)
	assert.GreaterOrEqual(t, elapsed, wait-50*time.Millisecond, "waits for the exit frame")
	assert.Less(t, elapsed, wait+2*time.Second, "but not much longer than CancelWait")
}

func TestExec_CancelReturnsTheExitFrameInsideCancelWait(t *testing.T) {
	t.Parallel()
	gotClose := make(chan int, 1)
	srv := newExecSrv(t, cancelServer(gotClose, func(ws *websocket.Conn) {
		_ = sendJSON(ws, map[string]any{"type": "exit", "exit_code": 143})
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(CloseNormal, ""), time.Now().Add(time.Second))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := execReq()
	req.CancelWait = 5 * time.Second
	stdout := &execBuf{}
	done := startExec(ctx, New(srv.URL), req, stdout, &execBuf{})
	waitOutput(t, stdout, "x")

	start := time.Now()
	cancel()
	r := waitExec(t, done)
	require.NoError(t, r.err)
	require.NotNil(t, r.res)
	assert.Equal(t, 143, r.res.ExitCode)
	assert.Equal(t, "web-0", r.res.Pod)
	assert.Less(t, time.Since(start), 3*time.Second, "returns at the exit frame, not at the end of CancelWait")
	assert.Equal(t, websocket.CloseNormalClosure, <-gotClose)
}

func TestExec_CancelWhileTheServerEchoesTheClose(t *testing.T) {
	t.Parallel()
	// The default close handler of the server answers the close at once:
	// Exec returns ctx.Err() without waiting for CancelWait.
	gotClose := make(chan int, 1)
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStream(ws, ExecStreamStdout, []byte("x"))
		_, _, err := ws.ReadMessage()
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			gotClose <- ce.Code
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := execReq()
	req.CancelWait = 5 * time.Second
	stdout := &execBuf{}
	done := startExec(ctx, New(srv.URL), req, stdout, &execBuf{})
	waitOutput(t, stdout, "x")
	start := time.Now()
	cancel()
	r := waitExec(t, done)
	assert.ErrorIs(t, r.err, context.Canceled)
	// No started frame, but the output proves that the command ran.
	assertCanceled(t, r.err, true)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, websocket.CloseNormalClosure, <-gotClose)
}

func TestExec_CancelledBeforeTheDial(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, serveFrames(0, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := runExec(t, ctx, New(srv.URL), execReq())
	assert.ErrorIs(t, r.err, context.Canceled)
	assertCanceled(t, r.err, false)
	assert.Nil(t, r.res)
}

// assertCanceled checks that err is a *ExecCanceledError with started.
func assertCanceled(t *testing.T, err error, started bool) {
	t.Helper()
	var ce *ExecCanceledError
	require.ErrorAs(t, err, &ce, "error %v", err)
	assert.Equal(t, started, ce.Started, "Started; error %v", err)
	assert.ErrorIs(t, err, context.Canceled)
}

// holdingSrv accepts every request and holds it without an answer until
// the client gives up (at most 5 s). dialing is closed at the first request.
func holdingSrv(t *testing.T) (srv *httptest.Server, dialing <-chan struct{}) {
	t.Helper()
	return holdingSrvWith(t, httptest.NewServer)
}

// holdingSrvWith is holdingSrv with a server constructor, for example
// httptest.NewTLSServer.
func holdingSrvWith(t *testing.T, newServer func(http.Handler) *httptest.Server) (srv *httptest.Server, dialing <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	srv = newServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(ch) })
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func TestExec_CancelDuringTheDial(t *testing.T) {
	t.Parallel()
	// The server holds the upgrade: gorilla alone would wait for its
	// handshake timeout (45 s).
	srv, dialing := holdingSrv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startExec(ctx, New(srv.URL), execReq(), &execBuf{}, &execBuf{})
	select {
	case <-dialing:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial did not reach the server")
	}
	start := time.Now()
	cancel()
	r := waitExec(t, done)
	assert.Nil(t, r.res)
	assertCanceled(t, r.err, false)
	assert.Equal(t, "exec canceled before the command started", r.err.Error())
	assert.Less(t, time.Since(start), 3*time.Second, "the cancel ends the dial")
}

func TestExec_CancelDuringTheTLSDial(t *testing.T) {
	t.Parallel()
	// wss: the TLS handshake is done and the server holds the upgrade
	// request. The cancel closes the TCP connection under the TLS one.
	srv, dialing := holdingSrvWith(t, httptest.NewTLSServer)
	require.True(t, strings.HasPrefix(srv.URL, "https://"))
	c := New(srv.URL)
	c.HTTPClient = &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server certificate
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startExec(ctx, c, execReq(), &execBuf{}, &execBuf{})
	select {
	case <-dialing:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial did not reach the server")
	}
	start := time.Now()
	cancel()
	r := waitExec(t, done)
	assert.Nil(t, r.res)
	assertCanceled(t, r.err, false)
	assert.Equal(t, "exec canceled before the command started", r.err.Error())
	assert.Less(t, time.Since(start), 3*time.Second, "the cancel ends the wss dial")
}

func TestCloseOnCancel_RefusesDialersThatSkipTheWrapper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netDial := func(string, string) (net.Conn, error) { return nil, errors.New("unused") }
	tlsDial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") }
	for name, d := range map[string]*websocket.Dialer{
		"NetDial":           {NetDial: netDial},
		"NetDialTLSContext": {NetDialTLSContext: tlsDial},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, release, err := closeOnCancel(ctx, d)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not supported for stream routes")
			assert.Nil(t, got)
			assert.Nil(t, release)
		})
	}
	t.Run("default dialer", func(t *testing.T) {
		t.Parallel()
		got, release, err := closeOnCancel(ctx, websocket.DefaultDialer)
		require.NoError(t, err)
		defer release()
		assert.NotNil(t, got.NetDialContext)
		assert.Nil(t, websocket.DefaultDialer.NetDialContext, "the dialer is copied, not changed")
	})
}

func TestDialPortForward_CancelDuringTheDial(t *testing.T) {
	t.Parallel()
	// The same stream dial as exec.
	srv, dialing := holdingSrv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		ws, err := New(srv.URL).DialPortForward(ctx, "42", "mysql", 3306)
		if ws != nil {
			_ = ws.Close()
		}
		done <- result{err}
	}()
	select {
	case <-dialing:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial did not reach the server")
	}
	start := time.Now()
	cancel()
	select {
	case r := <-done:
		assert.ErrorIs(t, r.err, context.Canceled)
		assert.Less(t, time.Since(start), 3*time.Second, "the cancel ends the dial")
	case <-time.After(10 * time.Second):
		t.Fatal("DialPortForward did not return after the cancel")
	}
}

func TestExec_CancelBeforeTheStartedFrame(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// answer: the server answers the close (a close frame); else it
		// sends nothing, and the client ends at CancelWait.
		answer      bool
		sendStarted bool
		wantStarted bool
	}{
		{name: "the server closes without started", answer: true, wantStarted: false},
		{name: "the server closes after started", answer: true, sendStarted: true, wantStarted: true},
		{name: "no answer: started can be unread", wantStarted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotStart := make(chan struct{})
			srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
				if _, _, err := readStart(ws); err != nil {
					return
				}
				if tt.sendStarted {
					_ = sendStarted(ws, "web-0", "php")
				}
				if !tt.answer {
					ws.SetCloseHandler(func(int, string) error { return nil })
				}
				close(gotStart)
				// The default close handler answers the close of the client.
				drain(ws)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := execReq()
			req.CancelWait = 300 * time.Millisecond
			var started atomic.Bool
			req.OnStarted = func(string, string) { started.Store(true) }
			done := startExec(ctx, New(srv.URL), req, &execBuf{}, &execBuf{})
			select {
			case <-gotStart:
			case <-time.After(5 * time.Second):
				t.Fatal("the server got no start message")
			}
			if tt.sendStarted {
				deadline := time.Now().Add(5 * time.Second)
				for !started.Load() && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				require.True(t, started.Load(), "the client did not see started")
			}
			cancel()
			r := waitExec(t, done)
			assert.Nil(t, r.res)
			assertCanceled(t, r.err, tt.wantStarted)
		})
	}
}

func TestExec_OutputProvesTheStart(t *testing.T) {
	t.Parallel()
	// The started frame is malformed or missing, but output came: the
	// command ran, so a close never says that it did not start.
	tests := []struct {
		name    string
		started func(ws *websocket.Conn)
		stream  byte
	}{
		{"malformed started frame", func(ws *websocket.Conn) {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"started","pod":`))
		}, ExecStreamStdout},
		{"no started frame", func(*websocket.Conn) {}, ExecStreamStderr},
		{"unknown stream byte", func(*websocket.Conn) {}, 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
				if _, _, err := readStart(ws); err != nil {
					return
				}
				tt.started(ws)
				_ = sendStream(ws, tt.stream, []byte("partial"))
				closeAndDrain(ws, CloseGoingAway, "server shutdown")
			})
			var onStarted atomic.Int32
			req := execReq()
			req.OnStarted = func(string, string) { onStarted.Add(1) }
			r := runExec(t, context.Background(), New(srv.URL), req)
			require.Error(t, r.err)
			var ce *ExecClosedError
			require.ErrorAs(t, r.err, &ce)
			assert.True(t, ce.Started)
			assert.Equal(t, "exec closed: the server is shutting down; the command can still run on the server", r.err.Error())
			assert.NotContains(t, r.err.Error(), "did not start")
			assert.Zero(t, onStarted.Load(), "OnStarted needs the started frame")
		})
	}
	t.Run("a started frame after output still sets the pod", func(t *testing.T) {
		t.Parallel()
		srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
			if _, _, err := readStart(ws); err != nil {
				return
			}
			_ = sendStream(ws, ExecStreamStdout, []byte("early"))
			_ = sendStarted(ws, "web-0", "php")
			sendExit(ws, 0)
		})
		r := runExec(t, context.Background(), New(srv.URL), execReq())
		require.NoError(t, r.err)
		assert.Equal(t, "early", r.stdout.String())
		assert.Equal(t, "web-0", r.res.Pod)
		assert.Equal(t, "php", r.res.Container)
	})
}

func TestExec_IgnoresUnknownFrames(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, serveFrames(0, func(ws *websocket.Conn) {
		_ = sendStream(ws, 9, []byte("unknown stream"))
		_ = sendStream(ws, 0, []byte("stream zero"))
		_ = ws.WriteMessage(websocket.BinaryMessage, nil) // empty binary frame
		_ = ws.WriteMessage(websocket.TextMessage, []byte("not json"))
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":`))
		_ = sendJSON(ws, map[string]any{"type": "resize", "rows": 40})
		_ = sendJSON(ws, map[string]any{"type": "exit"}) // no exit_code
		_ = sendJSON(ws, map[string]any{"type": "exit", "exit_code": "7"})
		_ = sendStarted(ws, "other-pod", "other") // a second started frame
		_ = sendStream(ws, ExecStreamStdout, []byte("ok"))
		_ = sendStream(ws, ExecStreamStderr, nil) // stream byte only
	}))
	r := runExec(t, context.Background(), New(srv.URL), execReq())
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.res.ExitCode)
	assert.Equal(t, "ok", r.stdout.String())
	assert.Empty(t, r.stderr.String())
	assert.Equal(t, "web-0", r.res.Pod, "the first started frame counts")
	assert.Equal(t, "php", r.res.Container)
}

func TestExec_OnStartedRunsBeforeOutput(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var events []string
	record := func(e string) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	srv := newExecSrv(t, serveFrames(0, func(ws *websocket.Conn) {
		_ = sendStream(ws, ExecStreamStdout, []byte("out"))
	}))
	req := execReq()
	var calls atomic.Int32
	req.OnStarted = func(pod, container string) {
		calls.Add(1)
		record("started " + pod + "/" + container)
	}
	stdout := writerFunc(func(p []byte) (int, error) {
		record("stdout " + string(p))
		return len(p), nil
	})
	res, err := New(srv.URL).Exec(context.Background(), "42", req, stdout, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, int32(1), calls.Load())
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"started web-0/php", "stdout out"}, events)
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestExec_DebugLogsOnlyPodAndContainer(t *testing.T) {
	t.Parallel()
	srv := newExecSrv(t, serveFrames(0, func(ws *websocket.Conn) {
		_ = sendStream(ws, ExecStreamStdout, []byte("SECRET-STDOUT"))
		_ = sendStream(ws, ExecStreamStderr, []byte("SECRET-STDERR"))
	}))
	var debug execBuf
	c := New(srv.URL)
	c.Token = execTestToken
	c.Debug = true
	c.DebugWriter = &debug
	r := runExec(t, context.Background(), c, execReq("printenv", "SECRET-ARG"))
	require.NoError(t, r.err)
	out := debug.String()
	assert.Contains(t, out, "exec: started in pod web-0, container php")
	for _, s := range []string{"SECRET-STDOUT", "SECRET-STDERR", "SECRET-ARG", "printenv", execTestToken} {
		assert.NotContains(t, out, s)
	}
}

func TestExec_ReadLimit(t *testing.T) {
	t.Parallel()
	t.Run("a 1 MiB frame passes", func(t *testing.T) {
		t.Parallel()
		data := bytes.Repeat([]byte{'a'}, execReadLimit-1) // plus the stream byte: 1 MiB
		srv := newExecSrv(t, serveFrames(0, func(ws *websocket.Conn) {
			_ = sendStream(ws, ExecStreamStdout, data)
		}))
		r := runExec(t, context.Background(), New(srv.URL), execReq())
		require.NoError(t, r.err)
		assert.Equal(t, len(data), len(r.stdout.Bytes()))
	})
	t.Run("a larger frame is refused", func(t *testing.T) {
		t.Parallel()
		data := bytes.Repeat([]byte{'a'}, execReadLimit) // plus the stream byte: 1 MiB + 1
		srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
			if _, _, err := readStart(ws); err != nil {
				return
			}
			_ = sendStarted(ws, "web-0", "php")
			_ = sendStream(ws, ExecStreamStdout, data)
			drain(ws)
		})
		r := runExec(t, context.Background(), New(srv.URL), execReq())
		require.Error(t, r.err)
		assert.ErrorIs(t, r.err, websocket.ErrReadLimit)
		var ce *ExecClosedError
		require.ErrorAs(t, r.err, &ce)
		assert.Equal(t, "exec failed: the server sent a frame larger than 1 MiB; stackctl closed the connection; the command can still run on the server", r.err.Error())
		assert.Empty(t, r.stdout.Bytes(), "nothing of the refused frame is written")
	})
}

func TestExec_LocalWriteErrorClosesTheStream(t *testing.T) {
	t.Parallel()
	gotClose := make(chan int, 1)
	srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
		if _, _, err := readStart(ws); err != nil {
			return
		}
		_ = sendStream(ws, ExecStreamStdout, []byte("x"))
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, err := ws.ReadMessage()
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			gotClose <- ce.Code
		}
	})
	broken := writerFunc(func([]byte) (int, error) { return 0, errors.New("broken pipe") })
	res, err := New(srv.URL).Exec(context.Background(), "42", execReq(), broken, io.Discard)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Equal(t, "exec: writing the output: broken pipe; the command can still run on the server", err.Error())
	select {
	case code := <-gotClose:
		assert.Equal(t, websocket.CloseNormalClosure, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no close")
	}
}

func TestExec_InvalidRequestsDoNotDial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     ExecRequest
		wantErr string
	}{
		{"empty chart", ExecRequest{Command: []string{"ls"}}, "the chart name must not be empty"},
		{"blank chart", ExecRequest{Chart: "  ", Command: []string{"ls"}}, "the chart name must not be empty"},
		{"no command", ExecRequest{Chart: "backend"}, "the command is empty"},
		{"empty program", ExecRequest{Chart: "backend", Command: []string{"", "x"}}, "the program) is empty"},
		{"NUL byte", ExecRequest{Chart: "backend", Command: []string{"ls", "a\x00b"}}, "argument 2 of the command contains a NUL byte"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newExecSrv(t, serveFrames(0, nil))
			r := runExec(t, context.Background(), New(srv.URL), tt.req)
			require.Error(t, r.err)
			assert.Contains(t, r.err.Error(), tt.wantErr)
			assert.Equal(t, int32(0), srv.requests.Load(), "no request")
		})
	}
}

func TestExec_SanitizesServerText(t *testing.T) {
	t.Parallel()
	const dirty = "a\x1b[31mb\u009b2Jc\u202ed\u2066e\u200f"
	tests := []struct {
		name  string
		serve func(ws *websocket.Conn)
	}{
		{"error frame", func(ws *websocket.Conn) {
			_ = sendJSON(ws, map[string]any{"type": "error", "message": dirty})
			closeAndDrain(ws, ClosePodStreamError, "x")
		}},
		{"close reason 4400", func(ws *websocket.Conn) { closeAndDrain(ws, CloseBadStart, dirty) }},
		{"close reason 4502", func(ws *websocket.Conn) { closeAndDrain(ws, ClosePodStreamError, dirty) }},
		{"close reason 1008", func(ws *websocket.Conn) { closeAndDrain(ws, ClosePolicy, dirty) }},
		{"close reason of an unknown code", func(ws *websocket.Conn) { closeAndDrain(ws, 4999, dirty) }},
		{"error frame of 1011", func(ws *websocket.Conn) {
			_ = sendJSON(ws, map[string]any{"type": "error", "message": dirty})
			closeAndDrain(ws, CloseInternalError, "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
				if _, _, err := readStart(ws); err != nil {
					return
				}
				_ = sendStarted(ws, "pod"+dirty, "ctr"+dirty)
				tt.serve(ws)
			})
			c := New(srv.URL)
			c.Token = execTestToken
			r := runExec(t, context.Background(), c, execReq())
			require.Error(t, r.err)
			assert.Contains(t, r.err.Error(), "a [31mb 2Jcde", "the text stays, the controls go")
			assertNoTerminalControls(t, r.err.Error())
			assertNoSecret(t, r.err)
		})
	}

	t.Run("started pod and container", func(t *testing.T) {
		t.Parallel()
		var gotPod, gotContainer string
		var debug execBuf
		srv := newExecSrv(t, func(_ *http.Request, ws *websocket.Conn) {
			if _, _, err := readStart(ws); err != nil {
				return
			}
			_ = sendStarted(ws, "pod"+dirty, "ctr"+dirty)
			sendExit(ws, 0)
		})
		c := New(srv.URL)
		c.Debug, c.DebugWriter = true, &debug
		req := execReq()
		req.OnStarted = func(pod, container string) { gotPod, gotContainer = pod, container }
		r := runExec(t, context.Background(), c, req)
		require.NoError(t, r.err)
		for _, s := range []string{gotPod, gotContainer, r.res.Pod, r.res.Container, debug.String()} {
			assertNoTerminalControls(t, s)
		}
		assert.Equal(t, "poda [31mb 2Jcde", r.res.Pod)
		assert.Equal(t, "ctra [31mb 2Jcde", r.res.Container)
		assert.Equal(t, r.res.Pod, gotPod)
	})
}

func TestExecClosedError_Unwrap(t *testing.T) {
	t.Parallel()
	var ws *WSClosedError
	e := &ExecClosedError{Code: ClosePolicy, Reason: CloseReasonSessionRevoked}
	require.ErrorAs(t, e, &ws)
	assert.True(t, ws.SessionRevoked())

	cause := errors.New("read tcp: reset")
	lost := &ExecClosedError{err: cause}
	assert.ErrorIs(t, lost, cause)
	assert.Equal(t, "exec: connection lost; the command can still run on the server (read tcp: reset)", lost.Error())
	assert.Equal(t, "exec: connection lost; the command can still run on the server", (&ExecClosedError{}).Error())

	assert.Nil(t, (&ExecClosedError{Code: CloseGoingAway}).Unwrap())

	abnormal := &ExecClosedError{Started: true, err: &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: io.ErrUnexpectedEOF.Error()}}
	assert.Equal(t, "exec: connection lost; the command can still run on the server", abnormal.Error(), "no gorilla text for 1006")
	abnormal.Started = false
	assert.Equal(t, "exec: connection lost; the command can still run on the server", abnormal.Error(), "without a close frame, started can be lost in flight")
	assert.Equal(t, "exec: connection lost; the command can still run on the server", (&ExecClosedError{}).Error())
}

func TestExecCanceledError(t *testing.T) {
	t.Parallel()
	before := &ExecCanceledError{Err: context.Canceled}
	assert.Equal(t, "exec canceled before the command started", before.Error())
	assert.ErrorIs(t, before, context.Canceled)
	after := &ExecCanceledError{Started: true, Err: context.DeadlineExceeded}
	assert.Equal(t, "exec canceled; the command can still run on the server", after.Error())
	assert.ErrorIs(t, after, context.DeadlineExceeded)
	assert.NotErrorIs(t, after, context.Canceled)
}

func TestValidateExecCommand(t *testing.T) {
	t.Parallel()
	args := func(n int) []string {
		a := make([]string, n)
		for i := range a {
			a[i] = "x"
		}
		return a
	}
	tests := []struct {
		name    string
		command []string
		wantErr string
	}{
		{"nil", nil, "the command is empty"},
		{"empty", []string{}, "the command is empty"},
		{"empty first argument", []string{""}, "the first argument of the command (the program) is empty"},
		{"empty later argument is fine", []string{"echo", ""}, ""},
		{"one argument", []string{"ls"}, ""},
		{"256 arguments", args(ExecMaxArgs), ""},
		{"257 arguments", args(ExecMaxArgs + 1), "the command has 257 arguments; the server allows at most 256"},
		{"NUL in the program", []string{"l\x00s"}, "argument 1 of the command contains a NUL byte"},
		{"NUL in an argument", []string{"ls", "-la", "\x00"}, "argument 3 of the command contains a NUL byte"},
		{"spaces and unicode", []string{"sh", "-c", "echo 'hä llo' | wc"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExecCommand(tt.command)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestExecWireTypes(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(types.ExecStartMessage{Type: ExecMessageStart, Command: []string{"ls"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"start","command":["ls"]}`, string(data))

	var msg types.ExecServerMessage
	require.NoError(t, json.Unmarshal([]byte(`{"type":"exit","exit_code":0}`), &msg))
	require.NotNil(t, msg.ExitCode, "exit code 0 is present")
	assert.Equal(t, 0, *msg.ExitCode)
}
