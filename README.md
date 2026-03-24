# stackctl

Command-line interface for [K8s Stack Manager](https://github.com/omattsson/k8s-stack-manager) — create, deploy, monitor, and manage Helm-based application stacks across Kubernetes clusters.

## Installation

### From source

```bash
git clone https://github.com/omattsson/stackctl.git
cd stackctl/cli
go build -o bin/stackctl .
sudo cp bin/stackctl /usr/local/bin/
```

### Go install

```bash
go install github.com/omattsson/stackctl/cli@latest
```

### From release binaries

Download the latest binary for your platform from [Releases](https://github.com/omattsson/stackctl/releases), then:

```bash
chmod +x stackctl-*
sudo mv stackctl-* /usr/local/bin/stackctl
```

## Quick Start

```bash
# 1. Configure a context
stackctl config use-context local
stackctl config set api-url http://localhost:8081

# 2. Verify your setup
stackctl version
stackctl config list

# 3. Authenticate (coming in Phase 1.2)
stackctl login

# 4. Browse templates and deploy (coming in Phase 2+)
stackctl template list
stackctl template quick-deploy 1
stackctl stack list --mine
```

## Configuration

stackctl uses named contexts to manage multiple environments. Configuration is stored in `~/.stackmanager/config.yaml`.

### Contexts

```bash
# Create and switch to a context
stackctl config use-context local
stackctl config set api-url http://localhost:8081

# Add a production context
stackctl config use-context production
stackctl config set api-url https://stackmanager.example.com
stackctl config set api-key sk_prod_...

# Switch between contexts
stackctl config use-context local

# List all contexts
stackctl config list
```

### Authentication

stackctl supports two authentication methods:

- **JWT token** — `stackctl login` prompts for credentials and stores the token in `~/.stackmanager/tokens/<context>.json`
- **API key** — `stackctl config set api-key sk_...` for non-interactive / CI use

API key takes precedence when both are configured.

### Precedence

Configuration values are resolved in this order (highest priority first):

1. Command-line flags (`--api-url`, `--api-key`)
2. Environment variables (`STACKCTL_API_URL`, `STACKCTL_API_KEY`)
3. Config file (`~/.stackmanager/config.yaml`)

## Usage

### Stack Instances

```bash
# List instances
stackctl stack list
stackctl stack list --mine --status running
stackctl stack list --cluster 1 -o json

# Create and deploy
stackctl stack create --definition 1 --name my-app --branch feature/xyz --ttl 480
stackctl stack deploy 42

# Monitor
stackctl stack status 42
stackctl stack logs 42

# Lifecycle
stackctl stack stop 42
stackctl stack clean 42
stackctl stack delete 42

# Clone an existing instance
stackctl stack clone 42

# Extend TTL
stackctl stack extend 42 --minutes 120
```

### Templates

```bash
# Browse published templates
stackctl template list --published
stackctl template get 1

# Deploy from template (one command)
stackctl template quick-deploy 1

# Or step by step
stackctl template instantiate 1 --name my-stack --branch main
```

### Stack Definitions

```bash
# List and inspect
stackctl definition list --mine
stackctl definition get 5

# Create from file
stackctl definition create --from-file definition.json

# Export / import
stackctl definition export 5 > backup.json
stackctl definition import --file backup.json
```

### Value and Branch Overrides

```bash
# Set Helm value overrides from a file
stackctl override set 42 3 --file values.yaml

# Set individual values
stackctl override set 42 3 --set image.tag=v2.0.0

# Per-chart branch overrides
stackctl override branch set 42 3 feature/hotfix

# View merged values
stackctl stack values 42
stackctl stack values 42 --chart 3

# Compare two instances side by side
stackctl stack compare 42 43
```

### Bulk Operations

```bash
# Bulk deploy/stop/clean/delete (up to 50 instances)
stackctl bulk deploy --ids 1,2,3,4,5
stackctl bulk stop --ids 1,2,3
stackctl bulk clean --ids 1,2,3

# Piping workflows with quiet mode
stackctl stack list --status stopped --mine -q | xargs -I{} stackctl stack deploy {}
```

### Clusters

```bash
stackctl cluster list
stackctl cluster get 1
```

### Git

```bash
stackctl git branches --repo https://dev.azure.com/org/project/_git/repo
stackctl git validate --repo https://dev.azure.com/org/project/_git/repo --branch main
```

## Output Formats

Most commands support multiple output formats via the `--output` flag:

```bash
# Table (default) — human-readable with colored status badges
stackctl stack list

# JSON — machine-readable, full API response
stackctl stack list -o json

# YAML — machine-readable
stackctl stack list -o yaml

# Quiet — IDs only, one per line (for piping)
stackctl stack list -q
```

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--output` | `-o` | Output format: `table`, `json`, `yaml` |
| `--quiet` | `-q` | Print only IDs (one per line) |
| `--no-color` | | Disable colored output |
| `--api-url` | | Override API server URL |
| `--api-key` | | Override API key |
| `--help` | `-h` | Show help |

## Shell Completion

```bash
# Bash
stackctl completion bash > /etc/bash_completion.d/stackctl

# Zsh
stackctl completion zsh > "${fpath[1]}/_stackctl"

# Fish
stackctl completion fish > ~/.config/fish/completions/stackctl.fish
```

## Contributing

### Prerequisites

- Go 1.22+
- A running [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) backend for integration/e2e tests (`make dev` in that repo)

### Getting Started

```bash
git clone https://github.com/omattsson/stackctl.git
cd stackctl/cli
go mod tidy
go build -o bin/stackctl .
```

### Project Structure

```
cli/
  main.go                 # Entry point
  cmd/                    # Cobra commands (one file per command group)
  pkg/
    client/               # HTTP client (auth, error handling)
    config/               # Config file management (named contexts)
    output/               # Table, JSON, YAML, quiet formatters
    types/                # Client-side structs matching API responses
  test/
    integration/          # Filesystem-based integration tests
    e2e/                  # Binary execution end-to-end tests
```

### Development Workflow

```bash
# Run all tests
cd cli
go test ./... -v

# Run only unit tests (skip integration/e2e)
go test ./... -v -short

# Run a specific test package
go test ./pkg/client/ -v

# Check coverage
go test ./pkg/... ./cmd/ -coverprofile=coverage.out
go tool cover -func=coverage.out

# Lint
go vet ./...
```

### Writing Tests

- **Unit tests** go next to the code they test (`foo_test.go` alongside `foo.go`)
- **Integration tests** go in `test/integration/` — skipped with `-short`
- **E2E tests** go in `test/e2e/` — build and run the actual binary, skipped with `-short`
- Use `testify/assert` and `testify/require`
- Table-driven tests with `t.Parallel()` where possible (not with `t.Setenv`)
- Mock HTTP servers (`httptest.NewServer`) for client tests — never call a real API in unit tests
- Target 80%+ coverage on `pkg/` packages

### Adding a New Command

1. Add types to `pkg/types/types.go` if the API returns new structs
2. Add client methods to `pkg/client/client.go`
3. Create a command file in `cmd/` with `Use`, `Short`, `Long`, `RunE`
4. Register flags and add to the parent command in `init()`
5. Use `pkg/output` for all formatted output
6. Write tests covering success, error, and output format cases

### Pull Request Guidelines

- Branch from `main` with a descriptive name (e.g., `feature/stack-commands`, `fix/token-expiry`)
- Include tests for new functionality
- Run `go test ./... -v` and `go vet ./...` before pushing
- Keep PRs focused — one feature or fix per PR
- Reference the relevant GitHub issue in the PR description (e.g., `Closes #3`)

## License

See [LICENSE](LICENSE) for details.
