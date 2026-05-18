---
applyTo: "cli/pkg/output/**"
---

# Output Package Conventions

## Printer Contract
All formatting goes through `*output.Printer`. A printer is created once in `cmd/root.go` `PersistentPreRunE` and shared across the command tree. Never instantiate a printer inside a command.

## Required Modes
Every formatter MUST handle all four modes consistently:
- `table` (default) — human readable, headers + rows, color-aware
- `json` — pretty-printed (`json.MarshalIndent` with 2-space indent), full structure
- `yaml` — `gopkg.in/yaml.v3`, full structure
- `quiet` — identifiers only, one per line, no headers, no color

  Most commands print numeric IDs. Documented exceptions that print other stable identifiers:
  - `orphaned list`, `cluster namespaces` → namespace names
  - `cluster nodes` → node names
  - `cluster utilization` → namespace names from the utilization payload
  - `cluster health` → derived status label (`healthy`/`degraded`/`unknown`)
  - `cluster test-connection` → connection status string (`success`/`error`)

`--quiet` is checked first and short-circuits the format switch. Quiet output goes to `printer.Writer` (which Cobra sets to the command's stdout).

## Color
- Color is enabled by default and disabled when `--no-color` is set, when `NO_COLOR` env is set, or when stdout is not a TTY.
- Centralise terminal detection here — commands MUST NOT call `isatty` themselves.

## Errors
- Format helpers return `error` for I/O failures only. Do not swallow encoder errors.
- Never write to `os.Stdout`/`os.Stderr` directly; always use `printer.Writer` so tests can capture output via `bytes.Buffer`.

## Adding a Formatter
1. Add a `Print<Thing>(...)` method on `*Printer` that handles all four modes.
2. Cover every mode in `*_test.go` with table-driven cases and `bytes.Buffer` capture.
3. Keep table column ordering stable — downstream `grep`/`awk` users depend on it.
