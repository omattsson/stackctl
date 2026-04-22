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
	"gopkg.in/yaml.v3"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

// sampleValueOverride returns a ValueOverride used across override tests.
func sampleValueOverride() types.ValueOverride {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.ValueOverride{
		Base:       types.Base{ID: "1", CreatedAt: now, UpdatedAt: now, Version: "1"},
		InstanceID: "42",
		ChartID:    "1",
		Values:     `{"replicas":3}`,
	}
}

// sampleBranchOverride returns a BranchOverride used across override tests.
func sampleBranchOverride() types.BranchOverride {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.BranchOverride{
		Base:       types.Base{ID: "2", CreatedAt: now, UpdatedAt: now, Version: "1"},
		InstanceID: "42",
		ChartID:    "1",
		Branch:     "feature/my-branch",
	}
}

// sampleQuotaOverride returns a QuotaOverride used across override tests.
func sampleQuotaOverride() types.QuotaOverride {
	return types.QuotaOverride{
		InstanceID: "42",
		CPURequest: "100m",
		CPULimit:   "500m",
		MemRequest: "128Mi",
		MemLimit:   "512Mi",
		UpdatedAt:  time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

// resetOverrideSetFlags properly resets both --file and --set flags on overrideSetCmd.
// StringSlice flags cannot be cleared via Set("") since that produces [""] not [].
func resetOverrideSetFlags(t *testing.T) {
	t.Helper()
	overrideSetCmd.Flags().Set("file", "")
	if f := overrideSetCmd.Flags().Lookup("set"); f != nil {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			sv.Replace([]string{})
		}
		f.Changed = false
	}
}

// ===================== override list =====================

func TestOverrideListCmd_TableOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/overrides", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "CHART ID")
	assert.Contains(t, out, "INSTANCE ID")
	assert.Contains(t, out, "HAS VALUES")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "true")
}

func TestOverrideListCmd_JSONOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	var result []types.ValueOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0].ChartID)
	assert.Equal(t, "42", result[0].InstanceID)
}

func TestOverrideListCmd_YAMLOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "instance_id: \"42\"")
	assert.Contains(t, out, "chart_id: \"1\"")
}

func TestOverrideListCmd_QuietOutput(t *testing.T) {
	o1 := sampleValueOverride()
	o2 := sampleValueOverride()
	o2.ChartID = "3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{o1, o2})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n3", lines)
}

func TestOverrideListCmd_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "CHART ID")
}

func TestOverrideListCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "database error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
}

func TestOverrideListCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "instance not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideListCmd.RunE(overrideListCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

func TestOverrideListCmd_HasValuesFalse(t *testing.T) {
	override := sampleValueOverride()
	override.Values = ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.ValueOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideListCmd.RunE(overrideListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "false")
}

// ===================== override set =====================

func TestOverrideSetCmd_WithSetFlag(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		var parsed map[string]interface{}
		require.NoError(t, yaml.Unmarshal([]byte(body.Values), &parsed))
		assert.Equal(t, 3, parsed["replicas"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("set", "replicas=3")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Set value override for chart 1 on instance 42")
}

func TestOverrideSetCmd_WithFile(t *testing.T) {
	override := sampleValueOverride()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "values.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"replicas":5,"image":{"tag":"v2"}}`), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		var parsed map[string]interface{}
		require.NoError(t, yaml.Unmarshal([]byte(body.Values), &parsed))
		assert.Equal(t, 5, parsed["replicas"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", filePath)
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Set value override for chart 1 on instance 42")
}

func TestOverrideSetCmd_FileAndSetCombined(t *testing.T) {
	override := sampleValueOverride()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "values.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"replicas":3}`), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		var parsed map[string]interface{}
		require.NoError(t, yaml.Unmarshal([]byte(body.Values), &parsed))
		// --set should override the file value for replicas
		assert.Equal(t, 5, parsed["replicas"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", filePath)
	overrideSetCmd.Flags().Set("set", "replicas=5")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)
}

func TestOverrideSetCmd_NoFileAndNoSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no --file or --set provided")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	resetOverrideSetFlags(t)
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of --file or --set is required")
}

func TestOverrideSetCmd_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "bad.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{not valid json`), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called with invalid JSON")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", filePath)
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestOverrideSetCmd_FileNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when file doesn't exist")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", "/nonexistent/path/values.json")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading file")
}

func TestOverrideSetCmd_PathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", "../../etc/passwd")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain '..'")
}

func TestOverrideSetCmd_InvalidSetFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called with invalid --set format")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("set", "noequalsign")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --set format")
}

func TestOverrideSetCmd_JSONOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	overrideSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)

	var result types.ValueOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "1", result.ChartID)
}

func TestOverrideSetCmd_YAMLOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	overrideSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "chart_id: \"1\"")
}

func TestOverrideSetCmd_QuietOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	overrideSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestOverrideSetCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("set", "key=val")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

// ===================== override delete =====================

func TestOverrideDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called with --yes flag")
	assert.Contains(t, buf.String(), "Deleted value override for chart 1 on instance 42")
}

func TestOverrideDeleteCmd_ConfirmAccept(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideDeleteCmd.Flags().Set("yes", "false")
		overrideDeleteCmd.SetIn(nil)
		overrideDeleteCmd.SetErr(nil)
	})

	overrideDeleteCmd.SetIn(strings.NewReader("y\n"))
	overrideDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called after confirming with y")
	assert.Contains(t, buf.String(), "Deleted value override")
}

func TestOverrideDeleteCmd_ConfirmDecline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideDeleteCmd.Flags().Set("yes", "false")
		overrideDeleteCmd.SetIn(nil)
		overrideDeleteCmd.SetErr(nil)
	})

	overrideDeleteCmd.SetIn(strings.NewReader("n\n"))
	overrideDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestOverrideDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestOverrideDeleteCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "delete failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestOverrideDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "override not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "override not found")
}

// ===================== override branch list =====================

func TestOverrideBranchListCmd_TableOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/branches", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.BranchOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideBranchListCmd.RunE(overrideBranchListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "CHART ID")
	assert.Contains(t, out, "BRANCH")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "feature/my-branch")
}

func TestOverrideBranchListCmd_JSONOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.BranchOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := overrideBranchListCmd.RunE(overrideBranchListCmd, []string{"42"})
	require.NoError(t, err)

	var result []types.BranchOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "feature/my-branch", result[0].Branch)
}

func TestOverrideBranchListCmd_YAMLOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.BranchOverride{override})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := overrideBranchListCmd.RunE(overrideBranchListCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "branch: feature/my-branch")
}

func TestOverrideBranchListCmd_QuietOutput(t *testing.T) {
	o1 := sampleBranchOverride()
	o2 := sampleBranchOverride()
	o2.ChartID = "5"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.BranchOverride{o1, o2})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := overrideBranchListCmd.RunE(overrideBranchListCmd, []string{"42"})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n5", lines)
}

func TestOverrideBranchListCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "server error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideBranchListCmd.RunE(overrideBranchListCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
}

// ===================== override branch set =====================

func TestOverrideBranchSetCmd_Success(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/branches/1", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.SetBranchOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "feature/my-branch", body.Branch)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "1", "feature/my-branch"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Set branch override")
	assert.Contains(t, out, "feature/my-branch")
}

func TestOverrideBranchSetCmd_JSONOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "1", "main"})
	require.NoError(t, err)

	var result types.BranchOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "feature/my-branch", result.Branch)
}

func TestOverrideBranchSetCmd_YAMLOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "1", "main"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "branch: feature/my-branch")
}

func TestOverrideBranchSetCmd_QuietOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "1", "main"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestOverrideBranchSetCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "branch set failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "1", "main"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch set failed")
}

// ===================== override branch delete =====================

func TestOverrideBranchDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/42/branches/1", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideBranchDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideBranchDeleteCmd.Flags().Set("yes", "false") })

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted branch override for chart 1 on instance 42")
}

func TestOverrideBranchDeleteCmd_ConfirmAccept(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideBranchDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideBranchDeleteCmd.Flags().Set("yes", "false")
		overrideBranchDeleteCmd.SetIn(nil)
		overrideBranchDeleteCmd.SetErr(nil)
	})

	overrideBranchDeleteCmd.SetIn(strings.NewReader("y\n"))
	overrideBranchDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted branch override")
}

func TestOverrideBranchDeleteCmd_ConfirmDecline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideBranchDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideBranchDeleteCmd.Flags().Set("yes", "false")
		overrideBranchDeleteCmd.SetIn(nil)
		overrideBranchDeleteCmd.SetErr(nil)
	})

	overrideBranchDeleteCmd.SetIn(strings.NewReader("n\n"))
	overrideBranchDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestOverrideBranchDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	overrideBranchDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideBranchDeleteCmd.Flags().Set("yes", "false") })

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestOverrideBranchDeleteCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "delete failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideBranchDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideBranchDeleteCmd.Flags().Set("yes", "false") })

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

// ===================== override quota get =====================

func TestOverrideQuotaGetCmd_TableOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "100m")
	assert.Contains(t, out, "500m")
	assert.Contains(t, out, "128Mi")
	assert.Contains(t, out, "512Mi")
}

func TestOverrideQuotaGetCmd_JSONOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.NoError(t, err)

	var result types.QuotaOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result.InstanceID)
	assert.Equal(t, "100m", result.CPURequest)
}

func TestOverrideQuotaGetCmd_YAMLOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "instance_id: \"42\"")
	assert.Contains(t, out, "cpu_request: 100m")
}

func TestOverrideQuotaGetCmd_QuietOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestOverrideQuotaGetCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "server error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
}

func TestOverrideQuotaGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideQuotaGetCmd.RunE(overrideQuotaGetCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota not found")
}

// ===================== override quota set =====================

func TestOverrideQuotaSetCmd_AllFlags(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.SetQuotaOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "100m", body.CPURequest)
		assert.Equal(t, "500m", body.CPULimit)
		assert.Equal(t, "128Mi", body.MemRequest)
		assert.Equal(t, "512Mi", body.MemLimit)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideQuotaSetCmd.Flags().Set("cpu-request", "100m")
	overrideQuotaSetCmd.Flags().Set("cpu-limit", "500m")
	overrideQuotaSetCmd.Flags().Set("memory-request", "128Mi")
	overrideQuotaSetCmd.Flags().Set("memory-limit", "512Mi")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Set quota override for instance 42")
}

func TestOverrideQuotaSetCmd_CPURequestOnly(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetQuotaOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "200m", body.CPURequest)
		assert.Empty(t, body.CPULimit)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaSetCmd.Flags().Set("cpu-request", "200m")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)
}

func TestOverrideQuotaSetCmd_MemoryLimitOnly(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetQuotaOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "1Gi", body.MemLimit)
		assert.Empty(t, body.CPURequest)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaSetCmd.Flags().Set("memory-limit", "1Gi")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)
}

func TestOverrideQuotaSetCmd_NoFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no quota flags are provided")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaSetCmd.Flags().Set("cpu-request", "")
	overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
	overrideQuotaSetCmd.Flags().Set("memory-request", "")
	overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestOverrideQuotaSetCmd_JSONOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	overrideQuotaSetCmd.Flags().Set("cpu-request", "100m")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)

	var result types.QuotaOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result.InstanceID)
}

func TestOverrideQuotaSetCmd_YAMLOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	overrideQuotaSetCmd.Flags().Set("cpu-limit", "500m")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "instance_id: \"42\"")
}

func TestOverrideQuotaSetCmd_QuietOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(quota)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	overrideQuotaSetCmd.Flags().Set("cpu-request", "100m")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestOverrideQuotaSetCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota set failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaSetCmd.Flags().Set("cpu-request", "100m")
	t.Cleanup(func() {
		overrideQuotaSetCmd.Flags().Set("cpu-request", "")
		overrideQuotaSetCmd.Flags().Set("cpu-limit", "")
		overrideQuotaSetCmd.Flags().Set("memory-request", "")
		overrideQuotaSetCmd.Flags().Set("memory-limit", "")
	})

	err := overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota set failed")
}

// ===================== override quota delete =====================

func TestOverrideQuotaDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/42/quota-overrides", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideQuotaDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideQuotaDeleteCmd.Flags().Set("yes", "false") })

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted quota override for instance 42")
}

func TestOverrideQuotaDeleteCmd_ConfirmAccept(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideQuotaDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideQuotaDeleteCmd.Flags().Set("yes", "false")
		overrideQuotaDeleteCmd.SetIn(nil)
		overrideQuotaDeleteCmd.SetErr(nil)
	})

	overrideQuotaDeleteCmd.SetIn(strings.NewReader("y\n"))
	overrideQuotaDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted quota override")
}

func TestOverrideQuotaDeleteCmd_ConfirmDecline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideQuotaDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		overrideQuotaDeleteCmd.Flags().Set("yes", "false")
		overrideQuotaDeleteCmd.SetIn(nil)
		overrideQuotaDeleteCmd.SetErr(nil)
	})

	overrideQuotaDeleteCmd.SetIn(strings.NewReader("n\n"))
	overrideQuotaDeleteCmd.SetErr(&bytes.Buffer{})

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestOverrideQuotaDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	overrideQuotaDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideQuotaDeleteCmd.Flags().Set("yes", "false") })

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.NoError(t, err)
	assert.Equal(t, "42\n", buf.String())
}

func TestOverrideQuotaDeleteCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "delete failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideQuotaDeleteCmd.Flags().Set("yes", "false") })

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestOverrideQuotaDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quota not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideQuotaDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideQuotaDeleteCmd.Flags().Set("yes", "false") })

	err := overrideQuotaDeleteCmd.RunE(overrideQuotaDeleteCmd, []string{"42"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota not found")
}

// ===================== override set — YAML file =====================

func TestOverrideSetCmd_WithYAMLFile(t *testing.T) {
	override := sampleValueOverride()

	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "values.yaml")
	require.NoError(t, os.WriteFile(fp, []byte("replicas: 3\nimage:\n  tag: v2\n"), 0644))

	var capturedYAML string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		capturedYAML = body.Values

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("file", fp)
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Set value override for chart 1 on instance 42")

	var captured map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(capturedYAML), &captured))
	assert.Equal(t, 3, captured["replicas"])
	imageMap, ok := captured["image"].(map[string]interface{})
	require.True(t, ok, "image should be a nested map")
	assert.Equal(t, "v2", imageMap["tag"])
}

// ===================== override set — scalar type parsing via --set =====================

func TestOverrideSetCmd_ScalarTypeParsing(t *testing.T) {
	override := sampleValueOverride()

	var capturedYAML string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.SetValueOverrideRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		capturedYAML = body.Values

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(override)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideSetCmd.Flags().Set("set", "replicas=3")
	overrideSetCmd.Flags().Set("set", "enabled=true")
	overrideSetCmd.Flags().Set("set", "image.tag=v2")
	t.Cleanup(func() { resetOverrideSetFlags(t) })

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "1"})
	require.NoError(t, err)

	var captured map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(capturedYAML), &captured))
	assert.Equal(t, 3, captured["replicas"])
	assert.Equal(t, true, captured["enabled"])
	imageMap, ok := captured["image"].(map[string]interface{})
	require.True(t, ok, "image should be a nested map")
	assert.Equal(t, "v2", imageMap["tag"])
}

// ===================== parseScalarValue =====================

func TestParseScalarValue(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"", nil},
		{"3", int64(3)},
		{"3.14", 3.14},
		{"hello", "hello"},
		{"0", int64(0)},
		{"-1", int64(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseScalarValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ===================== setNestedValue =====================

func TestSetNestedValue(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    interface{}
		expected map[string]interface{}
	}{
		{"simple", "key", "val", map[string]interface{}{"key": "val"}},
		{"nested", "a.b.c", "val", map[string]interface{}{"a": map[string]interface{}{"b": map[string]interface{}{"c": "val"}}}},
		{"overwrite", "a", int64(1), map[string]interface{}{"a": int64(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]interface{}{}
			setNestedValue(m, tt.key, tt.value)
			assert.Equal(t, tt.expected, m)
		})
	}
}
