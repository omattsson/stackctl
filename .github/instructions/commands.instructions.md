---
applyTo: "cli/cmd/**"
---

# Cobra Command Conventions

## Required Patterns
- Use `RunE` (not `Run`) to return errors properly
- Set `SilenceUsage: true` on all commands that take arguments
- Use `cobra.ExactArgs(N)` for fixed argument counts
- Register subcommands in `init()` using `parentCmd.AddCommand(childCmd)`

## Output Handling
All commands must support multiple output modes via the global `printer`:

```go
if printer.Quiet {
    fmt.Fprintln(printer.Writer, resource.ID)
    return nil
}

switch printer.Format {
case output.FormatJSON:
    return printer.PrintJSON(data)
case output.FormatYAML:
    return printer.PrintYAML(data)
default:
    // table output with headers and rows
    return printer.PrintTable(headers, rows)
}
```

- `--quiet` must output only numeric IDs, one per line
- Commands without numeric IDs (like `git branches`) should skip quiet mode handling

## Destructive Operations
Commands that delete or clean resources must:
1. Accept `--yes`/`-y` flag to skip confirmation
2. Prompt on stderr: `fmt.Fprintf(cmd.ErrOrStderr(), "prompt: ")`
3. Read from `cmd.InOrStdin()` using `bufio.NewReader`

## ID Parsing
Use the `parseID()` helper from root.go for all ID arguments. Do not parse IDs inline.

## API Calls
All API calls go through `pkg/client` via `newClient()`. Never use `http.Get` directly.

## Flag Conventions
- `--output`, `-o` — output format (table|json|yaml)
- `--quiet`, `-q` — IDs only, one per line
- `--yes`, `-y` — skip confirmation
- `--mine` — filter to current user
- `--ids` — comma-separated IDs for bulk operations
