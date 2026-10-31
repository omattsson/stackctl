package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests that touch package globals (cfg, printer, flag variables,
// bestEffortTimeout) or run rootCmd are not parallel.

const (
	uiConfigPath     = "/api/v1/ui-config"
	stackListPath    = "/api/v1/stack-instances"
	testSessionToken = "jwt-session-token"
	testStreamAPIKey = "sk_stream_test"
)

// seenRequest is one request that a mock server received.
type seenRequest struct {
	Path   string
	Query  url.Values
	Header http.Header
}

// requestLog records the requests of a mock server (embedded in
// execCmdServer and pfTestServer). The handler calls record.
type requestLog struct {
	logMu sync.Mutex
	seen  []seenRequest
}

// record adds r to the log.
func (l *requestLog) record(r *http.Request) {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	l.seen = append(l.seen, seenRequest{Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone()})
}

// requestsTo returns the requests to path.
func (l *requestLog) requestsTo(path string) []seenRequest {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	var out []seenRequest
	for _, r := range l.seen {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// paths returns the paths of all requests in order.
func (l *requestLog) paths() []string {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	out := make([]string, len(l.seen))
	for i, r := range l.seen {
		out[i] = r.Path
	}
	return out
}

// writeContextConfig writes config.yaml of the context "test" (api-url, and
// api-key when not empty) and sets cfg to the same values.
func writeContextConfig(t *testing.T, apiURL, apiKey string) {
	t.Helper()
	dir := os.Getenv("STACKCTL_CONFIG_DIR")
	require.NotEmpty(t, dir, "call setupStackTestCmd first")
	yml := "current-context: test\ncontexts:\n  test:\n    api-url: " + apiURL + "\n"
	if apiKey != "" {
		yml += "    api-key: " + apiKey + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yml), 0o600))
	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {APIURL: apiURL, APIKey: apiKey}}}
}

// writeSessionConfig is writeContextConfig plus a stored login session
// (testSessionToken) of the context.
func writeSessionConfig(t *testing.T, apiURL, apiKey string) {
	t.Helper()
	writeContextConfig(t, apiURL, apiKey)
	require.NoError(t, saveToken(testSessionToken, "alice", time.Now().Add(time.Hour)))
}

// stackListRoute resolves "my-stack" to 42.
func stackListRoute(w http.ResponseWriter, r *http.Request) {
	data := []map[string]any{}
	if r.URL.Query().Get("name") == "my-stack" {
		data = append(data, map[string]any{"id": "42", "name": "my-stack", "status": "running"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": len(data), "page": 1, "pageSize": 25})
}

// apiKeySource puts testStreamAPIKey into one of the three places.
type apiKeySource struct {
	name string
	// set prepares the source; it returns extra arguments for rootCmd.
	set func(t *testing.T, apiURL string) []string
}

var apiKeySources = []apiKeySource{
	{"flag", func(t *testing.T, apiURL string) []string {
		flagAPIKey = testStreamAPIKey // runPortForward reads the variable
		return []string{"--api-key", testStreamAPIKey}
	}},
	{"env", func(t *testing.T, apiURL string) []string {
		t.Setenv("STACKCTL_API_KEY", testStreamAPIKey)
		return nil
	}},
	{"context", func(t *testing.T, apiURL string) []string {
		writeContextConfig(t, apiURL, testStreamAPIKey)
		return nil
	}},
	{"context with a session too (the API key wins)", func(t *testing.T, apiURL string) []string {
		writeSessionConfig(t, apiURL, testStreamAPIKey)
		return nil
	}},
}

// uiConfigCase is one answer of GET /api/v1/ui-config.
type uiConfigCase struct {
	name  string
	route http.HandlerFunc
	// refused: the command stops before the dial.
	refused bool
}

var uiConfigCases = []uiConfigCase{
	{"false", jsonRoute(http.StatusOK, `{"title":"Stacks","stream_api_keys_allowed":false}`), true},
	{"true", jsonRoute(http.StatusOK, `{"title":"Stacks","stream_api_keys_allowed":true}`), false},
	{"field missing (older server)", jsonRoute(http.StatusOK, `{"title":"Stacks","max_instance_ttl_days":30}`), false},
	{"500", jsonRoute(http.StatusInternalServerError, `{"error":"boom"}`), false},
	{"404", jsonRoute(http.StatusNotFound, `{"error":"not found"}`), false},
	{"malformed JSON", jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":`), false},
	{"null", jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":null}`), false},
}

// assertPublicRequest checks that a /ui-config request had no credentials.
func assertPublicRequest(t *testing.T, reqs []seenRequest) {
	t.Helper()
	require.Len(t, reqs, 1, "one /ui-config request, no retry")
	assert.Empty(t, reqs[0].Header.Get("X-API-Key"), "no API key on the public route")
	assert.Empty(t, reqs[0].Header.Get("Authorization"), "no bearer token on the public route")
}

func TestStackExecCmd_APIKeyPreCheck(t *testing.T) {
	const wantRefusal = "exec not started: the server does not allow API keys for exec; run 'stackctl login' and remove the API key (--api-key, STACKCTL_API_KEY or the api-key of the context)"
	for _, src := range apiKeySources {
		for _, uc := range uiConfigCases {
			t.Run(src.name+"/"+uc.name, func(t *testing.T) {
				t.Setenv("STACKCTL_API_KEY", "")
				srv := newExecCmdServerWith(t, map[string]http.HandlerFunc{uiConfigPath: uc.route}, nil, exitWith(0, "ok", ""))
				e := setupExecTest(t, srv.URL)
				extra := src.set(t, srv.URL)
				err := e.run(t, append(extra, "my-stack", "web", "--", "ls")...)
				assertPublicRequest(t, srv.requestsTo(uiConfigPath))
				if uc.refused {
					ee := requireExit(t, err, 255)
					assert.Equal(t, wantRefusal, ee.Err.Error())
					assert.True(t, strings.HasSuffix(err.Error(), client.StreamAPIKeyAdvice), "the advice of the api_key_not_allowed refusal")
					assert.Equal(t, wantRefusal, err.Error())
					assert.Equal(t, []string{uiConfigPath}, srv.paths(), "no stack resolve and no dial")
					assert.Equal(t, int32(0), srv.lists.Load())
					assert.Equal(t, int32(0), srv.upgrades.Load())
					assert.Nil(t, srv.lastCommand())
					assert.Empty(t, e.stdout.String())
					return
				}
				require.NoError(t, err)
				assert.Equal(t, "ok", e.stdout.String())
				assert.Equal(t, []string{uiConfigPath, stackListPath, execCmdTestPath}, srv.paths(), "the check comes first, then resolve and dial")
				assert.Equal(t, int32(1), srv.upgrades.Load())
				dial := srv.requestsTo(execCmdTestPath)
				require.Len(t, dial, 1)
				assert.Equal(t, testStreamAPIKey, dial[0].Header.Get("X-API-Key"), "the dial still uses the API key")
			})
		}
	}
}

func TestStackExecCmd_APIKeyPreCheckSlowUIConfig(t *testing.T) {
	prev := bestEffortTimeout
	bestEffortTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bestEffortTimeout = prev })

	t.Run("a timeout lets the server decide", func(t *testing.T) {
		release := make(chan struct{})
		srv := newExecCmdServerWith(t, map[string]http.HandlerFunc{uiConfigPath: func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":false}`)(w, r)
		}}, nil, exitWith(0, "ok", ""))
		t.Cleanup(func() { close(release) }) // before srv.Close
		e := setupExecTest(t, srv.URL)
		t.Setenv("STACKCTL_API_KEY", "")
		start := time.Now()
		require.NoError(t, e.run(t, "--api-key", testStreamAPIKey, "42", "web", "--", "ls"))
		assert.Less(t, time.Since(start), 3*time.Second)
		assert.Equal(t, int32(1), srv.upgrades.Load())
	})
	t.Run("Ctrl-C during the check exits 130 without a dial", func(t *testing.T) {
		bestEffortTimeout = 5 * time.Second
		reached, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		srv := newExecCmdServerWith(t, map[string]http.HandlerFunc{uiConfigPath: func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(reached) })
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":true}`)(w, r)
		}}, nil, exitWith(0, "ok", ""))
		t.Cleanup(func() { close(release) }) // before srv.Close
		e := setupExecTest(t, srv.URL)
		t.Setenv("STACKCTL_API_KEY", "")
		done := e.start("--api-key", testStreamAPIKey, "my-stack", "web", "--", "ls")
		select {
		case <-reached:
		case <-time.After(5 * time.Second):
			t.Fatal("the /ui-config request did not reach the server")
		}
		start := time.Now()
		e.signals <- os.Interrupt
		ee := requireExit(t, waitDone(t, done), 130)
		assert.Equal(t, "interrupted before the command started", ee.Err.Error())
		assert.Less(t, time.Since(start), 3*time.Second, "Ctrl-C does not wait for the check")
		assert.Equal(t, int32(0), srv.upgrades.Load())
		assert.Equal(t, []string{uiConfigPath}, srv.paths(), "no stack lookup and no dial after Ctrl-C")
		assert.Equal(t, int32(0), srv.lists.Load())
		assert.Nil(t, srv.lastCommand())
		assert.Empty(t, e.stdout.String())
	})
}

func TestStackExecCmd_SessionMakesNoUIConfigCall(t *testing.T) {
	srv := newExecCmdServerWith(t, map[string]http.HandlerFunc{
		uiConfigPath: jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":false}`),
	}, nil, exitWith(0, "ok", ""))
	e := setupExecTest(t, srv.URL)
	t.Setenv("STACKCTL_API_KEY", "")
	writeSessionConfig(t, srv.URL, "")
	require.NoError(t, e.run(t, "my-stack", "web", "--", "ls"))
	assert.Equal(t, "ok", e.stdout.String())
	assert.Equal(t, []string{stackListPath, execCmdTestPath}, srv.paths(), "no /ui-config call with a session")
	dial := srv.requestsTo(execCmdTestPath)
	require.Len(t, dial, 1)
	assert.Equal(t, "Bearer "+testSessionToken, dial[0].Header.Get("Authorization"))
	assert.Empty(t, dial[0].Header.Get("X-API-Key"))
}

// runPortForwardAPIKey runs port-forward of my-stack (after
// setupPortForwardTest). When the command listens, it opens one
// connection, checks the tunnel and stops the command. It returns the
// error and whether the command listened.
func runPortForwardAPIKey(t *testing.T, stdout *lockedBuffer) (bool, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "my-stack", "mysql", "0:3306")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := forwardingLine.FindStringSubmatch(stdout.String()); m != nil {
			conn, err := net.Dial("tcp", m[1])
			require.NoError(t, err)
			pfRoundTrip(t, conn)
			_ = conn.Close()
			cancel()
			return true, waitDone(t, done)
		}
		select {
		case err := <-done:
			return false, err
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("port-forward neither listened nor ended; stdout: %q", stdout.String())
	return false, nil
}

func TestPortForwardCmd_APIKeyPreCheck(t *testing.T) {
	const wantRefusal = "port-forward not started: the server does not allow API keys for port-forward; run 'stackctl login' and remove the API key (--api-key, STACKCTL_API_KEY or the api-key of the context)"
	for _, src := range apiKeySources {
		for _, uc := range uiConfigCases {
			t.Run(src.name+"/"+uc.name, func(t *testing.T) {
				t.Setenv("STACKCTL_API_KEY", "")
				srv := newPFTestServerWith(t, map[string]http.HandlerFunc{uiConfigPath: uc.route, stackListPath: stackListRoute}, nil)
				// setupPortForwardTest resets cfg and flagAPIKey: set the
				// source after it.
				stdout, _ := setupPortForwardTest(t, srv.URL)
				src.set(t, srv.URL)
				listened, err := runPortForwardAPIKey(t, stdout)
				assertPublicRequest(t, srv.requestsTo(uiConfigPath))
				if uc.refused {
					require.Error(t, err)
					assert.False(t, listened)
					assert.Equal(t, wantRefusal, err.Error())
					assert.True(t, strings.HasSuffix(err.Error(), client.StreamAPIKeyAdvice), "the advice of the api_key_not_allowed refusal")
					var ee *ExitError
					assert.False(t, errors.As(err, &ee), "port-forward exits 1 like its other fatal errors")
					assert.Equal(t, []string{uiConfigPath}, srv.paths(), "no stack resolve and no dial")
					assert.Equal(t, int32(0), srv.upgrades.Load())
					assert.Empty(t, stdout.String(), "nothing listens")
					return
				}
				require.NoError(t, err)
				assert.True(t, listened)
				assert.Equal(t, []string{uiConfigPath, stackListPath, pfTestPath}, srv.paths(), "the check comes first, then resolve and dial")
				assert.Equal(t, int32(1), srv.upgrades.Load())
				dial := srv.requestsTo(pfTestPath)
				require.Len(t, dial, 1)
				assert.Equal(t, testStreamAPIKey, dial[0].Header.Get("X-API-Key"), "the dial still uses the API key")
			})
		}
	}
}

func TestPortForwardCmd_APIKeyPreCheckSlowUIConfig(t *testing.T) {
	prev := bestEffortTimeout
	bestEffortTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bestEffortTimeout = prev })
	release := make(chan struct{})
	srv := newPFTestServerWith(t, map[string]http.HandlerFunc{uiConfigPath: func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":false}`)(w, r)
	}, stackListPath: stackListRoute}, nil)
	t.Cleanup(func() { close(release) }) // before srv.Close
	t.Setenv("STACKCTL_API_KEY", "")
	stdout, _ := setupPortForwardTest(t, srv.URL)
	flagAPIKey = testStreamAPIKey
	listened, err := runPortForwardAPIKey(t, stdout)
	require.NoError(t, err)
	assert.True(t, listened, "a timeout of the check lets the server decide")
	assert.Equal(t, int32(1), srv.upgrades.Load())
}

func TestPortForwardCmd_APIKeyPreCheckInterrupted(t *testing.T) {
	// Ctrl-C while /ui-config blocks ends port-forward at once with exit 0
	// (as a Ctrl-C while it listens): no stack lookup, no port, no dial.
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := newPFTestServerWith(t, map[string]http.HandlerFunc{uiConfigPath: func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":true}`)(w, r)
	}, stackListPath: stackListRoute}, nil)
	t.Cleanup(func() { close(release) }) // before srv.Close
	t.Setenv("STACKCTL_API_KEY", "")
	stdout, stderr := setupPortForwardTest(t, srv.URL)
	flagAPIKey = testStreamAPIKey

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startPortForward(ctx, "my-stack", "mysql", "0:3306")
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the /ui-config request did not reach the server")
	}
	start := time.Now()
	cancel()
	require.NoError(t, waitDone(t, done), "Ctrl-C exits 0")
	assert.Less(t, time.Since(start), 3*time.Second, "Ctrl-C does not wait for the check")
	assert.Empty(t, stdout.String(), "nothing listens")
	assert.Empty(t, stderr.String())
	assert.Equal(t, []string{uiConfigPath}, srv.paths(), "only the /ui-config request")
	assert.Equal(t, int32(0), srv.upgrades.Load())
}

func TestPortForwardCmd_InterruptedDuringStackLookup(t *testing.T) {
	// Ctrl-C while the stack name resolves: exit 0 without binding a port.
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := newPFTestServerWith(t, map[string]http.HandlerFunc{stackListPath: func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		stackListRoute(w, r)
	}}, nil)
	t.Cleanup(func() { close(release) }) // before srv.Close
	t.Setenv("STACKCTL_API_KEY", "")
	stdout, stderr := setupPortForwardTest(t, srv.URL)

	done := startPortForward(ctx, "my-stack", "mysql", "0:3306")
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the stack lookup did not reach the server")
	}
	cancel()
	release <- struct{}{} // the lookup answers after the Ctrl-C
	require.NoError(t, waitDone(t, done), "Ctrl-C exits 0")
	assert.Empty(t, stdout.String(), "no port bound, no Forwarding line")
	assert.Empty(t, stderr.String())
	assert.Equal(t, []string{stackListPath}, srv.paths(), "no dial")
	assert.Equal(t, int32(0), srv.upgrades.Load())
}

func TestPortForwardCmd_SessionMakesNoUIConfigCall(t *testing.T) {
	srv := newPFTestServerWith(t, map[string]http.HandlerFunc{
		uiConfigPath:  jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":false}`),
		stackListPath: stackListRoute,
	}, nil)
	t.Setenv("STACKCTL_API_KEY", "")
	stdout, _ := setupPortForwardTest(t, srv.URL)
	writeSessionConfig(t, srv.URL, "")
	listened, err := runPortForwardAPIKey(t, stdout)
	require.NoError(t, err)
	assert.True(t, listened)
	assert.Equal(t, []string{stackListPath, pfTestPath}, srv.paths(), "no /ui-config call with a session")
	dial := srv.requestsTo(pfTestPath)
	require.Len(t, dial, 1)
	assert.Equal(t, "Bearer "+testSessionToken, dial[0].Header.Get("Authorization"))
	assert.Empty(t, dial[0].Header.Get("X-API-Key"))
}

func TestPortForwardCmd_NoCredentialsMakesNoUIConfigCall(t *testing.T) {
	srv := newPFTestServerWith(t, map[string]http.HandlerFunc{
		uiConfigPath:  jsonRoute(http.StatusOK, `{"stream_api_keys_allowed":false}`),
		stackListPath: stackListRoute,
	}, nil)
	t.Setenv("STACKCTL_API_KEY", "")
	stdout, _ := setupPortForwardTest(t, srv.URL)
	listened, err := runPortForwardAPIKey(t, stdout)
	require.NoError(t, err)
	assert.True(t, listened)
	assert.Empty(t, srv.requestsTo(uiConfigPath))
}

func TestBestEffortCtx(t *testing.T) {
	t.Parallel()
	t.Run("result", func(t *testing.T) {
		t.Parallel()
		v, ok := bestEffortCtx(context.Background(), func() (int, error) { return 7, nil })
		assert.True(t, ok)
		assert.Equal(t, 7, v)
	})
	t.Run("error", func(t *testing.T) {
		t.Parallel()
		v, ok := bestEffortCtx(context.Background(), func() (*int, error) { return nil, errors.New("boom") })
		assert.False(t, ok)
		assert.Nil(t, v)
	})
	t.Run("ctx ends first", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		v, ok := bestEffortCtx(ctx, func() (string, error) {
			<-release
			return "late", nil
		})
		assert.False(t, ok)
		assert.Empty(t, v, "the zero value, not a late result")
		assert.Less(t, time.Since(start), 2*time.Second)
	})
}

func TestPublicBestEffortClient(t *testing.T) {
	// Reads bestEffortTimeout: not parallel.
	c := client.New("https://stacks.example.com")
	c.APIKey = "sk_secret"
	c.Token = "jwt"
	c.Debug = true
	q := publicBestEffortClient(c)
	assert.Equal(t, c.BaseURL, q.BaseURL)
	assert.Empty(t, q.APIKey, "no API key on a public route")
	assert.Empty(t, q.Token, "no token on a public route")
	assert.Nil(t, q.Tokens, "no session renewal")
	assert.True(t, q.Debug)
	assert.NotNil(t, q.RetryBackoff)
	assert.Empty(t, q.RetryBackoff, "one attempt")
	require.NotNil(t, q.HTTPClient)
	assert.Equal(t, bestEffortTimeout, q.HTTPClient.Timeout)
	assert.Equal(t, "sk_secret", c.APIKey, "the original client keeps its credentials")
	assert.Equal(t, "jwt", c.Token)
}
