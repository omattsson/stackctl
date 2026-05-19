package cmd

import (
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

func sampleCleanupPolicies() []types.CleanupPolicy {
	t := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	return []types.CleanupPolicy{
		{
			Base:      types.Base{ID: "1", CreatedAt: t, UpdatedAt: t},
			Name:      "nightly-stop",
			ClusterID: "all",
			Action:    "stop",
			Condition: "idle_days:7",
			Schedule:  "0 2 * * *",
			Enabled:   true,
		},
		{
			Base:      types.Base{ID: "2", CreatedAt: t, UpdatedAt: t},
			Name:      "ttl-cleanup",
			ClusterID: "1",
			Action:    "delete",
			Condition: "ttl_expired",
			Schedule:  "*/30 * * * *",
			Enabled:   false,
			DryRun:    true,
		},
	}
}

// ---------- list ----------

func TestCleanupPolicyListCmd_TableOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/admin/cleanup-policies", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	require.NoError(t, cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "nightly-stop")
	assert.Contains(t, out, "ttl-cleanup")
	assert.Contains(t, out, "stop")
	assert.Contains(t, out, "delete")
}

func TestCleanupPolicyListCmd_JSONOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{}))

	var got []types.CleanupPolicy
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "nightly-stop", got[0].Name)
}

func TestCleanupPolicyListCmd_QuietOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{}))
	assert.Equal(t, "1\n2\n", buf.String(), "quiet mode must emit only the policy IDs")
}

func TestCleanupPolicyListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]types.CleanupPolicy{}))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{}))
	assert.Contains(t, buf.String(), "No cleanup policies found")
}

func TestCleanupPolicyListCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}

// ---------- get ----------

func TestCleanupPolicyGetCmd_ByID(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/admin/cleanup-policies", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	require.NoError(t, cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"2"}))

	out := buf.String()
	assert.Contains(t, out, "ttl-cleanup")
	assert.Contains(t, out, "delete")
}

func TestCleanupPolicyGetCmd_ByName(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"nightly-stop"}))
	assert.Contains(t, buf.String(), "0 2 * * *")
}

func TestCleanupPolicyGetCmd_NotFound(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"unknown"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cleanup policy found")
}

func TestCleanupPolicyGetCmd_AmbiguousName(t *testing.T) {
	// Two policies with the same name — should error.
	t0 := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	dupes := []types.CleanupPolicy{
		{Base: types.Base{ID: "1", CreatedAt: t0, UpdatedAt: t0}, Name: "dup", Action: "stop", Schedule: "0 * * * *"},
		{Base: types.Base{ID: "2", CreatedAt: t0, UpdatedAt: t0}, Name: "dup", Action: "delete", Schedule: "0 * * * *"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(dupes))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"dup"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple cleanup policies match")
}

func TestCleanupPolicyGetCmd_JSONOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"1"}))

	var got types.CleanupPolicy
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "nightly-stop", got.Name)
}

// ---------- create ----------

func writeTempPolicyFile(t *testing.T, name string, payload interface{}) string {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestCleanupPolicyCreateCmd_FromFile(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	created := types.CleanupPolicy{
		Base:      types.Base{ID: "99", CreatedAt: t0, UpdatedAt: t0},
		Name:      "new-policy",
		ClusterID: "all",
		Action:    "stop",
		Condition: "idle_days:14",
		Schedule:  "0 3 * * *",
		Enabled:   true,
	}

	var captured types.CreateCleanupPolicyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/admin/cleanup-policies", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(created))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	req := types.CreateCleanupPolicyRequest{
		Name: "new-policy", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 3 * * *", Enabled: true,
	}
	path := writeTempPolicyFile(t, "policy.json", req)
	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", path))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{}))
	assert.Equal(t, "new-policy", captured.Name)
	assert.Equal(t, "all", captured.ClusterID)
	assert.Contains(t, buf.String(), "new-policy")
}

func TestCleanupPolicyCreateCmd_MissingFromFile(t *testing.T) {
	_ = setupStackTestCmd(t, "http://localhost:0")
	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", ""))
	err := cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-file is required")
}

func TestCleanupPolicyCreateCmd_PathTraversalRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://localhost:0")
	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", "../policy.json"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })
	err := cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
}

func TestCleanupPolicyCreateCmd_ValidationError(t *testing.T) {
	_ = setupStackTestCmd(t, "http://localhost:0")

	bad := types.CreateCleanupPolicyRequest{Name: "x"} // missing required fields
	path := writeTempPolicyFile(t, "bad.json", bad)
	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", path))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	err := cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster_id")
}

func TestCleanupPolicyCreateCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	req := types.CreateCleanupPolicyRequest{
		Name: "p", ClusterID: "all", Action: "stop",
		Condition: "idle_days:7", Schedule: "0 2 * * *",
	}
	path := writeTempPolicyFile(t, "policy.json", req)
	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", path))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	err := cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}

// ---------- update ----------

func TestCleanupPolicyUpdateCmd_FromFile(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	updated := types.CleanupPolicy{
		Base:      types.Base{ID: "1", CreatedAt: t0, UpdatedAt: t0},
		Name:      "nightly-stop",
		ClusterID: "all",
		Action:    "stop",
		Condition: "idle_days:14",
		Schedule:  "0 4 * * *",
		Enabled:   true,
	}

	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(updated))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	req := types.UpdateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 4 * * *", Enabled: true,
	}
	path := writeTempPolicyFile(t, "update.json", req)
	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", path))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyUpdateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"1"}))
	assert.Equal(t, "/api/v1/admin/cleanup-policies/1", capturedPath)
}

func TestCleanupPolicyUpdateCmd_ResolveNameToID(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	updated := types.CleanupPolicy{
		Base:      types.Base{ID: "1", CreatedAt: t0, UpdatedAt: t0},
		Name:      "nightly-stop",
		ClusterID: "all",
		Action:    "stop",
		Condition: "idle_days:14",
		Schedule:  "0 4 * * *",
	}

	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			require.Equal(t, "/api/v1/admin/cleanup-policies", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(sampleCleanupPolicies()))
		case http.MethodPut:
			capturedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(updated))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	req := types.UpdateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 4 * * *",
	}
	path := writeTempPolicyFile(t, "update.json", req)
	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", path))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyUpdateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"nightly-stop"}))
	assert.Equal(t, "/api/v1/admin/cleanup-policies/1", capturedPath)
}

func TestCleanupPolicyUpdateCmd_MissingFromFile(t *testing.T) {
	_ = setupStackTestCmd(t, "http://localhost:0")
	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", ""))
	err := cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-file is required")
}

// ---------- delete ----------

func TestCleanupPolicyDeleteCmd_WithYesFlag(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/admin/cleanup-policies/1", r.URL.Path)
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyDeleteCmd.Flags(), "yes", "false") })

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"1"}))
	assert.True(t, deleted)
	assert.Contains(t, buf.String(), "Deleted cleanup policy 1")
}

func TestCleanupPolicyDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, cleanupPolicyDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyDeleteCmd.Flags(), "yes", "false") })

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"1"}))
	assert.Equal(t, "1\n", buf.String(), "quiet mode must echo only the policy ID")
}

func TestCleanupPolicyDeleteCmd_DryRun(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyDeleteCmd.Flags().Set("dry-run", "true"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyDeleteCmd.Flags(), "dry-run", "false") })

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"1"}))
	assert.False(t, called, "dry-run must not contact the backend")
	assert.Contains(t, buf.String(), "Would delete")
}

func TestCleanupPolicyDeleteCmd_ResolveNameToID(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(sampleCleanupPolicies()))
		case http.MethodDelete:
			capturedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyDeleteCmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyDeleteCmd.Flags(), "yes", "false") })

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"ttl-cleanup"}))
	assert.Equal(t, "/api/v1/admin/cleanup-policies/2", capturedPath)
}
