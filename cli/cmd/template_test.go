package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// sampleTemplate returns a StackTemplate used across template tests.
func sampleTemplate() types.StackTemplate {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	return types.StackTemplate{
		Base:            types.Base{ID: "10", CreatedAt: now, UpdatedAt: now, Version: "1"},
		Name:            "web-app-template",
		Description:     "Full web app stack",
		Published:       true,
		Owner:           "admin",
		DefinitionCount: 2,
		Charts: []types.ChartConfig{
			{
				Base:         types.Base{ID: "1"},
				Name:         "frontend",
				RepoURL:      "https://charts.example.com",
				ChartName:    "react-app",
				ChartVersion: "1.2.0",
			},
			{
				Base:         types.Base{ID: "2"},
				Name:         "backend",
				RepoURL:      "https://charts.example.com",
				ChartName:    "api-server",
				ChartVersion: "3.0.0",
			},
		},
	}
}

// ---------- template list ----------

func TestTemplateListCmd_TableOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{tmpl}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "DESCRIPTION")
	assert.Contains(t, out, "PUBLISHED")
	assert.Contains(t, out, "DEFINITIONS")
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
	assert.Contains(t, out, "Full web app stack")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "2")
}

func TestTemplateListCmd_JSONOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{tmpl}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)

	var result types.ListResponse[types.StackTemplate]
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, "web-app-template", result.Data[0].Name)
}

func TestTemplateListCmd_YAMLOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{tmpl}, Total: 1, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: web-app-template")
}

func TestTemplateListCmd_QuietOutput(t *testing.T) {
	t1 := sampleTemplate()
	t2 := sampleTemplate()
	t2.ID = "20"
	t2.Name = "second-template"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{t1, t2}, Total: 2, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "10\n20", lines)
}

func TestTemplateListCmd_WithPublishedFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.URL.Query().Get("published"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	templateListCmd.Flags().Set("published", "true")
	t.Cleanup(func() {
		templateListCmd.Flags().Set("published", "false")
	})

	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)
}

func TestTemplateListCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

func TestTemplateListCmd_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NAME")
}

// ---------- template get ----------

func TestTemplateGetCmd_TableOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateGetCmd.RunE(templateGetCmd, []string{"10"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
	assert.Contains(t, out, "Full web app stack")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "react-app")
	assert.Contains(t, out, "api-server")
}

func TestTemplateGetCmd_JSONOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateGetCmd.RunE(templateGetCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "10", result.ID)
	assert.Equal(t, "web-app-template", result.Name)
	assert.True(t, result.Published)
}

func TestTemplateGetCmd_QuietOutput(t *testing.T) {
	tmpl := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateGetCmd.RunE(templateGetCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

func TestTemplateGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateGetCmd.RunE(templateGetCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

// ---------- template instantiate ----------

func TestTemplateInstantiateCmd_Success(t *testing.T) {
	def := types.StackDefinition{
		Base:  types.Base{ID: "42"},
		Name:  "my-def",
		Owner: "uid-1",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/instantiate", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.InstantiateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-instance", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(def)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateInstantiateCmd.Flags().Set("name", "my-instance")
	t.Cleanup(func() {
		templateInstantiateCmd.Flags().Set("name", "")
		templateInstantiateCmd.Flags().Set("branch", "")
		templateInstantiateCmd.Flags().Set("cluster", "0")
	})

	err := templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "my-def")
}

func TestTemplateInstantiateCmd_WithBranchAndCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.InstantiateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-instance", body.Name)
		assert.Equal(t, "feature/xyz", body.Branch)
		assert.Equal(t, "2", body.ClusterID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "50"}, Name: "my-instance"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateInstantiateCmd.Flags().Set("name", "my-instance")
	templateInstantiateCmd.Flags().Set("branch", "feature/xyz")
	templateInstantiateCmd.Flags().Set("cluster", "2")
	t.Cleanup(func() {
		templateInstantiateCmd.Flags().Set("name", "")
		templateInstantiateCmd.Flags().Set("branch", "")
		templateInstantiateCmd.Flags().Set("cluster", "0")
	})

	err := templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "50")
	assert.Contains(t, out, "my-instance")
}

func TestTemplateInstantiateCmd_MissingName(t *testing.T) {
	// Verify that --name is marked as required via Cobra annotations.
	nameFlag := templateInstantiateCmd.Flags().Lookup("name")
	require.NotNil(t, nameFlag)
	assert.Contains(t, nameFlag.Annotations, cobra.BashCompOneRequiredFlag)
}

func TestTemplateInstantiateCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template instantiation failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	templateInstantiateCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateInstantiateCmd.Flags().Set("name", "")
	})

	err := templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template instantiation failed")
}

func TestTemplateInstantiateCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackDefinition{Base: types.Base{ID: "50"}})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	templateInstantiateCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateInstantiateCmd.Flags().Set("name", "")
	})

	err := templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "50\n", buf.String())
}

// ---------- template quick-deploy ----------

func TestTemplateQuickDeployCmd_Success(t *testing.T) {
	instance := sampleStack()
	instance.Status = "deploying"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/quick-deploy", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.QuickDeployRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "quick-stack", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(instance)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateQuickDeployCmd.Flags().Set("name", "quick-stack")
	t.Cleanup(func() {
		templateQuickDeployCmd.Flags().Set("name", "")
		templateQuickDeployCmd.Flags().Set("branch", "")
		templateQuickDeployCmd.Flags().Set("cluster", "0")
	})

	err := templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "my-stack")
}

func TestTemplateQuickDeployCmd_MissingName(t *testing.T) {
	nameFlag := templateQuickDeployCmd.Flags().Lookup("name")
	require.NotNil(t, nameFlag)
	assert.Contains(t, nameFlag.Annotations, cobra.BashCompOneRequiredFlag)
}

func TestTemplateQuickDeployCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "quick deploy failed"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	templateQuickDeployCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateQuickDeployCmd.Flags().Set("name", "")
	})

	err := templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quick deploy failed")
}

func TestTemplateQuickDeployCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.StackInstance{Base: types.Base{ID: "60"}, Status: "deploying"})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	templateQuickDeployCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateQuickDeployCmd.Flags().Set("name", "")
	})

	err := templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "60\n", buf.String())
}

func TestTemplateQuickDeployCmd_JSONOutput(t *testing.T) {
	instance := types.StackInstance{Base: types.Base{ID: "60"}, Name: "quick-stack", Status: "deploying"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(instance)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	templateQuickDeployCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateQuickDeployCmd.Flags().Set("name", "")
	})

	err := templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackInstance
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "60", result.ID)
	assert.Equal(t, "deploying", result.Status)
}

// ---------- template list auth error ----------

func TestTemplateListCmd_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateListCmd.RunE(templateListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
}

// ---------- template instantiate auth error ----------

func TestTemplateInstantiateCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	templateInstantiateCmd.Flags().Set("name", "test")
	t.Cleanup(func() {
		templateInstantiateCmd.Flags().Set("name", "")
	})

	err := templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ---------- template delete ----------

func TestTemplateDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/templates/10", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { templateDeleteCmd.Flags().Set("yes", "false") })

	err := templateDeleteCmd.RunE(templateDeleteCmd, []string{"10"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted template 10")
}

func TestTemplateDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		templateDeleteCmd.Flags().Set("yes", "false")
		templateDeleteCmd.SetIn(nil)
		templateDeleteCmd.SetErr(nil)
	})

	templateDeleteCmd.SetIn(strings.NewReader("y\n"))
	templateDeleteCmd.SetErr(&bytes.Buffer{})

	err := templateDeleteCmd.RunE(templateDeleteCmd, []string{"10"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted template 10")
}

func TestTemplateDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	templateDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		templateDeleteCmd.Flags().Set("yes", "false")
		templateDeleteCmd.SetIn(nil)
		templateDeleteCmd.SetErr(nil)
	})

	templateDeleteCmd.SetIn(strings.NewReader("n\n"))
	templateDeleteCmd.SetErr(&bytes.Buffer{})

	err := templateDeleteCmd.RunE(templateDeleteCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestTemplateDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	templateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { templateDeleteCmd.Flags().Set("yes", "false") })

	err := templateDeleteCmd.RunE(templateDeleteCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

func TestTemplateDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	templateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { templateDeleteCmd.Flags().Set("yes", "false") })

	err := templateDeleteCmd.RunE(templateDeleteCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

// ---------- template create ----------

func TestTemplateCreateCmd_WithNameFlag(t *testing.T) {
	created := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.CreateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-template", body.Name)
		assert.Equal(t, "A template", body.Description)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	templateCreateCmd.Flags().Set("name", "my-template")
	templateCreateCmd.Flags().Set("description", "A template")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
}

func TestTemplateCreateCmd_JSONOutput(t *testing.T) {
	created := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	templateCreateCmd.Flags().Set("name", "my-template")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "10", result.ID)
	assert.Equal(t, "web-app-template", result.Name)
}

func TestTemplateCreateCmd_YAMLOutput(t *testing.T) {
	created := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	templateCreateCmd.Flags().Set("name", "my-template")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

func TestTemplateCreateCmd_QuietOutput(t *testing.T) {
	created := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	templateCreateCmd.Flags().Set("name", "my-template")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

func TestTemplateCreateCmd_FromFile(t *testing.T) {
	tmpFile := t.TempDir() + "/template.json"
	payload := `{"name":"file-template","description":"from file"}`
	require.NoError(t, os.WriteFile(tmpFile, []byte(payload), 0o600))

	created := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.CreateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "file-template", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	templateCreateCmd.Flags().Set("from-file", tmpFile)
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "10")
}

func TestTemplateCreateCmd_MissingName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when --name is missing")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	templateCreateCmd.Flags().Set("name", "")
	templateCreateCmd.Flags().Set("from-file", "")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name is required")
}

func TestTemplateCreateCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal server error"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	templateCreateCmd.Flags().Set("name", "my-template")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("name", "")
		templateCreateCmd.Flags().Set("description", "")
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
}

// ---------- template update ----------

func TestTemplateUpdateCmd_WithNameFlag(t *testing.T) {
	updated := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.UpdateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "new-name", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	templateUpdateCmd.Flags().Set("name", "new-name")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
}

func TestTemplateUpdateCmd_JSONOutput(t *testing.T) {
	updated := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	templateUpdateCmd.Flags().Set("name", "new-name")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "10", result.ID)
}

func TestTemplateUpdateCmd_QuietOutput(t *testing.T) {
	updated := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	templateUpdateCmd.Flags().Set("name", "new-name")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

func TestTemplateUpdateCmd_MissingFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when no update flags provided")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	templateUpdateCmd.Flags().Set("name", "")
	templateUpdateCmd.Flags().Set("description", "")
	templateUpdateCmd.Flags().Set("from-file", "")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of")
}

func TestTemplateUpdateCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	templateUpdateCmd.Flags().Set("name", "new-name")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

// ---------- template clone ----------

func TestTemplateCloneCmd_Success(t *testing.T) {
	cloned := sampleTemplate()
	cloned.ID = "20"
	cloned.Name = "my-clone"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/clone", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.CloneTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-clone", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cloned)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	templateCloneCmd.Flags().Set("name", "my-clone")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "20")
	assert.Contains(t, out, "my-clone")
}

func TestTemplateCloneCmd_JSONOutput(t *testing.T) {
	cloned := sampleTemplate()
	cloned.ID = "20"
	cloned.Name = "my-clone"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cloned)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	templateCloneCmd.Flags().Set("name", "my-clone")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "20", result.ID)
	assert.Equal(t, "my-clone", result.Name)
}

func TestTemplateCloneCmd_QuietOutput(t *testing.T) {
	cloned := sampleTemplate()
	cloned.ID = "20"
	cloned.Name = "my-clone"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cloned)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	templateCloneCmd.Flags().Set("name", "my-clone")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "20\n", buf.String())
}

func TestTemplateCloneCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	templateCloneCmd.Flags().Set("name", "my-clone")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"999"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

// ---------- template create – path traversal ----------

func TestTemplateCreateCmd_FromFilePathTraversal(t *testing.T) {
	_ = setupStackTestCmd(t, "http://127.0.0.1:1") // no server needed
	templateCreateCmd.Flags().Set("from-file", "../../etc/passwd")
	t.Cleanup(func() {
		templateCreateCmd.Flags().Set("from-file", "")
	})

	err := templateCreateCmd.RunE(templateCreateCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
}

func TestTemplateUpdateCmd_FromFilePathTraversal(t *testing.T) {
	_ = setupStackTestCmd(t, "http://127.0.0.1:1")
	templateUpdateCmd.Flags().Set("from-file", "../../etc/passwd")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
}

// ---------- template update – from-file ----------

func TestTemplateUpdateCmd_FromFile(t *testing.T) {
	updated := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)

		var body types.UpdateTemplateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "from-file-name", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "tmpl-*.json")
	require.NoError(t, err)
	_, err = tmpFile.WriteString(`{"name":"from-file-name"}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	buf := setupStackTestCmd(t, server.URL)
	templateUpdateCmd.Flags().Set("from-file", tmpFile.Name())
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err = templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

// ---------- template update – YAML output ----------

func TestTemplateUpdateCmd_YAMLOutput(t *testing.T) {
	updated := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(updated)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	templateUpdateCmd.Flags().Set("name", "updated-name")
	t.Cleanup(func() {
		templateUpdateCmd.Flags().Set("name", "")
		templateUpdateCmd.Flags().Set("description", "")
		templateUpdateCmd.Flags().Set("from-file", "")
	})

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

// ---------- template clone – YAML output ----------

func TestTemplateCloneCmd_YAMLOutput(t *testing.T) {
	cloned := sampleTemplate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cloned)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	templateCloneCmd.Flags().Set("name", "my-clone")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

// ---------- template clone – empty name guard ----------

func TestTemplateCloneCmd_EmptyName(t *testing.T) {
	_ = setupStackTestCmd(t, "http://127.0.0.1:1")
	// --name set to empty string to bypass Cobra's required check
	templateCloneCmd.Flags().Set("name", "")
	t.Cleanup(func() {
		templateCloneCmd.Flags().Set("name", "")
	})

	err := templateCloneCmd.RunE(templateCloneCmd, []string{"1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name must not be empty")
}

// ---------- template publish ----------

func TestTemplatePublishCmd_Success(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/publish", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
}

func TestTemplatePublishCmd_JSONOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "10", result.ID)
	assert.True(t, result.Published)
}

func TestTemplatePublishCmd_YAMLOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

func TestTemplatePublishCmd_QuietOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

func TestTemplatePublishCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Permission denied"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ---------- template unpublish ----------

func TestTemplateUnpublishCmd_Success(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/unpublish", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateUnpublishCmd.RunE(templateUnpublishCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "10")
	assert.Contains(t, out, "web-app-template")
}

func TestTemplateUnpublishCmd_JSONOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateUnpublishCmd.RunE(templateUnpublishCmd, []string{"10"})
	require.NoError(t, err)

	var result types.StackTemplate
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "10", result.ID)
	assert.False(t, result.Published)
}

func TestTemplateUnpublishCmd_YAMLOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templateUnpublishCmd.RunE(templateUnpublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "web-app-template")
}

func TestTemplateUnpublishCmd_QuietOutput(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Published = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateUnpublishCmd.RunE(templateUnpublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "10\n", buf.String())
}

func TestTemplateUnpublishCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Permission denied"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateUnpublishCmd.RunE(templateUnpublishCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

// ---------- template versions list ----------

func sampleTemplateVersion() types.TemplateVersion {
	return types.TemplateVersion{
		ID:            "1",
		TemplateID:    "10",
		Version:       "v1",
		ChangeSummary: "Initial publish",
		CreatedBy:     "admin",
		CreatedAt:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func sampleTemplateVersionDetail() types.TemplateVersionDetail {
	return types.TemplateVersionDetail{
		TemplateVersion: sampleTemplateVersion(),
		Snapshot: types.TemplateSnapshot{
			Template: types.TemplateSnapshotData{Name: "web-app", DefaultBranch: "master", IsPublished: true, Version: "v1"},
			Charts:   []types.TemplateChartSnapshotData{{ChartName: "frontend", RepoURL: "https://charts.example.com"}},
		},
	}
}

func TestTemplateVersionsListCmd_Success(t *testing.T) {
	v := sampleTemplateVersion()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/versions", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.TemplateVersion{v})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "v1")
}

func TestTemplateVersionsListCmd_JSONOutput(t *testing.T) {
	v := sampleTemplateVersion()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.TemplateVersion{v})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"})
	require.NoError(t, err)

	var result []types.TemplateVersion
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0].ID)
}

func TestTemplateVersionsListCmd_QuietOutput(t *testing.T) {
	v := sampleTemplateVersion()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.TemplateVersion{v})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestTemplateVersionsListCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTemplateVersionsListCmd_YAMLOutput(t *testing.T) {
	v := sampleTemplateVersion()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.TemplateVersion{v})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "version:")
	assert.Contains(t, out, "v1")
}

// ---------- template versions get ----------

func TestTemplateVersionsGetCmd_Success(t *testing.T) {
	v := sampleTemplateVersionDetail()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/versions/v1", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "v1"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "v1")
}

func TestTemplateVersionsGetCmd_JSONOutput(t *testing.T) {
	v := sampleTemplateVersionDetail()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "v1"})
	require.NoError(t, err)

	var result types.TemplateVersionDetail
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "v1", result.Version)
}

func TestTemplateVersionsGetCmd_QuietOutput(t *testing.T) {
	v := sampleTemplateVersionDetail()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "v1"})
	require.NoError(t, err)
	assert.Equal(t, "1\n", buf.String())
}

func TestTemplateVersionsGetCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "version not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "uuid-not-found"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTemplateVersionsGetCmd_YAMLOutput(t *testing.T) {
	v := sampleTemplateVersionDetail()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "uuid-v1"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "version:")
	assert.Contains(t, out, "change_summary:")
}

// ---------- template versions diff ----------

func sampleTemplateVersionDiff() types.TemplateVersionDiff {
	return types.TemplateVersionDiff{
		Left:  types.TemplateVersionSide{Version: "v1"},
		Right: types.TemplateVersionSide{Version: "v2"},
		ChartDiffs: []types.ChartDiffEntry{
			{ChartName: "frontend", ChangeType: "modified", HasDifferences: true, LeftRepoURL: "https://charts.example.com", RightRepoURL: "https://charts2.example.com"},
		},
	}
}

func TestTemplateVersionsDiffCmd_Success(t *testing.T) {
	diff := sampleTemplateVersionDiff()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/10/versions/diff", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(diff)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	err := templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"10", "v1", "v2"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "frontend")
	assert.Contains(t, strings.ToLower(out), "modified")
	assert.Contains(t, out, "Comparing v1 -> v2")
	assert.Contains(t, out, "CHART")
}

func TestTemplateVersionsDiffCmd_JSONOutput(t *testing.T) {
	diff := sampleTemplateVersionDiff()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(diff)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	err := templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"10", "v1", "v2"})
	require.NoError(t, err)

	var result types.TemplateVersionDiff
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result.ChartDiffs, 1)
	assert.Equal(t, "frontend", result.ChartDiffs[0].ChartName)
}

func TestTemplateVersionsDiffCmd_QuietOutput(t *testing.T) {
	diff := sampleTemplateVersionDiff()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(diff)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	err := templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"10", "v1", "v2"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "frontend")
}

func TestTemplateVersionsDiffCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"99", "uuid-1", "uuid-2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTemplateVersionsDiffCmd_YAMLOutput(t *testing.T) {
	diff := sampleTemplateVersionDiff()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(diff)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatYAML
	err := templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"10", "uuid-v1", "uuid-v2"})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "chart_name:")
	assert.Contains(t, out, "frontend")
	assert.Contains(t, out, "change_type:")
}
