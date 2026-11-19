#!/usr/bin/env bash
# Exit 0 when the demo cluster is in the start state of demo.tape.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${HERE}/lib.sh"

fail() { printf 'check: %s\n' "$*" >&2; exit 1; }

curl -sf "${DEMO_API}/health/live" >/dev/null || fail "the API does not answer on ${DEMO_API} (run scripts/demo.sh up in k8s-stack-manager)"
[ -d "${DEMO_CONFIG_DIR}" ] || fail "no config directory ${DEMO_CONFIG_DIR} (run docs/demo/setup.sh)"

export STACKCTL_CONFIG_DIR="${DEMO_CONFIG_DIR}"
for persona in admin alice devon; do
  stackctl config use-context "${persona}" >/dev/null 2>&1 || fail "no context ${persona}"
  stackctl stack list >/dev/null 2>&1 || fail "${persona} is not signed in"
done
stackctl config use-context admin >/dev/null
if stackctl stack list -q | grep -q .; then
  stackctl stack list | awk '{print $2}' | grep -qx "${DEMO_STACK}" && fail "stack ${DEMO_STACK} still exists"
fi
echo "check: ok"
