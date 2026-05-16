---
applyTo: "cli/pkg/client/**"
---

# HTTP Client Conventions

## Dual Auth
The client supports JWT tokens and API keys. API key takes precedence.
Headers are injected automatically in `do()` — never set auth headers in individual methods.

## Method Patterns
IDs in client method signatures are `string` — no numeric conversion. Callers pass the value returned from `parseID()` or `resolveStackID()` directly.

- **GET with path**: `c.Get("/api/v1/resource/" + id, &result)`
- **GET with query**: `c.GetWithQuery("/api/v1/resource", params, &result)` where params is `map[string]string`
- **POST/PUT**: `c.Post("/api/v1/resource", requestBody, &result)` — body is JSON-marshaled automatically
- **DELETE**: `c.Delete("/api/v1/resource/" + id)`
- **Idempotent retries**: `do()` calls `doWithRetry()` for GET/HEAD on transient errors; do not add retry loops in individual methods.
- **WebSocket**: streaming methods live in `websocket.go` (e.g. `StreamDeploymentLogs`). They reuse the client's `APIKey`/`Token` for auth and the HTTP transport's TLS config — never construct a new dialer with hardcoded settings.

## Error Handling
The `do()` method maps HTTP status codes to user-friendly errors:
- 401 → "Not authenticated. Run 'stackctl login' first."
- 403 → "Permission denied."
- 404 → "Resource not found."
- 409 → "Conflict."
- 429 → "Rate limited."
- 500 → "Server error."

## Response Parsing
Use `json.NewDecoder(resp.Body).Decode(&result)` — never read the full body into a byte slice. Paginated list endpoints return `*types.ListResponse[T]`.

All methods return typed results: `(*types.SomeType, error)`, `([]types.SomeType, error)`, or `(*types.ListResponse[T], error)`.

## Debug Logging
When `c.Debug` is true, requests and responses are written to `c.DebugWriter` (set to `os.Stderr` from `cmd/root.go`). Auth headers (`Authorization`, `X-API-Key`) MUST be redacted. Never log request/response bodies for auth endpoints (`/auth/login`, `/auth/cli/*`).
