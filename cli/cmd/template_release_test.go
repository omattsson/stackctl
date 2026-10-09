package cmd

import (
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func resetTemplatePublishFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		resetFlag(t, templatePublishCmd.Flags(), "version", "")
		resetFlag(t, templatePublishCmd.Flags(), "change-summary", "")
	})
}

func resetTemplateUpdateFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for _, f := range []string{"name", "description", "version", flagFromFile} {
			resetFlag(t, templateUpdateCmd.Flags(), f, "")
		}
	})
}

// publishResponse is the v0.6.0+ publish response for template 10.
func publishResponse(version string, created bool) map[string]interface{} {
	return map[string]interface{}{
		"id": "10", "name": "web-app-template", "version": version, "is_published": true,
		"published_version": version, "published_version_id": "v-" + version,
		"snapshot_created": created, "has_unpublished_changes": false,
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// managerTemplate is template 10 as GET /templates/:id returns it to a
// manager on k8s-stack-manager v0.6.0+ (has_unpublished_changes is set).
func managerTemplate() types.StackTemplate {
	tmpl := sampleTemplate()
	tmpl.Version = "1.1.0"
	tmpl.PublishedVersion = strPtr("1.0.0")
	tmpl.HasUnpublishedChanges = boolPtr(true)
	return tmpl
}

// publishServer serves GET /api/v1/templates/10 with current and passes
// POST /api/v1/templates/10/publish to post. posts counts the publishes.
func publishServer(t *testing.T, current interface{}, posts *int, post http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates/10":
			writeJSON(w, http.StatusOK, current)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/templates/10/publish":
			*posts++
			post(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
}

// ---------- template publish ----------

func TestTemplatePublishCmd_WithVersionAndSummary(t *testing.T) {
	var body map[string]interface{}
	posts := 0
	server := publishServer(t, managerTemplate(), &posts, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		writeJSON(w, http.StatusOK, publishResponse("1.2.0", true))
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetTemplatePublishFlags(t)
	require.NoError(t, templatePublishCmd.Flags().Set("version", "1.2.0"))
	require.NoError(t, templatePublishCmd.Flags().Set("change-summary", "Chart app-core 0.3.7"))

	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Equal(t, 1, posts)
	assert.Equal(t, map[string]interface{}{"version": "1.2.0", "change_summary": "Chart app-core 0.3.7"}, body)
	assert.Equal(t, "Published version 1.2.0\n", buf.String())
}

func TestTemplatePublishCmd_NoFlagsSendsNoBody(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method, "no pre-check without flags")
		raw, _ = io.ReadAll(r.Body)
		writeJSON(w, http.StatusOK, publishResponse("1.1.0", false))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetTemplatePublishFlags(t)

	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(raw)))
	assert.Equal(t, "No changes since version 1.1.0; no new version created\n", buf.String())
}

func TestTemplatePublishCmd_JSONIncludesRelease(t *testing.T) {
	posts := 0
	server := publishServer(t, managerTemplate(), &posts, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, publishResponse("1.2.0", true))
	})
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	resetTemplatePublishFlags(t)
	require.NoError(t, templatePublishCmd.Flags().Set("version", "1.2.0"))

	require.NoError(t, templatePublishCmd.RunE(templatePublishCmd, []string{"10"}))
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "1.2.0", got["published_version"])
	assert.Equal(t, true, got["snapshot_created"])
	assert.Equal(t, false, got["has_unpublished_changes"])
}

// TestTemplatePublishCmd_ResolvesName uses a server that ignores ?name=
// (v0.5.0): the name must be matched exactly on the client.
func TestTemplatePublishCmd_ResolvesName(t *testing.T) {
	tests := []struct {
		name   string
		filter bool
	}{
		{name: "server ignores the name filter"},
		{name: "server filters by name", filter: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			all := []types.StackTemplate{
				{Base: types.Base{ID: "11"}, Name: "web-app-template-v2"},
				{Base: types.Base{ID: "12"}, Name: "Web-App-Template"},
				sampleTemplate(),
				{Base: types.Base{ID: "13"}, Name: "other"},
			}
			published := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates":
					assert.Equal(t, "web-app-template", r.URL.Query().Get("name"))
					data := all
					if tt.filter {
						data = []types.StackTemplate{sampleTemplate()}
					}
					writeJSON(w, http.StatusOK, types.ListResponse[types.StackTemplate]{Data: data, Total: len(data), Page: 1, PageSize: 100, TotalPages: 1})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates/10":
					writeJSON(w, http.StatusOK, managerTemplate())
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/templates/10/publish":
					published = true
					writeJSON(w, http.StatusOK, publishResponse("2.0.0", true))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()

			buf := setupStackTestCmd(t, server.URL)
			resetTemplatePublishFlags(t)
			require.NoError(t, templatePublishCmd.Flags().Set("version", "2.0.0"))

			require.NoError(t, templatePublishCmd.RunE(templatePublishCmd, []string{"web-app-template"}))
			assert.True(t, published)
			assert.Contains(t, buf.String(), "Published version 2.0.0")
		})
	}
}

func TestTemplatePublishCmd_Errors(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		status    int
		message   string
		wantErr   []string
		notWanted string
	}{
		{
			name: "version exists", version: "1.2.0", status: http.StatusConflict, message: "Version 1.2.0 already exists",
			wantErr: []string{"template 10: version 1.2.0 already exists", "stackctl template publish 10 --version <new-version>"},
		},
		{
			name: "working copy version exists", status: http.StatusConflict, message: "Version 1.0.0 already exists",
			wantErr: []string{"version 1.0.0 already exists", "--version <new-version>"},
		},
		{
			name: "no version", status: http.StatusBadRequest, message: "Version is required to publish",
			wantErr: []string{"template 10 has no version; set one: stackctl template publish 10 --version <version>"},
		},
		{
			name: "other validation error", version: "1.2.0", status: http.StatusBadRequest, message: "Change summary must be at most 2000 characters",
			wantErr: []string{"cannot publish template 10", "Change summary must be at most 2000 characters"},
		},
		{
			name: "forbidden", status: http.StatusForbidden, message: "Insufficient permissions",
			wantErr: []string{"Permission denied"}, notWanted: "cannot publish",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			posts := 0
			server := publishServer(t, managerTemplate(), &posts, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tt.status, types.ErrorResponse{Error: tt.message})
			})
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			resetTemplatePublishFlags(t)
			if tt.version != "" {
				require.NoError(t, templatePublishCmd.Flags().Set("version", tt.version))
			}

			err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			if tt.notWanted != "" {
				assert.NotContains(t, err.Error(), tt.notWanted)
			}
		})
	}
}

func TestTemplatePublishCmd_EmptyVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API must not be called")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetTemplatePublishFlags(t)
	require.NoError(t, templatePublishCmd.Flags().Set("version", "  "))

	err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--version must not be empty")
}

func TestTemplatePublishCmd_OlderServerStopsBeforePublish(t *testing.T) {
	for _, flag := range []string{"version", "change-summary"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			posts := 0
			server := publishServer(t, sampleTemplate(), &posts, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, sampleTemplate())
			})
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			resetTemplatePublishFlags(t)
			require.NoError(t, templatePublishCmd.Flags().Set(flag, "1.2.0"))

			err := templatePublishCmd.RunE(templatePublishCmd, []string{"10"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "need k8s-stack-manager v0.6.0 or later; nothing was published")
			assert.Equal(t, 0, posts, "no release on an older server")
		})
	}
}

func TestTemplatePublishCmd_OlderServerWithoutFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		writeJSON(w, http.StatusOK, sampleTemplate())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetTemplatePublishFlags(t)

	require.NoError(t, templatePublishCmd.RunE(templatePublishCmd, []string{"10"}))
	assert.Contains(t, buf.String(), "web-app-template", "older server: the template is printed")
}

// ---------- template create / update --version ----------

func TestTemplateCreateCmd_WithVersion(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		writeJSON(w, http.StatusCreated, sampleTemplate())
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	t.Cleanup(func() {
		resetFlag(t, templateCreateCmd.Flags(), "name", "")
		resetFlag(t, templateCreateCmd.Flags(), "version", "")
	})
	require.NoError(t, templateCreateCmd.Flags().Set("name", "web-app-template"))
	require.NoError(t, templateCreateCmd.Flags().Set("version", "1.0.0"))

	require.NoError(t, templateCreateCmd.RunE(templateCreateCmd, nil))
	assert.Equal(t, "web-app-template", body["name"])
	assert.Equal(t, "1.0.0", body["version"])
}

// TestTemplateUpdateCmd_OlderServerSendsFullRecord: a server without
// has_unpublished_changes replaces every field, so the PUT carries all.
func TestTemplateUpdateCmd_OlderServerSendsFullRecord(t *testing.T) {
	current := sampleTemplate()
	current.Version = "1.0.0"
	current.Category = "web"
	current.DefaultBranch = "develop"
	var put map[string]interface{}
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		require.Equal(t, "/api/v1/templates/10", r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, current)
		case http.MethodPut:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&put))
			updated := current
			updated.Version = "1.1.0"
			writeJSON(w, http.StatusOK, updated)
		}
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetTemplateUpdateFlags(t)
	require.NoError(t, templateUpdateCmd.Flags().Set("version", "1.1.0"))

	require.NoError(t, templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"}))
	assert.Equal(t, []string{http.MethodGet, http.MethodPut}, calls)
	assert.Equal(t, map[string]interface{}{
		"name":           "web-app-template",
		"description":    "Full web app stack",
		"category":       "web",
		"version":        "1.1.0",
		"default_branch": "develop",
	}, put)
	assert.Contains(t, buf.String(), "1.1.0")
}

func TestTemplateUpdateCmd_PartialOnNewServer(t *testing.T) {
	tests := []struct {
		name  string
		flags map[string]string
		file  string
		want  map[string]interface{}
	}{
		{
			name:  "version only",
			flags: map[string]string{"version": "1.2.0"},
			want:  map[string]interface{}{"version": "1.2.0"},
		},
		{
			name:  "clear description",
			flags: map[string]string{"description": ""},
			want:  map[string]interface{}{"description": ""},
		},
		{
			name:  "file keys plus flag",
			flags: map[string]string{"name": "renamed"},
			file:  `{"category":"web","default_branch":"main","charts":[]}`,
			want:  map[string]interface{}{"name": "renamed", "category": "web", "default_branch": "main"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var put map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/templates/10", r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					writeJSON(w, http.StatusOK, managerTemplate())
				case http.MethodPut:
					require.NoError(t, json.NewDecoder(r.Body).Decode(&put))
					writeJSON(w, http.StatusOK, sampleTemplate())
				}
			}))
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			resetTemplateUpdateFlags(t)
			for k, v := range tt.flags {
				require.NoError(t, templateUpdateCmd.Flags().Set(k, v))
			}
			if tt.file != "" {
				path := filepath.Join(t.TempDir(), "tmpl.json")
				require.NoError(t, os.WriteFile(path, []byte(tt.file), 0o600))
				require.NoError(t, templateUpdateCmd.Flags().Set(flagFromFile, path))
			}

			require.NoError(t, templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"}))
			assert.Equal(t, tt.want, put)
		})
	}
}

func TestTemplateUpdateCmd_OlderServerClearsDescription(t *testing.T) {
	var put map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, sampleTemplate())
		case http.MethodPut:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&put))
			writeJSON(w, http.StatusOK, sampleTemplate())
		}
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetTemplateUpdateFlags(t)
	require.NoError(t, templateUpdateCmd.Flags().Set("description", ""))

	require.NoError(t, templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"}))
	// The empty description is omitted; the older server then stores "".
	assert.Equal(t, map[string]interface{}{"name": "web-app-template", "version": "1"}, put)
}

func TestTemplateUpdateCmd_EmptyName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API must not be called")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetTemplateUpdateFlags(t)
	require.NoError(t, templateUpdateCmd.Flags().Set("name", " "))

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name must not be empty")
}

func TestTemplateUpdateCmd_GetFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "no PUT after a failed GET")
		writeJSON(w, http.StatusNotFound, types.ErrorResponse{Error: "Template not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetTemplateUpdateFlags(t)
	require.NoError(t, templateUpdateCmd.Flags().Set("version", "1.1.0"))

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Template not found")
}

func TestTemplateUpdateCmd_EmptyVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API must not be called")
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetTemplateUpdateFlags(t)
	require.NoError(t, templateUpdateCmd.Flags().Set("version", ""))

	err := templateUpdateCmd.RunE(templateUpdateCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--version must not be empty")
}

// ---------- template get: release state ----------

// releasedTemplate is a v0.6.0+ GET /templates/:id response with a release
// and unpublished changes in the working copy.
func releasedTemplate() types.StackTemplate {
	tmpl := sampleTemplate()
	tmpl.Version = "1.1.0"
	tmpl.Charts = []types.ChartConfig{
		{Base: types.Base{ID: "1"}, RepoURL: "https://charts.example.com", ChartName: "react-app", ChartVersion: "1.3.0"},
		{Base: types.Base{ID: "2"}, RepoURL: "https://charts.example.com", ChartName: "api-server", ChartVersion: "3.0.0"},
		{Base: types.Base{ID: "3"}, RepoURL: "https://charts.example.com", ChartName: "worker", ChartVersion: "0.1.0"},
	}
	tmpl.PublishedVersion = strPtr("1.0.0")
	tmpl.PublishedVersionID = strPtr("v-1")
	tmpl.HasUnpublishedChanges = boolPtr(true)
	tmpl.PublishedCharts = []types.ChartConfig{
		{Base: types.Base{ID: "1"}, RepoURL: "https://charts.example.com", ChartName: "react-app", ChartVersion: "1.2.0", DefaultValues: "a: 1"},
		{Base: types.Base{ID: "2"}, RepoURL: "https://charts.example.com", ChartName: "api-server", ChartVersion: "3.0.0"},
		{Base: types.Base{ID: "4"}, RepoURL: "https://charts.example.com", ChartName: "cache", ChartVersion: "2.0.0"},
	}
	return tmpl
}

func TestTemplateGetCmd_ReleaseState_Golden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, releasedTemplate())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetFlag(t, templateGetCmd.Flags(), "released", "false") })

	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	want, err := os.ReadFile(filepath.Join("testdata", "template_get_release.golden.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(want), buf.String())
}

func TestTemplateGetCmd_UnpublishedWithRelease(t *testing.T) {
	tmpl := releasedTemplate()
	tmpl.Published = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	assert.Regexp(t, `Released version:\s+1\.0\.0 \(unpublished; quick deploy and use are blocked until published\)`, buf.String())
}

func TestTemplateGetCmd_ValuesComparedNormalized(t *testing.T) {
	tmpl := releasedTemplate()
	tmpl.Charts = tmpl.Charts[:1]
	tmpl.PublishedCharts = tmpl.PublishedCharts[:1]
	tmpl.Charts[0].ChartVersion = "1.2.0"
	tmpl.Charts[0].DefaultValues = "a: 1\n\n  "
	tmpl.Charts[0].LockedValues = "b: 2\n"
	tmpl.PublishedCharts[0].LockedValues = "b: 2"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	assert.Contains(t, buf.String(), "react-app (https://charts.example.com@1.2.0)\n")
	assert.NotContains(t, buf.String(), "changed since release")
}

func TestTemplateGetCmd_ReleasedFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, releasedTemplate())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetFlag(t, templateGetCmd.Flags(), "released", "false") })
	require.NoError(t, templateGetCmd.Flags().Set("released", "true"))

	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	out := buf.String()
	assert.Contains(t, out, "Released chart:")
	assert.Contains(t, out, "react-app (https://charts.example.com@1.2.0)")
	assert.Contains(t, out, "cache (https://charts.example.com@2.0.0)")
	assert.NotContains(t, out, "worker")
}

func TestTemplateGetCmd_NoRelease(t *testing.T) {
	tmpl := sampleTemplate()
	tmpl.Version = ""
	tmpl.HasUnpublishedChanges = boolPtr(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tmpl)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	out := buf.String()
	assert.Regexp(t, `Released version:\s+none`, out)
	assert.Regexp(t, `Unpublished changes:\s+yes`, out)
	assert.Contains(t, out, "react-app (https://charts.example.com@1.2.0)\n")
}

func TestTemplateGetCmd_OlderServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sampleTemplate())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	t.Cleanup(func() { resetFlag(t, templateGetCmd.Flags(), "released", "false") })
	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	out := buf.String()
	assert.NotContains(t, out, "Released version")
	assert.NotContains(t, out, "Unpublished changes")
	assert.Regexp(t, `\nVersion:\s+1\n`, out, "older server: plain Version label")
	assert.Contains(t, out, "react-app")

	buf.Reset()
	require.NoError(t, templateGetCmd.Flags().Set("released", "true"))
	err := templateGetCmd.RunE(templateGetCmd, []string{"10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v0.6.0")
}

func TestTemplateGetCmd_JSONKeepsReleaseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, releasedTemplate())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, templateGetCmd.RunE(templateGetCmd, []string{"10"}))
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "1.0.0", got["published_version"])
	assert.Equal(t, true, got["has_unpublished_changes"])
	assert.Len(t, got["published_charts"], 3)
}

// ---------- template versions: created_by_username, working ----------

func TestTemplateVersionsListCmd_CreatedByUsername(t *testing.T) {
	withName := sampleTemplateVersion()
	withName.CreatedBy = "u-1"
	withName.CreatedByUsername = "alice"
	withoutName := sampleTemplateVersion()
	withoutName.ID = "2"
	withoutName.CreatedBy = "u-2"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []types.TemplateVersion{withName, withoutName})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateVersionsListCmd.RunE(templateVersionsListCmd, []string{"10"}))
	out := buf.String()
	assert.Contains(t, out, "alice")
	assert.NotContains(t, out, "u-1")
	assert.Contains(t, out, "u-2", "falls back to the user ID")
}

func TestTemplateVersionsGetCmd_CreatedByUsername(t *testing.T) {
	v := sampleTemplateVersionDetail()
	v.CreatedBy = "u-1"
	v.CreatedByUsername = "alice"
	v.Snapshot.Charts[0].ChartVersion = "1.2.0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, v)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateVersionsGetCmd.RunE(templateVersionsGetCmd, []string{"10", "1"}))
	out := buf.String()
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "frontend (1.2.0)")
}

func TestTemplateVersionsDiffCmd_WorkingCopy(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	diff := types.TemplateVersionDiff{
		Left:  types.TemplateVersionSide{ID: "v-1", Version: "1.0.0", CreatedAt: &created},
		Right: types.TemplateVersionSide{ID: "working", Version: "1.1.0", IsWorkingCopy: true, CreatedAt: &created},
		ChartDiffs: []types.ChartDiffEntry{{
			ChartName: "react-app", ChangeType: "modified", HasDifferences: true,
			LeftValues: "a: 1\n", RightValues: "a: 2\n",
			LeftChartVersion: "1.2.0", RightChartVersion: "1.3.0",
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "v-1", r.URL.Query().Get("left"))
		assert.Equal(t, "working", r.URL.Query().Get("right"))
		writeJSON(w, http.StatusOK, diff)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, templateVersionsDiffCmd.RunE(templateVersionsDiffCmd, []string{"10", "v-1", "Working"}))
	out := buf.String()
	assert.Contains(t, out, "Comparing 1.0.0 -> working copy (1.1.0)")
	assert.Contains(t, out, "CHART VERSION")
	assert.Contains(t, out, "1.2.0 -> 1.3.0")
	assert.Contains(t, out, "+++ react-app (working copy (1.1.0))")
}

func TestParseVersionRef(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "working", want: "working"},
		{in: " WORKING ", want: "working"},
		{in: "8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f", want: "8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f"},
		{in: " ", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseVersionRef(tt.in)
		if tt.wantErr {
			assert.Error(t, err, tt.in)
			continue
		}
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.want, got)
	}
}

// ---------- instantiate / quick deploy without a release ----------

func TestTemplateUse_NoPublishedVersion(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		message string
		current interface{} // GET /templates/10 answer; nil: the GET fails
		run     func() error
		wantErr string
	}{
		{
			name: "unpublished with a release", status: http.StatusConflict, message: "Template has no published version",
			current: func() types.StackTemplate { tm := releasedTemplate(); tm.Published = false; return tm }(),
			run:     func() error { return templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}) },
			wantErr: "template 10 is not published; publish it first: stackctl template publish 10",
		},
		{
			name: "no release yet", status: http.StatusConflict, message: "Template has no published version",
			current: func() types.StackTemplate {
				tm := sampleTemplate()
				tm.Published = false
				tm.HasUnpublishedChanges = boolPtr(true)
				return tm
			}(),
			run:     func() error { return templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"}) },
			wantErr: "template 10 has no published version; publish it first: stackctl template publish 10 --version <version>",
		},
		{
			name: "older server plain publish", status: http.StatusBadRequest, message: "Template is not published",
			current: sampleTemplate(),
			run:     func() error { return templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}) },
			wantErr: "template 10 is not published; publish it first: stackctl template publish 10",
		},
		{
			name: "quick deploy 409", status: http.StatusConflict, message: "Template has no published version",
			run:     func() error { return templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}) },
			wantErr: "template 10 has no published version; publish it first: stackctl template publish 10 --version <version>",
		},
		{
			name: "quick deploy older server 400", status: http.StatusBadRequest, message: "Template is not published",
			run:     func() error { return templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}) },
			wantErr: "template 10 has no published version",
		},
		{
			name: "instantiate 409", status: http.StatusConflict, message: "Template has no published version",
			run:     func() error { return templateInstantiateCmd.RunE(templateInstantiateCmd, []string{"10"}) },
			wantErr: "publish it first: stackctl template publish 10 --version <version>",
		},
		{
			name: "other conflict unchanged", status: http.StatusConflict, message: "A stack definition named \"x\" already exists for this owner",
			run:     func() error { return templateQuickDeployCmd.RunE(templateQuickDeployCmd, []string{"10"}) },
			wantErr: "Conflict: A stack definition named",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && tt.current != nil {
					writeJSON(w, http.StatusOK, tt.current)
					return
				}
				writeJSON(w, tt.status, types.ErrorResponse{Error: tt.message})
			}))
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			require.NoError(t, templateQuickDeployCmd.Flags().Set("name", "my-stack"))
			require.NoError(t, templateInstantiateCmd.Flags().Set("name", "my-stack"))
			t.Cleanup(func() {
				_ = templateQuickDeployCmd.Flags().Set("name", "")
				_ = templateInstantiateCmd.Flags().Set("name", "")
			})

			err := tt.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if strings.HasSuffix(tt.wantErr, "stackctl template publish 10") {
				assert.NotContains(t, err.Error(), "--version")
			}
		})
	}
}

// ---------- bulk template commands: exact name match ----------

func TestBulkTemplatePublish_ResolvesNameWhenServerIgnoresFilter(t *testing.T) {
	var got types.BulkTemplatesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates":
			// v0.5.0 ignores ?name= and returns every template.
			writeJSON(w, http.StatusOK, types.ListResponse[types.StackTemplate]{
				Data: []types.StackTemplate{
					{Base: types.Base{ID: "11"}, Name: "web-app-template-v2"},
					sampleTemplate(),
				},
				Total: 2, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/templates/bulk/publish":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			writeJSON(w, http.StatusOK, sampleBulkResponse())
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)
	t.Cleanup(func() { resetFlag(t, bulkTemplatePublishCmd.Flags(), "ids", "") })
	require.NoError(t, bulkTemplatePublishCmd.Flags().Set("ids", "web-app-template"))

	require.NoError(t, bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{}))
	assert.Equal(t, []string{"10"}, got.TemplateIDs)
}
