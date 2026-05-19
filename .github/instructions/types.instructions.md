---
applyTo: "cli/pkg/types/**"
---

# Types Package Conventions

## Purpose
`cli/pkg/types` is the client-side contract layer — structs that mirror the backend API's JSON response shapes. No business logic belongs here.

## Struct Rules

### Tags
Every field MUST have both `json:` and `yaml:` struct tags. Omit-empty where the field is optional in the API response:
```go
Name   string `json:"name" yaml:"name"`             // required in response
Region string `json:"region,omitempty" yaml:"region,omitempty"` // optional
```

### IDs
All ID fields are `string`, not `int` or `uint`. The backend uses string IDs in JSON responses. Never convert to a numeric type in this package.

### Embedded Base
Resource structs that correspond to backend entities embed `Base`:
```go
type Cluster struct {
    Base
    Name string `json:"name" yaml:"name"`
    // ...
}
```
Request/response-only structs (e.g. `CreateClusterRequest`, `LoginResponse`) do NOT embed `Base`.

### Update Requests
Update request structs use pointer fields for all optional fields so that only non-nil fields are marshaled:
```go
type UpdateFooRequest struct {
    Name *string `json:"name,omitempty" yaml:"name,omitempty"`
}
```

### Docstrings
Every exported type MUST have a godoc comment that states:
1. What API endpoint(s) it corresponds to
2. For response types: when a field is populated vs. zero-valued (especially for status/error fields)
3. For types that are only populated on a specific HTTP status code (e.g. 200 only), state that explicitly

```go
// ClusterTestConnectionResult is the response shape of
// POST /api/v1/clusters/:id/test. On success Status == "success". On an
// unreachable cluster the backend returns a non-2xx response that the client
// surfaces as an APIError; this struct is only populated on 200 responses.
type ClusterTestConnectionResult struct { ... }
```

## What Does NOT Belong Here
- No constants for status strings (they live in `cmd/` where they are used for comparison or output)
- No constructor functions
- No methods that call `pkg/client`
- No formatting or display logic
