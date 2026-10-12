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
`APIError.UserFacingError()` maps HTTP status codes to user-friendly errors. The server message (after `sanitizeServerMessage`) is added as ` (server: <msg>)` where shown:
- 401: "Not authenticated. Run 'stackctl login' first." plus ` (server: <msg>)`
- 403: "Permission denied." plus ` (server: <msg>)`
- 404: "Resource not found: <msg>" (`<msg>` is "unknown resource" when the server sends none)
- 409: "Conflict: <msg>" (`<msg>` is "unknown conflict" when the server sends none)
- 429: "Rate limited. Try again later." plus ` (server: <msg>)`
- 5xx: "Server error. Check backend logs." plus ` (server: <msg>)`
- Other status codes: the raw server message.

`sanitizeServerMessage` removes control and bidi characters, collapses whitespace and truncates long text. Always pass server text through it (or `SanitizeServerText`) before display.

## Session Renewal and Warnings
- `pkg/client/auth.go` renews the JWT session before a request when the token expires within `RenewMargin` (60 s), and after a 401 from a non-auth endpoint (one retry). Do not add renewal logic in individual methods.
- The client writes `Warning` response headers (for example deprecation notices) to the client warning writer (`WarnWriter`, default stderr) as `Warning: <text>`, once per text (`warnHeaders`).

## Response Parsing
Use `json.NewDecoder(resp.Body).Decode(&result)` for JSON responses. Do not read a JSON body into a byte slice. For raw payloads (export, import, download), `io.ReadAll` on an `io.LimitReader` is allowed; fail when the limit is exceeded. Paginated list endpoints return `*types.ListResponse[T]`.

All methods return typed results: `(*types.SomeType, error)`, `([]types.SomeType, error)`, or `(*types.ListResponse[T], error)`.

## Debug Logging
When `c.Debug` is true, requests and responses are written to `c.DebugWriter` (set to `os.Stderr` from `cmd/root.go`). Auth headers (`Authorization`, `X-API-Key`) MUST be redacted. Never log request/response bodies for auth endpoints (`/auth/login`, `/auth/cli/*`).
