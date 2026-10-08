package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupTestCmd. Do not add t.Parallel().

// setupStackTestCmd initialises globals and returns a buffer for captured output.
func setupStackTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	// Snapshot all mutated globals so t.Cleanup can restore them.
	prevCfg := cfg
	prevPrinter := printer
	prevFlagOutput := flagOutput
	prevFlagQuiet := flagQuiet
	prevFlagNoColor := flagNoColor
	prevFlagAPIURL := flagAPIURL
	prevFlagAPIKey := flagAPIKey
	prevFlagInsecure := flagInsecure
	prevFlagDebug := flagDebug
	t.Cleanup(func() {
		cfg = prevCfg
		printer = prevPrinter
		flagOutput = prevFlagOutput
		flagQuiet = prevFlagQuiet
		flagNoColor = prevFlagNoColor
		flagAPIURL = prevFlagAPIURL
		flagAPIKey = prevFlagAPIKey
		flagInsecure = prevFlagInsecure
		flagDebug = prevFlagDebug
	})

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
	clusterID := "1"
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.StackInstance{
		Base:              types.Base{ID: "42", CreatedAt: now, UpdatedAt: now, Version: "1"},
		Name:              "my-stack",
		StackDefinitionID: "5",
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
	s2.ID = "99"
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
	expires := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)
	stack.ExpiresAt = &expires
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/stack-instances/42":
			json.NewEncoder(w).Encode(stack)
		case "/api/v1/stack-instances/42/status":
			_, _ = w.Write([]byte(`{"status":"healthy","charts":[],"ingresses":[` +
				`{"name":"web","host":"my-stack.example.com","path":"/","url":"https://my-stack.example.com/","tls":true},` +
				`{"name":"web-2","host":"my-stack.example.com","path":"/","url":"https://my-stack.example.com/","tls":true},` +
				`{"name":"api","host":"api.my-stack.example.com","path":"/","url":"https://api.my-stack.example.com/","tls":true}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
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
	assert.Regexp(t, `Expires At:\s+2026-10-01T18:00:00Z`, out)
	assert.Regexp(t, `URL:\s+https://my-stack.example.com/`, out)
	assert.Regexp(t, `URL:\s+https://api.my-stack.example.com/`, out)
	assert.Equal(t, 2, strings.Count(out, "URL:"), "duplicate URLs are shown once")
}

func TestStackGetCmd_StatusErrorOmitsURLs(t *testing.T) {
	stack := sampleStack()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/stack-instances/42/status" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"K8s monitoring not configured"}`))
			return
		}
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	assert.NotContains(t, buf.String(), "URL:")
	assert.Contains(t, buf.String(), "my-stack")
}

func TestStackGetCmd_StoppedSkipsStatusCall(t *testing.T) {
	stack := sampleStack()
	stack.Status = "stopped"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42", r.URL.Path, "a stopped stack needs no status call")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	assert.Contains(t, buf.String(), "stopped")
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
	assert.Equal(t, "42", result.ID)
	assert.Equal(t, "my-stack", result.Name)
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
		assert.Equal(t, "5", body.StackDefinitionID)

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
		assert.Equal(t, "3", body.StackDefinitionID)
		assert.Equal(t, "feature/xyz", body.Branch)
		assert.Equal(t, "2", body.ClusterID)
		assert.Equal(t, 120, body.TTLMinutes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "50"}, Name: "feat-stack"})
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
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "100", Message: "Deployment started"})
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
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "100", Message: "Deployment started"})
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
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "101", Message: "Stop started"})
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
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "102", Message: "Clean started"})
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
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "103", Message: "Clean started"})
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
		_, _ = w.Write([]byte(recordedStackStatusJSON))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	prevNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = prevNow })

	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	want := "Status: healthy\n" +
		"CHART   POD                     PHASE    READY  RESTARTS  AGE  IMAGE\n" +
		"my-api  my-api-7d9f8b6c5-abcde  Running  true   2         2h   registry.example.com/my-api:1.2.3\n" +
		"my-api  my-api-7d9f8b6c5-fghij  Pending  false  0         45s  registry.example.com/my-api:1.2.3\n" +
		"my-db   -                       stopped  -      -         -    -\n" +
		"URLs:\n" +
		"  https://my-stack.example.com/\n"
	assert.Equal(t, want, buf.String())
}

// recordedStackStatusJSON is a GET /stack-instances/:id/status response in
// the shape of the backend k8s.NamespaceStatus (pods nested per chart).
const recordedStackStatusJSON = `{
  "last_checked": "2026-10-01T12:00:00Z",
  "namespace": "stack-my-stack-dev1",
  "status": "healthy",
  "charts": [
    {
      "release_name": "my-api",
      "chart_name": "my-api",
      "status": "healthy",
      "deployments": [{"name": "my-api", "ready_replicas": 1, "desired_replicas": 2, "updated_replicas": 2, "available": true}],
      "pods": [
        {"start_time": "2026-10-01T10:00:00Z", "container_states": [{"name": "api", "state": "running", "image": "registry.example.com/my-api:1.2.3", "restart_count": 2, "ready": true}], "name": "my-api-7d9f8b6c5-abcde", "phase": "Running", "image": "registry.example.com/my-api:1.2.3", "node_name": "node-1", "restart_count": 2, "ready": true},
        {"start_time": "2026-10-01T11:59:15Z", "container_states": [{"name": "api", "state": "waiting", "reason": "ContainerCreating", "image": "registry.example.com/my-api:1.2.3", "restart_count": 0, "ready": false}], "name": "my-api-7d9f8b6c5-fghij", "phase": "Pending", "image": "registry.example.com/my-api:1.2.3", "restart_count": 0, "ready": false}
      ],
      "services": [{"name": "my-api", "type": "ClusterIP", "cluster_ip": "10.0.0.10", "ports": ["80/TCP"]}]
    },
    {"release_name": "my-db", "chart_name": "my-db", "status": "stopped", "deployments": [], "pods": [], "services": []}
  ],
  "ingresses": [{"name": "my-api", "host": "my-stack.example.com", "path": "/", "url": "https://my-stack.example.com/", "tls": true}]
}`

func TestStackStatusCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(recordedStackStatusJSON))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	// Pass-through: the output equals the API response, per-chart pods included.
	var got, want interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.NoError(t, json.Unmarshal([]byte(recordedStackStatusJSON), &want))
	assert.Equal(t, want, got)
}

// ---------- stack logs ----------

func TestStackLogsCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/deploy-log", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data: []types.DeploymentLog{{
				ID:         "200",
				InstanceID: "42",
				Action:     "deploy",
				Status:     "completed",
				Output:     "Deployment succeeded.\nAll charts installed.",
			}},
			Total: 1,
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
		ID:         "200",
		InstanceID: "42",
		Action:     "deploy",
		Status:     "completed",
		Output:     "OK",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data:  []types.DeploymentLog{logEntry},
			Total: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackLogsCmd.RunE(stackLogsCmd, []string{"42"})
	require.NoError(t, err)

	var result types.DeploymentLog
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "200", result.ID)
	assert.Equal(t, "deploy", result.Action)
}

// ---------- stack clone ----------

func TestStackCloneCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/clone", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "55"}, Name: "my-stack-clone"})
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
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "55"}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackCloneCmd.RunE(stackCloneCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "55\n", buf.String())
}

// ---------- stack extend ----------

// startExtendServer serves GET /stack-instances/42 with the given expiry
// and POST /extend, which sets expires_at to now + ttl like the backend.
// now is the server clock; the Date header carries it.
func startExtendServer(t *testing.T, current *time.Time, now time.Time, extendCalls *int) *httptest.Server {
	t.Helper()
	return startExtendServerDate(t, current, now, extendCalls, true)
}

func startExtendServerDate(t *testing.T, current *time.Time, now time.Time, extendCalls *int, sendDate bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sendDate {
			w.Header().Set("Date", now.UTC().Format(http.TimeFormat))
		} else {
			w.Header()["Date"] = nil // suppress the automatic Date header
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/stack-instances/42":
			json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}, TTLMinutes: 240, ExpiresAt: current})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/stack-instances/42/extend":
			*extendCalls++
			var req map[string]int
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			exp := now.Add(time.Duration(req["ttl_minutes"]) * time.Minute)
			json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}, TTLMinutes: req["ttl_minutes"], ExpiresAt: &exp})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func setupExtend(t *testing.T, minutes string) time.Time {
	t.Helper()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	prevNow := nowFunc
	nowFunc = func() time.Time { return now }
	require.NoError(t, stackExtendCmd.Flags().Set("minutes", minutes))
	t.Cleanup(func() {
		nowFunc = prevNow
		resetFlag(t, stackExtendCmd.Flags(), "minutes", "0")
		resetFlag(t, stackExtendCmd.Flags(), "yes", "false")
		stackExtendCmd.SetIn(nil)
	})
	return now
}

func TestStackExtendCmd_Success(t *testing.T) {
	now := setupExtend(t, "60")
	current := now.Add(30 * time.Minute)
	calls := 0
	server := startExtendServer(t, &current, now, &calls)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))

	assert.Equal(t, 1, calls)
	want := "Set the TTL of stack 42 to 60 minutes\n" +
		"Old expiry: 2026-10-01T12:30:00Z\n" +
		"New expiry: 2026-10-01T13:00:00Z\n"
	assert.Equal(t, want, buf.String())
}

func TestStackExtendCmd_NoCurrentExpiry(t *testing.T) {
	now := setupExtend(t, "60")
	calls := 0
	server := startExtendServer(t, nil, now, &calls)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, 1, calls)
	assert.Contains(t, buf.String(), "Old expiry: -")
	assert.Contains(t, buf.String(), "New expiry: 2026-10-01T13:00:00Z")
}

func TestStackExtendCmd_ShorterExpiry_NonInteractiveNeedsYes(t *testing.T) {
	now := setupExtend(t, "60")
	current := now.Add(4 * time.Hour)
	calls := 0
	server := startExtendServer(t, &current, now, &calls)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	stackExtendCmd.SetIn(strings.NewReader("")) // stdin closed: no answer
	stackExtendCmd.SetErr(io.Discard)
	t.Cleanup(func() { stackExtendCmd.SetErr(nil) })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use --yes")
	assert.Zero(t, calls, "the expiry must not be shortened without confirmation")
}

func TestStackExtendCmd_ShorterExpiry_Declined(t *testing.T) {
	now := setupExtend(t, "60")
	current := now.Add(4 * time.Hour)
	calls := 0
	server := startExtendServer(t, &current, now, &calls)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	var prompt bytes.Buffer
	stackExtendCmd.SetIn(strings.NewReader("n\n"))
	stackExtendCmd.SetErr(&prompt)
	t.Cleanup(func() { stackExtendCmd.SetErr(nil) })

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Zero(t, calls)
	assert.Contains(t, buf.String(), "Aborted.")
	assert.Contains(t, prompt.String(), "expires at 2026-10-01T16:00:00Z")
	assert.Contains(t, prompt.String(), "can move the expiry earlier or keep it about the same (new expiry about 2026-10-01T13:00:00Z)")
}

func TestStackExtendCmd_ShorterExpiry_Yes(t *testing.T) {
	now := setupExtend(t, "60")
	require.NoError(t, stackExtendCmd.Flags().Set("yes", "true"))
	current := now.Add(4 * time.Hour)
	calls := 0
	server := startExtendServer(t, &current, now, &calls)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, 1, calls)
	assert.Contains(t, buf.String(), "Old expiry: 2026-10-01T16:00:00Z")
	assert.Contains(t, buf.String(), "New expiry: 2026-10-01T13:00:00Z")
}

// TestStackExtendCmd_SkewedLocalClock: the local clock is 3 hours ahead of
// the server. The check uses the server time (Date header), so a TTL of 60
// minutes is still seen as shorter than the 4 hours left.
func TestStackExtendCmd_SkewedLocalClock(t *testing.T) {
	serverNow := setupExtend(t, "60")
	nowFunc = func() time.Time { return serverNow.Add(3 * time.Hour) }
	current := serverNow.Add(4 * time.Hour)
	calls := 0
	server := startExtendServer(t, &current, serverNow, &calls)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	var prompt bytes.Buffer
	stackExtendCmd.SetIn(strings.NewReader("n\n"))
	stackExtendCmd.SetErr(&prompt)
	t.Cleanup(func() { stackExtendCmd.SetErr(nil) })

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Zero(t, calls)
	assert.Contains(t, prompt.String(), "can move the expiry earlier or keep it about the same (new expiry about 2026-10-01T13:00:00Z)")
}

// TestStackExtendCmd_SkewedLocalClockNoPrompt: the local clock is 3 hours
// behind. A TTL of 5 hours is longer than the 4 hours left on the server,
// so no prompt, although the local clock suggests 7 hours left.
func TestStackExtendCmd_SkewedLocalClockNoPrompt(t *testing.T) {
	serverNow := setupExtend(t, "300")
	nowFunc = func() time.Time { return serverNow.Add(-3 * time.Hour) }
	current := serverNow.Add(4 * time.Hour)
	calls := 0
	server := startExtendServer(t, &current, serverNow, &calls)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	stackExtendCmd.SetIn(strings.NewReader("")) // a prompt would fail
	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Equal(t, 1, calls)
}

// TestStackExtendCmd_NoDateHeaderUsesLocalClock: without a Date header the
// local clock is the reference.
func TestStackExtendCmd_NoDateHeaderUsesLocalClock(t *testing.T) {
	now := setupExtend(t, "60")
	current := now.Add(4 * time.Hour)
	calls := 0
	server := startExtendServerDate(t, &current, now, &calls, false)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	stackExtendCmd.SetIn(strings.NewReader(""))
	stackExtendCmd.SetErr(io.Discard)
	t.Cleanup(func() { stackExtendCmd.SetErr(nil) })

	err := stackExtendCmd.RunE(stackExtendCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use --yes")
	assert.Zero(t, calls)
}

// TestStackExtendCmd_AboutTheSamePrompts: 59.5 minutes left and
// --minutes 60 keeps the expiry about the same; the command asks first.
func TestStackExtendCmd_AboutTheSamePrompts(t *testing.T) {
	now := setupExtend(t, "60")
	current := now.Add(59*time.Minute + 30*time.Second)
	calls := 0
	server := startExtendServer(t, &current, now, &calls)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	var prompt bytes.Buffer
	stackExtendCmd.SetIn(strings.NewReader("n\n"))
	stackExtendCmd.SetErr(&prompt)
	t.Cleanup(func() { stackExtendCmd.SetErr(nil) })

	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))
	assert.Zero(t, calls)
	assert.Contains(t, prompt.String(), "can move the expiry earlier or keep it about the same")
}

func TestExtendCouldShorten(t *testing.T) {
	server := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := server.Add(d); return &v }
	tests := []struct {
		name    string
		expiry  *time.Time
		minutes int
		want    bool
	}{
		{"no expiry", nil, 1, false},
		{"already expired", at(-time.Hour), 1, false},
		{"much longer", at(time.Hour), 120, false},
		{"exactly remaining plus margin", at(time.Hour), 62, false},
		{"inside the margin", at(time.Hour), 61, true},
		{"equal to remaining", at(time.Hour), 60, true},
		{"shorter", at(4 * time.Hour), 60, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extendCouldShorten(tt.expiry, server, tt.minutes))
		})
	}
}

func TestStackExtendCmd_JSONOutput(t *testing.T) {
	now := setupExtend(t, "120")
	calls := 0
	server := startExtendServer(t, nil, now, &calls)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, stackExtendCmd.RunE(stackExtendCmd, []string{"42"}))

	var result types.StackInstance
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, 120, result.TTLMinutes)
	require.NotNil(t, result.ExpiresAt)
	assert.Equal(t, now.Add(2*time.Hour), result.ExpiresAt.UTC())
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

func TestStackStatusCmd_NoPods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"stopped","namespace":"stack-my-stack-dev1","charts":[{"chart_name":"my-db","status":"stopped","pods":[]}]}`))
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
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data:  []types.DeploymentLog{{ID: "200"}},
			Total: 1,
		})
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
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "101", Message: "Stop started"})
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
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "42"}})
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
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(types.DeployResponse{LogID: "102", Message: "Clean started"})
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
	assert.Contains(t, out, "owner_id: admin")
}

func TestStackStatusCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.InstanceStatus{
			Status: "running",
			Charts: []types.ChartStatus{{ChartName: "my-api", Pods: []types.PodStatus{{Name: "pod-1", Phase: "Running", Ready: true}}}},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackStatusCmd.RunE(stackStatusCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "status: running")
	assert.Contains(t, out, "chart_name: my-api")
	assert.Contains(t, out, "name: pod-1")
	assert.Contains(t, out, "phase: Running")
}

func TestStackLogsCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data: []types.DeploymentLog{{
				ID: "200", Action: "deploy", Status: "completed", Output: "OK",
			}},
			Total: 1,
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

// ---------- stack logs --follow ----------

// startFollowLogsWS spins up a websocket server that emits a log line +
// terminal-status event, then blocks on ReadMessage so the client's
// CloseMessage handshake completes cleanly. Reused across the follow
// tests.
func startFollowLogsWS(t *testing.T, status string, logLines []string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the subscribe message before sending anything (the client
		// always subscribes immediately after the upgrade).
		_, _, _ = conn.ReadMessage()
		for _, line := range logLines {
			payload, _ := json.Marshal(types.WSDeploymentLog{InstanceID: "42", Line: line})
			msg, _ := json.Marshal(types.WSMessage{Type: "deployment.log", Data: payload})
			_ = conn.WriteMessage(websocket.TextMessage, msg)
		}
		statusPayload, _ := json.Marshal(types.WSDeploymentStatus{InstanceID: "42", Status: status})
		statusMsg, _ := json.Marshal(types.WSMessage{Type: "deployment.status", Data: statusPayload})
		_ = conn.WriteMessage(websocket.TextMessage, statusMsg)
		_, _, _ = conn.ReadMessage()
	}))
}

// TestFollowLogsCtx_ExitsOnTerminalStatus locks acceptance criterion #1:
// "Stream closes cleanly when deploy reaches terminal status." Driven
// through the testable `followLogsCtx` so we can capture stdout.
func TestFollowLogsCtx_ExitsOnTerminalStatus(t *testing.T) {
	server := startFollowLogsWS(t, "running", []string{"hello", "world"})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	c, err := newClient()
	require.NoError(t, err)

	var out, warn bytes.Buffer
	require.NoError(t, followLogsCtx(context.Background(), c, "42", &out, &warn))
	got := out.String()
	assert.Contains(t, got, "hello")
	assert.Contains(t, got, "world")
}

// TestFollowLogsCtx_ErrorStatusBubblesError verifies the documented
// non-zero exit contract when the deployment ends with a terminal
// "error" status — the wrapped error must mention "deployment failed".
func TestFollowLogsCtx_ErrorStatusBubblesError(t *testing.T) {
	server := startFollowLogsWS(t, "error", []string{"hint: image pull"})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	c, err := newClient()
	require.NoError(t, err)

	var out, warn bytes.Buffer
	err = followLogsCtx(context.Background(), c, "42", &out, &warn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment failed")
}

// TestFollowLogsCtx_CtxCancelSurfacesAsCleanExit verifies the documented
// Ctrl-C path: a cancelled context returns nil so the operator's
// intentional interrupt doesn't look like a command failure. The
// package-level goleak.VerifyTestMain check will catch any goroutine
// leaked on this path.
func TestFollowLogsCtx_CtxCancelSurfacesAsCleanExit(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read subscribe, then block — only Ctrl-C terminates this.
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	c, err := newClient()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after the call starts so the read loop unwinds via
	// the ctx-cancel path.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	var out, warn bytes.Buffer
	err = followLogsCtx(ctx, c, "42", &out, &warn)
	assert.NoError(t, err, "ctx cancellation must surface as clean exit")
}

// TestStackLogsCmd_NoFollowRegression locks the documented acceptance
// "No behavioural regression for --follow=false path" — the default
// REST-fetch path must keep returning the most recent deployment log
// without touching the WebSocket.
func TestStackLogsCmd_NoFollowRegression(t *testing.T) {
	var wsHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			wsHits++
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		require.Equal(t, "/api/v1/stack-instances/42/deploy-log", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data:  []types.DeploymentLog{{ID: "1", InstanceID: "42", Action: "deploy", Status: "completed", Output: "ok"}},
			Total: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackLogsCmd.RunE(stackLogsCmd, []string{"42"}))
	assert.Contains(t, buf.String(), "ok")
	assert.Equal(t, 0, wsHits, "--follow=false must NOT open a WebSocket")
}

// ---------- stack list: additional filter tests ----------

func TestStackListCmd_PageAndPageSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "2", r.URL.Query().Get("page"))
		assert.Equal(t, "10", r.URL.Query().Get("pageSize"))
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
	clusterID := "5"
	stack := types.StackInstance{
		Base:      types.Base{ID: "10"},
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

// ---------- stack stop: additional tests ----------

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

const (
	valuesTestDefID   = "e9af3b10-4633-436b-a131-975a3b598e3e"
	valuesTestChartA  = "3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b"
	valuesTestChartDB = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
)

// startValuesServer serves a stack (42) whose definition has two charts:
// my-db (deploy order 0) and my-api (deploy order 1), the per-chart YAML
// values and the ZIP export. hits counts requests per path.
func startValuesServer(t *testing.T, hits map[string]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		if hits != nil {
			hits[r.URL.Path]++
		}
		switch r.URL.Path {
		case "/api/v1/stack-instances/42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"42","name":"my-stack","stack_definition_id":"` + valuesTestDefID + `","status":"running"}`))
		case "/api/v1/stack-definitions/" + valuesTestDefID:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + valuesTestDefID + `","name":"example-dev","charts":[` +
				`{"id":"` + valuesTestChartA + `","chart_name":"my-api","deploy_order":1},` +
				`{"id":"` + valuesTestChartDB + `","chart_name":"my-db","deploy_order":0}]}`))
		case "/api/v1/stack-instances/42/values/" + valuesTestChartA:
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte("replicaCount: 2\nimage:\n  tag: develop\n"))
		case "/api/v1/stack-instances/42/values/" + valuesTestChartDB:
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte("persistence: false"))
		case "/api/v1/stack-instances/42/values":
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="my-stack-values.zip"`)
			_, _ = w.Write([]byte("PK\x03\x04zip-bytes"))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Chart not found in this stack definition"}`))
		}
	}))
}

func resetValuesFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		resetFlag(t, stackValuesCmd.Flags(), "chart", "")
		resetFlag(t, stackValuesCmd.Flags(), "output-file", "")
		resetFlag(t, stackValuesCmd.Flags(), "force", "false")
	})
}

func TestStackValuesCmd_AllChartsTable(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	want := "# chart: my-db\n" +
		"persistence: false\n" +
		"---\n" +
		"# chart: my-api\n" +
		"replicaCount: 2\n" +
		"image:\n" +
		"  tag: develop\n"
	assert.Equal(t, want, buf.String())
}

func TestStackValuesCmd_ChartByName(t *testing.T) {
	hits := map[string]int{}
	server := startValuesServer(t, hits)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.Flags().Set("chart", "MY-API"))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	assert.Equal(t, "replicaCount: 2\nimage:\n  tag: develop\n", buf.String())
	assert.Equal(t, 1, hits["/api/v1/stack-instances/42/values/"+valuesTestChartA])
	assert.Zero(t, hits["/api/v1/stack-instances/42/values/"+valuesTestChartDB])
}

func TestStackValuesCmd_ChartByID(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.Flags().Set("chart", valuesTestChartDB))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	assert.Equal(t, "persistence: false\n", buf.String())
}

func TestStackValuesCmd_UnknownChart(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.Flags().Set("chart", "nope"))
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `chart "nope" is not part of the definition of stack 42`)
	assert.Contains(t, err.Error(), "my-db, my-api")
}

func TestStackValuesCmd_JSONOutput(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	printer.Format = output.FormatJSON
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	var result map[string]map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, float64(2), result["my-api"]["replicaCount"])
	assert.Equal(t, false, result["my-db"]["persistence"])
}

func TestStackValuesCmd_YAMLOutput(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	printer.Format = output.FormatYAML
	require.NoError(t, stackValuesCmd.Flags().Set("chart", "my-api"))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	var result map[string]map[string]interface{}
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, 2, result["my-api"]["replicaCount"])
	assert.NotContains(t, result, "my-db")
}

func TestStackValuesCmd_QuietOutput(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	printer.Quiet = true
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))
	assert.Equal(t, "42\n", buf.String())
}

func TestStackValuesCmd_OutputFileZip(t *testing.T) {
	hits := map[string]int{}
	server := startValuesServer(t, hits)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	path := filepath.Join(t.TempDir(), "values.zip")
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", path))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "PK\x03\x04zip-bytes", string(data), "ZIP must be saved unchanged")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	assert.Contains(t, buf.String(), "Wrote values of stack 42 to "+path)
	assert.Equal(t, 1, hits["/api/v1/stack-instances/42/values"])
	assert.Zero(t, hits["/api/v1/stack-definitions/"+valuesTestDefID], "the ZIP export needs no chart lookup")
}

func TestStackValuesCmd_OutputFileExistsNeedsForce(t *testing.T) {
	hits := map[string]int{}
	server := startValuesServer(t, hits)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	path := filepath.Join(t.TempDir(), "values.zip")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", path))

	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists; use --force")
	assert.Zero(t, hits["/api/v1/stack-instances/42/values"], "no download when the file exists")
	data, _ := os.ReadFile(path)
	assert.Equal(t, "old", string(data))
}

func TestStackValuesCmd_OutputFileForceReplacesWith0600(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	path := filepath.Join(t.TempDir(), "values.zip")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", path))
	require.NoError(t, stackValuesCmd.Flags().Set("force", "true"))

	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "PK\x03\x04zip-bytes", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "the mode of the old file is not kept")
}

func TestStackValuesCmd_OutputFileSymlinkNotFollowed(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("keep"), 0644))
	link := filepath.Join(dir, "values.zip")
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", link))

	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err, "an existing symlink counts as an existing file")

	require.NoError(t, stackValuesCmd.Flags().Set("force", "true"))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))
	data, _ := os.ReadFile(target)
	assert.Equal(t, "keep", string(data), "the symlink target must not be written")
	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "the symlink is replaced by a regular file")
}

func TestStackValuesCmd_ForceWithoutOutputFile(t *testing.T) {
	_ = setupStackTestCmd(t, "http://127.0.0.1:1")
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.Flags().Set("force", "true"))
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force needs --output-file")
}

func TestStackValuesCmd_JSONNonStringKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/stack-instances/42":
			_, _ = w.Write([]byte(`{"id":"42","stack_definition_id":"` + valuesTestDefID + `"}`))
		case "/api/v1/stack-definitions/" + valuesTestDefID:
			_, _ = w.Write([]byte(`{"id":"` + valuesTestDefID + `","charts":[{"id":"` + valuesTestChartA + `","chart_name":"my-api"}]}`))
		default:
			_, _ = w.Write([]byte("ports:\n  80: http\n  443: https\nlist:\n  - {1: a}\n"))
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	printer.Format = output.FormatJSON
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))
	assert.JSONEq(t, `{"my-api":{"ports":{"80":"http","443":"https"},"list":[{"1":"a"}]}}`, buf.String())
}

func TestStackGetCmd_StatusSingleAttempt(t *testing.T) {
	stack := sampleStack()
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/stack-instances/42/status" {
			statusCalls++
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"Failed to connect to cluster"}`))
			return
		}
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	start := time.Now()
	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	assert.Equal(t, 1, statusCalls, "a 502 must not be retried")
	assert.Less(t, time.Since(start), time.Second, "no retry backoff")
	assert.NotContains(t, buf.String(), "URL:")
}

func TestStackGetCmd_StatusTimeout(t *testing.T) {
	stack := sampleStack()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/stack-instances/42/status" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		json.NewEncoder(w).Encode(stack)
	}))
	defer server.Close()
	defer close(release)

	prev := bestEffortTimeout
	bestEffortTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bestEffortTimeout = prev })

	buf := setupStackTestCmd(t, server.URL)
	start := time.Now()
	require.NoError(t, stackGetCmd.RunE(stackGetCmd, []string{"42"}))
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Contains(t, buf.String(), "my-stack")
	assert.NotContains(t, buf.String(), "URL:")
}

func TestStackValuesCmd_OutputFileChartYAML(t *testing.T) {
	server := startValuesServer(t, nil)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	path := filepath.Join(t.TempDir(), "my-db.yaml")
	require.NoError(t, stackValuesCmd.Flags().Set("chart", "my-db"))
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", path))
	require.NoError(t, stackValuesCmd.RunE(stackValuesCmd, []string{"42"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "persistence: false", string(data))
}

func TestStackValuesCmd_OutputFilePathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for a path with '..'")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	require.NoError(t, stackValuesCmd.Flags().Set("output-file", "../values.zip"))
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"42"})
	require.Error(t, err)
}

func TestStackValuesCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetValuesFlags(t)
	err := stackValuesCmd.RunE(stackValuesCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

// ========== stack compare ==========

// recordedCompareJSON is a GET /stack-instances/compare response in the
// shape of the backend handlers.CompareInstancesResponse.
const recordedCompareJSON = `{
  "left": {"id": "42", "name": "my-stack", "definition_name": "example-dev", "branch": "main", "owner": "dev1"},
  "right": {"id": "43", "name": "other-stack", "definition_name": "example-dev", "branch": "feature/x", "owner": "dev2"},
  "charts": [
    {"chart_name": "my-api", "left_values": "replicaCount: 1\n", "right_values": "replicaCount: 2\n", "has_differences": true},
    {"chart_name": "my-db", "left_values": "a: 1\n", "right_values": "a: 1\n", "has_differences": false},
    {"chart_name": "my-extra", "left_values": null, "right_values": "b: 2\n", "has_differences": true}
  ]
}`

func startCompareServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/stack-instances/compare", r.URL.Path)
		assert.Equal(t, "42", r.URL.Query().Get("left"))
		assert.Equal(t, "43", r.URL.Query().Get("right"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestStackCompareCmd_TableOutput_WithDiffs(t *testing.T) {
	server := startCompareServer(t, recordedCompareJSON)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"}))

	want := "Left:  my-stack (42, branch main, owner dev1)\n" +
		"Right: other-stack (43, branch feature/x, owner dev2)\n" +
		"CHART     RESULT\n" +
		"my-api    differs\n" +
		"my-db     identical\n" +
		"my-extra  only in right\n" +
		"2 of 3 charts differ. Use -o json or -o yaml to see the merged values of both sides.\n"
	assert.Equal(t, want, buf.String())
}

func TestStackCompareCmd_TableOutput_NoDiffs(t *testing.T) {
	server := startCompareServer(t, `{"left":{"id":"42","name":"a"},"right":{"id":"43","name":"b"},`+
		`"charts":[{"chart_name":"my-api","left_values":"x: 1\n","right_values":"x: 1\n","has_differences":false}]}`)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"}))

	out := buf.String()
	assert.Contains(t, out, "my-api")
	assert.Contains(t, out, "identical")
	assert.Contains(t, out, "No differences found")
}

func TestStackCompareCmd_JSONOutput(t *testing.T) {
	server := startCompareServer(t, recordedCompareJSON)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"}))

	// Pass-through: the output equals the API response.
	var got, want interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.NoError(t, json.Unmarshal([]byte(recordedCompareJSON), &want))
	assert.Equal(t, want, got)
}

func TestStackCompareCmd_YAMLOutput(t *testing.T) {
	server := startCompareServer(t, recordedCompareJSON)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	require.NoError(t, stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"}))

	out := buf.String()
	assert.Contains(t, out, "name: my-stack")
	assert.Contains(t, out, "has_differences: true")
}

func TestStackCompareCmd_QuietOutput(t *testing.T) {
	server := startCompareServer(t, recordedCompareJSON)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, stackCompareCmd.RunE(stackCompareCmd, []string{"42", "43"}))
	assert.Equal(t, "42\n43\n", buf.String())
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

// ---------- stack deploy auth error ----------

func TestStackDeployCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := stackDeployCmd.RunE(stackDeployCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ========== stack history ==========

func TestStackHistoryCmd_Success(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/deploy-log", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data: []types.DeploymentLog{
				{ID: "300", InstanceID: "42", Action: "deploy", Status: "completed", StartedAt: &now, CompletedAt: &now},
				{ID: "299", InstanceID: "42", Action: "rollback", Status: "completed", StartedAt: &now, CompletedAt: &now},
			},
			Total: 2,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackHistoryCmd.RunE(stackHistoryCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "LOG ID")
	assert.Contains(t, out, "ACTION")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "300")
	assert.Contains(t, out, "deploy")
	assert.Contains(t, out, "299")
	assert.Contains(t, out, "rollback")
}

func TestStackHistoryCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{
			Data:  []types.DeploymentLog{{ID: "300", Action: "deploy", Status: "completed"}},
			Total: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackHistoryCmd.RunE(stackHistoryCmd, []string{"42"})
	require.NoError(t, err)

	var result types.DeploymentLogResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, int64(1), result.Total)
	assert.Len(t, result.Data, 1)
	assert.Equal(t, "300", result.Data[0].ID)
}

func TestStackHistoryCmd_EmptyHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeploymentLogResult{Data: nil, Total: 0})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := stackHistoryCmd.RunE(stackHistoryCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No deployment history for stack 42")
}

// ========== stack rollback ==========

func TestStackRollbackCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/rollback", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.RollbackResponse{LogID: "400", Message: "Rollback started"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackRollbackCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		stackRollbackCmd.Flags().Set("yes", "false")
		stackRollbackCmd.Flags().Set("target-log", "")
	})

	err := stackRollbackCmd.RunE(stackRollbackCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Rollback started for stack 42")
	assert.Contains(t, out, "log ID: 400")
}

func TestStackRollbackCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.RollbackResponse{LogID: "400", Message: "Rollback started"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	stackRollbackCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		stackRollbackCmd.Flags().Set("yes", "false")
		stackRollbackCmd.Flags().Set("target-log", "")
	})

	err := stackRollbackCmd.RunE(stackRollbackCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "400\n", buf.String())
}

// ========== stack history-values ==========

func TestStackHistoryValuesCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/deploy-log/300/values", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeployLogValuesResponse{
			LogID:  "300",
			Values: map[string]interface{}{"frontend": map[string]interface{}{"replicas": float64(3)}},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := stackHistoryValuesCmd.RunE(stackHistoryValuesCmd, []string{"42", "300"})
	require.NoError(t, err)

	var result types.DeployLogValuesResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "300", result.LogID)
	assert.Contains(t, result.Values, "frontend")
}

func TestStackHistoryValuesCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeployLogValuesResponse{
			LogID:  "300",
			Values: map[string]interface{}{"api": map[string]interface{}{"tag": "v1.2"}},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := stackHistoryValuesCmd.RunE(stackHistoryValuesCmd, []string{"42", "300"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "log_id: \"300\"")
}

func TestStackHistoryValuesCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.DeployLogValuesResponse{
			LogID:  "300",
			Values: map[string]interface{}{},
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := stackHistoryValuesCmd.RunE(stackHistoryValuesCmd, []string{"42", "300"})
	require.NoError(t, err)
	assert.Equal(t, "300\n", buf.String())
}

// ---- Dry-run Tests ----

func TestStackDeleteCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackDeleteCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		stackDeleteCmd.Flags().Set("dry-run", "false")
	})

	err := stackDeleteCmd.RunE(stackDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would delete 42")
}

func TestStackDeployCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	stackDeployCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		stackDeployCmd.Flags().Set("dry-run", "false")
	})

	err := stackDeployCmd.RunE(stackDeployCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would deploy")
}

// ---------- stack watch ----------

// startWatchWSServer spins up a websocket server that emits the supplied
// sequence of (instance_id, status) pairs as "deployment.status" envelopes
// then blocks on a ReadMessage so the client's close handshake completes
// cleanly. Returns the *httptest.Server; the caller must call Close.
func startWatchWSServer(t *testing.T, events []types.WSDeploymentStatus) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, ev := range events {
			payload, _ := json.Marshal(ev)
			msg, _ := json.Marshal(types.WSMessage{Type: "deployment.status", Data: payload})
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
		// Hold the connection open until the client closes it.
		_, _, _ = conn.ReadMessage()
	}))
}

// TestStackWatchCmd_ExitsOnRunning covers the primary acceptance
// criterion: `stack watch --id X` blocks until the instance reports a
// terminal status and exits cleanly when that status is "running".
func TestStackWatchCmd_ExitsOnRunning(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "42", Status: "deploying"},
		{InstanceID: "42", Status: "running"},
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "running")
}

// TestStackWatchCmd_NonZeroOnFailed verifies the documented exit-code
// contract: a terminal "failed"/"error" status causes RunE to return a
// non-nil error (which Cobra surfaces as a non-zero exit code).
func TestStackWatchCmd_NonZeroOnFailed(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "42", Status: "deploying"},
		{InstanceID: "42", Status: "error", ErrorMessage: "image pull backoff"},
	})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
}

// TestStackWatchCmd_MultiIDExitsOnAllTerminal covers the "wait for all
// listed IDs" semantic: the loop must NOT exit until every listed ID
// has reported a terminal status.
func TestStackWatchCmd_MultiIDExitsOnAllTerminal(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "1", Status: "running"},
		{InstanceID: "2", Status: "deploying"},
		{InstanceID: "2", Status: "running"},
	})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "1,2"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.NoError(t, err)
}

// TestStackWatchCmd_OwnerRejected documents that the --owner flag is
// currently a no-op surface placeholder; using it returns a clear error
// rather than silently dropping the filter.
func TestStackWatchCmd_OwnerRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	require.NoError(t, stackWatchCmd.Flags().Set("owner", "alice"))
	t.Cleanup(func() {
		_ = stackWatchCmd.Flags().Set("owner", "")
		_ = stackWatchCmd.Flags().Set("id", "")
	})

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--owner is not yet supported")
}

// TestStackWatchCmd_JSONOutput emits one JSON object per event in -o json
// mode — a contract worth locking in because operators pipe this to jq.
func TestStackWatchCmd_JSONOutput(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "42", Status: "running"},
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	require.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))

	var ev types.WatchEvent
	require.NoError(t, json.Unmarshal(buf.Bytes(), &ev))
	assert.Equal(t, "42", ev.InstanceID)
	assert.Equal(t, "running", ev.Status)
	assert.Equal(t, "deployment.status", ev.Type)
}

// TestStackWatchCmd_YAMLOutput rounds out the output-mode matrix
// (table/JSON/YAML/quiet) for stack watch. One YAML doc per event.
func TestStackWatchCmd_YAMLOutput(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "42", Status: "running"},
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	require.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))

	var ev types.WatchEvent
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &ev))
	assert.Equal(t, "42", ev.InstanceID)
	assert.Equal(t, "running", ev.Status)
	assert.Equal(t, "deployment.status", ev.Type)
}

// TestStackWatchCmd_IDIgnoresStatusFilter locks the documented
// precedence: when --id is set the --status filter is ignored (with a
// stderr warning) so the watch sees every transition for the listed
// instances and can detect opposite-terminal states. The bug this test
// guards against: --id 42 --status running, instance 42 fails — the
// "error" event would be dropped by --status and the watch would hang.
func TestStackWatchCmd_IDIgnoresStatusFilter(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		// Instance fails. With the bug, the status filter "running"
		// would drop this event and the watch would never exit.
		{InstanceID: "42", Status: "error", ErrorMessage: "boom"},
	})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	require.NoError(t, stackWatchCmd.Flags().Set("status", "running"))
	t.Cleanup(func() {
		_ = stackWatchCmd.Flags().Set("id", "")
		_ = stackWatchCmd.Flags().Set("status", "")
	})

	// Capture the warning to stderr so the test asserts the user-visible
	// signal that --status was suppressed.
	var stderr bytes.Buffer
	stackWatchCmd.SetErr(&stderr)
	t.Cleanup(func() { stackWatchCmd.SetErr(nil) })

	err := stackWatchCmd.RunE(stackWatchCmd, []string{})
	require.Error(t, err, "instance error must surface as a non-zero exit even when --status is set")
	assert.Contains(t, err.Error(), "failed terminal status")
	assert.Contains(t, stderr.String(), "ignoring --status")
}

// TestStackWatchCmd_QuietPrintsIDs locks in the convention: --quiet
// emits one instance ID per event.
func TestStackWatchCmd_QuietPrintsIDs(t *testing.T) {
	server := startWatchWSServer(t, []types.WSDeploymentStatus{
		{InstanceID: "42", Status: "running"},
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, stackWatchCmd.Flags().Set("id", "42"))
	t.Cleanup(func() { _ = stackWatchCmd.Flags().Set("id", "") })

	require.NoError(t, stackWatchCmd.RunE(stackWatchCmd, []string{}))
	assert.Equal(t, "42", strings.TrimSpace(buf.String()))
}

func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	tests := []struct {
		name  string
		start *time.Time
		want  string
	}{
		{"nil", nil, "-"},
		{"zero", &time.Time{}, "-"},
		{"future", at(-time.Minute), "0s"},
		{"seconds", at(45 * time.Second), "45s"},
		{"minutes", at(12 * time.Minute), "12m"},
		{"hours", at(3*time.Hour + 59*time.Minute), "3h"},
		{"days", at(50 * time.Hour), "2d"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatAge(tt.start, now))
		})
	}
}

// TestBestEffortClient: no session renewal (Tokens unset), exactly one
// attempt, same credentials and transport, short timeout.
func TestBestEffortClient(t *testing.T) {
	calls := 0
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"K8s monitoring not configured"}`))
	}))
	defer server.Close()

	c := client.New(server.URL)
	c.Token = "test-token"
	c.Tokens = nil
	transport := &http.Transport{}
	c.HTTPClient.Transport = transport

	q := bestEffortClient(c)
	assert.Nil(t, q.Tokens, "no session renewal")
	assert.Equal(t, server.URL, q.BaseURL)
	assert.Same(t, transport, q.HTTPClient.Transport)
	assert.Equal(t, bestEffortTimeout, q.HTTPClient.Timeout)

	_, err := q.GetStackStatus("42")
	require.Error(t, err)
	assert.Equal(t, 1, calls, "a 503 must not be retried")
	assert.Equal(t, "Bearer test-token", gotAuth)
}
