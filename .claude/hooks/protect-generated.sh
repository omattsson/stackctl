#!/usr/bin/env bash
# PreToolUse hook (Edit|Write): refuse hand edits to generated or vendored files.
# Exit 2 blocks the call and sends stderr to Claude.
set -u

file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -n "$file" ] || exit 0

case "$file" in
  */cli/test/contract/testdata/swagger.json)
    echo "swagger.json is vendored: refresh it with cli/test/contract/refresh-swagger.sh (or the refresh-contract skill), do not edit it by hand." >&2
    exit 2 ;;
  */cli/go.sum)
    echo "go.sum is generated: change dependencies with 'go get' / 'go mod tidy' (go get asks first), do not edit it by hand." >&2
    exit 2 ;;
  */cli/dist/* | */cli/bin/*)
    echo "$file is a build output: rebuild with 'cd cli && make build', do not edit it by hand." >&2
    exit 2 ;;
esac
exit 0
