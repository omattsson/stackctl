# stackctl guide for agents and scripts

This guide matches stackctl {{VERSION}}. stackctl is the CLI of k8s-stack-manager: it creates, deploys and manages Helm-based stacks on Kubernetes clusters through the API of the server. It never talks to Kubernetes directly.

Run `stackctl <command> --help` for the flags of a command. This guide covers what the help does not: how to use stackctl without a terminal and without a person.

## Rules

1. **Read with `-o json`.** stdout then holds only JSON: the resource, the list or the server response. Messages, warnings, notes and the deployment log of `--follow` go to stderr. A command without data (for example `stack delete` or `--dry-run`) leaves stdout empty. The default table output is for people and can change.
2. **`-q` prints IDs, one per line**, for pipes and `$(...)`. On `stack create`, `clone` and `template quick-deploy` it is the stack ID; on `stack deploy`, `stop`, `clean` and `rollback` it is the ID of the deployment log.
3. **Exit code 0 means no error, 1 means an error.** The error text is on stderr. Do not parse table output to detect errors. Exceptions: for operations that run on the server, exit 0 means that the server accepted the operation, not that it ended (see "Long-running operations"); `bulk` commands exit 0 even when some items fail (check `failed` and `results` in `-o json`); a plugin (`stackctl <plugin>`) returns its own exit code.
4. **Destructive commands ask for confirmation.** A command with a `--yes` flag (see its `--help`) reads the answer from stdin. Any answer other than `y` exits 1 with `aborted: not confirmed, nothing changed`; a closed stdin fails with `use --yes to confirm`; an open stdin without input waits forever. Pass `--yes` only when the user asked for that change, and run commands with `< /dev/null` so a missing `--yes` fails instead of hanging.
5. **`--dry-run` does not check anything.** It prints the action and the name you gave and contacts no server: a misspelled name still "succeeds". To check the target, run `stackctl stack get <name> -o json` first.
6. **Do not retry an operation that is still running.** See "Long-running operations".
7. **Never print or log secrets.** Do not echo `STACKCTL_API_KEY`, `STACKCTL_PASSWORD`, `STACKCTL_TOKEN` or the token files in the config directory. Prefer environment variables over `--api-key` and `--password`, which other processes can see.

## Authentication

Check the state before you start:

```sh
stackctl config current-context   # the active context (environment)
stackctl whoami                   # the authenticated user; exit 1 when not logged in
stackctl server info              # server reachable? TTL limits; needs no credentials
```

Ways to authenticate without a prompt, in order of preference:

- **API key:** set `STACKCTL_API_KEY` and `STACKCTL_API_URL`. No login and no token file. An API key wins over a stored login session. Servers before k8s-stack-manager v0.8.0 refuse an API key for `--follow` and `stack watch` (`This server does not accept API keys for live updates`): then poll `stack get` instead (see "Long-running operations").
- **Username and password:** `STACKCTL_USERNAME=... STACKCTL_PASSWORD=... stackctl login`. stackctl stores the session in the current context and renews it by itself. It needs a context: in a new config directory run `stackctl config use-context <name>` and `stackctl config set api-url <url>` first.
- `stackctl login --sso` opens a browser: do not use it unless a person is at the computer.

Values resolve in this order: flag (`--api-url`, `--api-key`), environment variable, config file. `STACKCTL_CONFIG_DIR` selects another config directory, for example an empty one for an isolated run. `stackctl config use-context <name>` switches the environment for every later command: ask the user before you change it.

`Not authenticated. Run 'stackctl login' first.` means the credentials are missing or the session ended. Ask the user for credentials; do not guess them.

## Concepts

- **Template:** a published blueprint of charts. `template quick-deploy` creates and deploys a stack from it in one step.
- **Definition:** the set of Helm charts and default values that a stack uses.
- **Stack (instance):** one deployment of a definition into its own namespace on a cluster, with an optional branch and an expiry (TTL).
- **Status:** `draft` (not deployed: new, or after a clean), `deploying`, `running`, `stopping`, `stopped`, `cleaning`, `error`, `failed`, `partial` (some charts failed).
- **stop** stops the stack and keeps it (a deploy starts it again); **clean** undeploys it and removes its namespace; **delete** removes the stack.
- **Overrides:** per-stack Helm values (`override`), per-chart branches (`override branch`) and quotas (`override quota`).

## Common tasks

Commands that take `<name|id>` accept the stack name or the ID.

```sh
# Find stacks
stackctl stack list --mine -o json        # paged: {"data": [...], "total", "page", "pageSize"}
stackctl stack list --mine -q             # IDs of all pages
stackctl stack get my-app -o json         # one stack: id, name, status, expires_at, error_message, ...
stackctl dashboard -o json                # failing and expiring stacks, recent deployments

# Create and deploy
ID=$(stackctl stack create --definition my-def --name my-app --branch feature/x -q)
stackctl stack deploy "$ID" --follow -o json  # log on stderr, the stack after the deploy on stdout
stackctl template quick-deploy <template-id> --name my-app --ttl 480 -o json

# Inspect
stackctl stack status my-app              # pods
stackctl stack pods my-app                # live pods and warning events
stackctl stack logs my-app                # latest deployment log
stackctl stack values my-app              # merged Helm values
stackctl stack preview my-app             # values that the next deploy uses
stackctl stack history my-app

# Reach a service of the stack: runs until interrupted, so start it in the background
stackctl stack port-forward my-app mysql 0:3306 -q   # stdout: 127.0.0.1:<port>; exit 1 on 403/404/409
# SIGTERM or Ctrl-C ends it with exit 0. At most 16 open connections; more are closed at once.
# After a local EOF the tunnel passes server data until the server closes or 2 s pass without data.
# 3 refusals with 429 in a row end it with exit 1: wait and try again.
# Needs k8s-stack-manager v0.15.0 or later (an older server: "upgrade the server to k8s-stack-manager v0.15.0 or later").
# An http:// API URL to a host that is not loopback is refused (add --allow-insecure-stream to accept the risk).

# Change values, then deploy again
stackctl override set my-app my-chart --set image.tag=1.2.3
stackctl override branch set my-app my-chart feature/x
stackctl stack deploy my-app --follow

# Expiry
stackctl stack extend my-app --minutes 120
stackctl stack update my-app --stop-at "<YYYY-MM-DD HH:MM>"  # a future local time; needs k8s-stack-manager v0.12.0+

# Stop, clean up, delete (only when the user asked)
stackctl stack stop my-app --follow
stackctl stack clean my-app --yes --follow
stackctl stack delete my-app --yes
stackctl stack list --status stopped --mine -q | xargs -r -n 50 stackctl bulk delete --yes
```

Paged lists (`stack list`, `definition list`, `template list`) return one page as `{"data": [...], "total", "page", "pageSize"}` in JSON. When there are more items, stderr says `Showing X of TOTAL`; use `--page` and `--page-size` (at most 100), or `-q`, which prints the IDs of all pages.

## Long-running operations

`stack deploy`, `stop`, `clean`, `rollback`, `template quick-deploy` and the `bulk` commands return when the server has accepted the operation; the work continues on the server (a `bulk` result `success` means that the operation started). To wait for the result:

- add `--follow` (`-f`, only `stack deploy`, `stop`, `clean` and `rollback`): stackctl streams the log and exits when the stack reaches a final status. With `-o json` the log goes to stderr and stdout gets the stack after the operation (also after a failed one);
- or run `stackctl stack watch --id <id>[,<id>...]`, which exits when every listed stack reaches a final status;
- or poll `stackctl stack get <id> -o json` every few seconds until `status` is final.

Final statuses: `running`, `stopped` and `draft` exit 0; `error`, `failed` and `partial` exit 1, with the server error on stderr.

**Check that the final status is the expected one.** `--follow` exits 0 for any final status that is not a failure, and also when it is interrupted (SIGINT), which does not stop the operation. Expect `running` after a deploy or rollback, `stopped` after a stop and `draft` after a clean.

When `--follow` or `watch --id` stops early for another reason (lost connection, session ended), it exits 1 and says that the operation continues on the server. **Do not start the operation again.** Run `stackctl stack watch --id <id>`, or poll `stackctl stack get <id> -o json`.

## Errors

- `Permission denied`: the user's role does not allow this. Tell the user; do not try other commands to get around it.
- `Resource not found` / `not found`: check the name or ID with a list command.
- `Conflict: ...`: the state does not allow the change (for example a name in use; the message lists free names).
- `Rate limited`: wait and try once more.
- `Server error`: report it; the server logs have the cause.
- `upgrade k8s-stack-manager to vX or later` or `upgrade the server to k8s-stack-manager vX or later`: the server is too old for this feature. Tell the user.
- `Warning: ...` on stderr: a notice from the server (for example a deprecation). The command still worked.

## More

- `stackctl --help` lists all command groups, including plugins (`stackctl-<name>` programs on `PATH`).
- `--debug` logs each HTTP request and the response status to stderr; credentials are masked.
- `stackctl version -o json` prints the version of stackctl.
