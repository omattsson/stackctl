package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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
		ID:              "1",
		UpdatedAt:       now,
		StackInstanceID: "42",
		ChartConfigID:   "1",
		Values:          `{"replicas":3}`,
	}
}

// sampleBranchOverride returns a BranchOverride used across override tests.
func sampleBranchOverride() types.BranchOverride {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.BranchOverride{
		ID:              "2",
		UpdatedAt:       now,
		StackInstanceID: "42",
		ChartConfigID:   "1",
		Branch:          "feature/my-branch",
	}
}

// sampleQuotaOverride returns a QuotaOverride used across override tests.
func sampleQuotaOverride() types.QuotaOverride {
	return types.QuotaOverride{
		StackInstanceID: "42",
		CPURequest:      "100m",
		CPULimit:        "500m",
		MemRequest:      "128Mi",
		MemLimit:        "512Mi",
		UpdatedAt:       time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

// resetOverrideSetFlags properly resets both --file and --set flags on overrideSetCmd.
// StringSlice flags cannot be cleared via Set("") since that produces [""] not [].
func resetOverrideSetFlags(t *testing.T) {
	t.Helper()
	overrideSetCmd.Flags().Set("file", "")
	resetFlag(t, overrideSetCmd.Flags(), "replace", "false")
	resetFlag(t, overrideSetCmd.Flags(), "yes", "false")
	overrideSetCmd.SetIn(nil)
	overrideSetCmd.SetErr(nil)
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Equal(t, "1", result[0].ChartConfigID)
	assert.Equal(t, "42", result[0].StackInstanceID)
}

func TestOverrideListCmd_YAMLOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Contains(t, out, "chart_config_id: \"1\"")
}

func TestOverrideListCmd_QuietOutput(t *testing.T) {
	o1 := sampleValueOverride()
	o2 := sampleValueOverride()
	o2.ChartConfigID = "3"
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/42/overrides/1", r.URL.Path)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Value override not found"}`))
			return
		}
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

	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Value override not found"}`))
			return
		}
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

	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Value override not found"}`))
			return
		}
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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

	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Equal(t, "1", result.ChartConfigID)
}

func TestOverrideSetCmd_YAMLOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Contains(t, out, "chart_config_id: \"1\"")
}

func TestOverrideSetCmd_QuietOutput(t *testing.T) {
	override := sampleValueOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Value override not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Equal(t, "chart 1 on stack 42 has no value override", err.Error())
}

func TestOverrideDeleteCmd_ChartNotInDefinition(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/stack-instances/42/overrides/"+valuesTestChartA, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Chart not found in this stack definition"}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", valuesTestChartA})
	require.Error(t, err)
	assert.Equal(t, "chart "+valuesTestChartA+" is not part of the definition of stack 42", err.Error())
}

func TestOverrideDeleteCmd_NonInteractiveNeedsYes(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called without confirmation")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	overrideDeleteCmd.SetIn(strings.NewReader(""))
	overrideDeleteCmd.SetErr(io.Discard)
	t.Cleanup(func() {
		overrideDeleteCmd.SetIn(nil)
		overrideDeleteCmd.SetErr(nil)
	})

	err := overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use --yes")
}

// ===================== override branch list =====================

func TestOverrideBranchListCmd_TableOutput(t *testing.T) {
	override := sampleBranchOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	o2.ChartConfigID = "5"
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Equal(t, "42", result.StackInstanceID)
	assert.Equal(t, "100m", result.CPURequest)
}

func TestOverrideQuotaGetCmd_YAMLOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Equal(t, "42", result.StackInstanceID)
}

func TestOverrideQuotaSetCmd_YAMLOutput(t *testing.T) {
	quota := sampleQuotaOverride()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Value override not found"}`))
			return
		}
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
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Value override not found"}`))
			return
		}
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
		{"", ""},
		{"3", int64(3)},
		{"3.14", "3.14"},
		{"1.10", "1.10"},
		{"0123", "0123"},
		{"1e3", "1e3"},
		{"+1", int64(1)},
		{"-01", int64(-1)},
		{"-0", int64(0)},
		{"True", true},
		{"FALSE", false},
		{"False", false},
		{"Null", nil},
		{"NULL", nil},
		{"yes", "yes"},
		{"00", "00"},
		{"hello", "hello"},
		{"0", int64(0)},
		{"-1", int64(-1)},
		{"99999999999999999999", "99999999999999999999"},
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
		{"escaped dot", `podAnnotations.prometheus\.io/scrape`, "true", map[string]interface{}{"podAnnotations": map[string]interface{}{"prometheus.io/scrape": "true"}}},
		{"escaped backslash", `a\\b`, 1, map[string]interface{}{`a\b`: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]interface{}{}
			require.NoError(t, setNestedValue(m, tt.key, tt.value))
			assert.Equal(t, tt.expected, m)
		})
	}
}

// ===================== override merge, get, unset, chart names =====================

// overrideAPI is a stateful fake of the per-chart override routes, with a
// stack (42) whose definition has the charts my-api and my-db.
type overrideAPI struct {
	values   map[string]string // chartID -> YAML
	puts     []string          // YAML bodies of PUT /overrides/:chartId
	branches map[string]string // chartID -> branch
}

func startOverrideAPI(t *testing.T, api *overrideAPI) *httptest.Server {
	t.Helper()
	if api.values == nil {
		api.values = map[string]string{}
	}
	if api.branches == nil {
		api.branches = map[string]string{}
	}
	known := map[string]bool{valuesTestChartA: true, valuesTestChartDB: true}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		notFound := func(msg string) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
		}
		path := r.URL.Path
		switch {
		case path == "/api/v1/stack-instances/42" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"42","name":"my-stack","stack_definition_id":"` + valuesTestDefID + `","status":"running"}`))
		case path == "/api/v1/stack-definitions/"+valuesTestDefID && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"` + valuesTestDefID + `","name":"example-dev","charts":[` +
				`{"id":"` + valuesTestChartA + `","chart_name":"my-api","deploy_order":1},` +
				`{"id":"` + valuesTestChartDB + `","chart_name":"my-db","deploy_order":0}]}`))
		case strings.HasPrefix(path, "/api/v1/stack-instances/42/overrides/"):
			chartID := strings.TrimPrefix(path, "/api/v1/stack-instances/42/overrides/")
			if !known[chartID] {
				notFound("Chart not found in this stack definition")
				return
			}
			switch r.Method {
			case http.MethodGet:
				v, ok := api.values[chartID]
				if !ok {
					notFound("Value override not found")
					return
				}
				json.NewEncoder(w).Encode(types.ValueOverride{ID: "ov-" + chartID, StackInstanceID: "42", ChartConfigID: chartID, Values: v})
			case http.MethodPut:
				var req types.SetValueOverrideRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				api.puts = append(api.puts, req.Values)
				if strings.TrimSpace(req.Values) == "" {
					delete(api.values, chartID)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				api.values[chartID] = req.Values
				json.NewEncoder(w).Encode(types.ValueOverride{ID: "ov-" + chartID, StackInstanceID: "42", ChartConfigID: chartID, Values: req.Values})
			case http.MethodDelete:
				if _, ok := api.values[chartID]; !ok {
					notFound("Value override not found")
					return
				}
				delete(api.values, chartID)
				w.WriteHeader(http.StatusNoContent)
			}
		case strings.HasPrefix(path, "/api/v1/stack-instances/42/branches/"):
			chartID := strings.TrimPrefix(path, "/api/v1/stack-instances/42/branches/")
			if !known[chartID] {
				notFound("Chart not found in this stack definition")
				return
			}
			switch r.Method {
			case http.MethodPut:
				var req types.SetBranchOverrideRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				api.branches[chartID] = req.Branch
				json.NewEncoder(w).Encode(types.BranchOverride{ID: "br-" + chartID, StackInstanceID: "42", ChartConfigID: chartID, Branch: req.Branch})
			case http.MethodDelete:
				if _, ok := api.branches[chartID]; !ok {
					notFound("Branch override not found")
					return
				}
				delete(api.branches, chartID)
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, path)
			notFound("not found")
		}
	}))
}

func parseYAMLMap(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	m := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &m))
	return m
}

func TestOverrideSetCmd_SetMergesIntoExisting(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "resources:\n  limits:\n    memory: 192Mi\nreplicas: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "sharedValuesTest=from-instance"))
	require.NoError(t, overrideSetCmd.Flags().Set("set", "replicas=2"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))

	require.Len(t, api.puts, 1)
	got := parseYAMLMap(t, api.puts[0])
	assert.Equal(t, "from-instance", got["sharedValuesTest"])
	assert.Equal(t, 2, got["replicas"])
	assert.Equal(t, map[string]interface{}{"limits": map[string]interface{}{"memory": "192Mi"}}, got["resources"], "--set must keep the other keys")

	want := "Set value override for chart my-api on instance 42\n" +
		"  ~ replicas (changed)\n" +
		"  + sharedValuesTest (added)\n"
	assert.Equal(t, want, buf.String())
}

func TestOverrideSetCmd_SetWithoutExistingOverride(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "image.tag=v2"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", valuesTestChartDB}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"image": map[string]interface{}{"tag": "v2"}}, parseYAMLMap(t, api.puts[0]))
}

func TestOverrideSetCmd_ReplaceDropsOtherKeys(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "resources:\n  limits:\n    memory: 192Mi\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "replicas=1"))
	require.NoError(t, overrideSetCmd.Flags().Set("replace", "true"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"replicas": 1}, parseYAMLMap(t, api.puts[0]))
	assert.Contains(t, buf.String(), "+ replicas (added)")
	assert.Contains(t, buf.String(), "- resources.limits.memory (removed)")
}

func TestOverrideSetCmd_FileWithoutSetReplaces(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "old: true\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	file := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(file, []byte("new: 1\n"), 0600))
	require.NoError(t, overrideSetCmd.Flags().Set("file", file))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"new": 1}, parseYAMLMap(t, api.puts[0]))
}

func TestOverrideSetCmd_NoChangeSkipsWrite(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "replicas: 2\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "replicas=2"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	assert.Empty(t, api.puts)
	assert.Contains(t, buf.String(), "No changes to the value override of chart my-api")
}

func TestOverrideSetCmd_UnknownChartNameRefused(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "a=1"))

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "no-such-chart"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `chart "no-such-chart" is not part of the definition of stack 42`)
	assert.Empty(t, api.puts)
}

func TestOverrideSetCmd_UnknownChartIDFromAPI(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "a=1"))

	const otherChart = "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a"
	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", otherChart})
	require.Error(t, err)
	assert.Equal(t, "chart "+otherChart+" is not part of the definition of stack 42", err.Error())
}

func TestOverrideGetCmd_TableByName(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "replicas: 2\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, overrideGetCmd.RunE(overrideGetCmd, []string{"42", "my-api"}))

	out := buf.String()
	assert.Regexp(t, `Chart ID:\s+`+valuesTestChartA, out)
	assert.Regexp(t, `Instance ID:\s+42`, out)
	assert.Contains(t, out, "Values:\nreplicas: 2\n")
}

func TestOverrideGetCmd_JSON(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartDB: "a: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, overrideGetCmd.RunE(overrideGetCmd, []string{"42", valuesTestChartDB}))

	var got types.ValueOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, valuesTestChartDB, got.ChartConfigID)
	assert.Equal(t, "42", got.StackInstanceID)
	assert.Equal(t, "a: 1\n", got.Values)
}

func TestOverrideGetCmd_NoOverride(t *testing.T) {
	server := startOverrideAPI(t, &overrideAPI{})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideGetCmd.RunE(overrideGetCmd, []string{"42", "my-db"})
	require.Error(t, err)
	assert.Equal(t, "chart my-db on stack 42 has no value override", err.Error())
}

func TestOverrideUnsetCmd_RemovesKeys(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "resources:\n  limits:\n    memory: 192Mi\nreplicas: 1\nimage:\n  tag: v1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	var stderr bytes.Buffer
	overrideUnsetCmd.SetErr(&stderr)
	t.Cleanup(func() { overrideUnsetCmd.SetErr(nil) })

	require.NoError(t, overrideUnsetCmd.RunE(overrideUnsetCmd, []string{"42", "my-api", "resources.limits.memory", "replicas", "missing.key"}))

	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"image": map[string]interface{}{"tag": "v1"}}, parseYAMLMap(t, api.puts[0]), "empty parent maps are removed")
	assert.Contains(t, buf.String(), "- replicas (removed)")
	assert.Contains(t, buf.String(), "- resources.limits.memory (removed)")
	assert.Contains(t, stderr.String(), `key "missing.key" is not set`)
}

func TestOverrideUnsetCmd_LastKeyRemovesOverride(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "replicas: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, overrideUnsetCmd.RunE(overrideUnsetCmd, []string{"42", "my-api", "replicas"}))

	require.Equal(t, []string{""}, api.puts, "an empty override is sent as empty values (the API removes it)")
	assert.NotContains(t, api.values, valuesTestChartA)
	assert.Contains(t, buf.String(), "Removed the value override of chart my-api on instance 42 (no keys left)")
}

func TestOverrideUnsetCmd_NoOverride(t *testing.T) {
	server := startOverrideAPI(t, &overrideAPI{})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideUnsetCmd.RunE(overrideUnsetCmd, []string{"42", "my-api", "replicas"})
	require.Error(t, err)
	assert.Equal(t, "chart my-api on stack 42 has no value override", err.Error())
}

func TestOverrideDeleteCmd_ByChartName(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartDB: "a: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	overrideDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideDeleteCmd.Flags().Set("yes", "false") })

	require.NoError(t, overrideDeleteCmd.RunE(overrideDeleteCmd, []string{"42", "my-db"}))
	assert.NotContains(t, api.values, valuesTestChartDB)
	assert.Contains(t, buf.String(), "Deleted value override for chart my-db on instance 42")
}

func TestOverrideBranchSetCmd_ByChartName(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, overrideBranchSetCmd.RunE(overrideBranchSetCmd, []string{"42", "my-api", "feature/x"}))
	assert.Equal(t, "feature/x", api.branches[valuesTestChartA])
	assert.Contains(t, buf.String(), `Set branch override "feature/x" for chart `+valuesTestChartA+` on instance 42`)
}

func TestOverrideBranchDeleteCmd_NoOverride(t *testing.T) {
	server := startOverrideAPI(t, &overrideAPI{})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	overrideBranchDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { overrideBranchDeleteCmd.Flags().Set("yes", "false") })

	err := overrideBranchDeleteCmd.RunE(overrideBranchDeleteCmd, []string{"42", "my-db"})
	require.Error(t, err)
	assert.Equal(t, "chart my-db on stack 42 has no branch override", err.Error())
}

func TestOverrideListCmd_ShowsIDsFromAPIShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"0b5c1e7a-2f4d-4c3b-9a8e-7d6f5e4c3b2a","stack_instance_id":"6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d","chart_config_id":"` + valuesTestChartA + `","values":"a: 1\n","updated_at":"2026-10-01T12:00:00Z"}]`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, overrideListCmd.RunE(overrideListCmd, []string{"6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{valuesTestChartA, "6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "true", "2026-10-01T12:00:00Z"}, strings.Fields(lines[1]))
}

func TestOverrideQuotaSetCmd_StorageAndPodLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		body, _ := io.ReadAll(r.Body)
		assert.JSONEq(t, `{"storage_limit":"10Gi","pod_limit":20}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"q1","stack_instance_id":"42","storage_limit":"10Gi","pod_limit":20}`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, overrideQuotaSetCmd.Flags().Set("storage-limit", "10Gi"))
	require.NoError(t, overrideQuotaSetCmd.Flags().Set("pod-limit", "20"))
	t.Cleanup(func() {
		resetFlag(t, overrideQuotaSetCmd.Flags(), "storage-limit", "")
		resetFlag(t, overrideQuotaSetCmd.Flags(), "pod-limit", "0")
	})

	require.NoError(t, overrideQuotaSetCmd.RunE(overrideQuotaSetCmd, []string{"42"}))
	var got types.QuotaOverride
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "42", got.StackInstanceID)
	require.NotNil(t, got.PodLimit)
	assert.Equal(t, 20, *got.PodLimit)
}

func TestDiffValueKeys(t *testing.T) {
	oldValues := map[string]interface{}{
		"a": 1,
		"b": map[string]interface{}{"c": "x", "d": "y"},
		"e": []interface{}{1, 2},
	}
	newValues := map[string]interface{}{
		"a": int64(1), // same value, other int type
		"b": map[string]interface{}{"c": "z"},
		"e": []interface{}{1, 2, 3},
		"f": true,
	}
	assert.Equal(t, []string{
		"~ b.c (changed)",
		"- b.d (removed)",
		"~ e (changed)",
		"+ f (added)",
	}, diffValueKeys(oldValues, newValues))
	assert.Empty(t, diffValueKeys(oldValues, deepCopyMap(oldValues)))
}

func TestUnsetNestedValue(t *testing.T) {
	m := map[string]interface{}{
		"a": map[string]interface{}{"b": map[string]interface{}{"c": 1}},
		"x": 1,
	}
	assert.False(t, unsetNestedValue(m, "a.b.missing"))
	assert.False(t, unsetNestedValue(m, "x.y"))
	assert.True(t, unsetNestedValue(m, "a.b.c"))
	assert.Equal(t, map[string]interface{}{"x": 1}, m)
	assert.True(t, unsetNestedValue(m, "x"))
	assert.Empty(t, m)
}

func TestOverrideSetCmd_SetValueWithCommaNotSplit(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "args=a,b,c"))
	require.NoError(t, overrideSetCmd.Flags().Set("set", "version=1.10"))
	require.NoError(t, overrideSetCmd.Flags().Set("set", "zip=0123"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"args": "a,b,c", "version": "1.10", "zip": "0123"}, parseYAMLMap(t, api.puts[0]))
}

func TestOverrideSetCmd_EscapedDotKey(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", `podAnnotations.prometheus\.io/scrape=true`))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"podAnnotations": map[string]interface{}{"prometheus.io/scrape": true}}, parseYAMLMap(t, api.puts[0]))
	assert.Contains(t, buf.String(), `+ podAnnotations.prometheus\.io/scrape (added)`)
}

func TestOverrideSetCmd_ListIndexRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("API should not be called for an invalid key")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "env[0].name=X"))

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list indexes are not supported; use --file")
}

func TestOverrideUnsetCmd_EscapedDotKey(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "podAnnotations:\n  prometheus.io/scrape: \"true\"\n  other: x\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	require.NoError(t, overrideUnsetCmd.RunE(overrideUnsetCmd, []string{"42", "my-api", `podAnnotations.prometheus\.io/scrape`}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"podAnnotations": map[string]interface{}{"other": "x"}}, parseYAMLMap(t, api.puts[0]))
}

func TestOverrideUnsetCmd_ListIndexRejected(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "a: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := overrideUnsetCmd.RunE(overrideUnsetCmd, []string{"42", "my-api", "a[0]"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list indexes are not supported")
	assert.Empty(t, api.puts)
}

func TestOverrideSetCmd_EmptyFileNeedsConfirmation(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "a: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	file := filepath.Join(t.TempDir(), "empty.yaml")
	require.NoError(t, os.WriteFile(file, nil, 0600))
	require.NoError(t, overrideSetCmd.Flags().Set("file", file))
	overrideSetCmd.SetIn(strings.NewReader(""))
	overrideSetCmd.SetErr(io.Discard)

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use --yes")
	assert.Empty(t, api.puts)
	assert.Contains(t, api.values, valuesTestChartA)
}

func TestOverrideSetCmd_EmptyFileWithYesRemoves(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "a: 1\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	file := filepath.Join(t.TempDir(), "empty.yaml")
	require.NoError(t, os.WriteFile(file, nil, 0600))
	require.NoError(t, overrideSetCmd.Flags().Set("file", file))
	require.NoError(t, overrideSetCmd.Flags().Set("yes", "true"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	assert.Equal(t, []string{""}, api.puts)
	assert.NotContains(t, api.values, valuesTestChartA)
	assert.Contains(t, buf.String(), "Removed the value override")
}

func TestOverrideSetCmd_MergeNonStringKeys(t *testing.T) {
	api := &overrideAPI{values: map[string]string{valuesTestChartA: "ports:\n  80: http\nnested:\n  1:\n    a: b\n"}}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "nested.1.c=d"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	got := parseYAMLMap(t, api.puts[0])
	assert.Equal(t, map[string]interface{}{"a": "b", "c": "d"}, got["nested"].(map[string]interface{})["1"], "a map under a non-string key must merge, not be replaced")
	assert.Equal(t, map[string]interface{}{"80": "http"}, got["ports"])
}

func TestOverrideSetCmd_DigitsChartNameIsNotAnID(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "a=1"))

	err := overrideSetCmd.RunE(overrideSetCmd, []string{"42", "123"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `chart "123" is not part of the definition of stack 42`)
	assert.Empty(t, api.puts)
}

func TestSplitKeyPath(t *testing.T) {
	tests := []struct {
		key     string
		want    []string
		wantErr string
	}{
		{key: "a", want: []string{"a"}},
		{key: "a.b.c", want: []string{"a", "b", "c"}},
		{key: `a\.b.c`, want: []string{"a.b", "c"}},
		{key: `a\\.b`, want: []string{`a\`, "b"}},
		{key: `a\b`, want: []string{`a\b`}},
		{key: "a..b", wantErr: "empty key part"},
		{key: ".a", wantErr: "empty key part"},
		{key: "a[0]", wantErr: "list indexes are not supported; use --file"},
		{key: "a]", wantErr: "list indexes are not supported"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.key, func(t *testing.T) {
			got, err := splitKeyPath(tt.key)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseValuesDocument(t *testing.T) {
	got, err := parseValuesDocument([]byte("a:\n  1: x\n  b: [ {2: y} ]\n"))
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{
		"a": map[string]interface{}{"1": "x", "b": []interface{}{map[string]interface{}{"2": "y"}}},
	}, got)

	got, err = parseValuesDocument(nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = parseValuesDocument([]byte("- a\n- b\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mapping")
}

func TestParseValuesDocument_LargeIntegers(t *testing.T) {
	// 2^53 + 1 is not exact as a float64; it must stay exact.
	got, err := parseValuesDocument([]byte(`{"big": 9007199254740993, "ratio": 1.5, "n": 3}`))
	require.NoError(t, err)
	assert.Equal(t, int64(9007199254740993), got["big"])
	assert.Equal(t, 1.5, got["ratio"])
	assert.Equal(t, int64(3), got["n"])

	out, err := yaml.Marshal(got)
	require.NoError(t, err)
	assert.Contains(t, string(out), "big: 9007199254740993")

	got, err = parseValuesDocument([]byte("big: 9007199254740993\n"))
	require.NoError(t, err)
	assert.EqualValues(t, 9007199254740993, got["big"])
}

func TestOverrideSetCmd_FileLargeIntegerKept(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	file := filepath.Join(t.TempDir(), "values.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"id": 9007199254740993}`), 0600))
	require.NoError(t, overrideSetCmd.Flags().Set("file", file))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, "id: 9007199254740993\n", api.puts[0])
}

func TestOverrideSetCmd_CaseInsensitiveBool(t *testing.T) {
	api := &overrideAPI{}
	server := startOverrideAPI(t, api)
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetOverrideSetFlags(t) })
	require.NoError(t, overrideSetCmd.Flags().Set("set", "a=False"))
	require.NoError(t, overrideSetCmd.Flags().Set("set", "b=TRUE"))

	require.NoError(t, overrideSetCmd.RunE(overrideSetCmd, []string{"42", "my-api"}))
	require.Len(t, api.puts, 1)
	assert.Equal(t, map[string]interface{}{"a": false, "b": true}, parseYAMLMap(t, api.puts[0]))
}
