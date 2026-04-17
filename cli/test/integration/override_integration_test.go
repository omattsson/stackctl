package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overrideMockState holds mutable state for the override mock server.
type overrideMockState struct {
	mu              sync.Mutex
	valueOverrides  map[string]*types.ValueOverride  // key: "instanceID:chartID"
	branchOverrides map[string]*types.BranchOverride // key: "instanceID:chartID"
	quotaOverrides  map[string]*types.QuotaOverride  // key: instanceID
	mergedValues    map[string]*types.MergedValues   // key: instanceID
}

func newOverrideMockState() *overrideMockState {
	return &overrideMockState{
		valueOverrides:  make(map[string]*types.ValueOverride),
		branchOverrides: make(map[string]*types.BranchOverride),
		quotaOverrides:  make(map[string]*types.QuotaOverride),
		mergedValues: map[string]*types.MergedValues{
			"42": {
				InstanceID: "42",
				Charts: map[string]map[string]interface{}{
					"api":      {"replicas": float64(2), "port": float64(8080)},
					"frontend": {"replicas": float64(1)},
				},
			},
		},
	}
}

// parsePathSegments splits a URL path and attempts to match the
// /api/v1/stack-instances/:id/<suffix>[/<chartID>] pattern.
// Returns (instanceID, chartID, suffix, ok). chartID is empty if absent.
func parsePathSegments(path string) (instanceID, chartID, suffix string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/api/v1/stack-instances/")
	if trimmed == path {
		return "", "", "", false
	}
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 2:
		return parts[0], "", parts[1], true
	case 3:
		return parts[0], parts[2], parts[1], true
	}
	return "", "", "", false
}

func startOverrideMockServer(t *testing.T, state *overrideMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		instanceID, chartID, suffix, ok := parsePathSegments(r.URL.Path)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
			return
		}

		// --- Value Override routes ---

		// List value overrides: GET /api/v1/stack-instances/:id/overrides
		if suffix == "overrides" && chartID == "" {
			if r.Method == http.MethodGet {
				state.mu.Lock()
				var overrides []types.ValueOverride
				for k, v := range state.valueOverrides {
					iID := strings.SplitN(k, ":", 2)[0]
					if iID == instanceID {
						overrides = append(overrides, *v)
					}
				}
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(overrides)
				return
			}
		}

		// Value override by chart: GET/PUT/DELETE /api/v1/stack-instances/:id/overrides/:chartID
		if suffix == "overrides" && chartID != "" {
			key := fmt.Sprintf("%s:%s", instanceID, chartID)

			switch r.Method {
			case http.MethodGet:
				state.mu.Lock()
				v, exists := state.valueOverrides[key]
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "override not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(v)
				return

			case http.MethodPut:
				var req types.SetValueOverrideRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				valBytes, err := json.Marshal(req.Values)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid values: " + err.Error()})
					return
				}
				state.mu.Lock()
				vo := &types.ValueOverride{
					Base:       types.Base{ID: chartID, Version: "1"},
					InstanceID: instanceID,
					ChartID:    chartID,
					Values:     string(valBytes),
				}
				state.valueOverrides[key] = vo
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(vo)
				return

			case http.MethodDelete:
				state.mu.Lock()
				_, exists := state.valueOverrides[key]
				if exists {
					delete(state.valueOverrides, key)
				}
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "override not found"})
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// --- Branch Override routes ---

		// List branch overrides: GET /api/v1/stack-instances/:id/branches
		if suffix == "branches" && chartID == "" {
			if r.Method == http.MethodGet {
				state.mu.Lock()
				var overrides []types.BranchOverride
				for k, v := range state.branchOverrides {
					iID := strings.SplitN(k, ":", 2)[0]
					if iID == instanceID {
						overrides = append(overrides, *v)
					}
				}
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(overrides)
				return
			}
		}

		// Branch override by chart: GET/PUT/DELETE /api/v1/stack-instances/:id/branches/:chartID
		if suffix == "branches" && chartID != "" {
			key := fmt.Sprintf("%s:%s", instanceID, chartID)

			switch r.Method {
			case http.MethodGet:
				state.mu.Lock()
				v, exists := state.branchOverrides[key]
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "branch override not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(v)
				return

			case http.MethodPut:
				var req types.SetBranchOverrideRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				state.mu.Lock()
				bo := &types.BranchOverride{
					Base:       types.Base{ID: chartID, Version: "1"},
					InstanceID: instanceID,
					ChartID:    chartID,
					Branch:     req.Branch,
				}
				state.branchOverrides[key] = bo
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(bo)
				return

			case http.MethodDelete:
				state.mu.Lock()
				_, exists := state.branchOverrides[key]
				if exists {
					delete(state.branchOverrides, key)
				}
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "branch override not found"})
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// --- Quota Override routes ---

		// GET/PUT/DELETE /api/v1/stack-instances/:id/quota-overrides
		if suffix == "quota-overrides" && chartID == "" {
			switch r.Method {
			case http.MethodGet:
				state.mu.Lock()
				q, exists := state.quotaOverrides[instanceID]
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(q)
				return

			case http.MethodPut:
				var req types.SetQuotaOverrideRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				state.mu.Lock()
				q := &types.QuotaOverride{
					InstanceID: instanceID,
					CPURequest: req.CPURequest,
					CPULimit:   req.CPULimit,
					MemRequest: req.MemRequest,
					MemLimit:   req.MemLimit,
				}
				state.quotaOverrides[instanceID] = q
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(q)
				return

			case http.MethodDelete:
				state.mu.Lock()
				_, exists := state.quotaOverrides[instanceID]
				if exists {
					delete(state.quotaOverrides, instanceID)
				}
				state.mu.Unlock()
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// --- Merged Values ---
		if suffix == "values" && chartID == "" && r.Method == http.MethodGet {
			state.mu.Lock()
			v, exists := state.mergedValues[instanceID]
			state.mu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(v)
			return
		}

		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
	}))
}

// ---------- Value Override CRUD lifecycle ----------

func TestValueOverrideWorkflow_CRUDLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newOverrideMockState()
	server := startOverrideMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. List — should be empty
	overrides, err := c.ListValueOverrides("42")
	require.NoError(t, err)
	assert.Empty(t, overrides)

	// 2. Set a value override
	vo, err := c.SetValueOverride("42", "1", &types.SetValueOverrideRequest{
		Values: map[string]interface{}{"replicas": float64(5)},
	})
	require.NoError(t, err)
	assert.Equal(t, "1", vo.ChartID)
	assert.Equal(t, "42", vo.InstanceID)
	assert.Contains(t, vo.Values, "replicas")

	// 3. Get — should find it
	got, err := c.GetValueOverride("42", "1")
	require.NoError(t, err)
	assert.Equal(t, "1", got.ChartID)
	assert.Contains(t, got.Values, "replicas")

	// 4. List — should be non-empty
	overrides, err = c.ListValueOverrides("42")
	require.NoError(t, err)
	assert.Len(t, overrides, 1)

	// 5. Set another override on different chart
	_, err = c.SetValueOverride("42", "2", &types.SetValueOverrideRequest{
		Values: map[string]interface{}{"debug": true},
	})
	require.NoError(t, err)

	overrides, err = c.ListValueOverrides("42")
	require.NoError(t, err)
	assert.Len(t, overrides, 2)

	// 6. Delete first override
	err = c.DeleteValueOverride("42", "1")
	require.NoError(t, err)

	// 7. Verify it's gone
	_, err = c.GetValueOverride("42", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override not found")

	// 8. List — should have 1 left
	overrides, err = c.ListValueOverrides("42")
	require.NoError(t, err)
	assert.Len(t, overrides, 1)

	// 9. Delete second override
	err = c.DeleteValueOverride("42", "2")
	require.NoError(t, err)

	// 10. List — should be empty again
	overrides, err = c.ListValueOverrides("42")
	require.NoError(t, err)
	assert.Empty(t, overrides)
}

// ---------- Branch Override CRUD lifecycle ----------

func TestBranchOverrideWorkflow_CRUDLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newOverrideMockState()
	server := startOverrideMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. List — empty
	overrides, err := c.ListBranchOverrides("42")
	require.NoError(t, err)
	assert.Empty(t, overrides)

	// 2. Set branch override
	bo, err := c.SetBranchOverride("42", "1", &types.SetBranchOverrideRequest{Branch: "feature/my-branch"})
	require.NoError(t, err)
	assert.Equal(t, "feature/my-branch", bo.Branch)
	assert.Equal(t, "42", bo.InstanceID)
	assert.Equal(t, "1", bo.ChartID)

	// 3. Get — should find it
	got, err := c.GetBranchOverride("42", "1")
	require.NoError(t, err)
	assert.Equal(t, "feature/my-branch", got.Branch)

	// 4. List — non-empty
	overrides, err = c.ListBranchOverrides("42")
	require.NoError(t, err)
	assert.Len(t, overrides, 1)

	// 5. Update branch
	updated, err := c.SetBranchOverride("42", "1", &types.SetBranchOverrideRequest{Branch: "main"})
	require.NoError(t, err)
	assert.Equal(t, "main", updated.Branch)

	// 6. Verify update persists
	got, err = c.GetBranchOverride("42", "1")
	require.NoError(t, err)
	assert.Equal(t, "main", got.Branch)

	// 7. Delete
	err = c.DeleteBranchOverride("42", "1")
	require.NoError(t, err)

	// 8. Verify gone
	_, err = c.GetBranchOverride("42", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch override not found")

	// 9. List — empty
	overrides, err = c.ListBranchOverrides("42")
	require.NoError(t, err)
	assert.Empty(t, overrides)
}

// ---------- Quota Override CRUD lifecycle ----------

func TestQuotaOverrideWorkflow_CRUDLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newOverrideMockState()
	server := startOverrideMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Get — not found
	_, err := c.GetQuotaOverride("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota not found")

	// 2. Set quota override
	q, err := c.SetQuotaOverride("42", &types.SetQuotaOverrideRequest{
		CPURequest: "100m",
		CPULimit:   "500m",
		MemRequest: "128Mi",
		MemLimit:   "512Mi",
	})
	require.NoError(t, err)
	assert.Equal(t, "42", q.InstanceID)
	assert.Equal(t, "100m", q.CPURequest)
	assert.Equal(t, "512Mi", q.MemLimit)

	// 3. Get — should find it
	got, err := c.GetQuotaOverride("42")
	require.NoError(t, err)
	assert.Equal(t, "100m", got.CPURequest)
	assert.Equal(t, "500m", got.CPULimit)
	assert.Equal(t, "128Mi", got.MemRequest)
	assert.Equal(t, "512Mi", got.MemLimit)

	// 4. Update
	updated, err := c.SetQuotaOverride("42", &types.SetQuotaOverrideRequest{
		CPURequest: "200m",
		MemLimit:   "1Gi",
	})
	require.NoError(t, err)
	assert.Equal(t, "200m", updated.CPURequest)
	assert.Equal(t, "1Gi", updated.MemLimit)

	// 5. Delete
	err = c.DeleteQuotaOverride("42")
	require.NoError(t, err)

	// 6. Verify gone
	_, err = c.GetQuotaOverride("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota not found")

	// 7. Delete again — should 404
	err = c.DeleteQuotaOverride("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota not found")
}

// ---------- Merged Values retrieval ----------

func TestMergedValuesWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newOverrideMockState()
	server := startOverrideMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Get merged values for existing instance
	values, err := c.GetMergedValues("42", "")
	require.NoError(t, err)
	assert.Equal(t, "42", values.InstanceID)
	assert.Contains(t, values.Charts, "api")
	assert.Contains(t, values.Charts, "frontend")

	// 2. Get merged values for non-existent instance
	_, err = c.GetMergedValues("999", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ---------- Error handling across overrides ----------

func TestOverrideWorkflow_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newOverrideMockState()
	server := startOverrideMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Get non-existent value override
	_, err := c.GetValueOverride("42", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override not found")

	// Delete non-existent value override
	err = c.DeleteValueOverride("42", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override not found")

	// Get non-existent branch override
	_, err = c.GetBranchOverride("42", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch override not found")

	// Delete non-existent branch override
	err = c.DeleteBranchOverride("42", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch override not found")
}
