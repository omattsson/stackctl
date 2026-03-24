# stackctl

## Overview

Go CLI tool (Cobra + Viper) for managing Kubernetes stack deployments. Pure API client — talks to the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend. No backend logic, no frontend, no database, no direct K8s interaction.

## Project Structure

```
cli/
  main.go                     # Entry point
  cmd/
    root.go                   # Root cobra command, global flags, config loading
    config.go                 # config set/get/list/use-context
    version.go                # Version info (build-time ldflags)
    login.go                  # login, logout, whoami
    stack.go                  # stack list/get/create/deploy/stop/clean/delete/status/logs/clone/extend
    template.go               # template list/get/instantiate/quick-deploy
    definition.go             # definition list/get/create/update/delete/export/import
    override.go               # override list/set/delete, branch overrides, quota overrides
    bulk.go                   # bulk deploy/stop/clean/delete
    git.go                    # git branches/validate
    cluster.go                # cluster list/get
    completion.go             # Shell completion generation
  pkg/
    client/
      client.go              # HTTP client wrapper (auth headers, base URL, error handling)
    types/
      types.go               # Client-side structs matching API responses
    config/
      config.go              # Viper-based config (~/.stackmanager/config.yaml)
    output/
      output.go              # Table, JSON, YAML formatters
```

## Development Commands

| Task | Command |
|------|---------|
| Build | `make build` → `bin/stackctl` |
| Build all platforms | `make build-all` |
| Run tests | `make test` or `go test ./... -v` |
| Lint | `make lint` (`go vet` + `staticcheck`) |
| Install | `make install` → `$GOPATH/bin/stackctl` |

## CLI Patterns

**Cobra command structure**: Each command group gets its own file in `cmd/`. Subcommands are added in `init()`. Use `RunE` (not `Run`) to return errors properly.

**Flag precedence**: flag > environment variable > config file. Viper binds all three. Environment variables use `STACKCTL_` prefix.

**Global flags**: `--output table|json|yaml`, `--quiet`, `--api-url`, `--api-key`, `--no-color`

**Output modes**:
- `table` (default): human-readable with colored status badges
- `json`: machine-readable, full API response
- `yaml`: machine-readable, full API response
- `--quiet`: IDs only, one per line (pipeable to `xargs`)

**Destructive operations**: Commands that delete or clean resources must prompt for confirmation. `--yes` flag skips the prompt.

## HTTP Client Pattern

**Dual auth**: JWT token (stored in `~/.stackmanager/tokens/<context>.json`) or API key (from config). API key takes precedence when both are configured.

**Error mapping**: HTTP status codes map to user-friendly messages:
- 401 → "Not authenticated. Run 'stackctl login' first."
- 403 → "Permission denied."
- 404 → "Resource not found."
- 409 → "Conflict (resource already exists or version mismatch)."
- 429 → "Rate limited. Try again later."
- 500 → "Server error. Check backend logs."

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

**Token storage**: `~/.stackmanager/tokens/<context>.json` — file permissions must be `0600`.

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
- `/api/v1/stack-instances` — CRUD + deploy/stop/clean/status/logs/clone/extend
- `/api/v1/stack-instances/bulk` — bulk operations
- `/api/v1/stack-definitions` — CRUD + export/import
- `/api/v1/templates` — list/get/instantiate/quick-deploy
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
