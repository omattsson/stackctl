package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStackID_UUID(t *testing.T) {
	t.Parallel()
	c := client.New("http://should-not-be-called")
	id, err := resolveStackID(c, "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", id)
}

func TestResolveStackID_NumericID(t *testing.T) {
	t.Parallel()
	c := client.New("http://should-not-be-called")
	id, err := resolveStackID(c, "42")
	require.NoError(t, err)
	assert.Equal(t, "42", id)
}

func TestResolveStackID_UUID_UpperCase(t *testing.T) {
	t.Parallel()
	c := client.New("http://should-not-be-called")
	id, err := resolveStackID(c, "550E8400-E29B-41D4-A716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, "550E8400-E29B-41D4-A716-446655440000", id)
}

func TestResolveStackID_Empty(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	_, err := resolveStackID(c, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveStackID_Whitespace(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	_, err := resolveStackID(c, "   ")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveStackID_NameSingleMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/stack-instances", r.URL.Path)
		assert.Equal(t, "my-stack", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{
				{Base: types.Base{ID: "abc-123"}, Name: "my-stack", Owner: "alice", Status: "running"},
			},
			Total:    1,
			Page:     1,
			PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveStackID(c, "my-stack")
	require.NoError(t, err)
	assert.Equal(t, "abc-123", id)
}

func TestResolveStackID_NameNoMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:     []types.StackInstance{},
			Total:    0,
			Page:     1,
			PageSize: 0,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveStackID(c, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `no stack found with name "nonexistent"`)
}

func TestResolveStackID_NameMultipleMatches(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{
				{Base: types.Base{ID: "id-1"}, Name: "my-stack", Owner: "alice", Status: "running"},
				{Base: types.Base{ID: "id-2"}, Name: "my-stack", Owner: "bob", Status: "stopped"},
			},
			Total:    2,
			Page:     1,
			PageSize: 2,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveStackID(c, "my-stack")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple stacks match")
	assert.Contains(t, err.Error(), "id-1")
	assert.Contains(t, err.Error(), "id-2")
}

func TestResolveStackID_NameWithWhitespace(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "my-stack", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{
				{Base: types.Base{ID: "abc-123"}, Name: "my-stack"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveStackID(c, "  my-stack  ")
	require.NoError(t, err)
	assert.Equal(t, "abc-123", id)
}

func TestResolveStackID_APIError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveStackID(c, "my-stack")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolving stack name")
}

func TestResolveStackID_NameMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{
				{Base: types.Base{ID: "abc-123"}, Name: "different-stack", Owner: "alice", Status: "running"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveStackID(c, "my-stack")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `no stack found with name "my-stack"`)
}

func TestPassthroughID(t *testing.T) {
	t.Parallel()
	id, err := passthroughID(nil, "some-id")
	require.NoError(t, err)
	assert.Equal(t, "some-id", id)

	_, err = passthroughID(nil, "")
	assert.Error(t, err)
}

func TestResolveDefinitionID_UUID(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	id, err := resolveDefinitionID(c, "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", id)
}

func TestResolveDefinitionID_NumericID(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	id, err := resolveDefinitionID(c, "42")
	require.NoError(t, err)
	assert.Equal(t, "42", id)
}

func TestResolveDefinitionID_Empty(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	_, err := resolveDefinitionID(c, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveDefinitionID_Whitespace(t *testing.T) {
	t.Parallel()
	c := client.New("http://unused")
	_, err := resolveDefinitionID(c, "   ")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveDefinitionID_NameWithWhitespace(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "example-dev", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-123"}, Name: "example-dev"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveDefinitionID(c, "  example-dev  ")
	require.NoError(t, err)
	assert.Equal(t, "def-123", id)
}

func TestResolveDefinitionID_NameSingleMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/stack-definitions", r.URL.Path)
		assert.Equal(t, "example-dev", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-123"}, Name: "example-dev", Owner: "alice"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveDefinitionID(c, "example-dev")
	require.NoError(t, err)
	assert.Equal(t, "def-123", id)
}

func TestResolveDefinitionID_NameNoMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{}, Total: 0, Page: 1, PageSize: 0,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `no definition found with name "nonexistent"`)
}

func TestResolveDefinitionID_NameMultipleMatches(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-1"}, Name: "example-dev", Owner: "alice"},
				{Base: types.Base{ID: "def-2"}, Name: "example-dev", Owner: "bob"},
			},
			Total: 2, Page: 1, PageSize: 2,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "example-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple definitions match")
	assert.Contains(t, err.Error(), "def-1")
	assert.Contains(t, err.Error(), "def-2")
}

func TestResolveDefinitionID_NameMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-123"}, Name: "other-def", Owner: "alice"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "example-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `no definition found with name "example-dev"`)
}

func TestResolveDefinitionID_APIError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "example-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolving definition name")
}

// ---------- resolveTemplateID ----------

func TestResolveTemplateID_UUID(t *testing.T) {
	c := client.New("http://unused")
	id, err := resolveTemplateID(c, "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", id)
}

func TestResolveTemplateID_NumericID(t *testing.T) {
	c := client.New("http://unused")
	id, err := resolveTemplateID(c, "42")
	require.NoError(t, err)
	assert.Equal(t, "42", id)
}

func TestResolveTemplateID_Empty(t *testing.T) {
	c := client.New("http://unused")
	_, err := resolveTemplateID(c, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveTemplateID_Whitespace(t *testing.T) {
	c := client.New("http://unused")
	_, err := resolveTemplateID(c, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestResolveTemplateID_NameSingleMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/templates", r.URL.Path)
		assert.Equal(t, "my-template", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:  []types.StackTemplate{{Base: types.Base{ID: "tmpl-123"}, Name: "my-template", Owner: "alice"}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveTemplateID(c, "my-template")
	require.NoError(t, err)
	assert.Equal(t, "tmpl-123", id)
}

func TestResolveTemplateID_NameNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{}, Total: 0, Page: 1, PageSize: 0,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveTemplateID(c, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no template found with name "nonexistent"`)
}

func TestResolveTemplateID_NameMultipleMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{
				{Base: types.Base{ID: "tmpl-1"}, Name: "my-template", Owner: "alice"},
				{Base: types.Base{ID: "tmpl-2"}, Name: "my-template", Owner: "bob"},
			},
			Total: 2, Page: 1, PageSize: 2,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveTemplateID(c, "my-template")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple templates match")
	assert.Contains(t, err.Error(), "tmpl-1")
	assert.Contains(t, err.Error(), "tmpl-2")
}

func TestResolveTemplateID_NameMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:  []types.StackTemplate{{Base: types.Base{ID: "tmpl-123"}, Name: "other-template", Owner: "alice"}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveTemplateID(c, "my-template")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no template found with name "my-template"`)
}

func TestResolveTemplateID_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveTemplateID(c, "my-template")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving template name")
}

func TestMatchChart(t *testing.T) {
	t.Parallel()
	charts := []types.ChartConfig{
		{Base: types.Base{ID: "3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b"}, ChartName: "my-api"},
		{Base: types.Base{ID: "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"}, ChartName: "my-db"},
		{Base: types.Base{ID: "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a"}, ChartName: "dup"},
		{Base: types.Base{ID: "0a1b2c3d-4e5f-4a6b-8c7d-8e9f0a1b2c3d"}, ChartName: "DUP"},
	}
	tests := []struct {
		name    string
		in      string
		wantID  string
		wantErr string
	}{
		{name: "by id", in: "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d", wantID: "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"},
		{name: "by name", in: "my-api", wantID: "3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b"},
		{name: "case-insensitive", in: "MY-DB", wantID: "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"},
		{name: "unknown", in: "nope", wantErr: `chart "nope" is not part of definition x (charts: my-api, my-db, dup, DUP)`},
		{name: "ambiguous", in: "dup", wantErr: "multiple charts in definition x match name"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ch, err := matchChart(charts, tt.in, "definition x")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, ch.ID)
		})
	}
}

func TestMatchChart_NoCharts(t *testing.T) {
	t.Parallel()
	_, err := matchChart(nil, "x", "definition y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "(charts: none)")
}

// withChartLookup answers the chart lookups of resolveChartID and
// resolveDefinitionChartID for stack 42 and definition 5 (charts "1" api,
// "3" web, "5" db) and passes every other request to next.
func withChartLookup(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/v1/stack-instances/42":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"42","name":"my-stack","stack_definition_id":"5","status":"running"}`))
				return
			case "/api/v1/stack-definitions/5":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"5","name":"api-service","charts":[` +
					`{"id":"1","chart_name":"api"},{"id":"3","chart_name":"web"},{"id":"5","chart_name":"db"}]}`))
				return
			}
		}
		next(w, r)
	}
}
