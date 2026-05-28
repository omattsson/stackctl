//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveCleanupPolicy_CRUDAndDryRun exercises the cleanup-policy
// wire contract end-to-end: create → list → update → run --dry-run →
// delete. Admin-gated on the backend; the CI suite runs as the
// env-seeded admin so all five verbs are reachable.
//
// The run is always invoked with dry_run=true so we never mutate stack
// instances — only the response wire shape ([]CleanupResult) is checked.
// Empty results are fine: the seed backend rarely has stacks matching
// "idle_days:9999", which is the deliberately-impossible condition.
func TestLiveCleanupPolicy_CRUDAndDryRun(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	cluster := requireCluster(t, c)
	prefix := liveResourcePrefix()

	// 1. Create — use the "stop" action and an idle_days condition that
	// will never match in CI (no stack has been idle for 9999 days), so
	// run --dry-run is guaranteed to return an empty result set.
	created, err := c.CreateCleanupPolicy(&types.CreateCleanupPolicyRequest{
		Name:      prefix + "-cleanup",
		ClusterID: cluster.ID,
		Action:    "stop",
		Condition: "idle_days:9999",
		Schedule:  "0 3 * * *",
		Enabled:   false,
		DryRun:    true,
	})
	require.NoError(t, err, "create cleanup policy")
	require.NotEmpty(t, created.ID, "created policy must have an ID")
	assert.Equal(t, cluster.ID, created.ClusterID, "policy must echo cluster_id")
	assert.Equal(t, "stop", created.Action, "policy must echo action")
	assert.Equal(t, "idle_days:9999", created.Condition, "policy must echo condition")
	assert.False(t, created.Enabled, "fresh policy must echo enabled=false")
	assert.True(t, created.DryRun, "fresh policy must echo dry_run=true")

	// Always best-effort delete so a failed assertion doesn't leave the
	// policy in the cluster's schedule.
	t.Cleanup(func() {
		_ = c.DeleteCleanupPolicy(created.ID)
	})

	// 2. List — newly-created policy must be visible.
	policies, err := c.ListCleanupPolicies()
	require.NoError(t, err, "list cleanup policies")
	var found *types.CleanupPolicy
	for i := range policies {
		if policies[i].ID == created.ID {
			found = &policies[i]
			break
		}
	}
	require.NotNilf(t, found, "newly-created cleanup policy %s must appear in list", created.ID)
	assert.Equal(t, created.Name, found.Name, "list entry must echo name")

	// 3. Update — flip the enabled flag. UpdateCleanupPolicy is a full PUT
	// so we must re-send every field (the type comment in types.go calls
	// this out explicitly).
	updated, err := c.UpdateCleanupPolicy(created.ID, &types.UpdateCleanupPolicyRequest{
		Name:      created.Name,
		ClusterID: created.ClusterID,
		Action:    created.Action,
		Condition: created.Condition,
		Schedule:  created.Schedule,
		Enabled:   true,
		DryRun:    created.DryRun,
	})
	require.NoError(t, err, "update cleanup policy")
	assert.True(t, updated.Enabled, "enabled flag must round-trip through PUT")

	// 4. Run with dry_run=true — wire-shape assertion only. Backend will
	// return an empty slice when nothing matches; that's fine. What
	// matters is that the response decodes into []CleanupResult without
	// dropping fields.
	results, err := c.RunCleanupPolicy(created.ID, true)
	require.NoError(t, err, "run cleanup policy (dry-run)")
	require.NotNil(t, results, "results slice must be non-nil (may be empty)")
	for i, r := range results {
		// On a real match each entry must populate the action +
		// status fields. Status MUST be one of the documented values.
		assert.NotEmptyf(t, r.InstanceID, "results[%d].instance_id must be set", i)
		assert.Containsf(t, []string{"success", "error", "dry_run"}, r.Status,
			"results[%d].status %q must be one of the documented values", i, r.Status)
	}

	// 5. Delete (explicit — cleanup is the safety net).
	require.NoError(t, c.DeleteCleanupPolicy(created.ID), "delete cleanup policy")
	after, err := c.ListCleanupPolicies()
	require.NoError(t, err, "list cleanup policies after delete")
	for _, p := range after {
		assert.NotEqualf(t, created.ID, p.ID,
			"deleted policy %s must not appear in list", created.ID)
	}
}
