# Extending stackctl

`stackctl` is built so your team can add its own subcommands without forking. Drop an executable named `stackctl-<name>` on your `$PATH` and it becomes `stackctl <name>` automatically — same pattern as `git`, `kubectl`, and `gh`.

Because the mechanism is "any executable with the right name", plugins can be written in **any language** (shell, Python, Go, Node, Rust, …) and distributed however you already ship binaries to your team (package manager, tarball, Docker, private registry, rsync).

---

## The 5-minute tutorial — your first plugin

### 1. Write the plugin

```bash
#!/usr/bin/env bash
# stackctl-hello — minimal stackctl plugin
set -euo pipefail

# Report a redacted marker for the API key; NEVER echo its value.
if [ -n "${STACKCTL_API_KEY:-}" ]; then
  key_status='***configured***'
else
  key_status='<not set>'
fi

cat <<MSG
Hello from a stackctl plugin!
  API URL: ${STACKCTL_API_URL:-<not set>}
  API key: ${key_status}
  args:    $*
MSG
```

Save it anywhere on your `PATH`:

```bash
install -m 0755 stackctl-hello ~/.local/bin/
```

### 2. Use it

```bash
stackctl hello world
# → Hello from a stackctl plugin!
#     API URL: http://localhost:8081
#     API key: ***configured***
#     args:    world
```

```bash
stackctl --help | grep hello
# → hello    Plugin: hello
```

The plugin's absolute path is kept out of `--help` (it was leaking `$HOME` in screenshots). Use `stackctl help hello` to see the full resolved path.

That's the whole mechanism. Your binary gets exec'd with:

- Whatever argv the user typed after `stackctl <name>` (plus flags stackctl didn't consume at the top level)
- `stdin`, `stdout`, `stderr` wired through directly
- The full parent environment, plus the API URL and credentials of the current context (`STACKCTL_API_URL`, `STACKCTL_API_KEY` or `STACKCTL_TOKEN`)
- The plugin's exit code becomes stackctl's exit code

---

## What plugins are for

Any team-specific or company-specific workflow that doesn't belong in core stackctl. Core stackctl knows how to speak the k8s-stack-manager API — nothing beyond that. If your workflow involves **your** infrastructure, your service catalog, your CMDB, your pager, your Slack — it belongs in a plugin.

**Good plugin candidates:**

- **Thin wrappers around [k8s-stack-manager action webhooks](https://github.com/omattsson/k8s-stack-manager/blob/main/EXTENDING.md#action-webhooks).** The action endpoint (`POST /api/v1/stack-instances/:id/actions/:name`) is a k8s-stack-manager feature; stackctl itself has no action logic. A plugin like `stackctl refresh-db <id>` simply POSTs to that server endpoint and pretty-prints the response. Pair your plugin with the webhook handler you registered server-side.
- **Operations that combine multiple stackctl calls** into a single higher-level command (e.g. "create + deploy + wait for healthy + run smoke test").
- **Company-specific reporting.** `stackctl stacks-costing-money` that lists instances + pulls cost data from your FinOps API.
- **Developer conveniences.** `stackctl port-forward <id>` that resolves the stack namespace and wires up a `kubectl port-forward` for common services.
- **Integration with out-of-band systems.** Jira, Linear, Datadog, PagerDuty — anything stackctl shouldn't know about directly.

**Bad plugin candidates:**

- Things core stackctl already does (shadowing is prevented — built-in subcommands always win).
- Infrastructure changes. That's k8s-stack-manager's job.
- Logic that should live on the server side — e.g. orchestration that needs cluster credentials. Put that in an **action webhook** on the k8s-stack-manager side and have the plugin call it. See [k8s-stack-manager EXTENDING.md](https://github.com/omattsson/k8s-stack-manager/blob/main/EXTENDING.md).

---

## Naming convention

Plugin names follow a strict rule:

- Binary on PATH: `stackctl-<name>`
- Appears as: `stackctl <name>`
- `<name>` can contain letters, digits, and dashes — e.g. `stackctl-refresh-db`, `stackctl-port-forward`, `stackctl-sync-cmdb`
- **Built-in commands always win.** If you create `stackctl-config`, it'll be shadowed by the built-in `stackctl config`. Pick a name that doesn't collide.

Discovery is **first-PATH-wins** (standard PATH semantics). If `stackctl-hello` exists in two PATH entries, the earlier one is used; the later one is silently ignored.

## What plugins receive

### Environment variables (inherited + flag-derived)

The plugin inherits **stackctl's entire environment** (same pattern as `git`,
`kubectl`, `gh`). On top of that, stackctl exports the values of the current
context and of the global flags before exec, so a plugin sees the same
effective config as a built-in command. The precedence is the same as for
built-in commands: flag, then environment variable, then config file. A value
that the user exported in the parent shell is kept.

| Variable | Source | Purpose |
|---|---|---|
| `STACKCTL_API_URL` | `--api-url` flag, parent shell, or current context `api-url` | Base URL of the k8s-stack-manager API |
| `STACKCTL_API_KEY` | `--api-key` flag, parent shell, or current context `api-key` | API key; send it in the header `X-API-Key` |
| `STACKCTL_TOKEN` | parent shell, or the session token from `stackctl login` / `stackctl login --sso` | Set only when no API key is set; send it as `Authorization: Bearer <token>`. An expired token is left out. |
| `STACKCTL_CONTEXT` | parent shell, or the current context name | Name of the current context |
| `STACKCTL_INSECURE` | `--insecure` flag, parent shell, or current context `insecure: true` | `1` to skip TLS verification |
| `STACKCTL_QUIET` | `--quiet` flag | `1` when the user requested quiet output |
| `STACKCTL_OUTPUT` | `--output` flag | `table` / `json` / `yaml` / a registered custom format |
| `STACKCTL_NO_COLOR` | `--no-color` flag | `1` when the user disabled colored output |
| `NO_COLOR` | `--no-color` flag, or parent shell | `1` with `--no-color` ([no-color.org](https://no-color.org) convention for the tools that the plugin runs) |
| `STACKCTL_DEBUG` | `--debug` flag | `1` when the user requested HTTP debug output |
| `STACKCTL_CONFIG_DIR` | parent shell, or the config directory that stackctl uses | Directory of `config.yaml` and `tokens/`. A plugin that runs `stackctl` again uses the same files |
| `HOME`, `PATH`, `LANG`, `AWS_*`, `KUBECONFIG`, … | parent shell | the rest of the user's environment |

> Send `STACKCTL_API_KEY` as `X-API-Key` and `STACKCTL_TOKEN` as
> `Authorization: Bearer`. The backend rejects an API key sent as a bearer
> token (401).

> **Security note:** because the full parent environment is forwarded,
> plugins have access to credentials stackctl doesn't know about
> (AWS_ACCESS_KEY_ID, GITHUB_TOKEN, KUBECONFIG contents, …). Install
> plugins from sources you trust. This matches the `git`/`kubectl`
> security model.

`STACKCTL_QUIET`, `STACKCTL_OUTPUT`, `STACKCTL_NO_COLOR` and `STACKCTL_DEBUG`
are set only when the user gives the flag. Without the flag, a value from
the parent shell is kept.

### Global flags

The global flags of stackctl (`--output`/`-o`, `--quiet`/`-q`, `--no-color`,
`--api-url`, `--api-key`, `--insecure`, `--debug`) go **before** the plugin
name. stackctl reads them and does not pass them to the plugin as
arguments. The plugin gets their values in the environment variables above.

```bash
stackctl --no-color -o json refresh-db status my-stack
# The plugin gets the arguments: status my-stack
# The plugin gets STACKCTL_NO_COLOR=1, NO_COLOR=1 and STACKCTL_OUTPUT=json
```

Arguments **after** the plugin name belong to the plugin. stackctl passes
them unchanged, also when they look like stackctl flags
(`stackctl refresh-db status my-stack -o json` gives the plugin `-o json`).
An unknown flag before the plugin name is an error of stackctl.

### Arguments

Whatever the user typed after `stackctl <name>`. Use `stackctl help <name>` for stackctl's built-in help entry (which shows the plugin's resolved absolute path); `stackctl <name> --help` is delegated to the plugin's own help handling.

### Stdin/stdout/stderr

Wired through directly. A plugin can prompt for confirmation, pipe into another command, or print JSON — whatever makes sense.

### Exit code

Propagated back. Non-zero exit from the plugin fails the outer `stackctl` invocation, matching every other Unix tool.

---

## Recipe: invoking a custom action

The common pattern. You have an action webhook registered on the k8s-stack-manager side (see [server-side extending](https://github.com/omattsson/k8s-stack-manager/blob/main/EXTENDING.md)); you want a stackctl plugin that invokes it.

### Bash (no dependencies)

```bash
#!/usr/bin/env bash
# stackctl-snapshot-pvc — invokes the snapshot-pvc action
set -euo pipefail

INSTANCE_ID=${1:?usage: stackctl snapshot-pvc <instance-id>}

: "${STACKCTL_API_URL:?STACKCTL_API_URL not set — run: stackctl config set api-url <url>}"

# Optional: allow insecure TLS per env
CURL_OPTS=()
[ "${STACKCTL_INSECURE:-}" = "1" ] && CURL_OPTS+=(--insecure)

curl -sS "${CURL_OPTS[@]}" \
     -X POST "${STACKCTL_API_URL%/}/api/v1/stack-instances/${INSTANCE_ID}/actions/snapshot-pvc" \
     -H "Content-Type: application/json" \
     -H "X-API-Key: ${STACKCTL_API_KEY:-}" \
     -d '{}'
echo
```

### Python (stdlib only)

```python
#!/usr/bin/env python3
# stackctl-snapshot-pvc — invokes the snapshot-pvc action
import json, os, ssl, sys
from urllib import request
from urllib.error import HTTPError, URLError

if len(sys.argv) < 2:
    sys.exit("usage: stackctl snapshot-pvc <instance-id>")
instance_id = sys.argv[1]

api = os.environ.get("STACKCTL_API_URL", "").rstrip("/")
if not api:
    sys.exit("STACKCTL_API_URL not set")

if os.environ.get("STACKCTL_INSECURE") == "1":
    print("WARNING: TLS verification disabled (STACKCTL_INSECURE=1)", file=sys.stderr)
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
else:
    ctx = None

req = request.Request(
    f"{api}/api/v1/stack-instances/{instance_id}/actions/snapshot-pvc",
    data=b"{}",
    method="POST",
    headers={
        "Content-Type": "application/json",
        "X-API-Key": os.environ.get("STACKCTL_API_KEY", ""),
    },
)

try:
    with request.urlopen(req, timeout=30, context=ctx) as resp:
        body = json.loads(resp.read())
except HTTPError as e:                            # 4xx/5xx — read body for details
    body = {"error": e.reason, "status": e.code, "body": e.read().decode(errors="replace")}
    print(json.dumps(body, indent=2)); sys.exit(1)
except URLError as e:
    sys.exit(f"request failed: {e.reason}")

# Echo server response. If the user set STACKCTL_OUTPUT=json we keep the
# raw shape; for other values we could reshape into a friendlier table.
print(json.dumps(body, indent=2))
```

### Go (compiled, single binary)

```go
// stackctl-snapshot-pvc/main.go — compile: go build -o stackctl-snapshot-pvc
package main

import (
    "bytes"
    "crypto/tls"
    "fmt"
    "io"
    "net/http"
    "os"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: stackctl snapshot-pvc <instance-id>")
        os.Exit(2)
    }
    api := os.Getenv("STACKCTL_API_URL")
    if api == "" {
        fmt.Fprintln(os.Stderr, "STACKCTL_API_URL not set")
        os.Exit(1)
    }
    url := fmt.Sprintf("%s/api/v1/stack-instances/%s/actions/snapshot-pvc", api, os.Args[1])
    req, _ := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-API-Key", os.Getenv("STACKCTL_API_KEY"))
    c := &http.Client{}
    if os.Getenv("STACKCTL_INSECURE") == "1" {
        c.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
    }
    resp, err := c.Do(req)
    if err != nil {
        fmt.Fprintln(os.Stderr, "request:", err)
        os.Exit(1)
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    fmt.Print(string(body))
    if resp.StatusCode >= 400 {
        os.Exit(1)
    }
}
```

---

## Recipe: combining multiple stackctl calls

For workflow plugins that orchestrate the built-in commands, just exec `stackctl` back into itself:

```bash
#!/usr/bin/env bash
# stackctl-deploy-and-wait — create + deploy + wait until Running
set -euo pipefail

DEF_ID=${1:?usage: stackctl deploy-and-wait <definition-id> <name>}
NAME=${2:?usage: stackctl deploy-and-wait <definition-id> <name>}

# Create
INST_ID=$(stackctl stack create --definition "$DEF_ID" --name "$NAME" --quiet)
echo "Created $INST_ID"

# Deploy
stackctl stack deploy "$INST_ID" --quiet

# Poll until Running (or Error)
while :; do
  STATUS=$(stackctl stack get "$INST_ID" -o json | jq -r .status)
  case "$STATUS" in
    running) echo "$INST_ID is running."; break ;;
    error)   echo "deploy failed: $(stackctl stack get "$INST_ID" -o json | jq -r .error_message)"; exit 1 ;;
    *)       echo "status=$STATUS, waiting..."; sleep 5 ;;
  esac
done
```

`stackctl stack get -o json` makes this composable — the built-in commands are designed for scripting.

---

## Best practices

### Respect `--quiet`

If the user sets `STACKCTL_QUIET=1` (or runs your plugin with `--quiet`), minimise output — print just the IDs or the raw result. Match core stackctl's behaviour.

### Honour `-o json`

When it makes sense, accept `-o json` and emit structured output. Downstream scripts can then `jq` into your plugin's result the same way they do with built-ins.

### Don't re-implement auth

Read `STACKCTL_API_URL` and the credentials from the environment. Send
`STACKCTL_API_KEY` as `X-API-Key` when it is set; otherwise send
`STACKCTL_TOKEN` as `Authorization: Bearer`. stackctl resolves both from the
current context, so the plugin works after `stackctl config set api-key ...`
or `stackctl login` without any export.

Don't parse `~/.stackmanager/config.yaml` or the token files directly — the
schema is internal and may change.

### Use a deny list for dangerous defaults

If your plugin mutates state (deletes, force-redeploys, wipes data), make it interactive or require `--yes`. The core stackctl conventions are:

- `-y` / `--yes` skips the "are you sure?" prompt
- No-flag means prompt; prompt reads from stdin
- A full "destructive" operation also prints a summary of what will happen before the prompt

### Emit JSON responses verbatim on `-o json`

When the server returns a response body, forward it unmodified rather than reshaping it. Lets users write stable jq expressions that survive plugin version bumps.

### Install to a consistent location

Document where your plugin should live. Common choices:

- `~/.local/bin/` (user-scoped; already on most PATHs via `~/.profile`)
- `/usr/local/bin/` (system-wide; requires sudo)
- A project-managed `bin/` dir added to PATH in your team's shell profile

### Version your plugin independently

Plugins and stackctl evolve separately. Tag releases, publish changelogs, keep a `--version` flag so users can report what they're running when they file bugs.

---

## Distribution

Because plugins are just executables, you ship them however your team already ships CLIs.

### Simple: a tarball or Docker image

```bash
# Install script
curl -sSfL https://your-host/install-plugin.sh | bash
```

The install script drops the binary in `~/.local/bin/` and that's it.

### Homebrew / apt / yum

Normal package manager flow. No plugin-specific infrastructure needed.

### Kubernetes / container-based distribution

If your org already distributes internal tools as container images, bake the plugin into an image the user pulls and copies out:

```bash
docker cp $(docker create your-company/stackctl-plugins:latest):/bin/stackctl-refresh-db ~/.local/bin/
```

### Team-internal: checked into a dotfiles repo

For small teams, plugin source lives in a shared dotfiles repo and ships via each developer's bootstrap script.

---

## How it works internally

On `Execute()`, stackctl scans every directory in `$PATH`. For each regular executable file whose name starts with `stackctl-`:

1. Strip the `stackctl-` prefix to get the plugin name.
2. Skip if a built-in subcommand with the same name is already registered.
3. Register a new Cobra subcommand:
    - `Use: <name>`
    - `Short: "Plugin: <name>"` (path kept out of the summary listing)
    - `Long`: includes the plugin's absolute path for `stackctl help <name>`
    - `DisableFlagParsing: true` — plugin handles its own flags
    - `RunE`: exec the binary with the remaining args, piping I/O, propagating exit code

If the user runs `stackctl <name> …`, Cobra routes to the registered subcommand's `RunE`, which exec's the plugin.

Source: [cli/cmd/plugins.go](cli/cmd/plugins.go) (≈110 lines). No plugin framework, no SDK — just `os/exec` + `$PATH`.

---

## Troubleshooting

### "unknown command" but my plugin exists

- Check the binary is **executable** (`chmod +x`). Non-executable files in PATH are ignored.
- Check the directory is actually in `$PATH`. `which stackctl-<name>` should find it.
- Check the name starts with `stackctl-` (not `stackctl_<name>` or `stackctl <name>`).
- Shadow check: is there a built-in `stackctl <name>`? `stackctl --help | grep '<name>'` — if the Short text doesn't say "Plugin:", a built-in is winning.

### Plugin runs but `$STACKCTL_API_URL` is empty

stackctl exports the `api-url` of the current context. If the variable is
empty, the current context has no `api-url`:

```bash
stackctl config current-context
stackctl config set api-url https://stacks.example.com
```

### Plugin gets 401

- Check that the plugin sends `STACKCTL_API_KEY` as `X-API-Key`, not as a bearer token.
- Without an API key, check that the session is valid: run `stackctl login` (or `stackctl login --sso`). stackctl leaves out an expired token.

### Plugin exits non-zero but stackctl exits 0

The plugin ran but didn't fail. Check the plugin's own error handling. `bash -x` or `set -x` inside a shell plugin is a quick way to see what happened.

### Plugin shadows a built-in

Built-ins always win. Rename the plugin to something that doesn't collide. (This is a safety feature — otherwise a malicious `stackctl-config` on PATH could intercept credentials.)

---

## Related

- [k8s-stack-manager EXTENDING.md](https://github.com/omattsson/k8s-stack-manager/blob/main/EXTENDING.md) — the server side: how to register a webhook handler that a stackctl plugin can invoke. Most plugins pair with an action webhook; this is the complete picture.
- [cli/cmd/plugins.go](cli/cmd/plugins.go) — plugin-discovery implementation.
- [cli/cmd/plugins_test.go](cli/cmd/plugins_test.go) — test patterns for plugin behaviour.
