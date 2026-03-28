//go:build live

package live

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseURL() string {
	if u := os.Getenv("STACKCTL_LIVE_URL"); u != "" {
		return u
	}
	return "http://localhost:8081"
}

// newLiveClient creates a client pointing at the live backend and verifies
// connectivity. If the backend is unreachable the test is skipped.
func newLiveClient(t *testing.T) *client.Client {
	t.Helper()

	if os.Getenv("STACKCTL_LIVE_USER") == "" || os.Getenv("STACKCTL_LIVE_PASS") == "" {
		t.Skip("STACKCTL_LIVE_USER and STACKCTL_LIVE_PASS must be set for live tests")
	}

	c := client.New(baseURL())

	// Quick connectivity check — skip (not fail) if backend is down.
	httpC := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpC.Get(baseURL() + "/api/v1/health")
	if err != nil {
		t.Skipf("Backend unreachable at %s: %v", baseURL(), err)
	}
	resp.Body.Close()

	return c
}

// login authenticates the client and returns the login response.
func login(t *testing.T, c *client.Client) *types.LoginResponse {
	t.Helper()
	resp, err := c.Login(os.Getenv("STACKCTL_LIVE_USER"), os.Getenv("STACKCTL_LIVE_PASS"))
	require.NoError(t, err, "login must succeed")
	require.NotEmpty(t, resp.Token, "token must not be empty")
	return resp
}

// cleanupInstance registers a t.Cleanup that best-effort deletes the instance.
func cleanupInstance(t *testing.T, c *client.Client, id uint) {
	t.Helper()
	t.Cleanup(func() {
		// Best-effort: stop -> clean -> delete. Ignore errors because the
		// instance may already be in a terminal state.
		_, _ = c.StopStack(id)
		_, _ = c.CleanStack(id)
		_ = c.DeleteStack(id)
	})
}

func TestLiveWorkflow_FullLifecycle(t *testing.T) {
	c := newLiveClient(t)

	// Step 1: Login
	t.Log("Step 1: Login")
	loginResp := login(t, c)
	assert.NotEmpty(t, loginResp.User.Username)

	// Step 2: List templates — pick the first available
	t.Log("Step 2: List templates")
	templates, err := c.ListTemplates(nil)
	require.NoError(t, err, "list templates")
	if len(templates.Data) == 0 {
		t.Skip("No templates available on backend — cannot run full lifecycle test")
	}
	tmpl := templates.Data[0]
	t.Logf("Using template %d (%s)", tmpl.ID, tmpl.Name)

	// Step 3: Instantiate template
	t.Log("Step 3: Instantiate template")
	instance, err := c.InstantiateTemplate(tmpl.ID, &types.InstantiateTemplateRequest{
		Name:   fmt.Sprintf("live-test-%d", time.Now().UnixMilli()),
		Branch: "main",
	})
	require.NoError(t, err, "instantiate template")
	require.NotZero(t, instance.ID, "instance ID must be set")
	t.Logf("Created instance %d (%s)", instance.ID, instance.Name)

	// Register cleanup so resources are removed even on failure.
	cleanupInstance(t, c, instance.ID)

	// Step 4: Deploy
	t.Log("Step 4: Deploy")
	deployLog, err := c.DeployStack(instance.ID)
	require.NoError(t, err, "deploy stack")
	assert.NotZero(t, deployLog.ID, "deploy log ID must be set")

	// Step 5: Check status
	t.Log("Step 5: Check status")
	status, err := c.GetStackStatus(instance.ID)
	require.NoError(t, err, "get stack status")
	assert.NotEmpty(t, status.Status, "status field must be present")
	t.Logf("Status: %s, pods: %d", status.Status, len(status.Pods))

	// Step 6: Set overrides — value override (replicas=2) and branch override on chart ID 1
	t.Log("Step 6: Set overrides")
	_, err = c.SetValueOverride(instance.ID, 1, &types.SetValueOverrideRequest{
		Values: map[string]interface{}{"replicas": 2},
	})
	require.NoError(t, err, "set value override")

	_, err = c.SetBranchOverride(instance.ID, 1, &types.SetBranchOverrideRequest{
		Branch: "feature/test",
	})
	require.NoError(t, err, "set branch override")

	// Verify overrides were persisted
	valOverrides, err := c.ListValueOverrides(instance.ID)
	require.NoError(t, err, "list value overrides")
	assert.NotEmpty(t, valOverrides, "should have at least one value override")

	branchOverrides, err := c.ListBranchOverrides(instance.ID)
	require.NoError(t, err, "list branch overrides")
	assert.NotEmpty(t, branchOverrides, "should have at least one branch override")

	// Step 7: Redeploy after overrides
	t.Log("Step 7: Redeploy")
	redeployLog, err := c.DeployStack(instance.ID)
	require.NoError(t, err, "redeploy stack")
	assert.NotZero(t, redeployLog.ID, "redeploy log ID must be set")

	// Step 8: View logs
	t.Log("Step 8: View logs")
	logEntry, err := c.GetStackLogs(instance.ID)
	require.NoError(t, err, "get stack logs")
	assert.NotEmpty(t, logEntry.Action, "log action must be present")

	// Step 9: Stop
	t.Log("Step 9: Stop")
	stopLog, err := c.StopStack(instance.ID)
	require.NoError(t, err, "stop stack")
	assert.NotZero(t, stopLog.ID, "stop log ID must be set")

	// Step 10: Clean
	t.Log("Step 10: Clean")
	cleanLog, err := c.CleanStack(instance.ID)
	require.NoError(t, err, "clean stack")
	assert.NotZero(t, cleanLog.ID, "clean log ID must be set")

	// Step 11: Delete
	t.Log("Step 11: Delete")
	err = c.DeleteStack(instance.ID)
	require.NoError(t, err, "delete stack")

	// Step 12: Verify deleted — GetStack should return an error.
	t.Log("Step 12: Verify deleted")
	_, err = c.GetStack(instance.ID)
	assert.Error(t, err, "GetStack on deleted instance should fail")
}

func TestLiveWorkflow_BulkOperations(t *testing.T) {
	c := newLiveClient(t)

	// Step 1: Login
	t.Log("Step 1: Login")
	login(t, c)

	// We need at least one definition. Use definition_id=1.
	const defID uint = 1

	// Step 2: Create 3 stack instances
	t.Log("Step 2: Create 3 stack instances")
	var ids []uint
	for i := 0; i < 3; i++ {
		inst, err := c.CreateStack(&types.CreateStackRequest{
			Name:              fmt.Sprintf("live-bulk-%d-%d", time.Now().UnixMilli(), i),
			StackDefinitionID: defID,
			Branch:            "main",
		})
		require.NoError(t, err, "create stack %d", i)
		require.NotZero(t, inst.ID)
		ids = append(ids, inst.ID)
		t.Logf("Created instance %d", inst.ID)
	}

	// Register cleanup for all created instances.
	for _, id := range ids {
		cleanupInstance(t, c, id)
	}

	// Step 3: Bulk deploy
	t.Log("Step 3: Bulk deploy")
	deployResp, err := c.BulkDeploy(ids)
	require.NoError(t, err, "bulk deploy")
	assert.Len(t, deployResp.Results, 3, "should have 3 results")
	for _, r := range deployResp.Results {
		assert.True(t, r.Success, "deploy should succeed for id %d: %s", r.ID, r.Error)
	}

	// Step 4: Bulk stop
	t.Log("Step 4: Bulk stop")
	stopResp, err := c.BulkStop(ids)
	require.NoError(t, err, "bulk stop")
	assert.Len(t, stopResp.Results, 3)
	for _, r := range stopResp.Results {
		assert.True(t, r.Success, "stop should succeed for id %d: %s", r.ID, r.Error)
	}

	// Step 5: Bulk clean
	t.Log("Step 5: Bulk clean")
	cleanResp, err := c.BulkClean(ids)
	require.NoError(t, err, "bulk clean")
	assert.Len(t, cleanResp.Results, 3)
	for _, r := range cleanResp.Results {
		assert.True(t, r.Success, "clean should succeed for id %d: %s", r.ID, r.Error)
	}

	// Step 6: Bulk delete
	t.Log("Step 6: Bulk delete")
	deleteResp, err := c.BulkDelete(ids)
	require.NoError(t, err, "bulk delete")
	assert.Len(t, deleteResp.Results, 3)
	for _, r := range deleteResp.Results {
		assert.True(t, r.Success, "delete should succeed for id %d: %s", r.ID, r.Error)
	}

	// Step 7: Verify all deleted
	t.Log("Step 7: Verify all deleted")
	for _, id := range ids {
		_, err := c.GetStack(id)
		assert.Error(t, err, "GetStack on deleted instance %d should fail", id)
	}
}
