package integration

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

// startBulkTemplateMockServer starts a minimal mock server for bulk template operations.
func startBulkTemplateMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/templates/bulk/publish" && r.Method == http.MethodPost:
			var req types.BulkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			results := make([]types.BulkOperationResult, len(req.IDs))
			for i, id := range req.IDs {
				results[i] = types.BulkOperationResult{ID: id, Success: true}
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.BulkResponse{Results: results})

		case r.URL.Path == "/api/v1/templates/bulk/unpublish" && r.Method == http.MethodPost:
			var req types.BulkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			results := make([]types.BulkOperationResult, len(req.IDs))
			for i, id := range req.IDs {
				results[i] = types.BulkOperationResult{ID: id, Success: true}
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.BulkResponse{Results: results})

		case r.URL.Path == "/api/v1/templates/bulk/delete" && r.Method == http.MethodPost:
			var req types.BulkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			results := make([]types.BulkOperationResult, len(req.IDs))
			for i, id := range req.IDs {
				results[i] = types.BulkOperationResult{ID: id, Success: true}
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.BulkResponse{Results: results})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

func TestBulkTemplatePublish_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startBulkTemplateMockServer(t)
	defer server.Close()

	c := client.New(server.URL)

	resp, err := c.BulkPublishTemplates([]string{"1", "2", "3"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Results, 3)
	for _, r := range resp.Results {
		assert.True(t, r.Success)
	}
}

func TestBulkTemplateUnpublish_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startBulkTemplateMockServer(t)
	defer server.Close()

	c := client.New(server.URL)

	resp, err := c.BulkUnpublishTemplates([]string{"1", "2"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Results, 2)
	for _, r := range resp.Results {
		assert.True(t, r.Success)
	}
}

func TestBulkTemplateDelete_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startBulkTemplateMockServer(t)
	defer server.Close()

	c := client.New(server.URL)

	resp, err := c.BulkDeleteTemplates([]string{"10", "20"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Results, 2)
	for _, r := range resp.Results {
		assert.True(t, r.Success)
	}
}

func TestBulkTemplatePublish_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startBulkTemplateMockServer(t)
	defer server.Close()

	c := client.New(server.URL)

	resp, err := c.BulkPublishTemplates([]string{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Results, 0)
}

func TestBulkTemplatePublish_ServerError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal server error"})
	}))
	defer errorServer.Close()

	c := client.New(errorServer.URL)

	resp, err := c.BulkPublishTemplates([]string{"1"})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "internal server error")
}
