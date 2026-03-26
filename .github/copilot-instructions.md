# Copilot Instructions for stackctl

## Project Overview

stackctl is a Go CLI tool (Cobra + Viper) for managing Kubernetes stack deployments. It is a pure API client that talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct Kubernetes interaction.

## Architecture

- All code lives under `cli/` — the Go module root
- Commands: `cli/cmd/` (one file per command group, subcommands in `init()`)
- HTTP client: `cli/pkg/client/client.go` (47 methods, dual auth: JWT + API key)
- Types: `cli/pkg/types/types.go` (34 types matching API responses)
- Output: `cli/pkg/output/output.go` (table, JSON, YAML, quiet formatters)
- Config: `cli/pkg/config/config.go` (Viper-based, `~/.stackmanager/config.yaml`)
- Tests: alongside code (`*_test.go`), plus `cli/test/integration/` and `cli/test/e2e/`

## Command Groups (47 commands total)

| Group | Subcommands | File |
|-------|------------|------|
| `config` | set, get, list, use-context, current-context, delete-context | config.go |
| `login/logout/whoami` | (top-level) | login.go |
| `stack` | list, get, create, deploy, stop, clean, delete, status, logs, clone, extend, values, compare | stack.go |
| `template` | list, get, instantiate, quick-deploy | template.go |
| `definition` | list, get, create, update, delete, export, import | definition.go |
| `override` | list, set, delete, branch (list/set/delete), quota (get/set/delete) | override.go |
| `bulk` | deploy, stop, clean, delete | bulk.go |
| `git` | branches, validate | git.go |
| `cluster` | list, get | cluster.go |
| `completion` | bash, zsh, fish, powershell | completion.go |
| `version` | (top-level) | version.go |

## Code Review Guidelines

When reviewing PRs for this project, check:

### Pattern Compliance
- Commands use `RunE` (not `Run`) and `SilenceUsage: true`
- All output goes through `pkg/output` formatters (table, JSON, YAML, quiet)
- All API calls go through `pkg/client` — never raw `http.Get`
- `--quiet` mode outputs only numeric IDs, one per line (for `xargs` piping)
- Destructive commands (delete, clean) prompt for confirmation unless `--yes` is passed
- Flag precedence: flag > env var (`STACKCTL_*`) > config file

### Security
- Token files must be `0600` — never world-readable
- Never log or print tokens, API keys, or passwords
- TLS certificate verification enabled by default (`--insecure` for dev only)

### Testing
- `testify/assert` + `testify/require`, table-driven tests
- `httptest.NewServer` for API mocking — never call real APIs in unit tests
- Test all output modes (table, JSON, YAML, quiet) for each command
- Target 80%+ coverage on `pkg/` packages

### Global Flags
- `--output`, `-o` — table (default), json, yaml
- `--quiet`, `-q` — IDs only, one per line
- `--no-color` — disable colored output
- `--api-url` — override API server URL
- `--api-key` — override API key
- `--insecure` — skip TLS verification

## Build & Test

```bash
cd cli && go build -o bin/stackctl .
cd cli && go test ./... -v
cd cli && go vet ./...
```
