package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupPolicyMockState is the in-memory backend used by the cleanup-policy
// integration tests. It implements LIST/CREATE/PUT/DELETE on
// /api/v1/admin/cleanup-policies (the GET-by-ID endpoint does not exist
// server-side and is resolved client-side via list+filter).
type cleanupPolicyMockState struct {
	mu       sync.Mutex
	nextID   uint
	policies map[string]*types.CleanupPolicy
}

func newCleanupPolicyMockState() *cleanupPolicyMockState {
	return &cleanupPolicyMockState{
		nextID:   1,
		policies: make(map[string]*types.CleanupPolicy),
	}
}

func startCleanupPolicyMockServer(t *testing.T, state *cleanupPolicyMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/admin/cleanup-policies" && r.Method == http.MethodGet:
			state.mu.Lock()
			out := make([]types.CleanupPolicy, 0, len(state.policies))
			for _, p := range state.policies {
				out = append(out, *p)
			}
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)

		case r.URL.Path == "/api/v1/admin/cleanup-policies" && r.Method == http.MethodPost:
			var req types.CreateCleanupPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			state.mu.Lock()
			id := fmt.Sprintf("%d", state.nextID)
			state.nextID++
			policy := &types.CleanupPolicy{
				Base:      types.Base{ID: id, CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Name:      req.Name,
				ClusterID: req.ClusterID,
				Action:    req.Action,
				Condition: req.Condition,
				Schedule:  req.Schedule,
				Enabled:   req.Enabled,
				DryRun:    req.DryRun,
			}
			state.policies[id] = policy
			state.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(policy)

		default:
			trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/cleanup-policies/")
			if trimmed == r.URL.Path || trimmed == "" {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}
			id := trimmed
			action := ""
			if i := strings.Index(id, "/"); i >= 0 {
				id, action = id[:i], id[i+1:]
			}
			state.mu.Lock()
			existing, exists := state.policies[id]
			state.mu.Unlock()

			// POST /api/v1/admin/cleanup-policies/:id/run — execute the policy.
			if action == "run" && r.Method == http.MethodPost {
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cleanup policy not found"})
					return
				}
				dryRun := r.URL.Query().Get("dry_run") == "true"
				status := "success"
				if dryRun {
					status = "dry_run"
				}
				results := []types.CleanupResult{
					{InstanceID: "i-" + id + "-1", InstanceName: "app-1", Namespace: "stack-app-1", OwnerID: "alice", Action: existing.Action, Status: status},
				}
				state.mu.Lock()
				existing.LastRunAt = func() *time.Time { t := time.Now(); return &t }()
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(results)
				return
			}

			switch r.Method {
			case http.MethodPut:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cleanup policy not found"})
					return
				}
				var req types.UpdateCleanupPolicyRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				state.mu.Lock()
				existing.Name = req.Name
				existing.ClusterID = req.ClusterID
				existing.Action = req.Action
				existing.Condition = req.Condition
				existing.Schedule = req.Schedule
				existing.Enabled = req.Enabled
				existing.DryRun = req.DryRun
				existing.UpdatedAt = time.Now()
				out := *existing
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(out)

			case http.MethodDelete:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cleanup policy not found"})
					return
				}
				state.mu.Lock()
				delete(state.policies, id)
				state.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)

			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "method not allowed"})
			}
		}
	}))
}

func TestCleanupPolicyWorkflow_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newCleanupPolicyMockState()
	server := startCleanupPolicyMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// create
	created, err := c.CreateCleanupPolicy(&types.CreateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:7", Schedule: "0 2 * * *", Enabled: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	// list returns the just-created policy
	policies, err := c.ListCleanupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "nightly-stop", policies[0].Name)

	// update — full upsert
	updated, err := c.UpdateCleanupPolicy(created.ID, &types.UpdateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 4 * * *", Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "idle_days:14", updated.Condition)
	assert.Equal(t, "0 4 * * *", updated.Schedule)

	// delete
	require.NoError(t, c.DeleteCleanupPolicy(created.ID))

	policies, err = c.ListCleanupPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestCleanupPolicyWorkflow_DeleteNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newCleanupPolicyMockState()
	server := startCleanupPolicyMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)
	err := c.DeleteCleanupPolicy("missing")
	require.Error(t, err)
	var apiErr *client.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
}

func TestCleanupPolicyCobra_CRUDViaFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newCleanupPolicyMockState()
	server := startCleanupPolicyMockServer(t, state)
	defer server.Close()

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)
	t.Setenv("STACKCTL_API_URL", server.URL)

	// Write a create payload to disk.
	createPath := filepath.Join(dir, "policy.json")
	createPayload := types.CreateCleanupPolicyRequest{
		Name: "ttl-cleanup", ClusterID: "all", Action: "delete",
		Condition: "ttl_expired", Schedule: "*/30 * * * *", Enabled: true,
	}
	data, err := json.Marshal(createPayload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(createPath, data, 0o600))

	var buf bytes.Buffer

	// create
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "create", "--from-file", createPath})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "ttl-cleanup")

	// list
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "ttl-cleanup")

	// get by name
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "get", "ttl-cleanup"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "ttl_expired")

	// determine ID from server state
	state.mu.Lock()
	var createdID string
	for id := range state.policies {
		createdID = id
	}
	state.mu.Unlock()
	require.NotEmpty(t, createdID)

	// update
	updatePayload := types.UpdateCleanupPolicyRequest{
		Name: "ttl-cleanup", ClusterID: "all", Action: "delete",
		Condition: "ttl_expired", Schedule: "0 1 * * *", Enabled: false,
	}
	updatePath := filepath.Join(dir, "update.json")
	data, err = json.Marshal(updatePayload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(updatePath, data, 0o600))

	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "update", createdID, "--from-file", updatePath})
	require.NoError(t, cmd.Execute())

	state.mu.Lock()
	got := *state.policies[createdID]
	state.mu.Unlock()
	assert.Equal(t, "0 1 * * *", got.Schedule)
	assert.False(t, got.Enabled)

	// delete
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "delete", createdID, "--yes"})
	require.NoError(t, cmd.Execute())

	state.mu.Lock()
	_, exists := state.policies[createdID]
	state.mu.Unlock()
	assert.False(t, exists)
}

func TestCleanupPolicyWorkflow_RunDryRunThenReal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newCleanupPolicyMockState()
	server := startCleanupPolicyMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	created, err := c.CreateCleanupPolicy(&types.CreateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:7", Schedule: "0 2 * * *", Enabled: true,
	})
	require.NoError(t, err)

	// dry-run first
	results, err := c.RunCleanupPolicy(created.ID, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "dry_run", results[0].Status)
	assert.Equal(t, "stop", results[0].Action)

	// real run
	results, err = c.RunCleanupPolicy(created.ID, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "success", results[0].Status)
}

func TestCleanupPolicyCobra_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newCleanupPolicyMockState()
	server := startCleanupPolicyMockServer(t, state)
	defer server.Close()

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)
	t.Setenv("STACKCTL_API_URL", server.URL)

	// Seed a policy via the client so we have an ID to run.
	c := client.New(server.URL)
	created, err := c.CreateCleanupPolicy(&types.CreateCleanupPolicyRequest{
		Name: "p1", ClusterID: "all", Action: "stop",
		Condition: "idle_days:7", Schedule: "0 2 * * *", Enabled: true,
	})
	require.NoError(t, err)

	var buf bytes.Buffer

	// Real run first — the --dry-run flag is sticky across cmd.Execute() calls
	// when reused in this in-process test loop, so do the explicit-false case
	// before the explicit-true case to avoid bleed-over.
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "run", created.ID, "--dry-run=false"})
	require.NoError(t, cmd.Execute())
	realOut := buf.String()
	assert.Contains(t, realOut, "success", "result row must show the success status")
	assert.Contains(t, realOut, "1 success", "summary must count the success result")

	// Then --dry-run
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cleanup-policy", "run", created.ID, "--dry-run"})
	require.NoError(t, cmd.Execute())
	dryOut := buf.String()
	assert.Contains(t, dryOut, "dry_run", "result row must show the dry_run status")
	assert.Contains(t, dryOut, "1 dry-run", "summary must count the dry-run result")
}
