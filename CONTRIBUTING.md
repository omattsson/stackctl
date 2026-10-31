# Contributing to stackctl

Thanks for helping. The project rules for code, tests and documentation are in [CLAUDE.md](CLAUDE.md). The plugin contract is in [EXTENDING.md](EXTENDING.md).

## Prerequisites

- Go version: see `cli/go.mod` (1.27.3 or later)
- A running [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend for integration/e2e tests (`make dev` in that repo)

## Getting Started

```bash
git clone https://github.com/omattsson/stackctl.git
cd stackctl/cli
make build
```

## Project Structure

```
cli/
  main.go
  Makefile                # build, build-all, test, lint, coverage, install
  cmd/                    # Cobra commands, one file per command group
    root.go  resolve.go  paging.go  plugins.go  browser.go  token.go  values.go
    config.go  login.go  version.go  completion.go  server.go
    stack.go  stack_inspect.go  stack_followers.go  stack_action.go  ttl.go
    template.go  definition.go  override.go  bulk.go
    cluster.go  git.go  orphaned.go  user.go  apikey.go  audit.go
    analytics.go  cleanup_policy.go  favorite.go  notification.go  notification_channel.go  chart_changes.go  dashboard.go
    testdata/             # golden files
  pkg/
    client/               # HTTP client, auth renewal, WebSocket
    config/               # config file, tokens, lock files
    output/               # table, JSON, YAML, quiet formatters
    types/                # structs for API responses
  test/
    integration/          # tests with a mock server
    e2e/                  # tests that run the built binary
    contract/             # tests against the server swagger file
    live/                 # tests against a running server (build tag `live`)
```

## Development Workflow

Run these commands in `cli/`.

```bash
make tools      # install staticcheck (needed once for make lint)
make build      # build bin/stackctl with version information
make test       # go test ./... -v
make lint       # go vet and staticcheck (CI runs this target)
make coverage   # test with a coverage profile and a summary

# Unit tests only (skip integration and e2e tests)
go test ./... -short

# One package
go test ./pkg/client/ -v

# Live tests against a running server (build tag live)
STACKCTL_LIVE_URL=http://localhost:8081 STACKCTL_LIVE_API_KEY=... \
  go test -tags live ./test/live/...

# Refresh the server swagger file for the contract tests
./test/contract/refresh-swagger.sh
```

The live tests skip when no auth is set. Set `STACKCTL_LIVE_API_KEY`, or `STACKCTL_LIVE_USER` and `STACKCTL_LIVE_PASS`. CI runs them in `.github/workflows/live-tests.yml` against a new server.

## Writing Tests

- **Unit tests** go next to the code they test (`foo_test.go` next to `foo.go`).
- **Integration tests** go in `test/integration/`. `-short` skips them.
- **E2E tests** go in `test/e2e/`. They build and run the binary. `-short` skips them.
- **Contract tests** in `test/contract/` check the client paths and responses against the server swagger file.
- Use `testify/assert` and `testify/require`.
- Use table-driven tests. Tests in `pkg/` use `t.Parallel()`. Tests in `cmd/` that change package globals (`cfg`, `printer`, flag variables) must not use `t.Parallel()`. Tests that use `t.Setenv` cannot use it either.
- Use mock HTTP servers (`httptest.NewServer`) for client tests. Never call a real API in unit tests.
- CI fails below 70% total coverage. Aim for 80% or more on `pkg/` packages.

## Adding a New Command

1. Add types to `pkg/types/types.go` if the API returns new structs
2. Add client methods to `pkg/client/client.go`
3. Create a command file in `cmd/` with `Use`, `Short`, `Long`, `RunE`
4. Register flags and add to the parent command in `init()`
5. Use `pkg/output` for all formatted output
6. Write tests covering success, error, and output format cases

## Pull Request Guidelines

- Branch from `main` with a descriptive name (e.g., `feature/stack-commands`, `fix/token-expiry`)
- Include tests for new functionality
- Run `make test` and `make lint` in `cli/` before you push
- Keep PRs focused — one feature or fix per PR
- Reference the relevant GitHub issue in the PR description (e.g., `Closes #3`)
