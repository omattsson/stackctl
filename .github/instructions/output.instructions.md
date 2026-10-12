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

  Most commands print IDs. For the commands that print other stable identifiers, see the list of documented exceptions in `commands.instructions.md`.

`--quiet` is checked first and short-circuits the format switch. Quiet output goes to `printer.Writer` (which Cobra sets to the command's stdout).

## Color
- Color is enabled by default. Only the `--no-color` flag disables it (`Printer.NoColor`, used by `StatusColor`).
- This package does not check the `NO_COLOR` env var. It does not detect a TTY.
- Note: TTY checks (`term.IsTerminal`) exist only in `cmd/login.go`, for prompts.

## Printer Methods
- `Print(data, headers, rows, ids)` — list output. Quiet mode prints `ids`. Table mode uses `headers` and `rows`.
- `PrintSingle(data, fields)` — one item. Table mode prints `Key:` and value pairs from `[]KeyValue`.
- `PrintIDs(ids)` — one ID per line, for quiet mode.
- `PrintJSON`, `PrintYAML`, `PrintTable`, `PrintMessage` — lower-level helpers.
- `StatusColor(status)` — colors a status string unless `NoColor` is set.

## Errors
- Format helpers return `error` for I/O failures only. Do not swallow encoder errors.
- Never write to `os.Stdout`/`os.Stderr` directly; always use `printer.Writer` so tests can capture output via `bytes.Buffer`.

## Adding a Formatter
1. Add a `Print<Thing>(...)` method on `*Printer` that handles all four modes.
2. Cover every mode in `*_test.go` with table-driven cases and `bytes.Buffer` capture.
3. Keep table column ordering stable — downstream `grep`/`awk` users depend on it.
