# Server version requirements

Some stackctl features need a newer k8s-stack-manager server. The table shows the first server version for each one; the list below it shows what happens with an older server. See the [README](../README.md) for an overview and the [documentation map](../README.md#documentation).

| Feature | k8s-stack-manager |
|---|---|
| `stack extend --minutes`, template draft and release, `template publish --version` | v0.6.0 or later |
| Owner, cluster and definition names in output; audit action and entity type values; paged `--mine` and name queries; quota override limit check; quota warning on `stack clone` | v0.7.0 or later |
| `--follow` and `stack watch` with an API key; `bulk delete` with clean first; `orphaned delete --confirm`; `user set-role` | v0.8.0 or later |
| `definition link-template` | v0.11.0 or later |
| `--stop-at`, `stack extend --until`, the TTL limit of `server info` and of the local check | v0.12.0 or later |
| Actions limited to some stacks (`applies_to`) and the `scope_incomplete` hint of `stack action list` (omattsson/k8s-stack-manager#720) | v0.15.0 or later |
| `stack port-forward` (omattsson/k8s-stack-manager#722) and `stack exec` (omattsson/k8s-stack-manager#723) | v0.15.0 or later |
| The `EXEC` column of `stack pods`, the containers in `stack exec` refusals, the API-key pre-check of `stack exec` and `stack port-forward` (omattsson/k8s-stack-manager#728) | v0.15.0 or later |

## With an older server

- `--stop-at` and `--until`: an older server ignores them. stackctl shows the result and exits with code 1. The local TTL check uses 30 days as the limit.
- `applies_to`: an older server lists every action for every stack.
- `stack port-forward` and `stack exec`: an older server answers 404 without an error code. stackctl says "upgrade the server to k8s-stack-manager v0.15.0 or later".
- Exec hints and the API-key pre-check: an older server sends no hints and no `stream_api_keys_allowed`. stackctl behaves as before.
