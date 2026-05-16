package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/pflag"
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

// ---------- shared values helpers ----------

func sampleSharedValues() types.SharedValues {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.SharedValues{
		Base:      types.Base{ID: "5", CreatedAt: now, UpdatedAt: now, Version: "1"},
		ClusterID: "1",
		Name:      "local-dev-defaults",
		Values:    "persistence:\n  storageClass: local-path\n",
		Priority:  10,
	}
}

func resetSharedValuesSetFlags(t *testing.T) {
	t.Helper()
	clusterSharedValuesSetCmd.Flags().Set("name", "")
	clusterSharedValuesSetCmd.Flags().Set("file", "")
	clusterSharedValuesSetCmd.Flags().Set("priority", "0")
	if f := clusterSharedValuesSetCmd.Flags().Lookup("set"); f != nil {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			sv.Replace([]string{})
		}
		f.Changed = false
	}
}

// ---------- shared-values list ----------

func TestClusterSharedValuesListCmd_TableOutput(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/clusters/1/shared-values", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.SharedValues{sv})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "PRIORITY")
	assert.Contains(t, out, "local-dev-defaults")
	assert.Contains(t, out, "10")
}

func TestClusterSharedValuesListCmd_JSONOutput(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.SharedValues{sv})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"1"})
	require.NoError(t, err)

	var result []types.SharedValues
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "local-dev-defaults", result[0].Name)
}

func TestClusterSharedValuesListCmd_QuietOutput(t *testing.T) {
	sv1 := sampleSharedValues()
	sv2 := sampleSharedValues()
	sv2.ID = "6"
	sv2.Name = "acr-pull-secrets"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.SharedValues{sv1, sv2})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"1"})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "5\n6", lines)
}

func TestClusterSharedValuesListCmd_YAMLOutput(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.SharedValues{sv})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: local-dev-defaults")
	assert.Contains(t, out, "cluster_id: \"1\"")
}

func TestClusterSharedValuesListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.SharedValues{})
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No shared values found")
}

func TestClusterSharedValuesListCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	err := clusterSharedValuesListCmd.RunE(clusterSharedValuesListCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found")
}

// ---------- shared-values set ----------

func TestClusterSharedValuesSetCmd_WithFile(t *testing.T) {
	sv := sampleSharedValues()

	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "values.yaml")
	require.NoError(t, os.WriteFile(fp, []byte("persistence:\n  storageClass: local-path\n"), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/clusters/1/shared-values", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.SetSharedValuesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "local-dev-defaults", body.Name)
		assert.Contains(t, body.Values, "storageClass")
		assert.Equal(t, 10, body.Priority)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sv)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	clusterSharedValuesSetCmd.Flags().Set("name", "local-dev-defaults")
	clusterSharedValuesSetCmd.Flags().Set("file", fp)
	clusterSharedValuesSetCmd.Flags().Set("priority", "10")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Set shared values")
	assert.Contains(t, buf.String(), "local-dev-defaults")
}

func TestClusterSharedValuesSetCmd_WithSetFlag(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetSharedValuesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "test-values", body.Name)
		assert.Contains(t, body.Values, "storageClass")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sv)
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	clusterSharedValuesSetCmd.Flags().Set("name", "test-values")
	clusterSharedValuesSetCmd.Flags().Set("set", "persistence.storageClass=local-path")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.NoError(t, err)
}

func TestClusterSharedValuesSetCmd_PathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal")
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	clusterSharedValuesSetCmd.Flags().Set("name", "test")
	clusterSharedValuesSetCmd.Flags().Set("file", "../../etc/passwd")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain '..'")
}

func TestClusterSharedValuesSetCmd_NoFileOrSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no --file or --set provided")
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	resetSharedValuesSetFlags(t)
	clusterSharedValuesSetCmd.Flags().Set("name", "test")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of --file or --set is required")
}

func TestClusterSharedValuesSetCmd_JSONOutput(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sv)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	clusterSharedValuesSetCmd.Flags().Set("name", "test")
	clusterSharedValuesSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.NoError(t, err)

	var result types.SharedValues
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "local-dev-defaults", result.Name)
}

func TestClusterSharedValuesSetCmd_QuietOutput(t *testing.T) {
	sv := sampleSharedValues()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sv)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true

	clusterSharedValuesSetCmd.Flags().Set("name", "test")
	clusterSharedValuesSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.NoError(t, err)
	assert.Equal(t, "5\n", buf.String())
}

func TestClusterSharedValuesSetCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal error"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	clusterSharedValuesSetCmd.Flags().Set("name", "test")
	clusterSharedValuesSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetSharedValuesSetFlags(t) })

	err := clusterSharedValuesSetCmd.RunE(clusterSharedValuesSetCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

// ---------- shared-values delete ----------

func TestClusterSharedValuesDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/clusters/1/shared-values/5", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	clusterSharedValuesDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { clusterSharedValuesDeleteCmd.Flags().Set("yes", "false") })

	err := clusterSharedValuesDeleteCmd.RunE(clusterSharedValuesDeleteCmd, []string{"1", "5"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted shared values 5 from cluster 1")
}

func TestClusterSharedValuesDeleteCmd_ConfirmAccept(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	clusterSharedValuesDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		clusterSharedValuesDeleteCmd.Flags().Set("yes", "false")
		clusterSharedValuesDeleteCmd.SetIn(nil)
		clusterSharedValuesDeleteCmd.SetErr(nil)
	})

	clusterSharedValuesDeleteCmd.SetIn(strings.NewReader("y\n"))
	clusterSharedValuesDeleteCmd.SetErr(&bytes.Buffer{})

	err := clusterSharedValuesDeleteCmd.RunE(clusterSharedValuesDeleteCmd, []string{"1", "5"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted shared values")
}

func TestClusterSharedValuesDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	clusterSharedValuesDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		clusterSharedValuesDeleteCmd.Flags().Set("yes", "false")
		clusterSharedValuesDeleteCmd.SetIn(nil)
		clusterSharedValuesDeleteCmd.SetErr(nil)
	})

	clusterSharedValuesDeleteCmd.SetIn(strings.NewReader("n\n"))
	clusterSharedValuesDeleteCmd.SetErr(&bytes.Buffer{})

	err := clusterSharedValuesDeleteCmd.RunE(clusterSharedValuesDeleteCmd, []string{"1", "5"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestClusterSharedValuesDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true

	clusterSharedValuesDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { clusterSharedValuesDeleteCmd.Flags().Set("yes", "false") })

	err := clusterSharedValuesDeleteCmd.RunE(clusterSharedValuesDeleteCmd, []string{"1", "5"})
	require.NoError(t, err)
	assert.Equal(t, "5\n", buf.String())
}

func TestClusterSharedValuesDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "shared values not found"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	clusterSharedValuesDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { clusterSharedValuesDeleteCmd.Flags().Set("yes", "false") })

	err := clusterSharedValuesDeleteCmd.RunE(clusterSharedValuesDeleteCmd, []string{"1", "999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared values not found")
}

// ---------- cluster create ----------

func resetClusterCreateFlags(t *testing.T) {
	t.Helper()
	clusterCreateCmd.Flags().Set("from-file", "")
	clusterCreateCmd.Flags().Set("name", "")
	clusterCreateCmd.Flags().Set("description", "")
	clusterCreateCmd.Flags().Set("kubeconfig-data", "")
	clusterCreateCmd.Flags().Set("kubeconfig-path", "")
}

func TestClusterCreateCmd_TableOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/clusters", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var req types.CreateClusterRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "dev-cluster", req.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterCreateCmd.Flags().Set("name", "dev-cluster")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "dev-cluster")
}

func TestClusterCreateCmd_JSONOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	clusterCreateCmd.Flags().Set("name", "dev-cluster")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.NoError(t, err)

	var result types.Cluster
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "dev-cluster", result.Name)
}

func TestClusterCreateCmd_YAMLOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	clusterCreateCmd.Flags().Set("name", "dev-cluster")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "name: dev-cluster")
}

func TestClusterCreateCmd_QuietOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true
	clusterCreateCmd.Flags().Set("name", "dev-cluster")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestClusterCreateCmd_FromFile(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var req types.CreateClusterRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "file-cluster", req.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "cluster.json")
	require.NoError(t, os.WriteFile(tmpFile, []byte(`{"name":"file-cluster"}`), 0600))

	buf := setupClusterTestCmd(t, server.URL)
	clusterCreateCmd.Flags().Set("from-file", tmpFile)
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "dev-cluster")
}

func TestClusterCreateCmd_MissingName(t *testing.T) {
	_ = setupClusterTestCmd(t, "http://unused")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name is required")
}

func TestClusterCreateCmd_PathTraversal(t *testing.T) {
	_ = setupClusterTestCmd(t, "http://unused")
	clusterCreateCmd.Flags().Set("from-file", "../etc/passwd")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path must not contain '..'")
}

func TestClusterCreateCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "forbidden"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)
	clusterCreateCmd.Flags().Set("name", "dev-cluster")
	t.Cleanup(func() { resetClusterCreateFlags(t) })

	err := clusterCreateCmd.RunE(clusterCreateCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ---------- cluster update ----------

func resetClusterUpdateFlags(t *testing.T) {
	t.Helper()
	clusterUpdateCmd.Flags().Set("from-file", "")
	clusterUpdateCmd.Flags().Set("name", "")
	clusterUpdateCmd.Flags().Set("description", "")
	clusterUpdateCmd.Flags().Set("kubeconfig-data", "")
	clusterUpdateCmd.Flags().Set("kubeconfig-path", "")
	clusterUpdateCmd.Flags().Set("default", "false")
	// reset changed state
	clusterUpdateCmd.Flags().Lookup("from-file").Changed = false
	clusterUpdateCmd.Flags().Lookup("name").Changed = false
	clusterUpdateCmd.Flags().Lookup("description").Changed = false
	clusterUpdateCmd.Flags().Lookup("kubeconfig-data").Changed = false
	clusterUpdateCmd.Flags().Lookup("kubeconfig-path").Changed = false
	clusterUpdateCmd.Flags().Lookup("default").Changed = false
}

func TestClusterUpdateCmd_TableOutput(t *testing.T) {
	cl := sampleCluster()
	cl.Name = "renamed-cluster"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/clusters/1", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)
		var req types.UpdateClusterRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.NotNil(t, req.Name)
		require.Equal(t, "renamed-cluster", *req.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterUpdateCmd.Flags().Set("name", "renamed-cluster")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "renamed-cluster")
}

func TestClusterUpdateCmd_NoFlags(t *testing.T) {
	_ = setupClusterTestCmd(t, "http://unused")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestClusterUpdateCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "forbidden"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)
	clusterUpdateCmd.Flags().Set("name", "x")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

func TestClusterUpdateCmd_SetDefault(t *testing.T) {
	cl := sampleCluster()
	cl.IsDefault = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateClusterRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.NotNil(t, req.IsDefault)
		require.True(t, *req.IsDefault)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterUpdateCmd.Flags().Set("default", "true")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "true")
}

func TestClusterUpdateCmd_PathTraversal(t *testing.T) {
	_ = setupClusterTestCmd(t, "http://unused")
	clusterUpdateCmd.Flags().Set("from-file", "../../etc/passwd")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path must not contain '..'")
}

func TestClusterUpdateCmd_JSONOutput(t *testing.T) {
	cl := sampleCluster()
	cl.Name = "renamed-cluster"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	clusterUpdateCmd.Flags().Set("name", "renamed-cluster")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), `"name":`)
	assert.Contains(t, buf.String(), "renamed-cluster")
}

func TestClusterUpdateCmd_YAMLOutput(t *testing.T) {
	cl := sampleCluster()
	cl.Name = "renamed-cluster"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	clusterUpdateCmd.Flags().Set("name", "renamed-cluster")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "name:")
	assert.Contains(t, buf.String(), "renamed-cluster")
}

func TestClusterUpdateCmd_QuietOutput(t *testing.T) {
	cl := sampleCluster()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cl)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	printer.Quiet = true
	clusterUpdateCmd.Flags().Set("name", "x")
	t.Cleanup(func() { resetClusterUpdateFlags(t) })

	err := clusterUpdateCmd.RunE(clusterUpdateCmd, []string{"1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

// ---------- cluster delete ----------

func TestClusterDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/clusters/1", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { clusterDeleteCmd.Flags().Set("yes", "false") })

	err := clusterDeleteCmd.RunE(clusterDeleteCmd, []string{"1"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted cluster 1")
}

func TestClusterDeleteCmd_ConfirmAccept(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		clusterDeleteCmd.Flags().Set("yes", "false")
		clusterDeleteCmd.SetIn(nil)
		clusterDeleteCmd.SetErr(nil)
	})
	clusterDeleteCmd.SetIn(strings.NewReader("y\n"))
	clusterDeleteCmd.SetErr(&bytes.Buffer{})

	err := clusterDeleteCmd.RunE(clusterDeleteCmd, []string{"1"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted cluster")
}

func TestClusterDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		clusterDeleteCmd.Flags().Set("yes", "false")
		clusterDeleteCmd.SetIn(nil)
		clusterDeleteCmd.SetErr(nil)
	})
	clusterDeleteCmd.SetIn(strings.NewReader("n\n"))
	clusterDeleteCmd.SetErr(&bytes.Buffer{})

	err := clusterDeleteCmd.RunE(clusterDeleteCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestClusterDeleteCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)
	clusterDeleteCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() { clusterDeleteCmd.Flags().Set("dry-run", "false") })

	err := clusterDeleteCmd.RunE(clusterDeleteCmd, []string{"1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would delete")
}

func TestClusterDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)
	clusterDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { clusterDeleteCmd.Flags().Set("yes", "false") })

	err := clusterDeleteCmd.RunE(clusterDeleteCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found")
}

// ---------- cluster set-default ----------

func TestClusterSetDefaultCmd_Success(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/clusters/1/default", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupClusterTestCmd(t, server.URL)

	err := clusterSetDefaultCmd.RunE(clusterSetDefaultCmd, []string{"1"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "set as default")
}

func TestClusterSetDefaultCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "cluster not found"})
	}))
	defer server.Close()

	_ = setupClusterTestCmd(t, server.URL)

	err := clusterSetDefaultCmd.RunE(clusterSetDefaultCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found")
}
