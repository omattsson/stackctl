---
applyTo: "cli/**_test.go"
---

# Test Conventions

## Framework
- Use `testify/assert` for non-fatal assertions and `testify/require` for fatal ones
- Table-driven tests with `t.Parallel()` on parent and subtests **in `pkg/` only**
- `cmd/` tests must NOT use `t.Parallel()` — they mutate package-level globals (`cfg`, `printer`, `flagAPIURL`)
- Capture range variable: `tt := tt` inside the loop (only needed with `t.Parallel()`)

## API Mocking
Always use `httptest.NewServer` — never call a real API in unit tests. Live-backend tests belong in `cli/test/live/` and must be opt-in via build tag or env var.

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    require.Equal(t, expectedPath, r.URL.Path)
    require.Equal(t, expectedMethod, r.Method)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}))
defer server.Close()
```

## WebSocket Mocking
For `StreamDeploymentLogs` and other WebSocket endpoints, use `httptest.NewServer` with `gorilla/websocket.Upgrader`. Translate the `http://` URL to `ws://` when configuring the client. Always close the test server and the upgraded connection.

## Browser-Opening Commands
For commands that call `openBrowser()` (OIDC loopback), override the `browserOpener` package var in `cmd/browser.go` with a fake that captures the URL. Never let tests spawn `open`, `xdg-open`, or `rundll32`.

## Plugin Tests
When testing `registerPlugins()`, set up an isolated `PATH` pointing to a `t.TempDir()` containing executable `stackctl-<name>` scripts. Do not rely on the host's `$PATH`.

## Coverage Requirements
- Test all output modes: table, JSON, YAML, quiet
- Test error cases: API errors (401, 404, 500), invalid input
- Test flag parsing and validation
- Test confirmation prompt behavior (yes/no/--yes flag)
- Target 80%+ coverage on `pkg/` packages

## cmd/ Test Setup
Use `setupStackTestCmd(t, apiURL)` or equivalent helper that:
1. Sets `flagAPIURL` to the mock server URL
2. Creates a fresh `printer` with a `bytes.Buffer`
3. Registers cleanup to restore defaults (`cfg`, `printer`, all `flag*` globals)

Because `cmd/` tests mutate package-level globals, they MUST NOT use `t.Parallel()` — even in subtests. Helpers should snapshot and restore globals via `t.Cleanup()`.
