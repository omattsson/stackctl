package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stackMockState holds mutable state for the mock server.
type stackMockState struct {
	mu        sync.Mutex
	nextID    uint
	instances map[uint]*types.StackInstance
}

func newStackMockState() *stackMockState {
	return &stackMockState{
		nextID:    1,
		instances: make(map[uint]*types.StackInstance),
	}
}

func startStackMockServer(t *testing.T, state *stackMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// Create stack
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodPost:
			var instance types.StackInstance
			if err := json.NewDecoder(r.Body).Decode(&instance); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			state.mu.Lock()
			instance.ID = state.nextID
			state.nextID++
			instance.Status = "draft"
			state.instances[instance.ID] = &instance
			state.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(instance)

		// List stacks
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodGet:
			state.mu.Lock()
			var data []types.StackInstance
			status := r.URL.Query().Get("status")
			owner := r.URL.Query().Get("owner")
			for _, inst := range state.instances {
				if status != "" && inst.Status != status {
					continue
				}
				if owner != "" && owner != "me" && inst.Owner != owner {
					continue
				}
				data = append(data, *inst)
			}
			state.mu.Unlock()

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
				Data:       data,
				Total:      len(data),
				Page:       1,
				PageSize:   20,
				TotalPages: 1,
			})

		// Match specific instance routes
		default:
			// Parse /api/v1/stack-instances/<id>[/<action>]
			var id uint
			var action string
			n, _ := fmt.Sscanf(r.URL.Path, "/api/v1/stack-instances/%d/%s", &id, &action)
			if n == 0 {
				// Try without action
				n, _ = fmt.Sscanf(r.URL.Path, "/api/v1/stack-instances/%d", &id)
			}
			if n == 0 {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}

			state.mu.Lock()
			inst, exists := state.instances[id]
			state.mu.Unlock()

			if !exists && action != "" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
				return
			}

			switch {
			// GET instance
			case action == "" && r.Method == http.MethodGet:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(inst)

			// DELETE instance
			case action == "" && r.Method == http.MethodDelete:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
					return
				}
				state.mu.Lock()
				delete(state.instances, id)
				state.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)

			// Deploy
			case action == "deploy" && r.Method == http.MethodPost:
				state.mu.Lock()
				inst.Status = "deploying"
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.DeploymentLog{
					Base:       types.Base{ID: 100 + id},
					InstanceID: id,
					Action:     "deploy",
					Status:     "started",
				})

			// Stop
			case action == "stop" && r.Method == http.MethodPost:
				state.mu.Lock()
				inst.Status = "stopped"
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.DeploymentLog{
					Base:       types.Base{ID: 200 + id},
					InstanceID: id,
					Action:     "stop",
					Status:     "started",
				})

			// Clean
			case action == "clean" && r.Method == http.MethodPost:
				state.mu.Lock()
				inst.Status = "cleaned"
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.DeploymentLog{
					Base:       types.Base{ID: 300 + id},
					InstanceID: id,
					Action:     "clean",
					Status:     "started",
				})

			// Status
			case action == "status" && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.InstanceStatus{
					Status: inst.Status,
					Pods: []types.PodStatus{
						{Name: inst.Name + "-pod-1", Status: "Running", Ready: true, Restarts: 0, Age: "5m"},
					},
				})

			// Logs
			case action == "deploy-log" && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.DeploymentLog{
					Base:       types.Base{ID: 400 + id},
					InstanceID: id,
					Action:     "deploy",
					Status:     "completed",
					Output:     "Deployment completed successfully.",
				})

			// Clone
			case action == "clone" && r.Method == http.MethodPost:
				state.mu.Lock()
				newInst := *inst
				newInst.ID = state.nextID
				state.nextID++
				newInst.Name = inst.Name + "-clone"
				newInst.Status = "draft"
				state.instances[newInst.ID] = &newInst
				state.mu.Unlock()

				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(newInst)

			// Extend
			case action == "extend" && r.Method == http.MethodPost:
				var body map[string]int
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				state.mu.Lock()
				inst.TTLMinutes += body["ttl_minutes"]
				state.mu.Unlock()

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(inst)

			default:
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
			}
		}
	}))
}

func TestStackWorkflow_CreateDeployStatusLogsStopCleanDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newStackMockState()
	server := startStackMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Create
	created, err := c.CreateStack(&types.StackInstance{
		Name:              "lifecycle-stack",
		StackDefinitionID: 1,
		Branch:            "main",
	})
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-stack", created.Name)
	assert.Equal(t, "draft", created.Status)
	id := created.ID

	// 2. Get — verify it exists
	got, err := c.GetStack(id)
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-stack", got.Name)

	// 3. Deploy
	deployLog, err := c.DeployStack(id)
	require.NoError(t, err)
	assert.Equal(t, "deploy", deployLog.Action)
	assert.Equal(t, id, deployLog.InstanceID)

	// 4. Status — should be deploying
	status, err := c.GetStackStatus(id)
	require.NoError(t, err)
	assert.Equal(t, "deploying", status.Status)
	assert.Len(t, status.Pods, 1)

	// 5. Logs
	log, err := c.GetStackLogs(id)
	require.NoError(t, err)
	assert.Equal(t, "deploy", log.Action)
	assert.Contains(t, log.Output, "completed successfully")

	// 6. Stop
	stopLog, err := c.StopStack(id)
	require.NoError(t, err)
	assert.Equal(t, "stop", stopLog.Action)

	// 7. Clean
	cleanLog, err := c.CleanStack(id)
	require.NoError(t, err)
	assert.Equal(t, "clean", cleanLog.Action)

	// 8. Delete
	err = c.DeleteStack(id)
	require.NoError(t, err)

	// 9. Verify gone
	_, err = c.GetStack(id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

func TestStackWorkflow_ListWithFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newStackMockState()
	server := startStackMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Create a few stacks
	s1, err := c.CreateStack(&types.StackInstance{Name: "stack-a", Owner: "alice"})
	require.NoError(t, err)
	s2, err := c.CreateStack(&types.StackInstance{Name: "stack-b", Owner: "bob"})
	require.NoError(t, err)

	// Deploy s1 so its status changes
	_, err = c.DeployStack(s1.ID)
	require.NoError(t, err)

	// List all — should return both
	resp, err := c.ListStacks(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)

	// List only deploying — should return s1
	resp, err = c.ListStacks(map[string]string{"status": "deploying"})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, s1.ID, resp.Data[0].ID)

	// List only draft — should return s2
	resp, err = c.ListStacks(map[string]string{"status": "draft"})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, s2.ID, resp.Data[0].ID)
}

func TestStackWorkflow_CloneAndExtend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newStackMockState()
	server := startStackMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Create original
	original, err := c.CreateStack(&types.StackInstance{Name: "original-stack", TTLMinutes: 60})
	require.NoError(t, err)

	// Clone it
	clone, err := c.CloneStack(original.ID)
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, clone.ID)
	assert.Equal(t, "original-stack-clone", clone.Name)
	assert.Equal(t, "draft", clone.Status)

	// Extend the clone's TTL
	extended, err := c.ExtendStack(clone.ID, 30)
	require.NoError(t, err)
	assert.Equal(t, 90, extended.TTLMinutes) // 60 (inherited) + 30

	// Both should be listed
	resp, err := c.ListStacks(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
}

func TestStackWorkflow_DestructiveOpsOnMissingInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newStackMockState()
	server := startStackMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Delete a non-existent instance
	err := c.DeleteStack(999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")

	// Deploy a non-existent instance
	_, err = c.DeployStack(999)
	require.Error(t, err)

	// Get non-existent
	_, err = c.GetStack(999)
	require.Error(t, err)
}

func TestStackWorkflow_ErrorStatusCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Separate server that returns specific error codes
	tests := []struct {
		name       string
		statusCode int
		wantMsg    string
	}{
		{"unauthorized", http.StatusUnauthorized, "Not authenticated. Run 'stackctl login' first."},
		{"forbidden", http.StatusForbidden, "Permission denied."},
		{"conflict", http.StatusConflict, "version mismatch"},
		{"rate_limited", http.StatusTooManyRequests, "Rate limited. Try again later."},
		{"server_error", http.StatusInternalServerError, "Server error. Check backend logs."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: tt.wantMsg})
			}))
			defer server.Close()

			c := client.New(server.URL)
			_, err := c.GetStack(1)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			apiErr, ok := err.(*client.APIError)
			require.True(t, ok)
			assert.Equal(t, tt.statusCode, apiErr.StatusCode)
		})
	}
}

func TestStackWorkflow_PaginationParams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "2", r.URL.Query().Get("page"))
		assert.Equal(t, "10", r.URL.Query().Get("page_size"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:       nil,
			Total:      25,
			Page:       2,
			PageSize:   10,
			TotalPages: 3,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	resp, err := c.ListStacks(map[string]string{
		"page":      strconv.Itoa(2),
		"page_size": strconv.Itoa(10),
	})
	require.NoError(t, err)
	assert.Equal(t, 25, resp.Total)
	assert.Equal(t, 2, resp.Page)
	assert.Equal(t, 3, resp.TotalPages)
}
