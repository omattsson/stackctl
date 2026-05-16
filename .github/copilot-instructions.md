# Copilot Instructions for stackctl

## Project Overview

stackctl is a Go CLI tool (Cobra + Viper) for managing Kubernetes stack deployments. It is a pure API client that talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct Kubernetes interaction.

## Architecture

- All code lives under `cli/` — the Go module root
- Commands: `cli/cmd/` (one file per command group, subcommands registered in `init()`)
- HTTP client: `cli/pkg/client/client.go` (REST) + `cli/pkg/client/websocket.go` (deployment log streaming)
- Types: `cli/pkg/types/types.go` (client-side structs matching API responses)
- Output: `cli/pkg/output/output.go` (table, JSON, YAML, quiet formatters)
- Config: `cli/pkg/config/config.go` (Viper-based, `~/.stackmanager/config.yaml`)
- Tests: alongside code (`*_test.go`), plus `cli/test/integration/`, `cli/test/e2e/`, and `cli/test/live/` (opt-in tests against a real backend)

## Command Groups

| Group | Subcommands | File |
|-------|------------|------|
| `config` | set, get, list, use-context, current-context, delete-context | config.go |
| `login`/`logout`/`whoami` | top-level; supports password + OIDC loopback flow | login.go |
| `stack` | list, get, create, deploy, stop, clean, delete, status, logs, clone, extend, values, compare, history, rollback, history-values | stack.go |
| `template` | list, get, instantiate, quick-deploy | template.go |
| `definition` | list, get, create, update, delete, export, import | definition.go |
| `override` | list, set, delete, branch (list/set/delete), quota (get/set/delete) | override.go |
| `bulk` | deploy, stop, clean, delete | bulk.go |
| `git` | branches, validate | git.go |
| `cluster` | list, get + health/quota/shared-values subcommands | cluster.go |
| `orphaned` | list, clean — namespaces with the stack-manager label but no DB record | orphaned.go |
| `completion` | bash, zsh, fish, powershell | completion.go |
| `version` | top-level | version.go |

Helper (non-command) files in `cli/cmd/`:
- `browser.go` — `openBrowser()` for OIDC loopback flow. Override `browserOpener` in tests.
- `resolve.go` — `resolveStackID()` lets stack commands accept a name or an ID/UUID.
- `plugins.go` — `registerPlugins()` discovers `stackctl-<name>` executables on `$PATH` and registers them as subcommands (kubectl/git/gh pattern). Built-in commands always win on name collision.
- `token.go` — token storage helpers (load/save/delete JWT under `~/.stackmanager/tokens/`).
- `root.go` — global flags, `newClient()`, `resolveAPIURL()`, `applyInsecureTLS()`.

## Code Review Guidelines

When reviewing PRs for this project, check:

### Pattern Compliance
- Commands use `RunE` (not `Run`) and `SilenceUsage: true`
- All output goes through `pkg/output` formatters (table, JSON, YAML, quiet)
- All HTTP/WebSocket calls go through `pkg/client` — never raw `http.Get` or `websocket.Dial`
- `--quiet` mode outputs only IDs (or identifying names like namespaces for `orphaned list`), one per line — for `xargs` piping
- Destructive commands (delete, clean) prompt for confirmation unless `--yes` is passed
- Flag precedence: flag > env var (`STACKCTL_*`) > config file
- Stack-scoped commands accept name or ID via `resolveStackID()`; commands that only take numeric/sub-resource IDs (chart IDs, cluster IDs) use `parseID()`
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
- `cmd/` tests must NOT use `t.Parallel()` (mutate package-level globals); `pkg/` tests should
- Target 80%+ coverage on `pkg/` packages

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
cd cli && go build -o bin/stackctl .
cd cli && go test ./... -v
cd cli && go vet ./...
```
