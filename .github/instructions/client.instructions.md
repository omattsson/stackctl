---
applyTo: "cli/pkg/client/**"
---

# HTTP Client Conventions

## Dual Auth
The client supports JWT tokens and API keys. API key takes precedence.
Headers are injected automatically in `do()` — never set auth headers in individual methods.

## Method Patterns
- **GET with path**: `c.Get("/api/v1/resource/" + strconv.FormatUint(uint64(id), 10))`
- **GET with query**: `c.GetWithQuery("/api/v1/resource", params)` where params is `url.Values`
- **POST/PUT**: `c.Post("/api/v1/resource", requestBody)` — body is JSON-marshaled automatically
- **DELETE**: `c.Delete("/api/v1/resource/" + strconv.FormatUint(uint64(id), 10))`

## Error Handling
The `do()` method maps HTTP status codes to user-friendly errors:
- 401 → "Not authenticated. Run 'stackctl login' first."
- 403 → "Permission denied."
- 404 → "Resource not found."
- 409 → "Conflict."
- 429 → "Rate limited."
- 500 → "Server error."

## Response Parsing
Use `json.NewDecoder(resp.Body).Decode(&result)` — never read the full body into a byte slice.

All methods return typed results: `(*types.SomeType, error)` or `([]types.SomeType, error)`.
