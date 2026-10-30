package contract

import (
	"encoding/json"
	"fmt"
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

// pendingRoute is a route that exists only in an open k8s-stack-manager PR.
type pendingRoute struct {
	// issue names the PR, for example "k8s-stack-manager#725".
	issue string
	// swagger is the swagger path that the PR adds, for example
	// "/api/v1/stack-instances/{id}/exec". The literal must match it.
	swagger string
}

// pendingRoutes are literals of routes that exist only in an open
// k8s-stack-manager PR: the vendored swagger.json comes from the backend
// main branch, which does not have them yet. Each entry names the PR and the
// swagger path that the PR adds; the literal must match that path (a %s, %d
// or %v verb for each {param}). As soon as the vendored swagger has the
// path, the test fails until the entry is removed: then the normal check
// runs, so a wrong literal cannot stay hidden after a refresh. Do not add
// the route to the vendored file by hand: it must stay a copy of a backend
// revision.
// The map is empty when no route is pending; the checks below then do
// nothing.
var pendingRoutes = map[string]pendingRoute{}

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

	used := map[string]bool{}
	for _, lit := range literals {
		if pr, ok := pendingRoutes[lit]; ok {
			used[lit] = true
			for _, p := range pendingRouteProblems(lit, pr, raw.Paths, swaggerPaths) {
				t.Error(p)
			}
			continue
		}
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
	for lit, pr := range pendingRoutes {
		assert.Truef(t, used[lit], "pending route %q (%s) is not used by pkg/client or cmd; remove it from pendingRoutes", lit, pr.issue)
	}
}

// pendingRouteProblems checks a pendingRoutes entry and returns what is
// wrong: the literal does not match the swagger path of the entry, or the
// vendored swagger has that path or another path that the literal matches.
func pendingRouteProblems(lit string, pr pendingRoute, paths map[string]json.RawMessage, swaggerPaths [][]string) []string {
	var problems []string
	if !strings.HasPrefix(pr.swagger, "/api/v1/") {
		problems = append(problems, fmt.Sprintf("pending route %q: swagger path %q must start with /api/v1/", lit, pr.swagger))
	}
	if !matchesSwaggerPath(lit, [][]string{strings.Split(strings.Trim(pr.swagger, "/"), "/")}) {
		problems = append(problems, fmt.Sprintf("pending route %q (%s) does not match its swagger path %q", lit, pr.issue, pr.swagger))
	}
	if _, exists := paths[pr.swagger]; exists {
		problems = append(problems, fmt.Sprintf("pending route %q (%s): %q is in the vendored swagger.json now; remove the entry from pendingRoutes", lit, pr.issue, pr.swagger))
	} else if matchesSwaggerPath(lit, swaggerPaths) {
		problems = append(problems, fmt.Sprintf("pending route %q (%s) matches a path of the vendored swagger.json now; remove the entry from pendingRoutes", lit, pr.issue))
	}
	return problems
}

// TestPendingRouteProblems covers the checks of a pendingRoutes entry.
func TestPendingRouteProblems(t *testing.T) {
	t.Parallel()
	exec := pendingRoute{issue: "k8s-stack-manager#725", swagger: "/api/v1/stack-instances/{id}/exec"}
	without := map[string]json.RawMessage{"/api/v1/stack-instances/{id}": nil}
	with := map[string]json.RawMessage{"/api/v1/stack-instances/{id}": nil, "/api/v1/stack-instances/{id}/exec": nil}
	split := func(paths map[string]json.RawMessage) [][]string {
		var out [][]string
		for p := range paths {
			out = append(out, strings.Split(strings.Trim(p, "/"), "/"))
		}
		return out
	}
	tests := []struct {
		name  string
		lit   string
		pr    pendingRoute
		paths map[string]json.RawMessage
		want  []string
	}{
		{"pending and matching", "/api/v1/stack-instances/%s/exec", exec, without, nil},
		{"literal does not match the template", "/api/v1/stack-instance/%s/exec", exec, without, []string{"does not match its swagger path"}},
		{"fixed segment in a param place", "/api/v1/stack-instances/42/exec", exec, without, []string{"does not match its swagger path"}},
		{"template in the swagger now", "/api/v1/stack-instances/%s/exec", exec, with, []string{"is in the vendored swagger.json now"}},
		{"wrong literal after the refresh", "/api/v1/stack-instance/%s/exec", exec, with,
			[]string{"does not match its swagger path", "is in the vendored swagger.json now"}},
		{"template outside /api/v1", "/api/v1/stack-instances/%s/exec", pendingRoute{issue: "x", swagger: "/stack-instances/{id}/exec"}, without,
			[]string{"must start with /api/v1/", "does not match its swagger path"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pendingRouteProblems(tt.lit, tt.pr, tt.paths, split(tt.paths))
			require.Len(t, got, len(tt.want), "problems: %v", got)
			for i, w := range tt.want {
				assert.Contains(t, got[i], w)
			}
		})
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
