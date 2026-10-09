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
	assert.Contains(t, out, "5")
	assert.Contains(t, out, "api-service")
	assert.Contains(t, out, "API microservice stack")
	assert.Contains(t, out, "admin")
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
	assert.Contains(t, err.Error(), "at least one of --name, --description, --branch, or --from-file must be specified")
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

// ---------- definition update --branch ----------

func TestDefinitionUpdateCmd_WithBranch(t *testing.T) {
	updated := sampleDefinition()
	updated.DefaultBranch = "develop"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-definitions/5", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.UpdateDefinitionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "develop", body.DefaultBranch)
		assert.Empty(t, body.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionUpdateCmd.Flags().Set("branch", "develop")
	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("branch", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"5"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "develop")
}

func TestDefinitionUpdateCmd_NoFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no flags provided")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	t.Cleanup(func() {
		definitionUpdateCmd.Flags().Set("name", "")
		definitionUpdateCmd.Flags().Set("description", "")
		definitionUpdateCmd.Flags().Set("branch", "")
		definitionUpdateCmd.Flags().Set("from-file", "")
	})

	err := definitionUpdateCmd.RunE(definitionUpdateCmd, []string{"5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

// ---------- definition update-chart ----------

func sampleChartConfig() types.ChartConfig {
	return types.ChartConfig{
		Base:            types.Base{ID: "1", Version: "1"},
		Name:            "api",
		RepoURL:         "https://charts.example.com",
		ChartName:       "api-chart",
		ChartPath:       "/charts/api",
		ChartVersion:    "2.0.0",
		SourceRepoURL:   "https://dev.azure.com/org/project/_git/repo",
		BuildPipelineID: "pipeline-42",
		DeployOrder:     8,
		ReleaseName:     "api-release",
		DefaultValues:   "replicas: 1\nimage: app:latest",
	}
}

func TestDefinitionUpdateChartCmd_ChartVersion(t *testing.T) {
	chart := sampleChartConfig()
	reqCount := 0
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.URL.Path, "/api/v1/stack-definitions/5/charts/1")
		reqCount++
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}

		require.Equal(t, http.MethodPut, r.Method)
		var body types.UpdateChartConfigRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "3.0.0", body.ChartVersion)
		assert.Equal(t, chart.ChartName, body.ChartName)
		// Fields not touched by the user flag must round-trip from the GET
		// response back into the PUT body — that's the wipe-fix this PR exists for.
		assert.Equal(t, chart.RepoURL, body.RepositoryURL)
		assert.Equal(t, chart.ChartPath, body.ChartPath)
		assert.Equal(t, chart.SourceRepoURL, body.SourceRepoURL)
		assert.Equal(t, chart.BuildPipelineID, body.BuildPipelineID)
		assert.Equal(t, chart.DefaultValues, body.DefaultValues)
		require.NotNil(t, body.DeployOrder)
		assert.Equal(t, chart.DeployOrder, *body.DeployOrder)

		chart.ChartVersion = "3.0.0"
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	definitionUpdateChartCmd.Flags().Set("chart-version", "3.0.0")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)
	assert.Equal(t, 2, reqCount)
	assert.Contains(t, buf.String(), "api-chart")
}

func TestDefinitionUpdateChartCmd_ValuesFromFile(t *testing.T) {
	chart := sampleChartConfig()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		var body types.UpdateChartConfigRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "replicas: 3\nimage: app:v2\n", body.DefaultValues)

		chart.DefaultValues = body.DefaultValues
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	require.NoError(t, os.WriteFile(valuesPath, []byte("replicas: 3\nimage: app:v2\n"), 0644))

	buf := setupStackTestCmd(t, server.URL)

	definitionUpdateChartCmd.Flags().Set("file", valuesPath)
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "api-chart")
}

func TestDefinitionUpdateChartCmd_DeployOrder(t *testing.T) {
	chart := sampleChartConfig()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		var body types.UpdateChartConfigRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.NotNil(t, body.DeployOrder)
		assert.Equal(t, 6, *body.DeployOrder)

		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateChartCmd.Flags().Set("deploy-order", "6")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)
}

func TestDefinitionUpdateChartCmd_RepositoryURL(t *testing.T) {
	chart := sampleChartConfig()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		var body types.UpdateChartConfigRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "oci://acr.example.com/helm", body.RepositoryURL)
		// Other fields still seeded from GET.
		assert.Equal(t, chart.ChartPath, body.ChartPath)
		assert.Equal(t, chart.BuildPipelineID, body.BuildPipelineID)

		chart.RepoURL = body.RepositoryURL
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateChartCmd.Flags().Set("repository-url", "oci://acr.example.com/helm")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)
}

func TestDefinitionUpdateChartCmd_RepositoryURL_Invalid(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when --repository-url is invalid")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
	})

	cases := []string{
		"not-a-url",
		"oci:/acr.example.com/helm",
		"ftp://acr.example.com/helm",
	}
	for _, raw := range cases {
		definitionUpdateChartCmd.Flags().Set("repository-url", raw)
		err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), "--repository-url")
	}
}

func TestDefinitionUpdateChartCmd_NoFlags(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no flags provided")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestDefinitionUpdateChartCmd_JSONOutput(t *testing.T) {
	chart := sampleChartConfig()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		chart.ChartVersion = "3.0.0"
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	definitionUpdateChartCmd.Flags().Set("chart-version", "3.0.0")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)

	var result types.ChartConfig
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "1", result.ID)
}

func TestDefinitionUpdateChartCmd_QuietOutput(t *testing.T) {
	chart := sampleChartConfig()
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	definitionUpdateChartCmd.Flags().Set("chart-version", "3.0.0")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
		definitionUpdateChartCmd.Flags().Set("chart-path", "")
		definitionUpdateChartCmd.Flags().Set("source-repo-url", "")
		definitionUpdateChartCmd.Flags().Set("repository-url", "")
		definitionUpdateChartCmd.Flags().Set("deploy-order", "-1")
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestDefinitionUpdateChartCmd_PathTraversal(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called for path traversal")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	definitionUpdateChartCmd.Flags().Set("file", "../../../etc/passwd")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("file", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestDefinitionUpdateChartCmd_BuildPipelineID(t *testing.T) {
	chart := sampleChartConfig()
	var put types.UpdateChartConfigRequest
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(chart)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&put))
		json.NewEncoder(w).Encode(chart)
	}))
	defer server.Close()

	setupStackTestCmd(t, server.URL)
	require.NoError(t, definitionUpdateChartCmd.Flags().Set("build-pipeline-id", "42"))
	t.Cleanup(func() { _ = definitionUpdateChartCmd.Flags().Set("build-pipeline-id", "") })

	require.NoError(t, definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"5", "1"}))
	assert.Equal(t, "42", put.BuildPipelineID)
	assert.Equal(t, chart.ChartVersion, put.ChartVersion)
	assert.Equal(t, chart.SourceRepoURL, put.SourceRepoURL)
}

// ---------- definition name resolution and chart IDs ----------

// startDefinitionNameServer serves a definition that is found by name
// (GET /stack-definitions?name=) and by ID, with two charts.
func startDefinitionNameServer(t *testing.T, onRequest func(r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/stack-definitions" && r.Method == http.MethodGet:
			assert.Equal(t, "example-dev", r.URL.Query().Get("name"))
			_, _ = w.Write([]byte(`{"data":[{"id":"` + valuesTestDefID + `","name":"example-dev"}],"total":1,"page":1,"pageSize":25}`))
		case r.URL.Path == "/api/v1/stack-definitions/"+valuesTestDefID && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"` + valuesTestDefID + `","name":"example-dev","owner_id":"0f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f","default_branch":"master","charts":[` +
				`{"id":"` + valuesTestChartDB + `","chart_name":"my-db","repository_url":"oci://registry.example.com/helm","chart_version":"1.0.0","deploy_order":0},` +
				`{"id":"` + valuesTestChartA + `","chart_name":"my-api","repository_url":"oci://registry.example.com/helm","chart_version":"2.1.0","deploy_order":1}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestDefinitionGetCmd_ByNameShowsChartIDs(t *testing.T) {
	server := startDefinitionNameServer(t, nil)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, definitionGetCmd.RunE(definitionGetCmd, []string{"example-dev"}))

	out := buf.String()
	assert.Regexp(t, `ID:\s+`+valuesTestDefID, out)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	tail := lines[len(lines)-3:]
	assert.Equal(t, []string{"CHART", "ID", "CHART", "REPOSITORY", "VERSION", "ORDER"}, strings.Fields(tail[0]))
	assert.Equal(t, []string{valuesTestChartDB, "my-db", "oci://registry.example.com/helm", "1.0.0", "0"}, strings.Fields(tail[1]))
	assert.Equal(t, []string{valuesTestChartA, "my-api", "oci://registry.example.com/helm", "2.1.0", "1"}, strings.Fields(tail[2]))
}

func TestDefinitionGetCmd_UnknownName(t *testing.T) {
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := definitionGetCmd.RunE(definitionGetCmd, []string{"no-such-def"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no definition found with name "no-such-def"`)
}

func TestDefinitionDeleteCmd_ByName(t *testing.T) {
	deleted := ""
	server := httptest.NewServer(withChartLookup(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			deleted = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"` + valuesTestDefID + `","name":"example-dev"}],"total":1}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	definitionDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { definitionDeleteCmd.Flags().Set("yes", "false") })

	require.NoError(t, definitionDeleteCmd.RunE(definitionDeleteCmd, []string{"example-dev"}))
	assert.Equal(t, "/api/v1/stack-definitions/"+valuesTestDefID, deleted)
}

func TestDefinitionUpdateChartCmd_ByNames(t *testing.T) {
	var putPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/stack-definitions":
			_, _ = w.Write([]byte(`{"data":[{"id":"` + valuesTestDefID + `","name":"example-dev"}],"total":1}`))
		case r.URL.Path == "/api/v1/stack-definitions/"+valuesTestDefID:
			_, _ = w.Write([]byte(`{"id":"` + valuesTestDefID + `","name":"example-dev","charts":[{"id":"` + valuesTestChartA + `","chart_name":"my-api"}]}`))
		case r.URL.Path == "/api/v1/stack-definitions/"+valuesTestDefID+"/charts/"+valuesTestChartA:
			if r.Method == http.MethodPut {
				putPath = r.URL.Path
			}
			_, _ = w.Write([]byte(`{"id":"` + valuesTestChartA + `","chart_name":"my-api","chart_version":"3.0.0"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	definitionUpdateChartCmd.Flags().Set("chart-version", "3.0.0")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
	})

	require.NoError(t, definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"example-dev", "my-api"}))
	assert.Equal(t, "/api/v1/stack-definitions/"+valuesTestDefID+"/charts/"+valuesTestChartA, putPath)
}

func TestDefinitionUpdateChartCmd_UnknownChartName(t *testing.T) {
	server := startDefinitionNameServer(t, func(r *http.Request) {
		assert.NotEqual(t, http.MethodPut, r.Method, "no update for an unknown chart")
	})
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	definitionUpdateChartCmd.Flags().Set("chart-version", "3.0.0")
	t.Cleanup(func() {
		definitionUpdateChartCmd.Flags().Set("chart-version", "")
	})

	err := definitionUpdateChartCmd.RunE(definitionUpdateChartCmd, []string{"example-dev", "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `chart "nope" is not part of definition `+valuesTestDefID+` (charts: my-db, my-api)`)
}

// ---------- owner display name ----------

func TestDefinitionOwnerDisplayName(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		wantList  string
		wantGetRe string
	}{
		{name: "username present", username: "alice", wantList: "alice", wantGetRe: `Owner:\s+alice \(admin\)`},
		{name: "username absent", username: "", wantList: "admin", wantGetRe: `Owner:\s+admin\n`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			def := sampleDefinition()
			def.OwnerUsername = tt.username
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/stack-definitions" {
					_ = json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
						Data: []types.StackDefinition{def}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
					})
					return
				}
				_ = json.NewEncoder(w).Encode(def)
			}))
			defer server.Close()

			buf := setupStackTestCmd(t, server.URL)
			require.NoError(t, definitionListCmd.RunE(definitionListCmd, []string{}))
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			require.Len(t, lines, 2)
			fields := strings.Fields(lines[1])
			assert.Equal(t, tt.wantList, fields[len(fields)-1])

			buf.Reset()
			require.NoError(t, definitionGetCmd.RunE(definitionGetCmd, []string{"5"}))
			assert.Regexp(t, tt.wantGetRe, buf.String())
		})
	}
}
