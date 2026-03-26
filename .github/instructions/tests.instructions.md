---
applyTo: "cli/**_test.go"
---

# Test Conventions

## Framework
- Use `testify/assert` for non-fatal assertions and `testify/require` for fatal ones
- Table-driven tests with `t.Parallel()` on parent and subtests
- Capture range variable: `tt := tt` inside the loop
- **Exception**: `cli/cmd/` tests must NOT use `t.Parallel()` because setup helpers (e.g. `setupStackTestCmd`) mutate package-level globals (`cfg`, `printer`, `flagAPIURL`)

## API Mocking
Always use `httptest.NewServer` — never call a real API in unit tests.

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    require.Equal(t, expectedPath, r.URL.Path)
    require.Equal(t, expectedMethod, r.Method)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}))
defer server.Close()
```

## Coverage Requirements
- Test all output modes: table, JSON, YAML, quiet
- Test error cases: API errors (401, 404, 500), invalid input
- Test flag parsing and validation
- Test confirmation prompt behavior (yes/no/--yes flag)
- Target 80%+ coverage on `pkg/` packages

## cmd/ Test Setup
Use `setupStackTestCmd(t, apiURL)` or equivalent helper that:
1. Sets `flagAPIURL` to the mock server URL
2. Creates a fresh `printer` with a `bytes.Buffer`
3. Registers cleanup to restore defaults
