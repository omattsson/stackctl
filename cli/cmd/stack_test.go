package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupTestCmd. Do not add t.Parallel().

// setupStackTestCmd initialises globals and returns a buffer for captured output.
func setupStackTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {APIURL: apiURL},
		},
	}

	var buf bytes.Buffer
	printer = output.NewPrinter("table", false, true)
	printer.Writer = &buf

	flagAPIURL = apiURL
	flagAPIKey = ""
	flagInsecure = false
	flagQuiet = false

	return &buf
}

// sampleStackJSON returns a StackInstance object used across many tests.
func sampleStack() types.StackInstance {
	clusterID := uint(1)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.StackInstance{
		Base:              types.Base{ID: 42, CreatedAt: now, UpdatedAt: now, Version: 1},
		Name:              "my-stack",
		StackDefinitionID: 5,
		DefinitionName:    "api-service",
		Owner:             "admin",
		Branch:            "main",
		Namespace:         "ns-my-stack",
		Status:            "running",
		ClusterID:         &clusterID,
		ClusterName:       "dev-cluster",
		TTLMinutes:        60,
		DeployedAt:        &now,
	}
}

// ---------- stack list ----------

func TestStackListCmd_TableOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{stack}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "my-stack")
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "dev-cluster")
}

func TestStackListCmd_JSONOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{stack}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	var result types.ListResponse[types.StackInstance]
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, "my-stack", result.Data[0].Name)
}

func TestStackListCmd_QuietOutput(t *testing.T) {
	s1 := sampleStack()
	s2 := sampleStack()
	s2.ID = 99
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{s1, s2}, Total: 2, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "42\n99", lines)
}

func TestStackListCmd_WithFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "me", r.URL.Query().Get("owner"))
		assert.Equal(t, "running", r.URL.Query().Get("status"))
		assert.Equal(t, "1", r.URL.Query().Get("cluster_id"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	_ = buf

	stackListCmd.Flags().Set("mine", "true")
	stackListCmd.Flags().Set("status", "running")
	stackListCmd.Flags().Set("cluster", "1")
	t.Cleanup(func() {
		stackListCmd.Flags().Set("mine", "false")
		stackListCmd.Flags().Set("status", "")
		stackListCmd.Flags().Set("cluster", "0")
	})

	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)
}

func TestStackListCmd_DefinitionFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "5", r.URL.Query().Get("definition_id"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	_ = buf

	stackListCmd.Flags().Set("definition", "5")
	t.Cleanup(func() {
		stackListCmd.Flags().Set("definition", "0")
	})

	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)
}

func TestStackListCmd_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	// Should still have the headers row
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
}

// ---------- stack get ----------

func TestStackGetCmd_TableOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackGetCmd.RunE(stackGetCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "my-stack")
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "main")
}

func TestStackGetCmd_JSONOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackGetCmd.RunE(stackGetCmd, []string{"42"})
	require.NoError(t, err)

	var result types.StackInstance
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, uint(42), result.ID)
	assert.Equal(t, "my-stack", result.Name)
}

func TestStackGetCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackGetCmd.RunE(stackGetCmd, []string{"abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackGetCmd.RunE(stackGetCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ---------- stack create ----------

func TestStackCreateCmd_Success(t *testing.T) {
	created := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.CreateStackRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-stack", body.Name)
		assert.Equal(t, uint(5), body.StackDefinitionID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCreateCmd.Flags().Set("name", "my-stack")
	stackCreateCmd.Flags().Set("definition", "5")
	t.Cleanup(func() {
		stackCreateCmd.Flags().Set("name", "")
		stackCreateCmd.Flags().Set("definition", "0")
		stackCreateCmd.Flags().Set("branch", "")
		stackCreateCmd.Flags().Set("cluster", "0")
		stackCreateCmd.Flags().Set("ttl", "0")
	})

	err := stackCreateCmd.RunE(stackCreateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "my-stack")
}

func TestStackCreateCmd_MissingRequiredFlags(t *testing.T) {
	// Verify that the "name" and "definition" flags are marked as required via Cobra annotations.
	// We check the flag annotations rather than executing through rootCmd because shared
	// global command state can leak between tests.
	nameFlag := stackCreateCmd.Flags().Lookup("name")
	require.NotNil(t, nameFlag)
	assert.Contains(t, nameFlag.Annotations, cobra.BashCompOneRequiredFlag)

	defFlag := stackCreateCmd.Flags().Lookup("definition")
	require.NotNil(t, defFlag)
	assert.Contains(t, defFlag.Annotations, cobra.BashCompOneRequiredFlag)
}

func TestStackCreateCmd_AllFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.CreateStackRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "feat-stack", body.Name)
		assert.Equal(t, uint(3), body.StackDefinitionID)
		assert.Equal(t, "feature/xyz", body.Branch)
		assert.Equal(t, uint(2), body.ClusterID)
		assert.Equal(t, 120, body.TTLMinutes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: 50}, Name: "feat-stack"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCreateCmd.Flags().Set("name", "feat-stack")
	stackCreateCmd.Flags().Set("definition", "3")
	stackCreateCmd.Flags().Set("branch", "feature/xyz")
	stackCreateCmd.Flags().Set("cluster", "2")
	stackCreateCmd.Flags().Set("ttl", "120")
	t.Cleanup(func() {
		stackCreateCmd.Flags().Set("name", "")
		stackCreateCmd.Flags().Set("definition", "0")
		stackCreateCmd.Flags().Set("branch", "")
		stackCreateCmd.Flags().Set("cluster", "0")
		stackCreateCmd.Flags().Set("ttl", "0")
	})

	err := stackCreateCmd.RunE(stackCreateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "50")
	assert.Contains(t, out, "feat-stack")
}

// ---------- stack deploy ----------

func TestStackDeployCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/deploy", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 100}, InstanceID: 42, Action: "deploy", Status: "started"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackDeployCmd.RunE(stackDeployCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Deploying stack 42")
	assert.Contains(t, out, "log ID: 100")
}

func TestStackDeployCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 100}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackDeployCmd.RunE(stackDeployCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "100\n", buf.String())
}

// ---------- stack stop ----------

func TestStackStopCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/stop", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 101}, InstanceID: 42, Action: "stop", Status: "started"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackStopCmd.RunE(stackStopCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Stopping stack 42")
	assert.Contains(t, out, "log ID: 101")
}

// ---------- stack clean (destructive) ----------

func TestStackCleanCmd_WithConfirmation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/42/clean", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 102}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		stackCleanCmd.Flags().Set("yes", "false")
		stackCleanCmd.SetIn(nil)
		stackCleanCmd.SetErr(nil)
	})

	stackCleanCmd.SetIn(strings.NewReader("y\n"))
	stackCleanCmd.SetErr(&bytes.Buffer{})

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called after confirming with y")
	assert.Contains(t, buf.String(), "Cleaning stack 42")
}

func TestStackCleanCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		stackCleanCmd.Flags().Set("yes", "false")
		stackCleanCmd.SetIn(nil)
		stackCleanCmd.SetErr(nil)
	})

	stackCleanCmd.SetIn(strings.NewReader("n\n"))
	stackCleanCmd.SetErr(&bytes.Buffer{})

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestStackCleanCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 103}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called with --yes flag")
	assert.Contains(t, buf.String(), "Cleaning stack 42")
}

// ---------- stack delete (destructive) ----------

func TestStackDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/42", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		stackDeleteCmd.Flags().Set("yes", "false")
		stackDeleteCmd.SetIn(nil)
		stackDeleteCmd.SetErr(nil)
	})

	stackDeleteCmd.SetIn(strings.NewReader("y\n"))
	stackDeleteCmd.SetErr(&bytes.Buffer{})

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called after confirming with y")
	assert.Contains(t, buf.String(), "Deleted stack 42")
}

func TestStackDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		stackDeleteCmd.Flags().Set("yes", "false")
		stackDeleteCmd.SetIn(nil)
		stackDeleteCmd.SetErr(nil)
	})

	stackDeleteCmd.SetIn(strings.NewReader("n\n"))
	stackDeleteCmd.SetErr(&bytes.Buffer{})

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestStackDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called with --yes flag")
	assert.Contains(t, buf.String(), "Deleted stack 42")
}

func TestStackDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ---------- stack status ----------

func TestStackStatusCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/status", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{
			Status: "running",
			Pods: []types.PodStatus{
				{Name: "api-pod-1", Status: "Running", Ready: true, Restarts: 0, Age: "2h"},
				{Name: "web-pod-1", Status: "Running", Ready: true, Restarts: 1, Age: "2h"},
			},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "READY")
	assert.Contains(t, out, "api-pod-1")
	assert.Contains(t, out, "web-pod-1")
	assert.Contains(t, out, "true")
}

func TestStackStatusCmd_JSONOutput(t *testing.T) {
	status := types.InstanceStatus{
		Status: "running",
		Pods:   []types.PodStatus{{Name: "pod-1", Status: "Running", Ready: true, Restarts: 0, Age: "1h"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(status)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	var result types.InstanceStatus
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "running", result.Status)
	assert.Len(t, result.Pods, 1)
	assert.Equal(t, "pod-1", result.Pods[0].Name)
}

// ---------- stack logs ----------

func TestStackLogsCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/deploy-log", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base:       types.Base{ID: 200},
			InstanceID: 42,
			Action:     "deploy",
			Status:     "completed",
			Output:     "Deployment succeeded.\nAll charts installed.",
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "200")
	assert.Contains(t, out, "deploy")
	assert.Contains(t, out, "completed")
	assert.Contains(t, out, "Deployment succeeded.")
}

func TestStackLogsCmd_JSONOutput(t *testing.T) {
	logEntry := types.DeploymentLog{
		Base:       types.Base{ID: 200},
		InstanceID: 42,
		Action:     "deploy",
		Status:     "completed",
		Output:     "OK",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(logEntry)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"42"})
	require.NoError(t, err)

	var result types.DeploymentLog
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, uint(200), result.ID)
	assert.Equal(t, "deploy", result.Action)
}

// ---------- stack clone ----------

func TestStackCloneCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/clone", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: 55}, Name: "my-stack-clone"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Cloned stack 42")
	assert.Contains(t, out, "new stack 55")
}

func TestStackCloneCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: 55}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "55\n", buf.String())
}

// ---------- stack extend ----------

func TestStackExtendCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/extend", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		body, _ := io.ReadAll(r.Body)
		var req map[string]int
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, 60, req["ttl_minutes"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: 42}, TTLMinutes: 120})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackExtendCmd.Flags().Set("minutes", "60")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Extended stack 42 TTL by 60 minutes")
}

func TestStackExtendCmd_MissingMinutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called without minutes")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackExtendCmd.Flags().Set("minutes", "0")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--minutes must be a positive integer")
}

// ---------- Error responses across commands ----------

func TestStackDeployCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackDeployCmd.RunE(stackDeployCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

func TestStackStopCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "stack not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackStopCmd.RunE(stackStopCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stack not found")
}

func TestStackCloneCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"not-a-number"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackStatusCmd_NoPods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{Status: "stopped", Pods: nil})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "stopped")
	assert.Contains(t, out, "No pods found")
}

func TestStackLogsCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 200}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "200\n", buf.String())
}

func TestStackStatusCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{Status: "running"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestStackGetCmd_QuietOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackGetCmd.RunE(stackGetCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestStackDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	stackDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestStackStopCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 101}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackStopCmd.RunE(stackStopCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "101\n", buf.String())
}

func TestStackExtendCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: 42}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	stackExtendCmd.Flags().Set("minutes", "30")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestStackCleanCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 102}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	stackCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "102\n", buf.String())
}

// ---------- YAML output ----------

func TestStackListCmd_YAMLOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{stack}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: my-stack")
	assert.Contains(t, out, "status: running")
}

func TestStackGetCmd_YAMLOutput(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackGetCmd.RunE(stackGetCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: my-stack")
	assert.Contains(t, out, "owner: admin")
}

func TestStackStatusCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{
			Status: "running",
			Pods:   []types.PodStatus{{Name: "pod-1", Status: "Running", Ready: true}},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "status: running")
	assert.Contains(t, out, "name: pod-1")
}

func TestStackLogsCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{
			Base: types.Base{ID: 200}, Action: "deploy", Status: "completed", Output: "OK",
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "action: deploy")
	assert.Contains(t, out, "status: completed")
}

func TestParseID_Zero(t *testing.T) {
	_, err := parseID("0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

// ========== Additional coverage tests ==========

// ---------- stack clean: error cases ----------

func TestStackCleanCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "backend failure"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend failure")
}

func TestStackCleanCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCleanCmd.RunE(stackCleanCmd, []string{"bad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackCleanCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

	err := stackCleanCmd.RunE(stackCleanCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ---------- stack delete: additional error cases ----------

func TestStackDeleteCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"xyz"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackDeleteCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "delete failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

// ---------- stack clone: additional tests ----------

func TestStackCloneCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "clone failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clone failed")
}

func TestStackCloneCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ---------- stack extend: additional tests ----------

func TestStackExtendCmd_NegativeMinutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called with negative minutes")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackExtendCmd.Flags().Set("minutes", "-10")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--minutes must be a positive integer")
}

func TestStackExtendCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackExtendCmd.Flags().Set("minutes", "60")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"not-a-number"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackExtendCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "extend failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackExtendCmd.Flags().Set("minutes", "60")
	t.Cleanup(func() { stackExtendCmd.Flags().Set("minutes", "0") })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extend failed")
}

// ---------- stack status: additional tests ----------

func TestStackStatusCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "stack not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stack not found")
}

func TestStackStatusCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

// ---------- stack logs: additional tests ----------

func TestStackLogsCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "no logs found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no logs found")
}

func TestStackLogsCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

// ---------- stack list: additional filter tests ----------

func TestStackListCmd_PageAndPageSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "2", r.URL.Query().Get("page"))
		assert.Equal(t, "10", r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackListCmd.Flags().Set("page", "2")
	stackListCmd.Flags().Set("page-size", "10")
	t.Cleanup(func() {
		stackListCmd.Flags().Set("page", "0")
		stackListCmd.Flags().Set("page-size", "0")
	})

	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)
}

func TestStackListCmd_OwnerFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "john", r.URL.Query().Get("owner"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackListCmd.Flags().Set("owner", "john")
	t.Cleanup(func() {
		stackListCmd.Flags().Set("owner", "")
	})

	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)
}

func TestStackListCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "server error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
}

// ---------- stack list: table with no cluster name (uses cluster ID) ----------

func TestStackListCmd_ClusterIDFallback(t *testing.T) {
	clusterID := uint(5)
	stack := types.StackInstance{
		Base:      types.Base{ID: 10},
		Name:      "no-cluster-name",
		Status:    "running",
		ClusterID: &clusterID,
		// ClusterName intentionally empty
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{stack}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackListCmd.RunE(stackListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "5") // cluster ID used as fallback
	assert.Contains(t, out, "no-cluster-name")
}

// ---------- stack create: negative TTL ----------

func TestStackCreateCmd_NegativeTTL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called with negative TTL")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackCreateCmd.Flags().Set("name", "test")
	stackCreateCmd.Flags().Set("definition", "1")
	stackCreateCmd.Flags().Set("ttl", "-5")
	t.Cleanup(func() {
		stackCreateCmd.Flags().Set("name", "")
		stackCreateCmd.Flags().Set("definition", "0")
		stackCreateCmd.Flags().Set("ttl", "0")
	})

	err := stackCreateCmd.RunE(stackCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--ttl must be a non-negative integer")
}

// ---------- stack deploy: invalid ID ----------

func TestStackDeployCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackDeployCmd.RunE(stackDeployCmd, []string{"not-valid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

// ---------- stack stop: additional tests ----------

func TestStackStopCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackStopCmd.RunE(stackStopCmd, []string{"abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackStopCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "stop failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackStopCmd.RunE(stackStopCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop failed")
}

// ========== stack values ==========

func TestStackValuesCmd_JSONOutput(t *testing.T) {
	values := types.MergedValues{
		InstanceID: 42,
		Charts: map[string]map[string]interface{}{
			"frontend": {"replicas": float64(3), "image": map[string]interface{}{"tag": "v2"}},
			"backend":  {"replicas": float64(1)},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/values", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(values)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.NoError(t, err)

	var result types.MergedValues
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, uint(42), result.InstanceID)
	assert.Contains(t, result.Charts, "frontend")
	assert.Contains(t, result.Charts, "backend")
}

func TestStackValuesCmd_TableOutputFallsBackToJSON(t *testing.T) {
	values := types.MergedValues{
		InstanceID: 42,
		Charts:     map[string]map[string]interface{}{"api": {"replicas": float64(2)}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(values)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	// table format should fall back to JSON for values
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.NoError(t, err)

	var result types.MergedValues
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, uint(42), result.InstanceID)
}

func TestStackValuesCmd_YAMLOutput(t *testing.T) {
	values := types.MergedValues{
		InstanceID: 42,
		Charts:     map[string]map[string]interface{}{"api": {"replicas": float64(2)}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(values)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "instance_id: 42")
}

func TestStackValuesCmd_QuietOutput(t *testing.T) {
	values := types.MergedValues{InstanceID: 42}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(values)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestStackValuesCmd_WithChartFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "frontend", r.URL.Query().Get("chart"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.MergedValues{InstanceID: 42})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	stackValuesCmd.Flags().Set("chart", "frontend")
	t.Cleanup(func() { stackValuesCmd.Flags().Set("chart", "") })

	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.NoError(t, err)
}

func TestStackValuesCmd_InvalidID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackValuesCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "values failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "values failed")
}

func TestStackValuesCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ========== stack compare ==========

func TestStackCompareCmd_TableOutput_WithDiffs(t *testing.T) {
	left := sampleStack()
	right := sampleStack()
	right.ID = 43
	right.Name = "other-stack"
	right.Status = "stopped"
	right.Branch = "feature/x"

	result := types.CompareResult{
		Left:  &left,
		Right: &right,
		Diffs: map[string]interface{}{
			"Name":   map[string]interface{}{"left": "my-stack", "right": "other-stack"},
			"Status": map[string]interface{}{"left": "running", "right": "stopped"},
			"Branch": map[string]interface{}{"left": "main", "right": "feature/x"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "42", r.URL.Query().Get("left"))
		assert.Equal(t, "43", r.URL.Query().Get("right"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "FIELD")
	assert.Contains(t, out, "LEFT")
	assert.Contains(t, out, "RIGHT")
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "my-stack")
	assert.Contains(t, out, "other-stack")
	assert.Contains(t, out, "Branch")
}

func TestStackCompareCmd_TableOutput_NoDiffs(t *testing.T) {
	left := sampleStack()
	right := sampleStack()
	right.ID = 43

	result := types.CompareResult{Left: &left, Right: &right, Diffs: map[string]interface{}{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No differences found")
}

func TestStackCompareCmd_JSONOutput(t *testing.T) {
	left := sampleStack()
	right := sampleStack()
	right.ID = 43

	result := types.CompareResult{Left: &left, Right: &right}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.NoError(t, err)

	var res types.CompareResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &res))
	assert.Equal(t, uint(42), res.Left.ID)
	assert.Equal(t, uint(43), res.Right.ID)
}

func TestStackCompareCmd_YAMLOutput(t *testing.T) {
	left := sampleStack()
	right := sampleStack()
	right.ID = 43

	result := types.CompareResult{Left: &left, Right: &right}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: my-stack")
}

func TestStackCompareCmd_QuietOutput(t *testing.T) {
	result := types.CompareResult{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.NoError(t, err)
	assert.Equal(t, "42\n43\n", buf.String())
}

func TestStackCompareCmd_InvalidLeftID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"abc", "43"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackCompareCmd_InvalidRightID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid ID")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "xyz"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID")
}

func TestStackCompareCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "compare failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compare failed")
}

func TestStackCompareCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackCompareCmd.RunE(stackCompareCmd, []string{"42", "999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}
