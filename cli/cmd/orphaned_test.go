package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file are NOT parallelized because they mutate package-level
// globals (cfg, printer, flagAPIURL) via setupStackTestCmd. Do not add t.Parallel().

func sampleOrphanedNamespaces() []types.OrphanedNamespace {
	return []types.OrphanedNamespace{
		{Namespace: "stack-old-app", Cluster: "dev-cluster", CreatedAt: "2025-06-01T10:00:00Z"},
		{Namespace: "stack-leftover", Cluster: "dev-cluster", CreatedAt: "2025-05-20T08:00:00Z"},
	}
}

// ---------- orphaned list ----------

func TestOrphanedListCmd_TableOutput(t *testing.T) {
	ns := sampleOrphanedNamespaces()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/orphaned-namespaces", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ns)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	err := orphanedListCmd.RunE(orphanedListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "NAMESPACE")
	assert.Contains(t, out, "CLUSTER")
	assert.Contains(t, out, "stack-old-app")
	assert.Contains(t, out, "stack-leftover")
}

func TestOrphanedListCmd_JSONOutput(t *testing.T) {
	ns := sampleOrphanedNamespaces()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ns)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	err := orphanedListCmd.RunE(orphanedListCmd, []string{})
	require.NoError(t, err)

	var result []types.OrphanedNamespace
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 2)
	assert.Equal(t, "stack-old-app", result[0].Namespace)
}

func TestOrphanedListCmd_QuietOutput(t *testing.T) {
	ns := sampleOrphanedNamespaces()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ns)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	err := orphanedListCmd.RunE(orphanedListCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "stack-old-app\nstack-leftover", lines)
}

func TestOrphanedListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]types.OrphanedNamespace{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	err := orphanedListCmd.RunE(orphanedListCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No orphaned namespaces found")
}

func TestOrphanedListCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	err := orphanedListCmd.RunE(orphanedListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
}

// ---------- orphaned delete ----------

func TestOrphanedDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/orphaned-namespaces/stack-old-app", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	orphanedDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { orphanedDeleteCmd.Flags().Set("yes", "false") })

	err := orphanedDeleteCmd.RunE(orphanedDeleteCmd, []string{"stack-old-app"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted orphaned namespace")
}

func TestOrphanedDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	orphanedDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		orphanedDeleteCmd.Flags().Set("yes", "false")
		orphanedDeleteCmd.SetIn(nil)
		orphanedDeleteCmd.SetErr(nil)
	})

	orphanedDeleteCmd.SetIn(strings.NewReader("y\n"))
	orphanedDeleteCmd.SetErr(&bytes.Buffer{})

	err := orphanedDeleteCmd.RunE(orphanedDeleteCmd, []string{"stack-old-app"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "Deleted orphaned namespace")
}

func TestOrphanedDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)

	orphanedDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		orphanedDeleteCmd.Flags().Set("yes", "false")
		orphanedDeleteCmd.SetIn(nil)
		orphanedDeleteCmd.SetErr(nil)
	})

	orphanedDeleteCmd.SetIn(strings.NewReader("n\n"))
	orphanedDeleteCmd.SetErr(&bytes.Buffer{})

	err := orphanedDeleteCmd.RunE(orphanedDeleteCmd, []string{"stack-old-app"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestOrphanedDeleteCmd_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "namespace not found"})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)

	orphanedDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { orphanedDeleteCmd.Flags().Set("yes", "false") })

	err := orphanedDeleteCmd.RunE(orphanedDeleteCmd, []string{"nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace not found")
}

func TestOrphanedDeleteCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true

	orphanedDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { orphanedDeleteCmd.Flags().Set("yes", "false") })

	err := orphanedDeleteCmd.RunE(orphanedDeleteCmd, []string{"stack-old-app"})
	require.NoError(t, err)
	assert.Equal(t, "stack-old-app\n", buf.String())
}
