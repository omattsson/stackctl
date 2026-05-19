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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestCleanupPolicyListCmd_YAMLOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, cleanupPolicyListCmd.RunE(cleanupPolicyListCmd, []string{}))

	var got []types.CleanupPolicy
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
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

func TestCleanupPolicyGetCmd_YAMLOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"1"}))

	var got types.CleanupPolicy
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "nightly-stop", got.Name)
}

func TestCleanupPolicyGetCmd_QuietOutput(t *testing.T) {
	policies := sampleCleanupPolicies()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(policies))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, cleanupPolicyGetCmd.RunE(cleanupPolicyGetCmd, []string{"nightly-stop"}))
	assert.Equal(t, "1\n", buf.String(), "quiet mode must emit only the policy ID")
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

// createMockServer returns a mock server that echoes the request payload back
// as a created CleanupPolicy with ID=99 and timestamps zeroed. Shared by the
// output-format tests below.
func createMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateCleanupPolicyRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(types.CleanupPolicy{
			Base:      types.Base{ID: "99"},
			Name:      req.Name,
			ClusterID: req.ClusterID,
			Action:    req.Action,
			Condition: req.Condition,
			Schedule:  req.Schedule,
			Enabled:   req.Enabled,
		})
	}))
}

func writeValidCreatePayload(t *testing.T) string {
	t.Helper()
	return writeTempPolicyFile(t, "policy.json", types.CreateCleanupPolicyRequest{
		Name: "new-policy", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 3 * * *", Enabled: true,
	})
}

func TestCleanupPolicyCreateCmd_JSONOutput(t *testing.T) {
	server := createMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", writeValidCreatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{}))

	var got types.CleanupPolicy
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "99", got.ID)
	assert.Equal(t, "new-policy", got.Name)
}

func TestCleanupPolicyCreateCmd_YAMLOutput(t *testing.T) {
	server := createMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", writeValidCreatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{}))

	var got types.CleanupPolicy
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "99", got.ID)
	assert.Equal(t, "new-policy", got.Name)
}

func TestCleanupPolicyCreateCmd_QuietOutput(t *testing.T) {
	server := createMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, cleanupPolicyCreateCmd.Flags().Set("from-file", writeValidCreatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyCreateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyCreateCmd.RunE(cleanupPolicyCreateCmd, []string{}))
	assert.Equal(t, "99\n", buf.String(), "quiet mode must emit only the created ID")
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

// updateMockServer returns a mock server that echoes the request payload back
// as an updated CleanupPolicy with ID=1. Shared by the output-format tests.
func updateMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		var req types.UpdateCleanupPolicyRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.CleanupPolicy{
			Base:      types.Base{ID: "1"},
			Name:      req.Name,
			ClusterID: req.ClusterID,
			Action:    req.Action,
			Condition: req.Condition,
			Schedule:  req.Schedule,
			Enabled:   req.Enabled,
		})
	}))
}

func writeValidUpdatePayload(t *testing.T) string {
	t.Helper()
	return writeTempPolicyFile(t, "update.json", types.UpdateCleanupPolicyRequest{
		Name: "nightly-stop", ClusterID: "all", Action: "stop",
		Condition: "idle_days:14", Schedule: "0 4 * * *", Enabled: true,
	})
}

func TestCleanupPolicyUpdateCmd_JSONOutput(t *testing.T) {
	server := updateMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", writeValidUpdatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyUpdateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"1"}))

	var got types.CleanupPolicy
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "1", got.ID)
	assert.Equal(t, "idle_days:14", got.Condition)
}

func TestCleanupPolicyUpdateCmd_YAMLOutput(t *testing.T) {
	server := updateMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", writeValidUpdatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyUpdateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"1"}))

	var got types.CleanupPolicy
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "1", got.ID)
	assert.Equal(t, "idle_days:14", got.Condition)
}

func TestCleanupPolicyUpdateCmd_QuietOutput(t *testing.T) {
	server := updateMockServer(t)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, cleanupPolicyUpdateCmd.Flags().Set("from-file", writeValidUpdatePayload(t)))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyUpdateCmd.Flags(), "from-file", "") })

	require.NoError(t, cleanupPolicyUpdateCmd.RunE(cleanupPolicyUpdateCmd, []string{"1"}))
	assert.Equal(t, "1\n", buf.String(), "quiet mode must emit only the updated ID")
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

func TestCleanupPolicyDeleteCmd_InteractiveConfirm(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/admin/cleanup-policies/1", r.URL.Path)
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	// Default --yes=false. Pipe "y\n" to stdin to confirm.
	cleanupPolicyDeleteCmd.SetIn(strings.NewReader("y\n"))
	cleanupPolicyDeleteCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		cleanupPolicyDeleteCmd.SetIn(nil)
		cleanupPolicyDeleteCmd.SetErr(nil)
	})

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"1"}))
	assert.True(t, deleted, "DELETE must fire when user confirms")
	assert.Contains(t, buf.String(), "Deleted cleanup policy 1")
}

func TestCleanupPolicyDeleteCmd_InteractiveDecline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("API must NOT be called when user declines: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	cleanupPolicyDeleteCmd.SetIn(strings.NewReader("n\n"))
	cleanupPolicyDeleteCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		cleanupPolicyDeleteCmd.SetIn(nil)
		cleanupPolicyDeleteCmd.SetErr(nil)
	})

	require.NoError(t, cleanupPolicyDeleteCmd.RunE(cleanupPolicyDeleteCmd, []string{"1"}))
	assert.Contains(t, buf.String(), "Aborted")
}

// ---------- run ----------

func sampleRunResults() []types.CleanupResult {
	return []types.CleanupResult{
		{InstanceID: "i1", InstanceName: "app-1", Namespace: "stack-app-1", OwnerID: "alice", Action: "stop", Status: "success"},
		{InstanceID: "i2", InstanceName: "app-2", Namespace: "stack-app-2", OwnerID: "bob", Action: "stop", Status: "dry_run"},
	}
}

func TestCleanupPolicyRunCmd_DryRun(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/admin/cleanup-policies/1/run", r.URL.Path)
		require.Equal(t, "true", r.URL.Query().Get("dry_run"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleRunResults())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyRunCmd.Flags().Set("dry-run", "true"))
	t.Cleanup(func() { resetFlag(t, cleanupPolicyRunCmd.Flags(), "dry-run", "false") })

	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))
	assert.True(t, called)
	out := buf.String()
	assert.Contains(t, out, "app-1")
	assert.Contains(t, out, "Summary: 1 success, 0 error, 1 dry-run")
}

func TestCleanupPolicyRunCmd_RealRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/admin/cleanup-policies/1/run", r.URL.Path)
		require.Empty(t, r.URL.Query().Get("dry_run"), "real run must NOT send dry_run param")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.CleanupResult{
			{InstanceID: "i1", InstanceName: "app-1", Action: "stop", Status: "success"},
		})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))
}

func TestCleanupPolicyRunCmd_PartialFailureExitCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.CleanupResult{
			{InstanceID: "i1", Action: "delete", Status: "success"},
			{InstanceID: "i2", Action: "delete", Status: "error", Error: "namespace stuck terminating"},
			{InstanceID: "i3", Action: "delete", Status: "error", Error: "kubeconfig expired"},
		})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"})
	require.Error(t, err, "partial failure must surface as a non-nil error so cobra exits non-zero")
	assert.Contains(t, err.Error(), "2 per-instance failure")
}

func TestCleanupPolicyRunCmd_AllSuccessExitsZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.CleanupResult{
			{InstanceID: "i1", Action: "stop", Status: "success"},
		})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))
}

func TestCleanupPolicyRunCmd_NoMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]types.CleanupResult{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))
	assert.Contains(t, buf.String(), "No instances matched")
}

func TestCleanupPolicyRunCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleRunResults())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))

	var got []types.CleanupResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "i1", got[0].InstanceID)
}

func TestCleanupPolicyRunCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleRunResults())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))

	var got []types.CleanupResult
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
}

func TestCleanupPolicyRunCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleRunResults())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"}))
	assert.Equal(t, "i1\ni2\n", buf.String(), "quiet mode must emit one instance ID per line")
}

func TestCleanupPolicyRunCmd_ResolveNameToID(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(sampleCleanupPolicies())
		case http.MethodPost:
			capturedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]types.CleanupResult{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"nightly-stop"}))
	assert.Equal(t, "/api/v1/admin/cleanup-policies/1/run", capturedPath)
}

func TestCleanupPolicyRunCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := cleanupPolicyRunCmd.RunE(cleanupPolicyRunCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "admin role required")
}
