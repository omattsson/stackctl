# Configuration

Contexts, the config directory, login and session renewal, and the order in which stackctl reads its settings. See the [README](../README.md) for an overview and the [documentation map](../README.md#documentation).

stackctl uses named contexts to manage multiple environments. The config file is `config.yaml` in the config directory. The default directory is `~/.stackmanager`. stackctl finds the directory in this order:

1. `STACKCTL_CONFIG_DIR`
2. `$XDG_CONFIG_HOME/stackmanager`
3. `~/.stackmanager`

Tokens (`tokens/<context>.json`) and lock files (`tokens/<context>.lock`) are in the same directory.

## Contexts

```bash
# Create and switch to a context
stackctl config use-context local
stackctl config set api-url http://localhost:8081

# Add a production context
stackctl config use-context production
stackctl config set api-url https://stackmanager.example.com
stackctl config set api-key sk_prod_...

# Switch between contexts
stackctl config use-context local

# Show current context
stackctl config current-context

# List all contexts
stackctl config list

# Delete a context
stackctl config delete-context staging
```

`config set` accepts these keys: `api-url`, `api-key` and `insecure`. `config get <key>` prints one value of the current context. `config list` shows the contexts and the settings of the current context. stackctl stores `api-key` in plain text in `config.yaml`. Restrict access to the file, or use the `STACKCTL_API_KEY` environment variable instead.

## Authentication

stackctl supports two authentication methods:

- **JWT token** — `stackctl login` prompts for credentials and stores the token in `tokens/<context>.json` in the config directory
- **API key** — `stackctl config set api-key sk_...` for non-interactive / CI use

API key takes precedence when both are configured.

### Non-interactive login

Set `STACKCTL_USERNAME` and `STACKCTL_PASSWORD`, or use the flags `--username` and `--password`, to log in without a prompt. Use `--sso` to log in through the browser.

```bash
STACKCTL_USERNAME=alice STACKCTL_PASSWORD=... stackctl login
stackctl whoami    # show the current user
stackctl logout    # end the session and remove the token file
stackctl logout --all   # end every session of your user (all devices), for example after a lost laptop
```

`stackctl auth register` creates a user. Its flags are `--username`, `--display-name`, `--role`, `--service-account` and `--password-stdin`. Only an admin can set `--role` and `--service-account`.

### Session renewal

By default, the server gives a username/password login a short-lived access token (15 minutes) and a refresh token. `stackctl login` stores both in the token file (mode `0600`). stackctl renews the access token automatically:

- before a request, when the access token expires within 60 seconds;
- after a `401` response, once, and then it retries the request once;
- before it starts a plugin, so the plugin gets a valid `STACKCTL_TOKEN`.

Renewal works until the session ends. By default, the server ends a session 12 hours after the login, or after 30 minutes without use. Then stackctl removes the stored token and shows `Not authenticated. Run 'stackctl login' first.` A network or server error during renewal keeps the stored token.

The server rotates the refresh token on each renewal. stackctl holds a lock on `tokens/<context>.lock` during a renewal, so parallel stackctl commands renew only once.

An SSO login (`stackctl login --sso`) gets a token without a refresh token. By default, the server sets its life time to 24 hours (`JWT_EXPIRATION`). stackctl does not renew it. API keys have no session.

`stackctl logout` sends the access token and the refresh token to the server, which ends the session, and then removes the token file. If the server cannot be reached, stackctl prints a warning and still removes the file. `stackctl logout --all` first asks the server to revoke all refresh tokens of your user (the user of the login session, else of the API key); when that call fails, the command stops and keeps the token file, so you can try again.

## Precedence

Configuration values are resolved in this order (highest priority first):

1. Command-line flags (`--api-url`, `--api-key`)
2. Environment variables (`STACKCTL_API_URL`, `STACKCTL_API_KEY`)
3. Config file (`config.yaml` in the config directory)
