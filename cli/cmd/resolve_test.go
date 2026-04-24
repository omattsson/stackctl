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
		assert.Equal(t, "klaravik-dev", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-123"}, Name: "klaravik-dev"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveDefinitionID(c, "  klaravik-dev  ")
	require.NoError(t, err)
	assert.Equal(t, "def-123", id)
}

func TestResolveDefinitionID_NameSingleMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/stack-definitions", r.URL.Path)
		assert.Equal(t, "klaravik-dev", r.URL.Query().Get("name"))
		json.NewEncoder(w).Encode(types.ListResponse[types.StackDefinition]{
			Data: []types.StackDefinition{
				{Base: types.Base{ID: "def-123"}, Name: "klaravik-dev", Owner: "alice"},
			},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	id, err := resolveDefinitionID(c, "klaravik-dev")
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
				{Base: types.Base{ID: "def-1"}, Name: "klaravik-dev", Owner: "alice"},
				{Base: types.Base{ID: "def-2"}, Name: "klaravik-dev", Owner: "bob"},
			},
			Total: 2, Page: 1, PageSize: 2,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "klaravik-dev")
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
	_, err := resolveDefinitionID(c, "klaravik-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `no definition found with name "klaravik-dev"`)
}

func TestResolveDefinitionID_APIError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := client.New(server.URL)
	_, err := resolveDefinitionID(c, "klaravik-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolving definition name")
}
