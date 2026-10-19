# Copilot Instructions for stackctl

## Project Overview

stackctl is a Go CLI tool (Cobra) for managing Kubernetes stack deployments. It is a pure API client that talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct Kubernetes interaction.

## Architecture

- All code lives under `cli/` — the Go module root (`cli/go.mod`). Run Go commands from `cli/`.
- Go version: see `cli/go.mod` (1.26.9 or later). CI uses `go-version-file: cli/go.mod`.
- Commands: `cli/cmd/` (one file per command group, subcommands registered in `init()`)
- HTTP client: `cli/pkg/client/client.go` (REST), `cli/pkg/client/auth.go` (session renewal), `cli/pkg/client/websocket.go` (deployment log streaming)
- Types: `cli/pkg/types/types.go` (client-side structs matching API responses)
- Output: `cli/pkg/output/output.go` (table, JSON, YAML, quiet formatters)
- Config: `cli/pkg/config/config.go` (YAML via `gopkg.in/yaml.v3`, `~/.stackmanager/config.yaml`), `cli/pkg/config/token.go` (token files), `lock_unix.go`, `lock_windows.go` and `lock_other.go` (file locks)
- Tests: alongside code (`*_test.go`), plus `cli/test/integration/`, `cli/test/e2e/`, `cli/test/contract/` (swagger contract tests) and `cli/test/live/` (tests against a real backend, build tag `live`)

## Command Groups

| Group | Subcommands | File |
|-------|------------|------|
| `config` | set, get, list, use-context, current-context, delete-context | config.go |
| `login`/`logout`/`whoami` | top-level; supports password + OIDC loopback flow; `logout --all` ends every session of the user | login.go |
| `auth` | register | login.go |
| `stack` | list, get, create, update, deploy, stop, clean, delete, status, logs, clone, extend, values, compare, history, rollback, history-values, watch, preview, pods, recent, follow, unfollow, action (list, run, log) | stack.go; preview/pods/recent: stack_inspect.go; follow/unfollow: stack_followers.go; action: stack_action.go; TTL and stop-time helpers: ttl.go |
| `template` | list, get, create, update, delete, clone, instantiate, quick-deploy, publish, unpublish, update-chart, add-chart, remove-chart, versions (list, get, diff) | template.go; add-chart/remove-chart: chart_changes.go |
| `definition` | list, get, create, update, delete, export, import, update-chart, add-chart, remove-chart, check-upgrade, upgrade, link-template | definition.go; add-chart/remove-chart/check-upgrade/upgrade/link-template: chart_changes.go |
| `override` | list, get, set, unset, delete, branch (list/get/set/delete), quota (get/set/delete) | override.go |
| `bulk` | deploy, stop, clean, delete, template (publish, unpublish, delete) | bulk.go |
| `git` | providers, branches, validate | git.go |
| `cluster` | list, get, create, update, delete, set-default, test-connection, health, nodes, namespaces, utilization, quota (get/set/delete), shared-values (list/set/update/delete) | cluster.go |
| `analytics` | overview, templates, users | analytics.go |
| `apikey` | list, create, revoke | apikey.go |
| `audit` | log, list, export | audit.go |
| `cleanup-policy` | list, get, create, update, delete, run | cleanup_policy.go |
| `favorite` | list, add, remove, check | favorite.go |
| `dashboard` | top-level | dashboard.go |
| `notification` | list, count, read, read-all, prefs (get/set), channel (list, get, create, update, delete, events, subscriptions, subscribe, test, logs) | notification.go; channel: notification_channel.go |
| `user` | list, delete, disable, enable, reset-password, set-role | user.go |
| `orphaned` | list, delete — namespaces with the stack-manager label but no DB record | orphaned.go |
| `completion` | bash, zsh, fish, powershell | completion.go |
| `server` | info | server.go |
| `version` | top-level | version.go |

Helper (non-command) files in `cli/cmd/`:
- `browser.go` — `openBrowser()` for OIDC loopback flow. Override `browserOpener` in tests.
- `resolve.go` — `resolveStackID()` lets stack commands accept a name or an ID/UUID.
- `paging.go` — `printAllIDs()` and `listAllIDs()` collect IDs across all pages for `--quiet` list output.
- `values.go` — parses `--set key=value` flags (`parseSetFlags`, dot-separated key paths).
- `plugins.go` — `registerPlugins()` discovers `stackctl-<name>` executables on `$PATH` and registers them as subcommands (kubectl/git/gh pattern). Built-in commands always win on name collision.
- `token.go` — token storage helpers (load/save/delete JWT under `~/.stackmanager/tokens/`).
- `version.go` — the `version` command and build-time version variables.
- `root.go` — global flags, `newClient()`, `resolveAPIURL()`, `resolveAPIKey()`, `applyInsecureTLS()`.

## Code Review Guidelines

When reviewing PRs for this project, check:

### Pattern Compliance
- Commands use `RunE` (not `Run`). `SilenceUsage` is set on the root command and inherited; set it on a command only to override
- All output goes through `pkg/output` formatters (table, JSON, YAML, quiet)
- All HTTP/WebSocket calls go through `pkg/client` — never raw `http.Get` or `websocket.Dial`
- `--quiet` mode outputs only IDs (or the documented identifying names, see `commands.instructions.md`), one per line — for `xargs` piping
- Destructive commands (delete, clean) prompt for confirmation unless `--yes` is passed
- Flag precedence: flag > env var (`STACKCTL_*`) > config file
- Stack-scoped commands accept name or ID via `resolveStackID()`; commands that take non-name sub-resource IDs (chart IDs, cluster IDs) use `parseID()`
- New `stackctl-<name>` plugin-style executables on PATH are discovered automatically — do not hardcode plugin paths

### Security
- Token files must be `0600` — never world-readable
- Never log or print tokens, API keys, or passwords (client debug logging redacts auth headers)
- TLS certificate verification enabled by default; `--insecure` (or per-context `insecure: true`) prints a stderr warning
- `openBrowser()` refuses non-http/https schemes — never bypass
- Plugin discovery rejects mixed-case or non-conforming names (`pluginNamePattern`)

### Testing
- `testify/assert` + `testify/require`, table-driven tests
- `httptest.NewServer` for API mocking — never call real APIs in unit tests
- WebSocket tests use `httptest.NewServer` + `websocket.Upgrader`
- Test all output modes (table, JSON, YAML, quiet) for each command
- `cmd/` tests that touch package globals (`cfg`, `printer`, `flag*`) must not use `t.Parallel()`; pure-function tests may. `pkg/` tests use `t.Parallel()`.
- CI gate: 70% total coverage. Aim for 80%+ on `pkg/` packages

### Global Flags
- `--output`, `-o` — table (default), json, yaml
- `--quiet`, `-q` — IDs only, one per line
- `--no-color` — disable colored output
- `--api-url` — override API server URL
- `--api-key` — override API key
- `--insecure` — skip TLS verification (prints warning)
- `--debug` — log HTTP requests/responses to stderr (also via `STACKCTL_DEBUG=1`)

## Build & Test

```bash
cd cli && make build          # bin/stackctl
cd cli && make test
cd cli && make lint           # go vet + staticcheck (run `make tools` once)
cd cli && make coverage
```
