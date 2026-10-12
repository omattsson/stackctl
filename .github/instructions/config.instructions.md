---
applyTo: "cli/pkg/config/**"
---

# Config Package Conventions

## Storage Layout
- Config file: `~/.stackmanager/config.yaml`. Directory resolved by `ConfigDir()`: checks `STACKCTL_CONFIG_DIR` env first, then `XDG_CONFIG_HOME` (appending `/stackmanager`), then falls back to `~/.stackmanager` via `os.UserHomeDir()`.
- Tokens: `~/.stackmanager/tokens/<context>.json` — one file per context. File mode MUST be `0600`; the directory MUST be `0700`. Verify mode after every write.
- Never store secrets (`api-key`, password) in process env beyond the lifetime of the command unless explicitly set by the user.

## Named Contexts
- Multiple environments are supported via named contexts. `current-context` selects the active one.
- All resolver helpers (`CurrentCtx()`, etc.) MUST return `nil` (never panic) when no context is selected.
- Per-context `insecure: true` opts the context into TLS skip without requiring `--insecure` on every command.

## Precedence
- Precedence is flag > `STACKCTL_*` env var > config file.
- `cmd/root.go` resolves it by hand (`resolveAPIURL`, `resolveAPIKey`) with `os.Getenv`. This package does not resolve precedence.
- This package reads only `STACKCTL_CONFIG_DIR` and `XDG_CONFIG_HOME` (in `ConfigDir()`).
- The config file is YAML (`gopkg.in/yaml.v3`). Keys set with `config set`: `api-url`, `api-key`, `insecure`. The `api-key` is stored in plain text in `config.yaml`.

## Load Behaviour
- `Load()` MUST succeed (returning an empty config) when the file is missing — first-run UX requires it.
- A malformed YAML file MUST return a wrapped error so the CLI can surface it instead of panicking. `version` and `completion` commands skip loading entirely (see `cmd/root.go`).
