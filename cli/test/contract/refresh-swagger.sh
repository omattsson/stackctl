#!/usr/bin/env bash
#
# refresh-swagger.sh — pull the latest swagger.json from
# omattsson/k8s-stack-manager and refresh the vendored copy used by
# the contract tests.
#
# The contract tests compare stackctl's request types against the
# backend's published OpenAPI schema. When the backend lands a new
# field (or renames one), run this script, re-run the tests, and
# adjust stackctl's pkg/types or the contract test's exclusion list
# until everything aligns.
#
# Usage:
#   ./refresh-swagger.sh                       # fetch from main
#   ./refresh-swagger.sh <git-ref>             # fetch from a specific tag/branch/sha
#
# Requires: curl, shasum.
set -euo pipefail

REF="${1:-main}"
URL="https://raw.githubusercontent.com/omattsson/k8s-stack-manager/${REF}/backend/docs/swagger.json"

cd "$(dirname "$0")/testdata"

echo "Fetching swagger.json from ${REF}…" >&2
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$URL" -o "$tmp"

# Refuse to overwrite if the fetched payload isn't valid JSON — protects
# against fetching a 404 HTML page that happens to be 200 from a CDN.
if ! python3 -c "import json,sys; json.load(open('$tmp'))" 2>/dev/null; then
    echo "ERROR: fetched file is not valid JSON, refusing to overwrite" >&2
    exit 1
fi

old_sum=$(shasum -a 256 swagger.json | cut -d' ' -f1)
new_sum=$(shasum -a 256 "$tmp" | cut -d' ' -f1)

if [ "$old_sum" = "$new_sum" ]; then
    echo "swagger.json unchanged (sha256 ${old_sum:0:12}…)" >&2
    exit 0
fi

mv "$tmp" swagger.json
trap - EXIT
echo "Updated swagger.json (sha256 ${new_sum:0:12}…, was ${old_sum:0:12}…)" >&2
echo "Now run: go test ./cli/test/contract/..." >&2
