package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupClusterTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	return setupStackTestCmd(t, apiURL)
}

func sampleCluster() types.Cluster {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.Cluster{
		Base:        types.Base{ID: "1", CreatedAt: now, UpdatedAt: now, Version: "1"},
		Name:        "dev-cluster",
		Description: "Development cluster",
		Status:      "online",
		IsDefault:   true,
		NodeCount:   3,
	}
}

func sampleClusterHealth() types.ClusterHealthSummary {
	return types.ClusterHealthSummary{
		Status:    "healthy",
		NodeCount: 3,
		CPUUsage:  "2.5",
		MemUsage:  "4Gi",
		CPUTotal:  "8",
		MemTotal:  "16Gi",
	}
}

// ---------- cluster list ----------

func TestClusterListCmd_TableOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/clusters", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.Cluster{cl})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "DEFAULT")
	assert.Contains(t, out, "NODES")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "dev-cluster")
	assert.Contains(t, out, "online")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "3")
}

func TestClusterListCmd_JSONOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.Cluster{cl})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.NoError(t, err)

	var result []types.Cluster
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 1)
	assert.Equal(t, "dev-cluster", result[0].Name)
}

func TestClusterListCmd_QuietOutput(t *testing.T) {
	c1 := sampleCluster()
	c2 := sampleCluster()
	c2.ID = "2"
	c2.Name = "prod-cluster"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.Cluster{c1, c2})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.NoError(t, err)

	assert.Equal(t, "1\n2\n", buf.String())
}

func TestClusterListCmd_YAMLOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.Cluster{cl})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: dev-cluster")
	assert.Contains(t, out, "status: online")
}

func TestClusterListCmd_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.Cluster{})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
}

func TestClusterListCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unauthorized"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	err := clusterListCmd.RunE(clusterListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

// ---------- cluster get ----------

func TestClusterGetCmd_TableOutput(t *testing.T) {
	cl := sampleCluster()
	health := sampleClusterHealth()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			json.NewEncoder(w).Encode(health)
		} else {
			require.Equal(t, "/api/v1/clusters/1", r.URL.Path)
			json.NewEncoder(w).Encode(cl)
		}
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "dev-cluster")
	assert.Contains(t, out, "Development cluster")
	assert.Contains(t, out, "online")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "healthy")
	assert.Contains(t, out, "2.5")
	assert.Contains(t, out, "16Gi")
}

func TestClusterGetCmd_HealthUnavailable(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster unreachable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "dev-cluster")
	assert.Contains(t, out, "unavailable")
}

func TestClusterGetCmd_JSONOutput(t *testing.T) {
	cl := sampleCluster()
	health := sampleClusterHealth()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			json.NewEncoder(w).Encode(health)
		} else {
			json.NewEncoder(w).Encode(cl)
		}
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Contains(t, string(result["cluster"]), "dev-cluster")
	assert.Contains(t, string(result["health"]), "healthy")
}

func TestClusterGetCmd_QuietOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			json.NewEncoder(w).Encode(sampleClusterHealth())
		} else {
			json.NewEncoder(w).Encode(cl)
		}
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	assert.Equal(t, "1\n", buf.String())
}

func TestClusterGetCmd_YAMLOutput(t *testing.T) {
	cl := sampleCluster()
	health := sampleClusterHealth()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			json.NewEncoder(w).Encode(health)
		} else {
			json.NewEncoder(w).Encode(cl)
		}
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: dev-cluster")
	assert.Contains(t, out, "status: healthy")
}

func TestClusterGetCmd_JSONOutput_HealthUnavailable(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters/1/health/summary" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster unreachable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.NoError(t, err)

	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Contains(t, string(result["cluster"]), "dev-cluster")
	_, hasHealth := result["health"]
	assert.False(t, hasHealth, "health key should not be present when unavailable")
}

func TestClusterGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found")
}

func TestClusterGetCmd_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	err := clusterGetCmd.RunE(clusterGetCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
}
