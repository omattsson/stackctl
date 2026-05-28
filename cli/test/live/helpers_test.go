//go:build live

package live

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/require"
)

// Live-test conventions for new files under this package:
//
//   1. Use newLiveClient(t) — handles connectivity + auth (API key or login)
//      and skips the test if neither path is configured.
//   2. Never create real workloads. The CI runner is shared dev infra; tests
//      must be safe to interleave. Stick to wire-shape round-trips: read
//      lists, create-then-delete bare resources, exercise filters. If a
//      command needs a deployed instance, GetStackStatus or GetStackLogs is
//      fine against an existing one; do NOT call DeployStack inside live
//      tests.
//   3. Register cleanup via t.Cleanup so a failed assertion never leaves a
//      named resource behind.
//   4. Skip — don't fail — when the backend doesn't have the precondition
//      (e.g. "no templates available") so partial environments stay green.

// liveResourcePrefix uniquely identifies resources created by this run.
// Use it as a name prefix so leftovers from a crashed test are obvious
// in `stackctl stack list` / `stackctl template list`.
func liveResourcePrefix() string {
	return fmt.Sprintf("live-%d", time.Now().UnixMilli())
}

// deleteTemplateIfExists registers a t.Cleanup that drops a template by ID
// regardless of test outcome.
func deleteTemplateIfExists(t *testing.T, c *client.Client, id string) {
	t.Helper()
	t.Cleanup(func() {
		_ = c.DeleteTemplate(id)
	})
}

// deleteDefinitionIfExists registers a t.Cleanup that drops a definition by ID.
func deleteDefinitionIfExists(t *testing.T, c *client.Client, id string) {
	t.Helper()
	t.Cleanup(func() {
		_ = c.DeleteDefinition(id)
	})
}

// requireTemplate fetches the first template available and skips if none.
// Used by tests that need a template to exist but don't care which one.
func requireTemplate(t *testing.T, c *client.Client) types.StackTemplate {
	t.Helper()
	resp, err := c.ListTemplates(nil)
	require.NoError(t, err, "list templates")
	if len(resp.Data) == 0 {
		t.Skip("backend has no templates — seed Klaravik Core/Full Stack to run this suite")
	}
	return resp.Data[0]
}

// requireDefinition fetches the first definition available and skips if none.
func requireDefinition(t *testing.T, c *client.Client) types.StackDefinition {
	t.Helper()
	resp, err := c.ListDefinitions(nil)
	require.NoError(t, err, "list definitions")
	if len(resp.Data) == 0 {
		t.Skip("backend has no definitions — seed Klaravik Full Stack SE/DK to run this suite")
	}
	return resp.Data[0]
}

// requireCluster fetches the default cluster (or the first if none default)
// and skips if no clusters are registered. Tests that need a cluster ID for
// a payload (cluster_id field) get one this way.
func requireCluster(t *testing.T, c *client.Client) types.Cluster {
	t.Helper()
	clusters, err := c.ListClusters()
	require.NoError(t, err, "list clusters")
	if len(clusters) == 0 {
		t.Skip("backend has no clusters — register one via stackctl cluster create to run this suite")
	}
	for _, cl := range clusters {
		if cl.IsDefault {
			return cl
		}
	}
	return clusters[0]
}

// skipUnlessHTTP200 issues a GET against the live backend and skips the
// test if the response status isn't 200. Used to gate tests that depend on
// optional endpoints (e.g. /api/v1/notifications when notifications are
// disabled in this backend build).
func skipUnlessHTTP200(t *testing.T, c *client.Client, path string) {
	t.Helper()
	httpC := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpC.Get(strings.TrimSuffix(c.BaseURL, "/") + path)
	if err != nil {
		t.Skipf("endpoint %s unreachable: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("endpoint %s returned 404 — feature not enabled on this backend", path)
	}
	t.Skipf("endpoint %s returned HTTP %d", path, resp.StatusCode)
}

// ensure imports stay used — these helpers are pulled in via _test.go
// files so the compiler keeps them; the OS import is here because
// live_test.go used it.
var _ = os.Getenv
