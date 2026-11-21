---
name: new-command
description: Scaffold a stackctl Cobra command or subcommand with the client method, types, tests for table/json/yaml/quiet, and the doc updates that CLAUDE.md requires. Use for adding one command to an existing or new command group.
argument-hint: "<group> <subcommand> - what it does and which API route it calls"
disable-model-invocation: true
---

# New command

Add one command to stackctl and finish every step of "Adding a New Command" in `CLAUDE.md`. For a multi-command feature or a GitHub issue, use `ship-issue` instead.

## Before you write code

1. Read `docs/claude/api-reference.md` for the route, its minimum server version and its error behavior. If the route is not there, stop and ask: the server may not have it yet.
2. Pick the closest exemplar and read it with its test file:
   - a list or get command with a `-q` ID output: `cli/cmd/orphaned.go`, `cli/cmd/analytics.go` (tests: `analytics_test.go`);
   - a destructive command: one that calls `confirmAction` (grep for it).
3. Check `docs/claude/structure.md` for where the file belongs. One file per command group in `cli/cmd/`.

## Build

1. **Types** in `cli/pkg/types/`. Every embedded struct carries `yaml:",inline"`. If the type is a request body, the contract tests in `cli/test/contract/` compare it with `testdata/swagger.json`.
2. **Client method** in `cli/pkg/client/`, with a `pkg/client` test against `httptest.NewServer` (`t.Parallel()` on the test and its subtests).
3. **Command** in `cli/cmd/<group>.go`:
   - `Use`, `Short`, `Long` with an `Examples:` block, `RunE` (never `Run`);
   - a client from `newClient()`, never a hand-built one;
   - stack name or ID through `resolveStackID()`;
   - output through `printer`: quiet prints IDs one per line (`printAllIDs` / `listAllIDs` for lists), JSON and YAML print the full response, table is the default. `printer.PrintMessage` for text for people (it goes to stderr in machine modes);
   - destructive: `confirmAction(cmd, msg)` and a `--yes` flag;
   - prompts write to `cmd.ErrOrStderr()`;
   - register it in the parent's `init()`.
4. **Tests** in `cli/cmd/<group>_test.go`, using `setupStackTestCmd(t, server.URL)`:
   - table, JSON (golden file in `cmd/testdata` when the output is large), YAML, quiet;
   - flag parsing and validation;
   - an error path (403, 404, 409 as they apply);
   - no `t.Parallel()` in `cmd/` tests that touch package globals.
5. **Docs**:
   - `cli/cmd/agent_guide.md` when scripts or agents need to know about output, prompts, exit codes or waiting;
   - `docs/claude/api-reference.md` for a new route or server behavior;
   - `docs/claude/structure.md` for a new file;
   - `docs/` user docs when the command is user-facing.

## Verify

Run from `cli/` and report one line each:

```
gofmt -l . ; make lint ; go test ./...
```

Then build and run the command with `-o json`, `-o yaml` and `-q` against the test server or a live backend, and check that stdout holds only data.

Do not commit or push unless asked.
