# stackctl

**The command-line client for [K8s Stack Manager](https://github.com/omattsson/k8s-stack-manager): deploy and operate per-developer Helm stacks on Kubernetes from your terminal, scripts, CI and AI agents, without a kubeconfig.**

stackctl talks to the K8s Stack Manager API, so you need a K8s Stack Manager server. To try one on your machine, run `make demo` in the [k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager) repository.

- **No cluster credentials.** Deploy, follow the logs, `stack port-forward` and `stack exec` go through the API, with your login and your role.
- **Built for scripts and CI.** `-o json|yaml`, `-q` for IDs, defined exit codes, `--follow` and `stack watch` to wait for a result, `--yes` for confirmations, and API keys for CI.
- **Built for AI agents.** `stackctl agent-guide` prints a guide that matches the installed version.
- **Plugins in any language.** An executable `stackctl-<name>` on `PATH` becomes `stackctl <name>`.
- **Several environments.** Named contexts, username and password or SSO login, and automatic session renewal.

<p align="center">
  <img src="assets/stackctl-help.svg" alt="stackctl CLI" width="700">
</p>

## Installation

```bash
# Homebrew (macOS, Linux)
brew install --cask omattsson/tap/stackctl

# Or the install script (Linux, macOS; amd64 and arm64)
curl -fsSL https://raw.githubusercontent.com/omattsson/stackctl/main/install.sh | sudo bash
```

Release binaries with checksums, release candidates, building from source and the container image: see [docs/installation.md](docs/installation.md).

## Quick start

```bash
# Point stackctl at your server and log in
stackctl config use-context my-company
stackctl config set api-url https://stacks.example.com
stackctl login                      # or: stackctl login --sso

# Start a stack from a template and follow its deploy
stackctl template list
stackctl template quick-deploy <template-id> --name my-stack
stackctl stack logs my-stack -f

# Work with it
stackctl stack pods my-stack
stackctl stack port-forward my-stack web 8080:80
stackctl stack exec my-stack backend -- php -v
```

See [docs/commands.md](docs/commands.md) for every command group, and [docs/configuration.md](docs/configuration.md) for contexts and login.

<p align="center">
  <img src="assets/stackctl-stack.svg" alt="stackctl stack commands" width="700">
</p>

## Use with AI agents and scripts

`stackctl agent-guide` prints a Markdown guide for AI agents (Claude Code, Copilot, Codex, ...) and scripts: machine-readable output, exit codes, authentication without a prompt, `--yes`, waiting for deploys, and common tasks. The guide is part of the binary, so it matches the installed version, and it works without a config. `stackctl --help` points agents to it.

To give an agent the guide up front, add a line to the instructions of your project (for example `CLAUDE.md` or `AGENTS.md`):

```markdown
Before you use stackctl, run `stackctl agent-guide` and follow it.
```

Or save it next to them: `stackctl agent-guide > STACKCTL.md`.

## Extending — add your own subcommands

Drop an executable named `stackctl-<name>` anywhere on your `$PATH` and it becomes `stackctl <name>` automatically. No plugin SDK, no recompile, no changes to stackctl itself. Same pattern as `git`, `kubectl`, and `gh`.

Because the contract is "any executable with the right name", plugins can be written in any language (shell, Python, Go, Node, Rust, …) and shipped however your team already distributes binaries.

Quick example:

```bash
cat > ~/.local/bin/stackctl-hello <<'EOF'
#!/usr/bin/env bash
echo "Hello! API=${STACKCTL_API_URL} args=$*"
EOF
chmod +x ~/.local/bin/stackctl-hello

stackctl hello world
stackctl --help | grep hello
```

Output:

```
Hello! API=http://... args=world
hello    Plugin: hello
```

Plugins get the user's environment plus the settings of the current context and the global flags as `STACKCTL_*` variables (see [EXTENDING.md](EXTENDING.md#what-plugins-receive)). Put the global flags before the plugin name. Built-in subcommands always win on name collisions. This prevents a malicious `stackctl-config` on `PATH` from intercepting credentials.

**[Full guide: EXTENDING.md](EXTENDING.md)** — tutorial, recipes in bash/Python/Go, best practices, and how plugins pair with [server-side action webhooks](https://github.com/omattsson/k8s-stack-manager/blob/main/EXTENDING.md) for end-to-end custom operations.

## Documentation

| Document | Contents |
|---|---|
| [docs/installation.md](docs/installation.md) | All install methods, checksums, release candidates, the container image |
| [docs/configuration.md](docs/configuration.md) | Contexts, the config directory, login, session renewal, precedence |
| [docs/commands.md](docs/commands.md) | Examples for every command group, output formats, confirmations, global flags, shell completion |
| [docs/server-versions.md](docs/server-versions.md) | Which k8s-stack-manager version each feature needs |
| [EXTENDING.md](EXTENDING.md) | Plugins: the contract, recipes, best practices |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development setup, project structure, tests, pull requests |
| `stackctl agent-guide` | Guide for AI agents and scripts: output, exit codes, waiting, common tasks |
| `stackctl <command> --help` | All flags of a command |

## License

stackctl is released under the MIT license. See [LICENSE](LICENSE) for details.
