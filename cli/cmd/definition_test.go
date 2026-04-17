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
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

// sampleDefinition returns a StackDefinition used across definition tests.
func sampleDefinition() types.StackDefinition {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.StackDefinition{
		Base:          types.Base{ID: "5", CreatedAt: now, UpdatedAt: now, Version: "1"},
		Name:          "api-service",
		Description:   "API microservice stack",
		DefaultBranch: "main",
		Owner:         "admin",
		Charts: []types.ChartConfig{
			{
				Base:         types.Base{ID: "1"},
				Name:         "api",
				RepoURL:      "https://charts.example.com",
				ChartName:    "api-chart",
				ChartVersion: "2.0.0",
			},
		},
	}
}

// ---------- definition list ----------

func TestDefinitionListCmd_TableOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{def}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "DESCRIPTION")
	assert.Contains(t, out, "OWNER")
	assert.Contains(t, out, "CHARTS")
	assert.Contains(t, out, "5")
	assert.Contains(t, out, "api-service")
	assert.Contains(t, out, "API microservice stack")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "1")
}

func TestDefinitionListCmd_JSONOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{def}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.NoError(t, err)

	var result types.ListResponse[types.StackDefinition]
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, "api-service", result.Data[0].Name)
}

func TestDefinitionListCmd_YAMLOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{def}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: api-service")
}

func TestDefinitionListCmd_QuietOutput(t *testing.T) {
	d1 := sampleDefinition()
	d2 := sampleDefinition()
	d2.ID = "15"
	d2.Name = "second-def"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{d1, d2}, Total: 2, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "5\n15", lines)
}

func TestDefinitionListCmd_WithMineFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "me", r.URL.Query().Get("owner"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionListCmd.Flags().Set("mine", "true")
	t.Cleanup(func() {
		definitionListCmd.Flags().Set("mine", "false")
	})

	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.NoError(t, err)
}

func TestDefinitionListCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "database error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := definitionListCmd.RunE(definitionListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
}

// ---------- definition get ----------

func TestDefinitionGetCmd_TableOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(def)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := definitionGetCmd.RunE(definitionGetCmd, []string{"5"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "5")
	assert.Contains(t, out, "api-service")
	assert.Contains(t, out, "API microservice stack")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "api")
}

func TestDefinitionGetCmd_JSONOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(def)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := definitionGetCmd.RunE(definitionGetCmd, []string{"5"})
	require.NoError(t, err)

	var result types.StackDefinition
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "5", result.ID)
	assert.Equal(t, "api-service", result.Name)
}

func TestDefinitionGetCmd_QuietOutput(t *testing.T) {
	def := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(def)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := definitionGetCmd.RunE(definitionGetCmd, []string{"5"})
	require.NoError(t, err)
	assert.Equal(t, "5\n", buf.String())
}

func TestDefinitionGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := definitionGetCmd.RunE(definitionGetCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")
}

// ---------- definition create ----------

func TestDefinitionCreateCmd_WithNameFlag(t *testing.T) {
	created := sampleDefinition()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.CreateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "new-def", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("name", "new-def")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("description", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "5")
	assert.Contains(t, out, "api-service")
}

func TestDefinitionCreateCmd_WithDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.CreateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "test-def", body.Name)
		assert.Equal(t, "My description", body.Description)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "20"}, Name: "test-def"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("name", "test-def")
	definitionCreateCmd.Flags().Set("description", "My description")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("description", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "20")
}

func TestDefinitionCreateCmd_WithFromFile(t *testing.T) {
	defJSON := `{"name": "file-def", "description": "from file", "charts": []}`

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "definition.json")
	require.NoError(t, os.WriteFile(filePath, []byte(defJSON), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.CreateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "file-def", body.Name)
		assert.Equal(t, "from file", body.Description)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "25"}, Name: "file-def"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("from-file", filePath)
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("description", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "25")
}

func TestDefinitionCreateCmd_MissingName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when --name is missing")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("name", "")
	definitionCreateCmd.Flags().Set("from-file", "")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name is required")
}

func TestDefinitionCreateCmd_FromFileNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when file doesn't exist")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("from-file", "/nonexistent/path/file.json")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading file")
}

func TestDefinitionCreateCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "30"}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	definitionCreateCmd.Flags().Set("name", "quiet-def")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.NoError(t, err)
	assert.Equal(t, "30\n", buf.String())
}

// ---------- definition update ----------

func TestDefinitionUpdateCmd_WithName(t *testing.T) {
	updated := sampleDefinition()
	updated.Name = "updated-def"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.UpdateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "updated-def", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("name", "updated-def")
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"5"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "updated-def")
}

func TestDefinitionUpdateCmd_WithFromFile(t *testing.T) {
	updateJSON := `{"name": "file-update", "description": "updated from file"}`

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "update.json")
	require.NoError(t, os.WriteFile(filePath, []byte(updateJSON), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.UpdateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "file-update", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "5"}, Name: "file-update"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("from-file", filePath)
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"5"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "file-update")
}

func TestDefinitionUpdateCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "version mismatch"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version mismatch")
}

// ---------- definition delete ----------

func TestDefinitionDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { definitionDeleteCmd.Flags().Set("yes", "false") })

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"5"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called with --yes flag")
	assert.Contains(t, buf.String(), "Deleted definition 5")
}

func TestDefinitionDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		definitionDeleteCmd.Flags().Set("yes", "false")
		definitionDeleteCmd.SetIn(nil)
		definitionDeleteCmd.SetErr(nil)
	})

	definitionDeleteCmd.SetIn(strings.NewReader("y\n"))
	definitionDeleteCmd.SetErr(&bytes.Buffer{})

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"5"})
	require.NoError(t, err)
	assert.True(t, called, "API should be called after confirming with y")
	assert.Contains(t, buf.String(), "Deleted definition 5")
}

func TestDefinitionDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		definitionDeleteCmd.Flags().Set("yes", "false")
		definitionDeleteCmd.SetIn(nil)
		definitionDeleteCmd.SetErr(nil)
	})

	definitionDeleteCmd.SetIn(strings.NewReader("n\n"))
	definitionDeleteCmd.SetErr(&bytes.Buffer{})

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"5"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestDefinitionDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { definitionDeleteCmd.Flags().Set("yes", "false") })

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")
}

func TestDefinitionDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	definitionDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { definitionDeleteCmd.Flags().Set("yes", "false") })

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"5"})
	require.NoError(t, err)
	assert.Equal(t, "5\n", buf.String())
}

// ---------- definition export ----------

func TestDefinitionExportCmd_ToStdout(t *testing.T) {
	exportData := `{"name":"exported","description":"test","charts":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions/5/export", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(exportData))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionExportCmd.Flags().Set("output-file", "")
	t.Cleanup(func() {
		definitionExportCmd.Flags().Set("output-file", "")
	})

	err := definitionExportCmd.RunE(definitionExportCmd, []string{"5"})
	require.NoError(t, err)
	assert.Equal(t, exportData, buf.String())
}

func TestDefinitionExportCmd_ToFile(t *testing.T) {
	exportData := `{"name":"exported","description":"test","charts":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(exportData))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "export.json")

	definitionExportCmd.Flags().Set("output-file", outFile)
	t.Cleanup(func() {
		definitionExportCmd.Flags().Set("output-file", "")
	})

	err := definitionExportCmd.RunE(definitionExportCmd, []string{"5"})
	require.NoError(t, err)

	// File should contain the export data
	written, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, exportData, string(written))

	// Stdout should have confirmation message
	assert.Contains(t, buf.String(), "Exported definition 5")
}

func TestDefinitionExportCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := definitionExportCmd.RunE(definitionExportCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")
}

// ---------- definition import ----------

func TestDefinitionImportCmd_Success(t *testing.T) {
	importData := `{"name":"imported-def","description":"imported","charts":[]}`

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "import.json")
	require.NoError(t, os.WriteFile(filePath, []byte(importData), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions/import", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "50"}, Name: "imported-def"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionImportCmd.Flags().Set("file", filePath)
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "50")
	assert.Contains(t, out, "imported-def")
}

func TestDefinitionImportCmd_MissingFileFlag(t *testing.T) {
	// Verify that --file is marked as required via Cobra annotations.
	fileFlag := definitionImportCmd.Flags().Lookup("file")
	require.NotNil(t, fileFlag)
	assert.Contains(t, fileFlag.Annotations, cobra.BashCompOneRequiredFlag)
}

func TestDefinitionImportCmd_NonexistentFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when file doesn't exist")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionImportCmd.Flags().Set("file", "/nonexistent/import.json")
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading file")
}

func TestDefinitionImportCmd_ServerError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "import.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"name":"test"}`), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition already exists"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionImportCmd.Flags().Set("file", filePath)
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition already exists")
}

func TestDefinitionImportCmd_QuietOutput(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "import.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"name":"test"}`), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "55"}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	definitionImportCmd.Flags().Set("file", filePath)
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.NoError(t, err)
	assert.Equal(t, "55\n", buf.String())
}

// ---------- path traversal rejection ----------

func TestDefinitionCreateCmd_FromFilePathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal attempt")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("from-file", "../../etc/passwd")
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("description", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path must not contain '..'")
}

func TestDefinitionUpdateCmd_FromFilePathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal attempt")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("from-file", "../secret.json")
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path must not contain '..'")
}

func TestDefinitionExportCmd_OutputFilePathTraversal(t *testing.T) {
	exportData := `{"name":"exported","description":"test","charts":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(exportData))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionExportCmd.Flags().Set("output-file", "../../evil.json")
	t.Cleanup(func() {
		definitionExportCmd.Flags().Set("output-file", "")
	})

	err := definitionExportCmd.RunE(definitionExportCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output file path must not contain '..'")
}

func TestDefinitionImportCmd_FilePathTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal attempt")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionImportCmd.Flags().Set("file", "../../../hack.json")
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path must not contain '..'")
}

// ---------- update requires at least one flag ----------

func TestDefinitionUpdateCmd_NoFlagsSpecified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no flags are specified")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("name", "")
	definitionUpdateCmd.Flags().Set("description", "")
	definitionUpdateCmd.Flags().Set("from-file", "")
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of --name, --description, or --from-file must be specified")
}

// ---------- create --from-file requires name field ----------

func TestDefinitionCreateCmd_FromFileMissingNameField(t *testing.T) {
	defJSON := `{"description": "no name"}`

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "no-name.json")
	require.NoError(t, os.WriteFile(filePath, []byte(defJSON), 0644))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when name is missing from file")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionCreateCmd.Flags().Set("from-file", filePath)
	t.Cleanup(func() {
		definitionCreateCmd.Flags().Set("name", "")
		definitionCreateCmd.Flags().Set("description", "")
		definitionCreateCmd.Flags().Set("from-file", "")
	})

	err := definitionCreateCmd.RunE(definitionCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'name' field is required")
}

// ---------- definition delete auth error ----------

func TestDefinitionDeleteCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { definitionDeleteCmd.Flags().Set("yes", "false") })

	err := definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ---------- definition import edge cases ----------

func TestDefinitionImportCmd_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for invalid JSON content")
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "bad.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{invalid json}`), 0644))

	_ = setupStackTestCmd(t, server.URL)

	definitionImportCmd.Flags().Set("file", filePath)
	t.Cleanup(func() {
		definitionImportCmd.Flags().Set("file", "")
	})

	err := definitionImportCmd.RunE(definitionImportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}
