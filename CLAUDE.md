# stackctl

## Overview

Go CLI tool (Cobra + yaml.v3) for managing Kubernetes stack deployments. Pure API client — talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct K8s interaction.

Go version: see `cli/go.mod` (1.27.3 or later); the Dockerfile builder image may be newer.

**Map.** The Go module root is `cli/`; run go commands there.

- `cli/cmd/`: Cobra commands, one file per command group. `root.go` holds the global flags and config resolution; `agent_guide.md` is the embedded guide for agents and scripts.
- `cli/pkg/client/` (HTTP client, auth renewal, WebSocket), `pkg/config/` (config file, tokens), `pkg/output/` (table, JSON, YAML, quiet), `pkg/types/` (API structs).
- `cli/test/`: `integration/`, `e2e/`, `contract/` (against `testdata/swagger.json`), `live/` (build tag `live`).
- `docs/`: user docs. `.claude/agents/`, `.claude/skills/`: Claude Code agents and skills.

## Reference Docs

These files do not load on every request. Read the matching file before you work in its area.

| Area | File |
|---|---|
| Server routes that stackctl calls, server behavior and minimum versions per endpoint (`pkg/client`, `pkg/types`, commands that call the API) | `docs/claude/api-reference.md` |
| Full file tree with the role of each file | `docs/claude/structure.md` |

## Development Commands

| Task | Command |
|------|---------|
| Build | `cd cli && make build` (or `go build -o bin/stackctl .`) |
| Build all platforms | `cd cli && make build-all` |
| Run tests | `cd cli && make test` (`go test ./... -v`). Agents run `cd cli && go test ./...` instead: same tests, but `-v` prints about 470 KB. Add `-run <Test> -v` only to look at one failing test |
| Install lint tool | `cd cli && make tools` (installs staticcheck; run once before `make lint`) |
| Lint | `cd cli && make lint` (`go vet` + `staticcheck`) |
| Coverage | `cd cli && make coverage` (`go test ./... -coverprofile=coverage.out` + `go tool cover -func`) |
| Install | `cd cli && make install` |
| Clean | `cd cli && make clean` |
| Live tests | `cd cli && go test -tags live ./test/live/...` (env: `STACKCTL_LIVE_URL` and `STACKCTL_LIVE_API_KEY`, or `STACKCTL_LIVE_USER` + `STACKCTL_LIVE_PASS`; skipped without auth) |
| Contract tests | `cd cli && go test ./test/contract/...`; refresh the vendored swagger with `cli/test/contract/refresh-swagger.sh` |

## CLI Patterns

**Cobra command structure**: Each command group gets its own file in `cmd/`. Subcommands are added in `init()`. Use `RunE` (not `Run`) to return errors properly.

**Flag precedence**: flag > environment variable > config file. Cobra + yaml.v3 only (no Viper). `cmd/root.go` resolves the order by hand (`resolveAPIURL`, `resolveAPIKey`, with `os.Getenv`). Environment variables use the `STACKCTL_` prefix.

**Global flags**: `--output`/`-o table|json|yaml`, `--quiet`/`-q`, `--api-url`, `--api-key`, `--no-color`, `--insecure`, `--debug` (or `STACKCTL_DEBUG=1`; logs HTTP requests and responses to stderr)

**Output modes**:
- `table` (default): human-readable with colored status badges. Colors go only to a terminal: `--no-color`, `NO_COLOR` (non-empty) or a stdout that is not a terminal turn them off (`root.go`: `noColorEnv`, `isTerminalWriter`).
- Embedded structs in `pkg/types` (`Base`, `StackTemplate`, `TemplateVersion`) carry `yaml:",inline"`, so `-o yaml` has the same top-level keys as `-o json`. Add the tag to every new embedded struct.
- `json`: machine-readable, full API response
- `yaml`: machine-readable, full API response
- `--quiet`: IDs only, one per line (pipeable to `xargs`)
- `printer.PrintMessage` (text for people) writes to `printer.ErrWriter` (stderr) when `printer.MachineReadable()` (`-o json|yaml` or `-q`; not the table or a custom format), so stdout holds only data. An empty list prints `[]` in those modes, not a message. deploy/stop/clean/rollback end in `printOperation` (202 response for json/yaml, log ID for `-q`); with `--follow` in those modes `followOperation` sends the log to stderr (`followLogWriter`) and prints the instance (GET) or the log ID; `stack logs -f` prints the log as without `--follow`

**Exit codes**: an error exits 1 (`main.go`). An `*ExitError{Code, Err}` (`cmd/exitcode.go`, found with `errors.As`) exits with `Code`; when `Err` is not nil, `main.go` prints the returned error (the whole wrapped chain; `ExitError.Error()` is the text of `Err`). Only `stack exec` uses it: the remote exit code N (no message), 124 server time limit, 130 Ctrl-C or SIGTERM, 255 any other error after `RunE` starts (also the `--insecure`/`http://` refusal, an exit code outside 0..255, an exit frame without a valid `exit_code`); argument errors stay 1. A remote command can return any code, including 1, 124, 130 and 255; stackctl's own codes come with a stackctl message on stderr (remote stderr goes to the same stream, so read the message, not just its presence). stackctl itself uses 1 only for invalid flags or arguments. Ctrl-C closes the connection and exits 130 within a few seconds (a second Ctrl-C exits at once); when the command had already ended, stderr names its exit code. The remote process can keep running. Plugins exit with their own code (`plugins.go`).

**Destructive operations**: Commands that delete or clean resources must prompt for confirmation: `if err := confirmAction(cmd, msg); err != nil { return err }`. `--yes` flag skips the prompt. A declined prompt returns `errAborted` (exit 1, "aborted: not confirmed, nothing changed" on stderr), so scripts can tell that nothing changed.

## HTTP Client Pattern

**Dual auth**: JWT token (stored in `~/.stackmanager/tokens/<context>.json`) or API key (from config). API key takes precedence when both are configured.

**Session renewal** (`pkg/client/auth.go`): a username/password login stores the `refresh_token` cookie value in the token file. When `Client.Tokens` is set and no API key is used, `do()` renews the access token before a request if it expires within `RenewMargin` (60 s), and after a 401 from a non-auth endpoint it renews once and retries once. Renewal holds `config.TokenStore.Lock()` (flock on `tokens/<context>.lock`) and re-reads the file first; if another process already renewed, it uses that token without an API call (the backend revokes the session family when a rotated refresh token is reused). Refresh 401/403 → delete the token file and return the 401 login hint; network/5xx → keep the file. A refresh response without `Set-Cookie` (backend grace path) keeps the stored refresh token. SSO logins and old token files have no refresh token and are not renewed. Auth endpoints (`/auth/login`, `/auth/refresh`, `/auth/logout*`, `/auth/oidc/*`) never trigger renewal. Plugins get a renewed `STACKCTL_TOKEN` (`freshSessionToken()`); `stackctl logout` posts the bearer and the refresh cookie to `/auth/logout`, warns on failure, and always deletes the file.

**Error mapping**: HTTP status codes map to user-friendly messages (with server error message appended when available):
- 401 → "Not authenticated. Run 'stackctl login' first. (server: ...)"
- 403 → "Permission denied. (server: ...)"
- 404 → "Resource not found: <server message>"
- 409 → "Conflict: <server message>"
- 429 → "Rate limited. Try again later. (server: ...)"
- 500 → "Server error. Check backend logs. (server: ...)"

**Warning headers**: `send()` writes each `Warning` response header (for example a `299` deprecation notice) to `Client.WarnWriter` (stderr when nil) as `Warning: <warn-text>`, for every command and also on error responses. The quoted warn-text is unescaped and sanitized like server error messages; one header can hold several comma-separated warnings (warn-dates are skipped). The same text is written once per client (retries do not repeat it; the dedupe set holds at most 64 texts), and writes are serialized under `warnMu`. `sanitizeServerMessage` replaces control characters (`unicode.IsControl`: C0, DEL, C1) with a space and drops bidi controls (U+061C, U+200E, U+200F, U+202A–U+202E, U+2066–U+2069).

**User-Agent**: every request sends `User-Agent: stackctl/<version> (<GOOS>/<GOARCH>)` (`pkg/client/useragent.go`). `cmd.SetVersionInfo` calls `client.SetVersion`, so it matches `stackctl version` (empty: `dev`). `SetVersion` builds the header value once; `UserAgent()` only loads it. Set it where the request is built (`setUserAgent` in `send()` and `sendSession()`; `baseWSHeader()` for both WebSocket dials, also the `?token=` fallback), not in a transport: `stack.go` replaces `HTTPClient`.

**Plugins** (`cmd/plugins.go`): for a plugin, `Execute` sets `TraverseChildren`, so the global flags before the plugin name are parsed by stackctl and not passed to the plugin; `pluginEnv` exports them (`STACKCTL_OUTPUT`, `STACKCTL_QUIET`, `STACKCTL_NO_COLOR` + `NO_COLOR`, `STACKCTL_DEBUG`, `STACKCTL_INSECURE`, `STACKCTL_CONFIG_DIR`, plus the context values). Arguments after the plugin name pass unchanged. Contract in `EXTENDING.md`.

**Config-free commands**: `version`, `agent-guide` and `completion` skip config file loading and work even if the config is missing or corrupted. Mark such a command (and its subcommands) with the annotation `skipConfigAnnotation` (`skipsConfig` in `root.go`).

**Insecure mode**: When `--insecure` (or the `insecure` setting of the context) is active, a warning is printed to stderr. `effectiveInsecure()` (`root.go`) decides it for the requests of stackctl itself (TLS, the warning, the stream refusals). The plugin environment has its own rule: `pluginEnv` sets `STACKCTL_INSECURE` from an explicit `--insecure[=false]`, and `contextEnv` (`plugins.go`) adds `1` for a context with `insecure: true` only when the variable is not set yet.

**Base URL**: From `--api-url` flag, `STACKCTL_API_URL` env, or config file `api-url` key.

## Config System

**Config file**: `config.yaml` in the config directory. Lookup order: `STACKCTL_CONFIG_DIR`, else `$XDG_CONFIG_HOME/stackmanager`, else `~/.stackmanager`. `tokens/<context>.json` and the `.lock` files live in the same directory.

**Named contexts**: Support multiple environments (production, local, staging). `current-context` selects the active one.

```yaml
current-context: local
contexts:
  local:
    api-url: http://localhost:8081
    api-key: sk_local_...
  production:
    api-url: https://stackmanager.example.com
```

**Token storage**: `~/.stackmanager/tokens/<context>.json` — file permissions must be `0600`. Format: `{"expires_at": RFC 3339, "token": "<JWT>", "username": "...", "refresh_token": "...", "api_url": "..."}` (`refresh_token` and `api_url` are optional; files without them still load, and a missing `api_url` means the URL of the context). The refresh token is only sent (renewal, logout, revoke at login) when the client API URL equals `api_url`. `expires_at` is on the local clock: receive time + JWT lifetime (`exp - iat`, `client.LocalExpiry`), so clock skew to the server does not cause a refresh storm; without `iat` it is `exp`. Writes go to a temp file and are renamed into place (rename and read retry 5 × 50 ms for Windows). If saving a renewed token fails twice, the client deletes the file (it holds the used refresh token), keeps the rotated session in memory for the rest of the process and warns on stderr. A refresh 401/403 deletes the file only if it still holds the refresh token that was sent. `LocalExpiry` trusts at most 1 year of lifetime. Login (`replaceSession`) runs under one lock: load the old session, save the new one, then revoke the old one (5 s best-effort logout); if the save fails, the new session is revoked and the old one is kept. `tokens/<context>.lock` is the renewal lock file.

## Testing Conventions

- `testify/assert`, table-driven tests
- `pkg/` tests call `t.Parallel()` on the parent test and on subtests. Exception: a test that changes package-global state (for example `client.SetVersion`, see `TestUserAgent_Requests`) must not call `t.Parallel()`, nor its subtests; it restores the state with `t.Cleanup`. Go runs the parallel top-level tests only after the serial ones
- `cmd/` tests that touch package globals (`cfg`, `printer`, `flagAPIURL` and other flag variables) must not call `t.Parallel()`. Pure-function tests in `cmd/` (for example `resolve_test.go`) may
- No `tt := tt` needed (Go 1.22+ loop variable semantics)
- Test helpers: `setupStackTestCmd` (`cmd/stack_test.go`), `setupTestCmd` (`cmd/config_test.go`)
- Golden files live in `cmd/testdata`
- Mock HTTP server (`httptest.NewServer`) for client tests — never call real API in unit tests
- Test all output modes (table, JSON, YAML, quiet) for each command
- Test flag parsing and validation for all commands
- Coverage: the CI gate is 70% total; aim for 80%+ on `pkg/` packages

## Security Rules

- Token files must have `0600` permissions — never world-readable
- Never log or print tokens, API keys, or passwords
- Never hardcode credentials
- Validate TLS certificates by default (allow `--insecure` flag for dev only)
- Clear credentials from memory after use where possible

## Adding a New Command

1. Create command file in `cmd/` (or add subcommand to existing file)
2. Define Cobra command with `Use`, `Short`, `Long`, `RunE`
3. Add flags; resolve flag, env and config values in `cmd/root.go` style (no Viper)
4. Use `pkg/client` for API calls
5. Use `pkg/output` for formatting results
6. Add to parent command in `init()`
7. Write tests: flag parsing, success output, error handling
8. If agents or scripts need to know about it (output, prompts, exit codes, waiting), update `cmd/agent_guide.md`
9. If the command depends on a new route or server behavior, add it to `docs/claude/api-reference.md`. A new file goes in `docs/claude/structure.md`
