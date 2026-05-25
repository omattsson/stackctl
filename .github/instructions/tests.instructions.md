---
applyTo: "cli/**_test.go"
---

# Test Conventions

## Framework
- Use `testify/assert` for non-fatal assertions and `testify/require` for fatal ones
- Table-driven tests with `t.Parallel()` on parent and subtests **in `pkg/` only**
- `cmd/` tests must NOT use `t.Parallel()` — they mutate package-level globals (`cfg`, `printer`, `flagAPIURL`)
- Capture range variable: `tt := tt` inside the loop (only needed with `t.Parallel()`)

### When NOT to use table-driven tests
Table-driven structure is the right shape when cases share the same setup and differ only in inputs/expectations (e.g. parsing, validation, name-or-ID resolution).

Do NOT refactor scenario tests into a table when cases:
- exercise meaningfully different mock-server state or seed data
- need distinct stdin/stderr plumbing (e.g. confirm/decline interactive prompts)
- combine different flag sets or run different sequences of commands
- are CRUD-round-trip tests where each step depends on the previous one

These cases stay clearer as separate `func TestX_Scenario` blocks. Reviewers that reflexively suggest "convert to table-driven" on heterogeneous scenarios are wrong here — push back with the scenario-difference reason.

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
  - **Quiet tests**: set `printer.Quiet = true`, assert the expected identifiers/labels are
    present (one per line), and use `assert.NotContains` to verify table headers (`NAME`,
    `STATUS`, `NAMESPACE`, etc.) are absent
  - **Table tests**: assert header presence and key column values
  - **JSON/YAML tests**: unmarshal the buffer and assert on struct fields
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

## E2E Tests (`cli/test/e2e/`)
E2E tests build and invoke the actual `stackctl` binary. Rules:
- `TestMain` builds the binary into a temp dir before running any test, and removes it after.
- Every test calls `t.Skip()` when `testing.Short()` is true — they are excluded from `go test ./... -short`.
- Use `runStackctl(t, configDir, args...)` / `runStackctlWithStdin(...)` helpers — never call `exec.Command` directly in individual tests.
- Inject an isolated config dir via `STACKCTL_CONFIG_DIR` env var so tests don't touch `~/.stackmanager`.
- Use `httptest.NewServer` for the mock API server; the server URL is passed via `--api-url`.
- Assert on stdout/stderr strings and `err` (nil means exit 0; non-nil means non-zero exit).
- Do NOT use `t.Parallel()` — binary builds and package-global `binaryPath` are not concurrency-safe.

## Live Tests (`cli/test/live/`)
Live tests call a real running backend. Rules:
- File-level build tag: `//go:build live` — they are never compiled in normal `go test ./...` runs.
- Run with: `cd cli && go test -tags live ./test/live/ -v`
- Require env vars `STACKCTL_LIVE_USER` and `STACKCTL_LIVE_PASS`; skip (not fail) if unset.
- Optional `STACKCTL_LIVE_URL`; defaults to `http://localhost:8081`.
- The `newLiveClient(t)` helper does a connectivity check and calls `t.Skip()` if the backend is unreachable — never let live tests fail due to infrastructure absence.
- Do not add live tests to CI pipelines — they are developer opt-in only.
