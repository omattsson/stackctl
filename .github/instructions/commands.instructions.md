---
applyTo: "cli/cmd/**"
---

# Cobra Command Conventions

## Required Patterns
- Use `RunE` (not `Run`) to return errors properly
- Set `SilenceUsage: true` on all commands that take arguments
- Use `cobra.ExactArgs(N)` ONLY when positional args are required (e.g. `Args: cobra.ExactArgs(1)` for `delete <id>`). Commands that take no positional args (list, create-with-flags, analytics subcommands, etc.) deliberately OMIT the `Args` field — see `clusterListCmd`, `templateListCmd`, `userListCmd`. Do not add `Args: cobra.NoArgs` / `cobra.ExactArgs(0)` to these commands.
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
- **Documented exceptions** (print stable identifiers other than numeric IDs):
  - `orphaned list` → prints namespace names
  - `cluster nodes <id>` → prints node names via `printer.PrintIDs`
  - `cluster namespaces <id>` → prints namespace names via `printer.PrintIDs`
  - `cluster utilization <id>` → prints namespace names via `printer.PrintIDs`
  - `cluster health <id>` → prints the derived status label (`healthy`/`degraded`/`unknown`) via `fmt.Fprintln(printer.Writer, deriveHealthStatus(health))`
  - `cluster test-connection <id>` → prints `result.Status` (only on 2xx responses; non-2xx surfaces as an `APIError` command error, nothing is printed to stdout) via `fmt.Fprintln(printer.Writer, result.Status)`
  - `notification prefs get` / `notification prefs set` → prints `event_type` for each preference. Preferences are keyed by `(user_id, event_type)` server-side, so `EventType` is the stable functional identifier; the database UUID `ID` field is opaque and unstable across resets, so scripting like `prefs get -q \| grep failed` requires the event type.

## Destructive Operations
Commands that delete or clean resources must:
1. Accept `--yes`/`-y` flag to skip confirmation
2. Prompt on stderr: `fmt.Fprintf(cmd.ErrOrStderr(), "prompt: ")`
3. Read from `cmd.InOrStdin()` using `bufio.NewReader`

## ID & Name Parsing
- `parseID()` (in `stack.go`) trims whitespace and validates non-empty; returns `string` (no numeric conversion). Use for chart IDs, cluster IDs, version IDs, and other sub-resource identifiers.
- `resolveStackID()` (in `resolve.go`) accepts a stack name OR a numeric ID OR a UUID and resolves to the canonical ID via the API. Use for ALL stack-instance-scoped commands so users can pass `my-stack` instead of `42`.
- Never parse IDs inline.

## API Calls
All API and WebSocket calls go through `pkg/client` via `newClient()` (or `newUnauthenticatedClient()` for login). Never use `http.Get` or `websocket.Dial` directly.

## OIDC / Browser Flow
For commands that open a browser (OIDC loopback login), call `openBrowser()` from `browser.go`. In tests, override the package-level `browserOpener` variable to capture the URL without spawning a process.

## Plugins
External `stackctl-<name>` executables on `$PATH` are auto-registered as subcommands by `registerPlugins()` in `Execute()`. Do not add command files that shadow common plugin names; do not call `registerPlugins()` from other code paths.

## Flag Conventions
- `--output`, `-o` — output format (table|json|yaml)
- `--quiet`, `-q` — IDs only, one per line
- `--yes`, `-y` — skip confirmation on destructive operations
- `--mine` — filter to current user
- `--ids` — comma-separated IDs for bulk operations
- `--debug` — log HTTP traffic to stderr (global; also via `STACKCTL_DEBUG=1`)
- `--insecure` — skip TLS verification (global; prints stderr warning)
