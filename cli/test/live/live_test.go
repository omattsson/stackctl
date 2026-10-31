//go:build live

package live

import (
	"errors"
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
//
// Auth: prefers STACKCTL_LIVE_API_KEY (header-based, no session) and falls
// back to STACKCTL_LIVE_USER + STACKCTL_LIVE_PASS via Login. If neither is
// configured the suite is skipped.
func newLiveClient(t *testing.T) *client.Client {
	t.Helper()

	apiKey := os.Getenv("STACKCTL_LIVE_API_KEY")
	user, pass := os.Getenv("STACKCTL_LIVE_USER"), os.Getenv("STACKCTL_LIVE_PASS")
	if apiKey == "" && (user == "" || pass == "") {
		t.Skip("STACKCTL_LIVE_API_KEY or (STACKCTL_LIVE_USER + STACKCTL_LIVE_PASS) must be set for live tests")
	}

	c := client.New(baseURL())

	// Quick connectivity check — skip (not fail) if backend is down.
	// Backend exposes /health/live (Kubernetes liveness probe), not /api/v1/health.
	httpC := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpC.Get(baseURL() + "/health/live")
	if err != nil {
		t.Skipf("Backend unreachable at %s: %v", baseURL(), err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Backend unhealthy at %s: HTTP %d", baseURL(), resp.StatusCode)
	}

	// API-key path: stamps the X-API-Key header on every subsequent request,
	// no login needed. Password path is handled by the per-test login() helper.
	if apiKey != "" {
		c.APIKey = apiKey
	}

	return c
}

// login authenticates the client when running in password mode and returns
// the login response. When STACKCTL_LIVE_API_KEY is set this is a no-op —
// the client is already authenticated via the header set in newLiveClient.
func login(t *testing.T, c *client.Client) *types.LoginResponse {
	t.Helper()
	if os.Getenv("STACKCTL_LIVE_API_KEY") != "" {
		return &types.LoginResponse{}
	}
	resp, err := c.Login(os.Getenv("STACKCTL_LIVE_USER"), os.Getenv("STACKCTL_LIVE_PASS"))
	require.NoError(t, err, "login must succeed")
	require.NotEmpty(t, resp.Token, "token must not be empty")
	return resp
}

// liveOpTimeout bounds the wait for one deploy, stop, clean or delete of a
// heavy live test.
const liveOpTimeout = 10 * time.Minute

// cleanupInstance registers a t.Cleanup that best-effort deletes the
// instance. It first waits until no operation runs (the server refuses a
// delete during one); a delete of a deployed stack cleans it first.
func cleanupInstance(t *testing.T, c *client.Client, id string) {
	t.Helper()
	t.Cleanup(func() {
		deadline := time.Now().Add(liveOpTimeout)
		for time.Now().Before(deadline) {
			inst, err := c.GetStack(id)
			if isNotFound(err) {
				return // already deleted
			}
			if err == nil && client.IsTerminalStatus(inst.Status) {
				break
			}
			if err != nil {
				t.Logf("cleanup: get stack %s: %v", id, err)
			}
			time.Sleep(2 * time.Second)
		}
		if err := c.DeleteStack(id); err != nil {
			t.Logf("cleanup: delete stack %s: %v", id, err)
			return
		}
		waitStackGone(t, c, id, liveOpTimeout)
	})
}

// isNotFound reports a 404 of the API.
func isNotFound(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// waitStackGone waits until GET of the instance returns 404 (deleted).
// Other errors (a network error, a 5xx) are retried until the timeout.
func waitStackGone(t *testing.T, c *client.Client, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		inst, err := c.GetStack(id)
		if isNotFound(err) {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Errorf("stack %s: no 404 after %s (last error: %v)", id, timeout, err)
			} else {
				t.Errorf("stack %s still exists after %s (status %q)", id, timeout, inst.Status)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// TestLiveWorkflow_FullLifecycle exercises the complete deploy-stop-clean-
// delete path against a real backend by actually rolling out a stack
// instance. It pulls images, allocates PVs, and burns cluster quota — so
// it's gated behind STACKCTL_LIVE_HEAVY=1 to keep it off the default
// suite. CI smoke + per-endpoint wire-contract coverage lives in the
// other *_live_test.go files in this package. Each operation is awaited
// before the next one: the server refuses a clean during a stop and a
// delete during an operation.
func TestLiveWorkflow_FullLifecycle(t *testing.T) {
	if os.Getenv("STACKCTL_LIVE_HEAVY") != "1" {
		t.Skip("set STACKCTL_LIVE_HEAVY=1 to run real-workload lifecycle tests (~5–10 min, requires cluster capacity)")
	}
	c := newLiveClient(t)

	// Step 1: Login
	t.Log("Step 1: Login")
	loginResp := login(t, c)
	if loginResp.User.Username != "" {
		t.Logf("Logged in as %s", loginResp.User.Username)
	}

	// Step 2: List templates — pick the first available
	t.Log("Step 2: List templates")
	templates, err := c.ListTemplates(nil)
	require.NoError(t, err, "list templates")
	if len(templates.Data) == 0 {
		t.Skip("No templates available on backend — cannot run full lifecycle test")
	}
	tmpl := templates.Data[0]
	t.Logf("Using template %s (%s)", tmpl.ID, tmpl.Name)

	// Step 3: Instantiate the template (a definition), then create a stack
	// from the definition.
	t.Log("Step 3: Instantiate template and create a stack")
	name := fmt.Sprintf("live-test-%d", time.Now().UnixMilli())
	def, err := c.InstantiateTemplate(tmpl.ID, &types.InstantiateTemplateRequest{
		Name:   name,
		Branch: "main",
	})
	require.NoError(t, err, "instantiate template")
	require.NotEmpty(t, def.ID, "definition ID must be set")
	deleteDefinitionIfExists(t, c, def.ID)
	instance, err := c.CreateStack(&types.CreateStackRequest{
		Name:              name,
		StackDefinitionID: def.ID,
		Branch:            "main",
	})
	require.NoError(t, err, "create stack")
	require.NotEmpty(t, instance.ID, "instance ID must be set")
	t.Logf("Created definition %s and instance %s (%s)", def.ID, instance.ID, instance.Name)

	// Register cleanup so resources are removed even on failure. It runs
	// before the definition cleanup (t.Cleanup is last in, first out).
	cleanupInstance(t, c, instance.ID)

	// Step 4: Deploy
	t.Log("Step 4: Deploy")
	deployLog, err := c.DeployStack(instance.ID)
	require.NoError(t, err, "deploy stack")
	assert.NotEmpty(t, deployLog.LogID, "deploy log ID must be set")
	waitStackStatus(t, c, instance.ID, "running", liveOpTimeout)

	// Step 5: Check status
	t.Log("Step 5: Check status")
	status, err := c.GetStackStatus(instance.ID)
	require.NoError(t, err, "get stack status")
	assert.NotEmpty(t, status.Status, "status field must be present")
	pods := 0
	for _, ch := range status.Charts {
		pods += len(ch.Pods)
	}
	t.Logf("Status: %s, charts: %d, pods: %d", status.Status, len(status.Charts), pods)

	// Step 6–7: Set overrides and redeploy. Overrides are per chart config
	// of the definition.
	charts := def.Charts
	if len(charts) == 0 {
		full, err := c.GetDefinition(def.ID)
		require.NoError(t, err, "get definition")
		charts = full.Charts
	}
	if len(charts) == 0 {
		t.Log("Step 6–7: Skipped — the definition has no charts")
	} else {
		chartID := charts[0].ID

		t.Log("Step 6: Set overrides")
		_, err = c.SetValueOverride(instance.ID, chartID, &types.SetValueOverrideRequest{
			Values: "replicas: 2\n",
		})
		require.NoError(t, err, "set value override")

		branch := os.Getenv("STACKCTL_LIVE_BRANCH")
		if branch == "" {
			t.Log("Step 6 (branch override): Skipped — STACKCTL_LIVE_BRANCH not set")
		} else {
			_, err = c.SetBranchOverride(instance.ID, chartID, &types.SetBranchOverrideRequest{
				Branch: branch,
			})
			require.NoError(t, err, "set branch override")
		}

		// Verify overrides were persisted
		valOverrides, err := c.ListValueOverrides(instance.ID)
		require.NoError(t, err, "list value overrides")
		assert.NotEmpty(t, valOverrides, "should have at least one value override")

		if branch != "" {
			branchOverrides, err := c.ListBranchOverrides(instance.ID)
			require.NoError(t, err, "list branch overrides")
			assert.NotEmpty(t, branchOverrides, "should have at least one branch override")
		}

		// Step 7: Redeploy after overrides
		t.Log("Step 7: Redeploy")
		redeployLog, err := c.DeployStack(instance.ID)
		require.NoError(t, err, "redeploy stack")
		assert.NotEmpty(t, redeployLog.LogID, "redeploy log ID must be set")
		waitStackStatus(t, c, instance.ID, "running", liveOpTimeout)
	}

	// Step 8: View logs
	t.Log("Step 8: View logs")
	logEntry, err := c.GetStackLogs(instance.ID)
	require.NoError(t, err, "get stack logs")
	assert.NotEmpty(t, logEntry.Action, "log action must be present")

	// Step 9: Stop
	t.Log("Step 9: Stop")
	stopLog, err := c.StopStack(instance.ID)
	require.NoError(t, err, "stop stack")
	assert.NotEmpty(t, stopLog.LogID, "stop log ID must be set")
	waitStackStatus(t, c, instance.ID, "stopped", liveOpTimeout)

	// Step 10: Clean
	t.Log("Step 10: Clean")
	cleanLog, err := c.CleanStack(instance.ID)
	require.NoError(t, err, "clean stack")
	assert.NotEmpty(t, cleanLog.LogID, "clean log ID must be set")
	waitStackStatus(t, c, instance.ID, "draft", liveOpTimeout)

	// Step 11: Delete
	t.Log("Step 11: Delete")
	err = c.DeleteStack(instance.ID)
	require.NoError(t, err, "delete stack")

	// Step 12: Verify deleted — GetStack should return an error.
	t.Log("Step 12: Verify deleted")
	waitStackGone(t, c, instance.ID, liveOpTimeout)
}

// TestLiveWorkflow_BulkOperations deploys 3 real stack instances and bulk-
// operates on them. Heavy: ~3 cluster-CPU and ~5 Gi memory per instance,
// plus golden-db pulls. Wire-shape coverage for bulk lives in
// TestLiveBulk_StackInstanceWireShape / TestLiveBulk_TemplateRoundTrip;
// this test only adds the actual deploy/stop/clean assertions, so it's
// gated behind STACKCTL_LIVE_HEAVY=1. Each bulk operation is awaited on
// every instance before the next one starts.
func TestLiveWorkflow_BulkOperations(t *testing.T) {
	if os.Getenv("STACKCTL_LIVE_HEAVY") != "1" {
		t.Skip("set STACKCTL_LIVE_HEAVY=1 to run real-workload bulk tests (creates 3 instances)")
	}
	c := newLiveClient(t)

	// Step 1: Login
	t.Log("Step 1: Login")
	login(t, c)

	// Discover an available definition dynamically instead of assuming ID 1 exists.
	defs, err := c.ListDefinitions(nil)
	require.NoError(t, err, "list definitions")
	if len(defs.Data) == 0 {
		t.Skip("No definitions available on backend — cannot run bulk test")
	}
	defID := defs.Data[0].ID
	t.Logf("Using definition %s (%s)", defID, defs.Data[0].Name)

	// Step 2: Create 3 stack instances
	t.Log("Step 2: Create 3 stack instances")
	var ids []string
	for i := 0; i < 3; i++ {
		inst, err := c.CreateStack(&types.CreateStackRequest{
			Name:              fmt.Sprintf("live-bulk-%d-%d", time.Now().UnixMilli(), i),
			StackDefinitionID: defID,
			Branch:            "main",
		})
		require.NoError(t, err, "create stack %d", i)
		require.NotEmpty(t, inst.ID)
		ids = append(ids, inst.ID)
		t.Logf("Created instance %s", inst.ID)
	}

	// Register cleanup for all created instances.
	for _, id := range ids {
		cleanupInstance(t, c, id)
	}
	waitAll := func(status string) {
		t.Helper()
		for _, id := range ids {
			waitStackStatus(t, c, id, status, liveOpTimeout)
		}
	}

	// Step 3: Bulk deploy
	t.Log("Step 3: Bulk deploy")
	deployResp, err := c.BulkDeploy(ids)
	require.NoError(t, err, "bulk deploy")
	assert.Len(t, deployResp.Results, 3, "should have 3 results")
	for _, r := range deployResp.Results {
		assert.True(t, r.Success(), "deploy should succeed for id %s: %s", r.ID(), r.Error)
	}
	waitAll("running")

	// Step 4: Bulk stop
	t.Log("Step 4: Bulk stop")
	stopResp, err := c.BulkStop(ids)
	require.NoError(t, err, "bulk stop")
	assert.Len(t, stopResp.Results, 3)
	for _, r := range stopResp.Results {
		assert.True(t, r.Success(), "stop should succeed for id %s: %s", r.ID(), r.Error)
	}
	waitAll("stopped")

	// Step 5: Bulk clean
	t.Log("Step 5: Bulk clean")
	cleanResp, err := c.BulkClean(ids)
	require.NoError(t, err, "bulk clean")
	assert.Len(t, cleanResp.Results, 3)
	for _, r := range cleanResp.Results {
		assert.True(t, r.Success(), "clean should succeed for id %s: %s", r.ID(), r.Error)
	}
	waitAll("draft")

	// Step 6: Bulk delete
	t.Log("Step 6: Bulk delete")
	deleteResp, err := c.BulkDelete(ids)
	require.NoError(t, err, "bulk delete")
	assert.Len(t, deleteResp.Results, 3)
	for _, r := range deleteResp.Results {
		assert.True(t, r.Success(), "delete should succeed for id %s: %s", r.ID(), r.Error)
	}

	// Step 7: Verify all deleted
	t.Log("Step 7: Verify all deleted")
	for _, id := range ids {
		waitStackGone(t, c, id, liveOpTimeout)
	}
}
