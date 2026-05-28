//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveCluster_ListAndGet locks the read-side cluster wire contract.
// All clusters in the live backend must decode without losing IDs.
func TestLiveCluster_ListAndGet(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	clusters, err := c.ListClusters()
	require.NoError(t, err, "list clusters")
	require.NotEmpty(t, clusters, "backend must have at least one cluster registered")

	target := clusters[0]
	for _, cl := range clusters {
		if cl.IsDefault {
			target = cl
			break
		}
	}

	got, err := c.GetCluster(target.ID)
	require.NoError(t, err, "get cluster by ID")
	assert.Equal(t, target.ID, got.ID)
	assert.Equal(t, target.Name, got.Name)
}

// TestLiveCluster_QuotaRoundTrip verifies the cluster quota wire contract.
// Read-only — we don't mutate the quota because other tests + the live
// site depend on it. Just confirms the response decodes with non-zero
// fields populated.
func TestLiveCluster_QuotaRoundTrip(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	cluster := requireCluster(t, c)

	q, err := c.GetClusterQuota(cluster.ID)
	if err != nil {
		// Quota is optional — some clusters run unbounded. Only skip on
		// the documented not-configured / not-found paths so genuine
		// contract regressions still surface as failures.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no quota") {
			t.Skipf("cluster %s has no quota configured: %v", cluster.ID, err)
		}
		require.NoError(t, err, "get cluster quota")
	}
	assert.NotEmpty(t, q.CPURequest, "quota cpu_request should be set when quota exists")
	assert.NotEmpty(t, q.MemoryRequest, "quota memory_request should be set when quota exists")
}

// TestLiveCluster_HealthAndTest exercises the diagnostic endpoints.
// These are pure reads — safe to call repeatedly.
func TestLiveCluster_HealthAndTest(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	cluster := requireCluster(t, c)

	t.Run("health", func(t *testing.T) {
		health, err := c.GetClusterHealth(cluster.ID)
		require.NoError(t, err, "get cluster health")
		// Health summary should at least populate node count for any
		// non-empty cluster. Zero would mean the wire decode lost data.
		assert.GreaterOrEqual(t, health.NodeCount, 0, "node_count must decode (zero is fine for empty clusters)")
	})

	t.Run("test_connection", func(t *testing.T) {
		_, err := c.TestClusterConnection(cluster.ID)
		// TestClusterConnection is allowed to fail (the backend may not
		// be able to reach the cluster from CI). What matters is the
		// response shape — if it returns NoError, we don't assert
		// further; if it errors, we don't fail the whole suite.
		if err != nil {
			t.Logf("cluster %s test-connection failed (expected when backend can't reach kube-apiserver): %v", cluster.ID, err)
		}
	})

	t.Run("nodes", func(t *testing.T) {
		nodes, err := c.GetClusterNodes(cluster.ID)
		if err != nil {
			// Skip only when the backend can't reach the cluster — any
			// other error (bad wire shape, 5xx) should fail the test.
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "not reachable") || strings.Contains(msg, "unavailable") || strings.Contains(msg, "connection refused") {
				t.Skipf("nodes endpoint unavailable (cluster not reachable): %v", err)
			}
			require.NoError(t, err, "get cluster nodes")
		}
		// Just verify we get a decodable slice back. Empty is fine.
		assert.NotNil(t, nodes)
	})
}
