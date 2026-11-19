#!/usr/bin/env bash
# Prepare the k8s-stack-manager demo cluster (scripts/demo.sh up) for the
# recording of docs/demo/demo.tape:
#
#   - the users alice (user) and devon (devops), with random passwords kept
#     in ${DEMO_CONFIG_DIR}/passwords.env (mode 0600, never printed)
#   - the stackctl contexts admin, alice and devon, all signed in, in a
#     config directory of their own (your stackctl contexts stay as they are)
#   - no leftover stack ${DEMO_STACK}; the namespace of alice's stack exists and
#     its default ServiceAccount has no token mount (the server refuses exec
#     into a pod that holds one)
#
# Safe to run again.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${HERE}/lib.sh"

CONTEXT="kind-${KSM_DEMO_CLUSTER:-ksm-demo}"
NAMESPACE="stack-${DEMO_STACK}-alice"

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
kc() { kubectl --context "${CONTEXT}" "$@"; }

for cmd in stackctl kubectl curl openssl awk sed; do
  command -v "${cmd}" &>/dev/null || die "${cmd} is not installed."
done
curl -sf "${DEMO_API}/health/live" >/dev/null ||
  die "the API does not answer on ${DEMO_API}; run scripts/demo.sh up in k8s-stack-manager first."
[ -f "${DEMO_DIR}/secrets.yaml" ] || die "${DEMO_DIR}/secrets.yaml is missing; run scripts/demo.sh up first."

mkdir -p "${DEMO_CONFIG_DIR}"
chmod 700 "${DEMO_CONFIG_DIR}"
export STACKCTL_CONFIG_DIR="${DEMO_CONFIG_DIR}"

# Passwords: the admin one from the demo, the others random, created once.
PASSWORDS="${DEMO_CONFIG_DIR}/passwords.env"
NEW_PASSWORDS=0
if [ ! -f "${PASSWORDS}" ]; then
  NEW_PASSWORDS=1
  (
    umask 077
    cat > "${PASSWORDS}" <<EOP
ADMIN_PASSWORD="$(awk -F'"' '/ADMIN_PASSWORD/{print $2}' "${DEMO_DIR}/secrets.yaml")"
ALICE_PASSWORD="$(openssl rand -hex 12)"
DEVON_PASSWORD="$(openssl rand -hex 12)"
EOP
  )
fi
# shellcheck disable=SC1090
source "${PASSWORDS}"

login() { # login <persona> <password>
  stackctl config use-context "$1" >/dev/null
  stackctl config set api-url "${DEMO_API}" >/dev/null
  STACKCTL_PASSWORD="$2" stackctl login --username "$1" >/dev/null
}

login admin "${ADMIN_PASSWORD}"
ADMIN_TOKEN="$(sed -n 's/.*"token": *"\([^"]*\)".*/\1/p' "${DEMO_CONFIG_DIR}/tokens/admin.json")"

# A leftover stack of an earlier run goes first: it belongs to alice.
if stackctl stack list | awk 'NR>1{print $2}' | grep -qx "${DEMO_STACK}"; then
  stackctl stack delete "${DEMO_STACK}" --yes >/dev/null
fi
kc delete namespace "${NAMESPACE}" --ignore-not-found --wait >/dev/null

user_id() { stackctl user list | awk -v u="$1" 'NR>1 && $2==u {print $1}'; }

# ensure_user <name> <display name> <role> <password>: an existing user gets
# a fresh password by being recreated (its password is not known to this run).
ensure_user() {
  local id
  id="$(user_id "$1")"
  if [ -n "${id}" ] && [ "${NEW_PASSWORDS}" = 1 ]; then
    stackctl user delete "${id}" --yes >/dev/null
    id=""
  fi
  if [ -z "${id}" ]; then
    printf '{"username":"%s","password":"%s","display_name":"%s","role":"%s"}' "$1" "$4" "$2" "$3" |
      curl -sf -o /dev/null -X POST "${DEMO_API}/api/v1/auth/register" \
        -H @<(printf 'Authorization: Bearer %s\n' "${ADMIN_TOKEN}") \
        -H 'Content-Type: application/json' --data-binary @- ||
      die "could not create the user $1"
  fi
}

ensure_user alice Alice user "${ALICE_PASSWORD}"
ensure_user devon Devon devops "${DEVON_PASSWORD}"
login alice "${ALICE_PASSWORD}"
login devon "${DEVON_PASSWORD}"
stackctl config use-context admin >/dev/null

kc create namespace "${NAMESPACE}" >/dev/null
for _ in $(seq 1 30); do
  kc -n "${NAMESPACE}" get serviceaccount default &>/dev/null && break
  sleep 1
done
kc -n "${NAMESPACE}" patch serviceaccount default -p '{"automountServiceAccountToken": false}' >/dev/null

printf 'Ready. Config directory of the video: %s\n' "${DEMO_CONFIG_DIR}"
