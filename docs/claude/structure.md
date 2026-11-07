# Project Structure

The full file tree of stackctl, with the role of each file. `CLAUDE.md` has the short map; read this file before you add, move or look for a file.

```
.github/
  workflows/                  # ci.yml, docker.yml, live-tests.yml, release.yml, security.yml
  instructions/               # Copilot path instructions
.claude/agents/               # Claude Code agent definitions
.claude/skills/               # Claude Code skills (ship-issue)
.goreleaser.yml               # Release config (GoReleaser)
Dockerfile                    # Container image
install.sh                    # Install script (honours INSTALL_DIR)
LICENSE                       # MIT license
README.md                     # Overview, install, quick start, documentation map (keep it short)
CONTRIBUTING.md               # Development setup, structure, tests, pull requests
docs/claude/                  # Reference docs for agents: api-reference.md, structure.md (this file)
docs/                         # installation.md, configuration.md, commands.md (examples per command group, output formats, flags), server-versions.md
assets/                       # Terminal images of the README (made with `freeze --execute "stackctl --help" --theme dracula --window`)
entities.json, mempalace.yaml # Mempalace mining config
cli/                          # Go module root (github.com/omattsson/stackctl/cli); run go commands here
  main.go                     # Entry point
  Makefile                    # build, build-all, test, lint, coverage, install, clean, tools
  cmd/
    root.go                   # Root cobra command, global flags, config loading, flag > env > config (resolveAPIURL, resolveAPIKey)
    config.go                 # config set/get/list/use-context/current-context/delete-context
    version.go                # Version info (build-time ldflags)
    agent_guide.go, .md       # agent-guide: embedded Markdown guide for agents and scripts ({{VERSION}} = build version); agent_guide_test.go checks every stackctl command and flag in it
    login.go                  # login, logout (--all: logout-all first), whoami, auth register
    token.go                  # Token helpers (save/load/delete session, fresh token for plugins)
    stack.go                  # stack list/get/create/update/deploy/stop/clean/delete/status/logs/clone/extend/values/compare/history/rollback/history-values/watch
    stack_inspect.go          # stack preview/pods/recent (deploy-preview, live pods and events with the EXEC column of the exec hints, recent)
    stack_action.go           # stack action list/run/log (custom actions of the hooks config; job log polling with backoff)
    stack_followers.go        # stack follow/unfollow (in-app notifications; not the --follow log flag)
    stack_portforward.go      # stack port-forward (parsePortSpec, --address, listeners with port fallback, one tunnel per local connection); addrinuse_*.go: EADDRINUSE per OS
    stack_exec.go             # stack exec (args after '--', -c, signals: execSignals/execForceExit, exit code mapping)
    exitcode.go               # ExitError{Code, Err}: main.go exits with Code (stack exec); exit codes 124, 130, 255
    ttl.go                    # --ttl/--stop-at/--until parsing, TTL limit check (GET /api/v1/ui-config, 30-day fallback), expiry text
    server.go                 # server info (ping, ui-config; public routes, no credentials)
    template.go               # template list/get/create/update/clone/publish/unpublish/versions/instantiate/quick-deploy/delete
    definition.go             # definition list/get/create/update/delete/export/import/update-chart
    chart_changes.go          # definition add-chart/remove-chart/check-upgrade/upgrade/link-template, template add-chart/remove-chart
    override.go               # override list/get/set/unset/delete, branch overrides (list/get/set/delete), quota overrides (quota set merges with GET, --replace)
    bulk.go                   # bulk deploy/stop/clean/delete, bulk template publish/unpublish/delete (--ids flag or positional args)
    git.go                    # git providers/branches/validate
    cluster.go                # cluster list/get/create/update/delete/set-default/test-connection/health/nodes/namespaces/utilization (create/update --allow-port-forward/--allow-exec: per-cluster stream switches; update sends false. --api-server-url is required with a kubeconfig flag; --use-in-cluster excludes the kubeconfig flags), quota get/set/delete, shared-values list/set/update/delete (update: GET list, merge, PUT the full record)
    analytics.go              # analytics overview/templates/users
    apikey.go                 # apikey list/create/revoke
    audit.go                  # audit log list/export
    cleanup_policy.go         # cleanup-policy list/get/create/update/delete/run
    favorite.go               # favorite list/add/remove/check
    dashboard.go              # dashboard (GET /api/v1/dashboard: failing, expiring, recent deployments, clusters)
    notification.go           # notification list/count/read/read-all/prefs
    notification_channel.go   # notification channel list/get/create/update/delete/events/subscriptions/subscribe/test/logs (webhook URL masked unless --show-url)
    orphaned.go               # orphaned list/delete
    user.go                   # user list/delete/disable/enable/reset-password/set-role
    values.go                 # --set value parsing helpers (key paths, scalar types) for stack and override commands
    browser.go                # Open a URL in the browser (SSO login)
    paging.go                 # List paging helpers (page, pageSize, listAllIDs)
    resolve.go                # Name-to-ID resolution
    plugins.go                # Plugin discovery (stackctl-<name> on PATH) and exec
    completion.go             # Shell completion generation (bash/zsh/fish/powershell)
    testdata/                 # Golden files for cmd tests
  pkg/
    client/
      client.go              # HTTP client wrapper (auth headers, base URL, error handling)
      auth.go                # Session renewal (refresh token, 401 retry), login/refresh/logout calls
      websocket.go           # WebSocket client (watch, deploy log streaming); stream dial without ?token= (dialWSOpts), handshake error codes, close codes, keepalive
      portforward.go         # DialPortForward and Tunnel (byte copy, half-close handling, keepalive)
      exec.go                # Exec (start message, stdout/stderr frames, exit frame, close codes), ValidateExecCommand
    types/
      types.go               # Client-side structs matching API responses
    config/
      config.go              # YAML config via yaml.v3 (config.yaml in the config dir)
      token.go               # Token file store (tokens/<context>.json) + cross-process lock
      lock_*.go              # flock (unix) / LockFileEx (windows) / no-op (other)
    output/
      output.go              # Table, JSON, YAML formatters
  test/
    e2e/                     # Binary execution end-to-end tests (cli_e2e_test.go)
    integration/             # Integration tests against a mock API (*_integration_test.go)
    contract/                # Swagger contract tests; refresh-swagger.sh refreshes testdata/swagger.json
    live/                    # Live tests against a real backend (build tag `live`)
```
