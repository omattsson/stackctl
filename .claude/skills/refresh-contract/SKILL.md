---
name: refresh-contract
description: Refresh the vendored k8s-stack-manager swagger.json, run the contract tests, and bring pkg/types and docs/claude/api-reference.md in line with what changed. Use when the backend added, renamed or removed a route or field.
argument-hint: "[git ref of k8s-stack-manager, default main]"
disable-model-invocation: true
---

# Refresh the API contract

The contract tests in `cli/test/contract/` compare stackctl's request types with the backend's OpenAPI schema vendored at `cli/test/contract/testdata/swagger.json`. Only the script may change that file: a hook blocks hand edits.

## Steps

1. Work on a branch, not `main`: `git switch -c chore/refresh-swagger origin/main` after `git fetch origin main`.
2. Keep the old copy for the diff: `cp cli/test/contract/testdata/swagger.json "$TMPDIR/swagger.old.json"`.
3. Refresh from `$ARGUMENTS` (default `main`):
   ```
   cli/test/contract/refresh-swagger.sh $ARGUMENTS
   ```
   It needs network access (`curl`). If it says "unchanged", stop and report that nothing changed.
4. Summarize what changed. Compare paths and definitions of the old and new file (`jq` on `.paths | keys` and `.definitions`). List: new routes, removed routes, and added, removed or renamed fields per definition. Do not paste the raw diff.
5. Run the contract tests:
   ```
   cd cli && go test ./test/contract/...
   ```
   Do not fix a failure by widening an exclusion list in the tests without saying so. Prefer to fix `cli/pkg/types` (JSON tags, new fields). If the fix needs a new flag or command, say so and leave it for the user.
6. Update `docs/claude/api-reference.md` for each route that stackctl calls and that changed (behavior, minimum server version). Routes stackctl does not call need no entry.
7. Run `cd cli && gofmt -l . && make lint && go test ./...` and report one line per command.

## Report

- the ref and the old and new checksums (the script prints them);
- routes and fields that changed, split into "stackctl uses it" and "stackctl does not use it";
- what you changed in `pkg/types` and the docs, and what you left for the user.

Do not commit or push unless asked.
