package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apiPathLiteral matches a Go string literal that starts with /api/v1/.
// Group 2 is set when the literal ends with "/" and is followed by "+":
// a path built by concatenation ("/api/v1/x/" + id).
var apiPathLiteral = regexp.MustCompile(`"(/api/v1/[^"]*)"(\s*\+)?`)

// pathPrefixesNotInSwagger are literals that stackctl uses only as a prefix
// to classify routes (for example the auth routes that never renew a
// session). They are not request paths. Each must still be the prefix of a
// swagger path.
var pathPrefixesNotInSwagger = map[string]struct{}{
	"/api/v1/auth":       {},
	"/api/v1/auth/oidc/": {},
}

// twoStepPaths maps a literal with a fixed segment in the place of a path
// parameter to the swagger path it builds. Add an entry only for a known
// builder; every other literal must match a swagger path exactly.
var twoStepPaths = map[string]string{}

// TestClientPaths_ExistInBackend checks that every /api/v1 path in the
// non-test source of pkg/client and cmd exists in the vendored swagger.
// A wrong path gives a 404 only against a real server (stackctl#141:
// /api/v1/orphaned-namespaces instead of /api/v1/admin/orphaned-namespaces).
//
// Matching: a fixed segment must equal the swagger segment, and a %s, %d or
// %v verb must be a swagger {param}. A literal that ends with "/" and is
// followed by "+" is checked as the literal plus one parameter. A fixed
// segment in a {param} place is allowed only through twoStepPaths.
func TestClientPaths_ExistInBackend(t *testing.T) {
	t.Parallel()

	var raw struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(swaggerJSON, &raw))
	require.NotEmpty(t, raw.Paths)

	swaggerPaths := make([][]string, 0, len(raw.Paths))
	for p := range raw.Paths {
		swaggerPaths = append(swaggerPaths, strings.Split(strings.Trim(p, "/"), "/"))
	}

	literals := collectPathLiterals(t, "../../pkg/client", "../../cmd")
	require.NotEmpty(t, literals, "no /api/v1 literals found; did the source layout change?")

	for _, lit := range literals {
		if _, ok := pathPrefixesNotInSwagger[lit]; ok {
			assert.Truef(t, isSwaggerPrefix(lit, raw.Paths), "prefix %q is not the prefix of a swagger path", lit)
			continue
		}
		if want, ok := twoStepPaths[lit]; ok {
			_, exists := raw.Paths[want]
			assert.Truef(t, exists, "two-step path %q builds %q, which is not in the vendored swagger.json", lit, want)
			continue
		}
		assert.Truef(t, matchesSwaggerPath(lit, swaggerPaths),
			"path %q is not a route in the vendored swagger.json (wrong path, or refresh swagger.json)", lit)
	}
}

// collectPathLiterals returns the sorted unique /api/v1 literals of the
// non-test .go files in dirs, without a query string. A literal that ends
// with "/" and is followed by "+" gets "%s" appended (one parameter).
func collectPathLiterals(t *testing.T, dirs ...string) []string {
	t.Helper()
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			require.NoError(t, err)
			for _, m := range apiPathLiteral.FindAllStringSubmatch(string(src), -1) {
				p := m[1]
				if i := strings.IndexByte(p, '?'); i >= 0 {
					p = p[:i]
				}
				if m[2] != "" && strings.HasSuffix(p, "/") {
					p += "%s"
				}
				seen[p] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func isSwaggerPrefix(prefix string, paths map[string]json.RawMessage) bool {
	for p := range paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func matchesSwaggerPath(lit string, swaggerPaths [][]string) bool {
	segs := strings.Split(strings.Trim(lit, "/"), "/")
	for _, sp := range swaggerPaths {
		if len(sp) != len(segs) {
			continue
		}
		ok := true
		for i, s := range segs {
			if !segmentMatches(s, sp[i]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func segmentMatches(lit, swagger string) bool {
	litParam := lit == "%s" || lit == "%d" || lit == "%v"
	swaggerParam := strings.HasPrefix(swagger, "{") && strings.HasSuffix(swagger, "}")
	if litParam || swaggerParam {
		return litParam && swaggerParam
	}
	return lit == swagger
}

// TestPathMatching covers the matcher itself.
func TestPathMatching(t *testing.T) {
	t.Parallel()
	paths := [][]string{
		{"api", "v1", "stack-instances", "{id}"},
		{"api", "v1", "stack-instances", "compare"},
	}
	tests := []struct {
		lit  string
		want bool
	}{
		{"/api/v1/stack-instances/%s", true},
		{"/api/v1/stack-instances/compare", true},
		{"/api/v1/stack-instances/42", false},
		{"/api/v1/stack-instances", false},
		{"/api/v1/stack-instances/%s/deploy", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.lit, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, matchesSwaggerPath(tt.lit, paths))
		})
	}
}
