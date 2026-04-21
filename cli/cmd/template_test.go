package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
