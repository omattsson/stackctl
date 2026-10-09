# stackctl

## Overview

Go CLI tool (Cobra + Viper) for managing Kubernetes stack deployments. Pure API client — talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct K8s interaction.

## Project Structure

```
cli/
  main.go                     # Entry point
  cmd/
    root.go                   # Root cobra command, global flags, config loading
    config.go                 # config set/get/list/use-context/current-context/delete-context
    version.go                # Version info (build-time ldflags)
    login.go                  # login, logout, whoami
    token.go                  # Token helpers (save/load/delete session, fresh token for plugins)
    stack.go                  # stack list/get/create/deploy/stop/clean/delete/status/logs/clone/extend/values/compare
    template.go               # template list/get/create/update/publish/versions/instantiate/quick-deploy
    definition.go             # definition list/get/create/update/delete/export/import
    override.go               # override list/set/delete, branch overrides, quota overrides (quota set merges with GET, --replace)
    bulk.go                   # bulk deploy/stop/clean/delete (--ids flag or positional args)
    git.go                    # git branches/validate
    cluster.go                # cluster list/get (with health summary)
    completion.go             # Shell completion generation (bash/zsh/fish/powershell)
  pkg/
    client/
      client.go              # HTTP client wrapper (auth headers, base URL, error handling)
      auth.go                # Session renewal (refresh token, 401 retry), login/refresh/logout calls
    types/
      types.go               # Client-side structs matching API responses
    config/
      config.go              # Viper-based config (~/.stackmanager/config.yaml)
      token.go               # Token file store (tokens/<context>.json) + cross-process lock
      lock_*.go              # flock (unix) / LockFileEx (windows) / no-op (other)
    output/
      output.go              # Table, JSON, YAML formatters
  test/
    e2e/
      cli_e2e_test.go        # Binary execution end-to-end tests
    integration/
      auth_integration_test.go
      config_integration_test.go
      override_integration_test.go
      stack_integration_test.go
      template_definition_integration_test.go
```

## Development Commands

| Task | Command |
|------|---------|
| Build | `cd cli && go build -o bin/stackctl .` |
| Run tests | `cd cli && go test ./... -v` |
| Lint | `cd cli && go vet ./...` |
| Coverage | `cd cli && go test ./pkg/... ./cmd/ -coverprofile=coverage.out && go tool cover -func=coverage.out` |
| Install | `cd cli && go install .` → `$GOPATH/bin/stackctl` |

## CLI Patterns

**Cobra command structure**: Each command group gets its own file in `cmd/`. Subcommands are added in `init()`. Use `RunE` (not `Run`) to return errors properly.

**Flag precedence**: flag > environment variable > config file. Viper binds all three. Environment variables use `STACKCTL_` prefix.

**Global flags**: `--output table|json|yaml`, `--quiet`, `--api-url`, `--api-key`, `--no-color`, `--insecure`

**Output modes**:
- `table` (default): human-readable with colored status badges
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

**Config-free commands**: `version` and `completion` skip config file loading and work even if the config is missing or corrupted.

**Insecure mode**: When `--insecure` is active, a warning is printed to stderr.

**Base URL**: From `--api-url` flag, `STACKCTL_API_URL` env, or config file `api-url` key.

## Config System

**Config file**: `~/.stackmanager/config.yaml` (XDG-aware)

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

- `testify/assert`, table-driven tests with `t.Parallel()` on parent and subtests
- `tt := tt` to capture range variable in table-driven tests
- Mock HTTP server (`httptest.NewServer`) for client tests — never call real API in unit tests
- Test all output modes (table, JSON, YAML, quiet) for each command
- Test flag parsing and validation for all commands
- Target 80%+ coverage on `pkg/` packages

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
- `/api/v1/stack-definitions` — CRUD + export/import
- `/api/v1/templates` — list/get/instantiate/quick-deploy/publish/versions
  - Draft and release (k8s-stack-manager v0.6.0+): `PUT /:id` and the template chart routes change only the working copy. Users, quick deploy, instantiate and definition upgrades get the latest release. After `template update-chart`, run `template publish <name|id> --version <new>` so users get the change.
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
3. Add flags and bind to Viper where appropriate
4. Use `pkg/client` for API calls
5. Use `pkg/output` for formatting results
6. Add to parent command in `init()`
7. Write tests: flag parsing, success output, error handling
