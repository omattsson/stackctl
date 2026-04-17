package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// templateDefMockState holds mutable state for the template/definition mock server.
type templateDefMockState struct {
	mu          sync.Mutex
	nextDefID   uint
	nextInstID  uint
	definitions map[string]*types.StackDefinition
	instances   map[string]*types.StackInstance
	templates   []types.StackTemplate
}

func newTemplateDefMockState() *templateDefMockState {
	return &templateDefMockState{
		nextDefID:   1,
		nextInstID:  100,
		definitions: make(map[string]*types.StackDefinition),
		instances:   make(map[string]*types.StackInstance),
		templates: []types.StackTemplate{
			{
				Base:        types.Base{ID: "1", Version: "1"},
				Name:        "web-app",
				Description: "Full web application stack",
				Published:   true,
				Owner:       "admin",
				Charts: []types.ChartConfig{
					{Base: types.Base{ID: "1"}, Name: "frontend", RepoURL: "https://charts.example.com", ChartVersion: "1.0.0"},
					{Base: types.Base{ID: "2"}, Name: "backend", RepoURL: "https://charts.example.com", ChartVersion: "2.0.0"},
				},
			},
			{
				Base:        types.Base{ID: "2", Version: "1"},
				Name:        "api-only",
				Description: "API-only stack",
				Published:   false,
				Owner:       "admin",
				Charts: []types.ChartConfig{
					{Base: types.Base{ID: "3"}, Name: "api", RepoURL: "https://charts.example.com", ChartVersion: "3.0.0"},
				},
			},
		},
	}
}

func startTemplateDefMockServer(t *testing.T, state *templateDefMockState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// --- Template routes ---
		switch {
		case r.URL.Path == "/api/v1/templates" && r.Method == http.MethodGet:
			published := r.URL.Query().Get("published")
			var data []types.StackTemplate
			for _, tmpl := range state.templates {
				if published == "true" && !tmpl.Published {
					continue
				}
				data = append(data, tmpl)
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
				Data: data, Total: len(data), Page: 1, PageSize: 20, TotalPages: 1,
			})
			return
		}

		// Template get/instantiate/quick-deploy
		if tmplTrim := strings.TrimPrefix(r.URL.Path, "/api/v1/templates/"); tmplTrim != r.URL.Path {
			parts := strings.Split(tmplTrim, "/")
			var tmplID string
			var tmplAction string
			switch len(parts) {
			case 1:
				tmplID = parts[0]
			case 2:
				tmplID = parts[0]
				tmplAction = parts[1]
			}
			if tmplID != "" {
				// Find template
				var tmpl *types.StackTemplate
				for i := range state.templates {
					if state.templates[i].ID == tmplID {
						tmpl = &state.templates[i]
						break
					}
				}
				if tmpl == nil {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(types.ErrorResponse{Error: "template not found"})
					return
				}

				switch {
				case tmplAction == "" && r.Method == http.MethodGet:
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(tmpl)
					return

				case tmplAction == "instantiate" && r.Method == http.MethodPost:
					var req types.InstantiateTemplateRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						w.WriteHeader(http.StatusBadRequest)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
						return
					}
					state.mu.Lock()
					inst := &types.StackInstance{
						Base:              types.Base{ID: fmt.Sprintf("%d", state.nextInstID), Version: "1"},
						Name:              req.Name,
						Branch:            req.Branch,
						Status:            "draft",
						Owner:             "admin",
						StackDefinitionID: "",
					}
					if req.ClusterID != "" {
						cid := req.ClusterID
						inst.ClusterID = &cid
					}
					state.instances[inst.ID] = inst
					state.nextInstID++
					state.mu.Unlock()

					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(inst)
					return

				case tmplAction == "quick-deploy" && r.Method == http.MethodPost:
					var req types.QuickDeployRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						w.WriteHeader(http.StatusBadRequest)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
						return
					}
					state.mu.Lock()
					inst := &types.StackInstance{
						Base:   types.Base{ID: fmt.Sprintf("%d", state.nextInstID), Version: "1"},
						Name:   req.Name,
						Branch: req.Branch,
						Status: "deploying",
						Owner:  "admin",
					}
					if req.ClusterID != "" {
						cid := req.ClusterID
						inst.ClusterID = &cid
					}
					state.instances[inst.ID] = inst
					state.nextInstID++
					state.mu.Unlock()

					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(inst)
					return
				}
			}
		}

		// --- Definition routes ---

		// List definitions
		if r.URL.Path == "/api/v1/stack-definitions" && r.Method == http.MethodGet {
			state.mu.Lock()
			var data []types.StackDefinition
			for _, d := range state.definitions {
				data = append(data, *d)
			}
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
				Data: data, Total: len(data), Page: 1, PageSize: 20, TotalPages: 1,
			})
			return
		}

		// Create definition
		if r.URL.Path == "/api/v1/stack-definitions" && r.Method == http.MethodPost {
			var req types.CreateDefinitionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			state.mu.Lock()
			def := &types.StackDefinition{
				Base:          types.Base{ID: fmt.Sprintf("%d", state.nextDefID), Version: "1"},
				Name:          req.Name,
				Description:   req.Description,
				DefaultBranch: "main",
				Owner:         "admin",
				Charts:        req.Charts,
			}
			state.definitions[def.ID] = def
			state.nextDefID++
			state.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(def)
			return
		}

		// Import definition
		if r.URL.Path == "/api/v1/stack-definitions/import" && r.Method == http.MethodPost {
			var importedDef types.StackDefinition
			if err := json.NewDecoder(r.Body).Decode(&importedDef); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
				return
			}
			state.mu.Lock()
			importedDef.ID = fmt.Sprintf("%d", state.nextDefID)
			importedDef.Version = "1"
			importedDef.Owner = "admin"
			state.definitions[importedDef.ID] = &importedDef
			state.nextDefID++
			state.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(importedDef)
			return
		}

		// Definition by ID: get/update/delete/export
		if defTrim := strings.TrimPrefix(r.URL.Path, "/api/v1/stack-definitions/"); defTrim != r.URL.Path {
			parts := strings.Split(defTrim, "/")
			var defID string
			var defAction string
			switch len(parts) {
			case 1:
				defID = parts[0]
			case 2:
				defID = parts[0]
				defAction = parts[1]
			}
			if defID != "" {
				state.mu.Lock()
				def, exists := state.definitions[defID]
				state.mu.Unlock()

				switch {
				case defAction == "" && r.Method == http.MethodGet:
					if !exists {
						w.WriteHeader(http.StatusNotFound)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
						return
					}
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(def)
					return

				case defAction == "" && r.Method == http.MethodPut:
					if !exists {
						w.WriteHeader(http.StatusNotFound)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
						return
					}
					var req types.UpdateDefinitionRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						w.WriteHeader(http.StatusBadRequest)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid body"})
						return
					}
					state.mu.Lock()
					if req.Name != "" {
						def.Name = req.Name
					}
					if req.Description != "" {
						def.Description = req.Description
					}
					if v, err := strconv.Atoi(def.Version); err == nil {
						def.Version = strconv.Itoa(v + 1)
					}
					state.mu.Unlock()
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(def)
					return

				case defAction == "" && r.Method == http.MethodDelete:
					if !exists {
						w.WriteHeader(http.StatusNotFound)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
						return
					}
					state.mu.Lock()
					delete(state.definitions, defID)
					state.mu.Unlock()
					w.WriteHeader(http.StatusNoContent)
					return

				case defAction == "export" && r.Method == http.MethodGet:
					if !exists {
						w.WriteHeader(http.StatusNotFound)
						json.NewEncoder(w).Encode(types.ErrorResponse{Error: "definition not found"})
						return
					}
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(def)
					return
				}
			}
		}

		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
	}))
}

// ---------- Template integration tests ----------

func TestTemplateWorkflow_BrowseAndInstantiate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. List all templates
	resp, err := c.ListTemplates(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)

	// 2. List only published
	resp, err = c.ListTemplates(map[string]string{"published": "true"})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, "web-app", resp.Data[0].Name)

	// 3. Get template details
	tmpl, err := c.GetTemplate("1")
	require.NoError(t, err)
	assert.Equal(t, "web-app", tmpl.Name)
	assert.Len(t, tmpl.Charts, 2)

	// 4. Instantiate from template
	instance, err := c.InstantiateTemplate("1", &types.InstantiateTemplateRequest{
		Name:   "my-web-app",
		Branch: "main",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-web-app", instance.Name)
	assert.Equal(t, "draft", instance.Status)
	assert.Equal(t, "main", instance.Branch)

	// 5. Verify instance is stored
	state.mu.Lock()
	_, exists := state.instances[instance.ID]
	state.mu.Unlock()
	assert.True(t, exists)
}

func TestTemplateWorkflow_QuickDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Quick deploy creates and deploys in one step
	instance, err := c.QuickDeployTemplate("1", &types.QuickDeployRequest{
		Name:   "quick-web-app",
		Branch: "feature/xyz",
	})
	require.NoError(t, err)
	assert.Equal(t, "quick-web-app", instance.Name)
	assert.Equal(t, "deploying", instance.Status)
	assert.Equal(t, "feature/xyz", instance.Branch)
}

func TestTemplateWorkflow_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Get non-existent template
	_, err := c.GetTemplate("999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")

	// Instantiate non-existent template
	_, err = c.InstantiateTemplate("999", &types.InstantiateTemplateRequest{Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

// ---------- Definition integration tests ----------

func TestDefinitionWorkflow_CRUDLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Create
	created, err := c.CreateDefinition(&types.CreateDefinitionRequest{
		Name:        "lifecycle-def",
		Description: "Test lifecycle definition",
	})
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-def", created.Name)
	assert.Equal(t, "Test lifecycle definition", created.Description)
	assert.Equal(t, "admin", created.Owner)
	id := created.ID

	// 2. Get — verify it exists
	got, err := c.GetDefinition(id)
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-def", got.Name)

	// 3. Update
	updated, err := c.UpdateDefinition(id, &types.UpdateDefinitionRequest{
		Name:        "updated-lifecycle-def",
		Description: "Updated description",
	})
	require.NoError(t, err)
	assert.Equal(t, "updated-lifecycle-def", updated.Name)
	assert.Equal(t, "Updated description", updated.Description)

	// 4. Verify update persists
	got, err = c.GetDefinition(id)
	require.NoError(t, err)
	assert.Equal(t, "updated-lifecycle-def", got.Name)

	// 5. List — should contain the definition
	resp, err := c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)

	// 6. Delete
	err = c.DeleteDefinition(id)
	require.NoError(t, err)

	// 7. Verify gone
	_, err = c.GetDefinition(id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")

	// 8. List should be empty
	resp, err = c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, resp.Total)
}

func TestDefinitionWorkflow_ExportImportRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// 1. Create a definition
	created, err := c.CreateDefinition(&types.CreateDefinitionRequest{
		Name:        "export-test-def",
		Description: "Definition for export/import test",
	})
	require.NoError(t, err)

	// 2. Export it
	exported, err := c.ExportDefinition(created.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, exported)

	// Verify exported data contains the definition name
	assert.Contains(t, string(exported), "export-test-def")

	// 3. Import the exported data
	imported, err := c.ImportDefinition(exported)
	require.NoError(t, err)
	assert.NotEqual(t, created.ID, imported.ID, "imported definition should get a new ID")
	assert.Equal(t, "export-test-def", imported.Name)

	// 4. Verify both exist
	resp, err := c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
}

func TestDefinitionWorkflow_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Get non-existent definition
	_, err := c.GetDefinition("999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")

	// Delete non-existent definition
	err = c.DeleteDefinition("999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")

	// Export non-existent definition
	_, err = c.ExportDefinition("999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")

	// Update non-existent definition
	_, err = c.UpdateDefinition("999", &types.UpdateDefinitionRequest{Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "definition not found")
}

func TestDefinitionWorkflow_MultipleDefinitions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := newTemplateDefMockState()
	server := startTemplateDefMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	// Create several definitions
	_, err := c.CreateDefinition(&types.CreateDefinitionRequest{Name: "def-a", Description: "First"})
	require.NoError(t, err)
	_, err = c.CreateDefinition(&types.CreateDefinitionRequest{Name: "def-b", Description: "Second"})
	require.NoError(t, err)
	_, err = c.CreateDefinition(&types.CreateDefinitionRequest{Name: "def-c", Description: "Third"})
	require.NoError(t, err)

	// List all — should return 3
	resp, err := c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 3, resp.Total)

	// Delete one
	err = c.DeleteDefinition("2")
	require.NoError(t, err)

	// List — should return 2
	resp, err = c.ListDefinitions(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
}
