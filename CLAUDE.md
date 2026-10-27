# stackctl

## Overview

Go CLI tool (Cobra + yaml.v3) for managing Kubernetes stack deployments. Pure API client — talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct K8s interaction.

Go version: see `cli/go.mod` (1.26.10 or later); the Dockerfile builder image may be newer.

## Project Structure

```
.github/
  workflows/                  # ci.yml, docker.yml, live-tests.yml, release.yml, security.yml
  instructions/               # Copilot path instructions
.claude/agents/               # Claude Code agent definitions
.goreleaser.yml               # Release config (GoReleaser)
Dockerfile                    # Container image
install.sh                    # Install script (honours INSTALL_DIR)
LICENSE                       # MIT license
entities.json, mempalace.yaml # Mempalace mining config
cli/                          # Go module root (github.com/omattsson/stackctl/cli); run go commands here
  main.go                     # Entry point
  Makefile                    # build, build-all, test, lint, coverage, install, clean, tools
  cmd/
    root.go                   # Root cobra command, global flags, config loading, flag > env > config (resolveAPIURL, resolveAPIKey)
    config.go                 # config set/get/list/use-context/current-context/delete-context
    version.go                # Version info (build-time ldflags)
    agent_guide.go, .md       # agent-guide: embedded Markdown guide for agents and scripts ({{VERSION}} = build version); agent_guide_test.go checks every stackctl command and flag in it
    login.go                  # login, logout (--all: logout-all first), whoami, auth register
    token.go                  # Token helpers (save/load/delete session, fresh token for plugins)
    stack.go                  # stack list/get/create/update/deploy/stop/clean/delete/status/logs/clone/extend/values/compare/history/rollback/history-values/watch
    stack_inspect.go          # stack preview/pods/recent (deploy-preview, live pods and events, recent)
    stack_action.go           # stack action list/run/log (custom actions of the hooks config; job log polling with backoff)
    stack_followers.go        # stack follow/unfollow (in-app notifications; not the --follow log flag)
    ttl.go                    # --ttl/--stop-at/--until parsing, TTL limit check (GET /api/v1/ui-config, 30-day fallback), expiry text
    server.go                 # server info (ping, ui-config; public routes, no credentials)
    template.go               # template list/get/create/update/clone/publish/unpublish/versions/instantiate/quick-deploy/delete
    definition.go             # definition list/get/create/update/delete/export/import/update-chart
    chart_changes.go          # definition add-chart/remove-chart/check-upgrade/upgrade/link-template, template add-chart/remove-chart
    override.go               # override list/get/set/unset/delete, branch overrides (list/get/set/delete), quota overrides (quota set merges with GET, --replace)
    bulk.go                   # bulk deploy/stop/clean/delete, bulk template publish/unpublish/delete (--ids flag or positional args)
    git.go                    # git providers/branches/validate
    cluster.go                # cluster list/get/create/update/delete/set-default/test-connection/health/nodes/namespaces/utilization, quota get/set/delete, shared-values list/set/update/delete (update: GET list, merge, PUT the full record)
    analytics.go              # analytics overview/templates/users
    apikey.go                 # apikey list/create/revoke
    audit.go                  # audit log list/export
    cleanup_policy.go         # cleanup-policy list/get/create/update/delete/run
    favorite.go               # favorite list/add/remove/check
    dashboard.go              # dashboard (GET /api/v1/dashboard: failing, expiring, recent deployments, clusters)
    notification.go           # notification list/count/read/read-all/prefs
    notification_channel.go   # notification channel list/get/create/update/delete/events/subscriptions/subscribe/test/logs (webhook URL masked unless --show-url)
    orphaned.go               # orphaned list/delete
    user.go                   # user list/delete/disable/enable/reset-password/set-role
    values.go                 # --set value parsing helpers (key paths, scalar types) for stack and override commands
    browser.go                # Open a URL in the browser (SSO login)
    paging.go                 # List paging helpers (page, pageSize, listAllIDs)
    resolve.go                # Name-to-ID resolution
    plugins.go                # Plugin discovery (stackctl-<name> on PATH) and exec
    completion.go             # Shell completion generation (bash/zsh/fish/powershell)
    testdata/                 # Golden files for cmd tests
  pkg/
    client/
      client.go              # HTTP client wrapper (auth headers, base URL, error handling)
      auth.go                # Session renewal (refresh token, 401 retry), login/refresh/logout calls
      websocket.go           # WebSocket client (watch, deploy log streaming)
    types/
      types.go               # Client-side structs matching API responses
    config/
      config.go              # YAML config via yaml.v3 (config.yaml in the config dir)
      token.go               # Token file store (tokens/<context>.json) + cross-process lock
      lock_*.go              # flock (unix) / LockFileEx (windows) / no-op (other)
    output/
      output.go              # Table, JSON, YAML formatters
  test/
    e2e/                     # Binary execution end-to-end tests (cli_e2e_test.go)
    integration/             # Integration tests against a mock API (*_integration_test.go)
    contract/                # Swagger contract tests; refresh-swagger.sh refreshes testdata/swagger.json
    live/                    # Live tests against a real backend (build tag `live`)
```

## Development Commands

| Task | Command |
|------|---------|
| Build | `cd cli && make build` (or `go build -o bin/stackctl .`) |
| Build all platforms | `cd cli && make build-all` |
| Run tests | `cd cli && make test` (`go test ./... -v`) |
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

**Destructive operations**: Commands that delete or clean resources must prompt for confirmation. `--yes` flag skips the prompt.

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

**Warning headers**: `send()` writes each `Warning` response header (for example a `299` deprecation notice) to `Client.WarnWriter` (stderr when nil) as `Warning: <warn-text>`, for every command and also on error responses. The quoted warn-text is unescaped and sanitized like server error messages; one header can hold several comma-separated warnings (warn-dates are skipped). The same text is written once per client (retries do not repeat it; the dedupe set holds at most 64 texts), and writes are serialized under `warnMu`. `sanitizeServerMessage` replaces control characters (`unicode.IsControl`: C0, DEL, C1) with a space and drops bidi controls (U+200E, U+200F, U+202A–U+202E, U+2066–U+2069).

**User-Agent**: every request sends `User-Agent: stackctl/<version> (<GOOS>/<GOARCH>)` (`pkg/client/useragent.go`). `cmd.SetVersionInfo` calls `client.SetVersion`, so it matches `stackctl version` (empty: `dev`). `SetVersion` builds the header value once; `UserAgent()` only loads it. Set it where the request is built (`setUserAgent` in `send()` and `sendSession()`; `baseWSHeader()` for both WebSocket dials, also the `?token=` fallback), not in a transport: `stack.go` replaces `HTTPClient`.

**Plugins** (`cmd/plugins.go`): for a plugin, `Execute` sets `TraverseChildren`, so the global flags before the plugin name are parsed by stackctl and not passed to the plugin; `pluginEnv` exports them (`STACKCTL_OUTPUT`, `STACKCTL_QUIET`, `STACKCTL_NO_COLOR` + `NO_COLOR`, `STACKCTL_DEBUG`, `STACKCTL_INSECURE`, `STACKCTL_CONFIG_DIR`, plus the context values). Arguments after the plugin name pass unchanged. Contract in `EXTENDING.md`.

**Config-free commands**: `version`, `agent-guide` and `completion` skip config file loading and work even if the config is missing or corrupted.

**Insecure mode**: When `--insecure` is active, a warning is printed to stderr.

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

## API Reference

Backend: [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager)

All API calls go to `/api/v1/*`. Key route groups:
- `/api/v1/auth` — login, register, current user
- `/api/v1/stack-instances` — CRUD + deploy/stop/clean/status/logs/clone/extend/values/compare
  - `POST /:id/extend` with `{"minutes": N}` adds N minutes to the expiry (never earlier, TTL unchanged, capped at now + 30 days; N <= 0 or no TTL and no expiry → 400). `stack extend --minutes` uses this body and needs k8s-stack-manager v0.6.0 or later. After the call it checks that the new expiry is at least max(old expiry, server time) + N − 2 min (server time = `updated_at` of the response, else the local clock) or at the 30-day cap; if not (an older server ignored `minutes`), it prints the result and fails with "upgrade k8s-stack-manager to v0.6.0 or later". The deprecated `{"ttl_minutes": N}` resets the expiry to now + N and sets the TTL; the server adds a `Warning` header. Only `stack extend --reset-ttl` (deprecated) sends it. `--yes` on `stack extend` is hidden and has no effect (script compatibility).
  - List paging (`cmd/paging.go`): instances, definitions and templates return `data`, `total`, `page`, `pageSize` (default 25, max 100; v0.7.0+ also pages `owner=me` and `name`). List commands write "Showing X of TOTAL" to stderr when `total` > rows. `-q` without `--page` loops pages with `pageSize=100` (`listAllIDs`: stops at `total`, a short page, or a page without new IDs). Name resolution errors add "(N matches, first M shown)".
- `/api/v1/stack-instances/bulk` — bulk operations
  - `GET /:id` returns `values_drift` (v0.6.0+, not on lists); the rollback response has `values_drift` (`*bool`, only for `target_log_id`) and `warning`; the clone response adds `warning` (quota override not copied; v0.7.0+). `stack rollback -o json|yaml` prints the response. `stack get`, `stack rollback` and `stack clone` write these warnings to stderr as `Warning: <text>` (`printServerWarning`, sanitized). `stack get` fills `warning` with `types.ValuesDriftWarning` when the server sends `values_drift` without text, so `-o json` has both fields.
  - `owner_username`, `cluster_name`, `definition_name` (k8s-stack-manager#470, v0.7.0+) are optional; tables show the name (`displayName`) or "name (id)" (`displayNameWithID`), else the ID. No extra API calls.
  - `PUT /:id/quota-overrides` replaces the whole override. `override quota set` does GET (only 404 "Instance quota override not found" = no override), applies the given flags (`""` clears a string field; pod_limit only via `--replace` or delete; `--pod-limit 0` = no limit) and PUTs; `--replace` skips the GET. An empty result is refused (points to `override quota delete`). GET and PUT are not atomic: a concurrent change is overwritten. Owners without admin/devops get 403 above the cluster quota (v0.7.0+); a value equal to the stored value passes.
- `/api/v1/audit-logs` — list/export; `--action` and `--entity-type` are exact matches (`deploy`, `stack_instance`, …; v0.7.0+, see `auditFilterValuesHelp`). Older servers: HTTP method + last route segment (`create` / `deploy`)
- `/api/v1/users` — list/delete/disable/enable/password (admin). `PUT /:id/role` `{"role"}` (k8s-stack-manager v0.8.0+, `user set-role <user> <role>`, user by ID or username via `resolveUserID`): response `id`, `old_role`, `new_role`, `changed`; 403 own role / caller not admin, 409 SSO user / last admin, 400 invalid role; a plain 404 (no route) maps to the v0.8.0 upgrade hint (`setRoleError`)
- `/ws` — the server closes a socket with code 1008 and reason `session revoked` or `token expired`. `Client.Watch` returns a `WatchStream`; `Err()` gives a `*WSClosedError` ("Connection closed by the server: <reason>. Log in again."). `stack watch` exits non-zero on it; for `token expired` it calls `Client.RenewSession` (forced renewal, false without refresh token) and reconnects, at most once per 30 s (`stackWatchMinReconnect`), then with `--id` GETs each pending instance (`watchCatchUp`) so a terminal status in the gap counts. Ctrl-C during renewal or dial exits 0. Another connection loss: `--id` with pending instances exits non-zero ("connection lost before all instances reached a terminal status"), else a stderr note and exit 0. `StreamDeploymentLogs` returns the same `WSClosedError`; `--follow` (`followLogsCtx`, op deploy/stop/clean/rollback, "" for `stack logs -f`) uses the same `renewAfterClose`; it reads the instance after the first connect (the server sets deploying/stopping/cleaning before the 202; not for `logs -f`) and after each reconnect (`StreamOptions.AfterSubscribe`). When it cannot follow to the end it says that the operation continues on the server and points to `stack watch --id` / `stack status` (never "run again"); errors name the operation ("rollback failed (status error): ..."). Terminal statuses live in `client.IsTerminalStatus` (running, stopped, draft, error, partial, failed) and `client.IsFailedStatus` (error, partial, failed → exit 1)
- `/api/v1/admin/orphaned-namespaces` — `orphaned list [--details]`; `DELETE /:ns?confirm=<ns>` is needed for a namespace without the label managed-by=k8s-stack-manager (409 without it; `orphaned delete --confirm`). `test/contract/path_contract_test.go` checks every `/api/v1` literal of `pkg/client` and `cmd` against the vendored swagger
- `POST /api/v1/auth/logout-all` — `logout --all`: uses the session token of the context when a token file exists (also when an API key is set), else the API key; a 401 gets one forced `RenewSession` and one retry. On failure the command stops and keeps the token file; on success the normal logout follows.
- `GET /api/v1/dashboard` — `dashboard`: covers all users; `failing_instances` has status error only, `expiring_soon` the next hour. For the role `user`, `clusters` has ID and name only (no health): the table hides clusters without `health_status`.
- `GET /api/v1/favorites/check?entity_type=&entity_id=` — `favorite check` prints yes or no (exit 0).
- `PUT /api/v1/clusters/:id/shared-values/:valueId` replaces the full record (no GET for one entry): `shared-values update` reads the list, merges and sends name, description, values and priority. `--set` re-marshals the stored YAML (comments and key order are lost; a stderr note says so).
- `/api/v1/admin/notification-channels` (devops or admin) — `notification channel ...`. The responses carry the full `webhook_url` (it can hold a token): every output mode masks it to scheme and host (`maskWebhookURL`) unless `--show-url`. The server never returns the secret; the CLI reads it from `--secret-env NAME` or `--secret-stdin` only. The URL can also come from `--url-env NAME`; error texts never contain the URL. `PUT /:id` changes only the fields sent (`secret: ""` removes the secret; `filters` replaces all filters, `{}` removes them). `PUT /:id/subscriptions` replaces all event types (400 for an unknown one). `POST /:id/test` answers 200 with `success`; the command exits 1 when it is false. Create and update return `warnings` (unknown filter IDs) → stderr.
- `/api/v1/notifications` and `/count` — the server caps the counts (k8s-stack-manager #654): `total` is exact up to offset + 1000 (`total_capped`), `unread_count` up to 1000 (`unread_count_capped`); an older server sends neither flag (false). `notification list` prints "1000+" in the footer; `notification count` keeps stdout a bare integer and writes a note to stderr when capped; `-o json`/`-o yaml` carry the flags.
- `/api/v1/notifications/preferences` — `notification prefs set` warns for an event type not in `notificationEventTypes` (`notifier.AllEventTypes()` of k8s-stack-manager); v0.8.0+ servers return 400
- `/api/v1/stack-definitions` — CRUD + export/import
  - `POST /:id/charts` and `DELETE /:id/charts/:chartId` (devops or admin; `definition add-chart`, `remove-chart`). A linked definition cannot lose a chart that the template requires (409 "Cannot delete required chart: X").
  - `GET /:id/check-upgrade` sends only `{"upgrade_available": false}` when there is no upgrade (no source template, unpublished template, latest version). `printUpgradeCheck` reads the definition (`source_template_id`, `source_template_version`) for the reason. The server marks a chart `modified` only for other default values or another repository URL; the table also marks a chart with `has_differences` in `chart_diffs` as modified.
  - `POST /:id/upgrade` (owner, devops or admin) replaces the default values of the template charts. `definition upgrade` shows the check on stderr and confirms unless `--yes`. 409 "Definition is already at the latest version" or "Template has no published version".
  - `PUT /:id/template-link` `{"template_id","version"}` (k8s-stack-manager v0.11.0+, devops or admin; empty `template_id` removes the link). A plain 404 (no route) maps to the v0.11.0 upgrade hint (`linkTemplateError`).
- `/api/v1/templates` — list/get/instantiate/quick-deploy/publish/versions
  - Draft and release (k8s-stack-manager v0.6.0+): `PUT /:id` and the template chart routes change only the working copy. `POST /:id/charts` and `DELETE /:id/charts/:chartId` (`template add-chart`, `remove-chart`) write a note to stderr: publish with a new `--version` (the working-copy version already exists after a change). Users, quick deploy, instantiate and definition upgrades get the latest release. After `template update-chart`, run `template publish <name|id> --version <new>` so users get the change.
  - `POST /:id/publish` takes an optional body `{"version", "change_summary"}`; `template publish` sends it only when `--version` or `--change-summary` is set. Without a version the server uses the working-copy version. 409 "Version x already exists" and 400 "Version is required to publish" map to messages with the next command. 200 with `snapshot_created: false` prints "No changes since version X; no new version created". With `--version` or `--change-summary`, the command first does GET `/:id`; when `has_unpublished_changes` is absent (older server, which ignores the body) it fails with the v0.6.0 upgrade hint before the publish.
  - `PUT /:id`: v0.6.0+ changes only the fields sent (`PatchTemplateRequest`, pointer fields; `""` clears); older servers replace name/description/category/version/default_branch. `template update` does GET first: with `has_unpublished_changes` present it sends only the changed fields, else the full record (`UpdateTemplateRequest`). `--version` sets the working-copy version; `--description ""` clears.
  - Name resolution (`resolveTemplateID`, used by `template publish` and bulk template commands): sends `?name=` (v0.5.0 ignores it), pages through the result with `pageSize=100`, and matches the exact name on the client. 0 → not found; more than 1 → "multiple templates named X: <ids>".
  - `GET /:id` adds `published_version`, `published_version_id`, `published_charts`, `has_unpublished_changes` (absent on older servers; `StackTemplate.HasUnpublishedChanges == nil` means an older server). `template get` marks each chart against the release; `--released` lists the released charts.
  - Instantiate and quick deploy of a template without a release: 409 "Template has no published version" (older quick deploy: 400 "Template is not published") map to "publish it first: stackctl template publish X --version ...". A best-effort GET picks a plain `stackctl template publish X` when the template has a release (or the server is older).
  - `template get` labels the version "Working copy version" when `has_unpublished_changes` is present (managers); chart notes compare values after trimming trailing whitespace (like the server's `NormalizeValues`). An unpublished template with a release shows "(unpublished; quick deploy and use are blocked until published)".
  - Version list/detail add `created_by_username` (fallback `created_by`). The diff accepts `working` for either side; sides carry id, change_summary, created_by(_username), created_at, is_working_copy.
- `/api/v1/git` — branch listing, validation
- `/api/v1/clusters` — list/get/health
- `/api/v1/stack-instances/:id/overrides` — value overrides
- `/api/v1/stack-instances/:id/branches` — branch overrides

## Adding a New Command

1. Create command file in `cmd/` (or add subcommand to existing file)
2. Define Cobra command with `Use`, `Short`, `Long`, `RunE`
3. Add flags; resolve flag, env and config values in `cmd/root.go` style (no Viper)
4. Use `pkg/client` for API calls
5. Use `pkg/output` for formatting results
6. Add to parent command in `init()`
7. Write tests: flag parsing, success output, error handling
8. If agents or scripts need to know about it (output, prompts, exit codes, waiting), update `cmd/agent_guide.md`
