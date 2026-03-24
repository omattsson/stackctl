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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestStackListCmd_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		var body types.StackInstance
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-stack", body.Name)
		assert.Equal(t, uint(5), body.StackDefinitionID)

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
	assert.Contains(t, nameFlag.Annotations, "cobra_annotation_bash_completion_one_required_flag")

	defFlag := stackCreateCmd.Flags().Lookup("definition")
	require.NotNil(t, defFlag)
	assert.Contains(t, defFlag.Annotations, "cobra_annotation_bash_completion_one_required_flag")
}

func TestStackCreateCmd_AllFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.StackInstance
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "feat-stack", body.Name)
		assert.Equal(t, uint(3), body.StackDefinitionID)
		assert.Equal(t, "feature/xyz", body.Branch)
		assert.NotNil(t, body.ClusterID)
		assert.Equal(t, uint(2), *body.ClusterID)
		assert.Equal(t, 120, body.TTLMinutes)

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
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLog{Base: types.Base{ID: 102}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackCleanCmd.Flags().Set("yes", "false")
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

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
	t.Cleanup(func() { stackCleanCmd.Flags().Set("yes", "false") })

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
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

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
	t.Cleanup(func() { stackDeleteCmd.Flags().Set("yes", "false") })

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
