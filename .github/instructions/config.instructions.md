---
applyTo: "cli/pkg/config/**"
---

# Config Package Conventions

## Storage Layout
- Config file: `~/.stackmanager/config.yaml` (XDG-aware via `os.UserConfigDir()` fallback).
- Tokens: `~/.stackmanager/tokens/<context>.json` — one file per context. File mode MUST be `0600`; the directory MUST be `0700`. Verify mode after every write.
- Never store secrets (`api-key`, password) in process env beyond the lifetime of the command unless explicitly set by the user.

## Named Contexts
- Multiple environments are supported via named contexts. `current-context` selects the active one.
- All resolver helpers (`CurrentCtx()`, etc.) MUST return `nil` (never panic) when no context is selected.
- Per-context `insecure: true` opts the context into TLS skip without requiring `--insecure` on every command.

## Viper Bindings
- Bind every CLI flag with a config equivalent so flag > env > file precedence is preserved.
- Environment variable prefix is `STACKCTL_`. Use Viper's `SetEnvPrefix` + `AutomaticEnv` — never read `os.Getenv` directly in this package.

## Load Behaviour
- `Load()` MUST succeed (returning an empty config) when the file is missing — first-run UX requires it.
- A malformed YAML file MUST return a wrapped error so the CLI can surface it instead of panicking. `version` and `completion` commands skip loading entirely (see `cmd/root.go`).
