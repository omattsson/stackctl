#!/usr/bin/env bash
# PostToolUse hook (Edit|Write): run gofmt on the Go file that was just edited.
# Never blocks: a failed format is reported on stderr and the edit stands.
set -u

file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac
[ -f "$file" ] || exit 0

if ! out=$(gofmt -w "$file" 2>&1); then
  echo "gofmt failed on $file: $out" >&2
fi
exit 0
