package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// clusterMockState holds mutable state for the cluster mock server.
type clusterMockState struct {
	mu          sync.Mutex
	nextID      uint
	clusters    map[string]*types.Cluster
	unreachable map[string]bool // cluster IDs that POST /test should report as unreachable
}

func newClusterMockState() *clusterMockState {
	return &clusterMockState{
		nextID:      1,
		clusters:    make(map[string]*types.Cluster),
		unreachable: make(map[string]bool),
	}
}

func startClusterMockServer(t *testing.T, state *clusterMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// POST /api/v1/clusters — create
		case r.URL.Path == "/api/v1/clusters" && r.Method == http.MethodPost:
			var createReq types.CreateClusterRequest
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			cluster := types.Cluster{
				Name:        createReq.Name,
				Description: createReq.Description,
			}
			state.mu.Lock()
			cluster.ID = fmt.Sprintf("%d", state.nextID)
			state.nextID++
			cluster.Status = "active"
			cluster.CreatedAt = time.Now()
			cluster.UpdatedAt = time.Now()
			cluster.Version = "1"
			state.clusters[cluster.ID] = &cluster
			state.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(cluster)

		// GET /api/v1/clusters — list
		case r.URL.Path == "/api/v1/clusters" && r.Method == http.MethodGet:
			state.mu.Lock()
			data := make([]types.Cluster, 0, len(state.clusters))
			for _, cl := range state.clusters {
				data = append(data, *cl)
			}
			state.mu.Unlock()

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(data)

		default:
			// Parse /api/v1/clusters/<id>[/<action>]
			trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
			if trimmed == r.URL.Path {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}
			parts := strings.Split(trimmed, "/")
			var id, action string
			switch len(parts) {
			case 1:
				id = parts[0]
			case 2:
				id = parts[0]
				action = parts[1]
			case 3:
				// /api/v1/clusters/<id>/health/<sub> — handled below as a special case.
				id = parts[0]
			default:
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}
			if id == "" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
				return
			}

			state.mu.Lock()
			cl, exists := state.clusters[id]
			state.mu.Unlock()

			// Two-level paths: /api/v1/clusters/<id>/health/<sub> — handle
			// before the action switch since `action` stays empty for len==3.
			if len(parts) == 3 && parts[1] == "health" && r.Method == http.MethodGet {
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				switch parts[2] {
				case "summary":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(types.ClusterHealthSummary{
						NodeCount: 3, ReadyNodeCount: 3,
						TotalCPU: "8", TotalMemory: "16Gi",
						AllocatableCPU: "7", AllocatableMemory: "14Gi",
						NamespaceCount: 12,
					})
					return
				case "nodes":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode([]types.ClusterNodeStatus{
						{
							Name: "node-a", Status: "Ready", PodCount: 14,
							Capacity:    types.ClusterResourceQuantity{CPU: "4", Memory: "8Gi", Pods: "110"},
							Allocatable: types.ClusterResourceQuantity{CPU: "3800m", Memory: "7Gi"},
						},
					})
					return
				default:
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
					return
				}
			}

			switch {
			// GET /api/v1/clusters/:id
			case action == "" && r.Method == http.MethodGet:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				state.mu.Lock()
				copy := *cl
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(copy)

			// PUT /api/v1/clusters/:id
			case action == "" && r.Method == http.MethodPut:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				var req types.UpdateClusterRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
					return
				}
				state.mu.Lock()
				if req.Name != nil {
					cl.Name = *req.Name
				}
				if req.Description != nil {
					cl.Description = *req.Description
				}
				if req.IsDefault != nil {
					cl.IsDefault = *req.IsDefault
				}
				copy := *cl
				state.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(copy)

			// DELETE /api/v1/clusters/:id
			case action == "" && r.Method == http.MethodDelete:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				state.mu.Lock()
				delete(state.clusters, id)
				state.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)

			// POST /api/v1/clusters/:id/default
			case action == "default" && r.Method == http.MethodPost:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				state.mu.Lock()
				for _, other := range state.clusters {
					other.IsDefault = false
				}
				cl.IsDefault = true
				state.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)

			// POST /api/v1/clusters/:id/test
			case action == "test" && r.Method == http.MethodPost:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				state.mu.Lock()
				unreachable := state.unreachable[id]
				state.mu.Unlock()
				if unreachable {
					w.WriteHeader(http.StatusBadGateway)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Cluster is unreachable"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.ClusterTestConnectionResult{
					Status: "success", Message: "Connection successful", ServerVersion: "v1.29.4",
				})

			// GET /api/v1/clusters/:id/namespaces
			case action == "namespaces" && r.Method == http.MethodGet:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]types.ClusterNamespace{
					{Name: "stack-prod-web", Phase: "Active"},
					{Name: "stack-dev-api", Phase: "Active"},
				})

			// GET /api/v1/clusters/:id/utilization
			case action == "utilization" && r.Method == http.MethodGet:
				if !exists {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.ClusterUtilization{
					ClusterID: id,
					Namespaces: []types.NamespaceResourceUsage{
						{Namespace: "stack-prod-web", CPUUsed: "1500m", CPULimit: "4", MemoryUsed: "2Gi", MemoryLimit: "8Gi", PodCount: 10, PodLimit: 50},
					},
				})

			default:
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
			}
		}
	}))
}

// strPtr returns a pointer to the provided string value.
func strPtr(s string) *string { v := s; return &v }

func TestClusterWorkflow_CreateListSetDefaultDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Create
	created, err := c.CreateCluster(&types.CreateClusterRequest{Name: "prod-cluster"})
	require.NoError(t, err)
	assert.Equal(t, "prod-cluster", created.Name)
	assert.NotEmpty(t, created.ID)
	id := created.ID

	// 2. List — length 1, name matches
	clusters, err := c.ListClusters()
	require.NoError(t, err)
	assert.Len(t, clusters, 1)
	assert.Equal(t, "prod-cluster", clusters[0].Name)

	// 3. Get by ID
	got, err := c.GetCluster(id)
	require.NoError(t, err)
	assert.Equal(t, "prod-cluster", got.Name)

	// 4. Set default
	require.NoError(t, c.SetDefaultCluster(id))

	// 5. Get again — IsDefault == true
	got, err = c.GetCluster(id)
	require.NoError(t, err)
	assert.True(t, got.IsDefault)

	// 6. Delete
	require.NoError(t, c.DeleteCluster(id))

	// 7. List — empty
	clusters, err = c.ListClusters()
	require.NoError(t, err)
	assert.Len(t, clusters, 0)
}

func TestClusterWorkflow_UpdateMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Create
	created, err := c.CreateCluster(&types.CreateClusterRequest{Name: "dev-cluster"})
	require.NoError(t, err)
	id := created.ID

	// 2. Update name
	updated, err := c.UpdateCluster(id, &types.UpdateClusterRequest{Name: strPtr("dev-cluster-v2")})
	require.NoError(t, err)
	assert.Equal(t, "dev-cluster-v2", updated.Name)

	// 3. Get — verify persisted
	got, err := c.GetCluster(id)
	require.NoError(t, err)
	assert.Equal(t, "dev-cluster-v2", got.Name)
}

func TestClusterWorkflow_DeleteNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	err := c.DeleteCluster("9999")
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "not found")
}

func TestClusterWorkflow_MultipleSetDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Create cluster-A
	clusterA, err := c.CreateCluster(&types.CreateClusterRequest{Name: "cluster-a"})
	require.NoError(t, err)
	idA := clusterA.ID

	// Create cluster-B
	clusterB, err := c.CreateCluster(&types.CreateClusterRequest{Name: "cluster-b"})
	require.NoError(t, err)
	idB := clusterB.ID

	// Set A as default
	require.NoError(t, c.SetDefaultCluster(idA))
	gotA, err := c.GetCluster(idA)
	require.NoError(t, err)
	assert.True(t, gotA.IsDefault)

	// Set B as default — server unsets A
	require.NoError(t, c.SetDefaultCluster(idB))
	gotB, err := c.GetCluster(idB)
	require.NoError(t, err)
	assert.True(t, gotB.IsDefault)

	gotA, err = c.GetCluster(idA)
	require.NoError(t, err)
	assert.False(t, gotA.IsDefault)
}

// TestClusterCobra_CreateListSetDefaultDelete drives the actual Cobra commands
// in-process to validate flag parsing, output formatting, and confirmation
// behavior across the full create → list → set-default → delete lifecycle.
// NOT parallel: drives cmd package globals (rootCmd, printer).
func TestClusterCobra_CreateListSetDefaultDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// ── create ──────────────────────────────────────────────────────────────
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "create", "--name", "cobra-prod"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "cobra-prod")

	// ── list ─────────────────────────────────────────────────────────────────
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "cobra-prod")

	// determine the created cluster's ID from server state
	state.mu.Lock()
	var createdID string
	for id := range state.clusters {
		createdID = id
	}
	state.mu.Unlock()
	require.NotEmpty(t, createdID)

	// ── set-default ──────────────────────────────────────────────────────────
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "set-default", createdID})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "set as default")

	state.mu.Lock()
	isDefault := state.clusters[createdID].IsDefault
	state.mu.Unlock()
	assert.True(t, isDefault)

	// ── delete (--yes skips confirmation prompt) ─────────────────────────────
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "delete", createdID, "--yes"})
	require.NoError(t, cmd.Execute())

	state.mu.Lock()
	_, exists := state.clusters[createdID]
	state.mu.Unlock()
	assert.False(t, exists)
}

func TestClusterWorkflow_HealthSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	cluster, err := c.CreateCluster(&types.CreateClusterRequest{Name: "h-cluster"})
	require.NoError(t, err)
	id := cluster.ID

	// test-connection (success path)
	res, err := c.TestClusterConnection(id)
	require.NoError(t, err)
	assert.Equal(t, "success", res.Status)
	assert.Equal(t, "v1.29.4", res.ServerVersion)

	// health summary
	health, err := c.GetClusterHealth(id)
	require.NoError(t, err)
	assert.Equal(t, 3, health.NodeCount)
	assert.Equal(t, 3, health.ReadyNodeCount)
	assert.Equal(t, 12, health.NamespaceCount)

	// nodes
	nodes, err := c.GetClusterNodes(id)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "Ready", nodes[0].Status)

	// namespaces
	ns, err := c.GetClusterNamespaces(id)
	require.NoError(t, err)
	require.Len(t, ns, 2)

	// utilization
	util, err := c.GetClusterUtilization(id)
	require.NoError(t, err)
	require.Len(t, util.Namespaces, 1)
	assert.Equal(t, "stack-prod-web", util.Namespaces[0].Namespace)
}

func TestClusterWorkflow_TestConnectionUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	cluster, err := c.CreateCluster(&types.CreateClusterRequest{Name: "broken"})
	require.NoError(t, err)
	id := cluster.ID

	state.mu.Lock()
	state.unreachable[id] = true
	state.mu.Unlock()

	res, err := c.TestClusterConnection(id)
	require.Error(t, err)
	assert.Nil(t, res)
	// 502 maps through the client error mapper; we just need a non-nil error.
}

// Cobra in-process drive: cluster health <id> happy path against the mock.
// NOT parallel: mutates cmd package globals.
func TestClusterCobra_Health(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newClusterMockState()
	server := startClusterMockServer(t, state)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// Seed a cluster via Cobra create so we exercise the same path users hit.
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "create", "--name", "h-cobra"})
	require.NoError(t, cmd.Execute())

	state.mu.Lock()
	var id string
	for k := range state.clusters {
		id = k
	}
	state.mu.Unlock()
	require.NotEmpty(t, id)

	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"cluster", "health", id})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "Health Status")
	assert.Contains(t, out, "healthy")
	assert.Contains(t, out, "3/3")
}
