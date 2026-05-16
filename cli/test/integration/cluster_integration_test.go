package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clusterMockState holds mutable state for the cluster mock server.
type clusterMockState struct {
	mu       sync.Mutex
	nextID   uint
	clusters map[string]*types.Cluster
}

func newClusterMockState() *clusterMockState {
	return &clusterMockState{
		nextID:   1,
		clusters: make(map[string]*types.Cluster),
	}
}

func startClusterMockServer(t *testing.T, state *clusterMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// POST /api/v1/clusters — create
		case r.URL.Path == "/api/v1/clusters" && r.Method == http.MethodPost:
			var cluster types.Cluster
			if err := json.NewDecoder(r.Body).Decode(&cluster); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
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
