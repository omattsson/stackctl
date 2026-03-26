package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGitTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	return setupStackTestCmd(t, apiURL)
}

func sampleBranches() []types.GitBranch {
	return []types.GitBranch{
		{Name: "main", IsHead: true},
		{Name: "develop", IsHead: false},
		{Name: "feature/xyz", IsHead: false},
	}
}

// ---------- git branches ----------

func TestGitBranchesCmd_TableOutput(t *testing.T) {
	branches := sampleBranches()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/git/branches", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "https://github.com/org/repo", r.URL.Query().Get("repo"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(branches)
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)

	gitBranchesCmd.Flags().Set("repo", "https://github.com/org/repo")
	t.Cleanup(func() { gitBranchesCmd.Flags().Set("repo", "") })

	err := gitBranchesCmd.RunE(gitBranchesCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "HEAD")
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "*")
	assert.Contains(t, out, "develop")
	assert.Contains(t, out, "feature/xyz")
}

func TestGitBranchesCmd_JSONOutput(t *testing.T) {
	branches := sampleBranches()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(branches)
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	gitBranchesCmd.Flags().Set("repo", "https://github.com/org/repo")
	t.Cleanup(func() { gitBranchesCmd.Flags().Set("repo", "") })

	err := gitBranchesCmd.RunE(gitBranchesCmd, []string{})
	require.NoError(t, err)

	var result []types.GitBranch
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 3)
	assert.Equal(t, "main", result[0].Name)
	assert.True(t, result[0].IsHead)
}

func TestGitBranchesCmd_YAMLOutput(t *testing.T) {
	branches := sampleBranches()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(branches)
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	gitBranchesCmd.Flags().Set("repo", "https://github.com/org/repo")
	t.Cleanup(func() { gitBranchesCmd.Flags().Set("repo", "") })

	err := gitBranchesCmd.RunE(gitBranchesCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "name: main")
	assert.Contains(t, out, "name: develop")
}

func TestGitBranchesCmd_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]types.GitBranch{})
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)

	gitBranchesCmd.Flags().Set("repo", "https://github.com/org/empty-repo")
	t.Cleanup(func() { gitBranchesCmd.Flags().Set("repo", "") })

	err := gitBranchesCmd.RunE(gitBranchesCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "HEAD")
}

func TestGitBranchesCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "repository not found"})
	}))
	defer server.Close()

	_ = setupGitTestCmd(t, server.URL)

	gitBranchesCmd.Flags().Set("repo", "https://github.com/org/nonexistent")
	t.Cleanup(func() { gitBranchesCmd.Flags().Set("repo", "") })

	err := gitBranchesCmd.RunE(gitBranchesCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository not found")
}

// ---------- git validate ----------

func TestGitValidateCmd_ValidBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/git/validate", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "https://github.com/org/repo", r.URL.Query().Get("repo"))
		assert.Equal(t, "main", r.URL.Query().Get("branch"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:  true,
			Branch: "main",
		})
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)

	gitValidateCmd.Flags().Set("repo", "https://github.com/org/repo")
	gitValidateCmd.Flags().Set("branch", "main")
	t.Cleanup(func() {
		gitValidateCmd.Flags().Set("repo", "")
		gitValidateCmd.Flags().Set("branch", "")
	})

	err := gitValidateCmd.RunE(gitValidateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "BRANCH")
	assert.Contains(t, out, "VALID")
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "true")
}

func TestGitValidateCmd_InvalidBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:   false,
			Branch:  "nonexistent",
			Message: "branch does not exist",
		})
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)

	gitValidateCmd.Flags().Set("repo", "https://github.com/org/repo")
	gitValidateCmd.Flags().Set("branch", "nonexistent")
	t.Cleanup(func() {
		gitValidateCmd.Flags().Set("repo", "")
		gitValidateCmd.Flags().Set("branch", "")
	})

	err := gitValidateCmd.RunE(gitValidateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "false")
	assert.Contains(t, out, "branch does not exist")
}

func TestGitValidateCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:  true,
			Branch: "main",
		})
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	gitValidateCmd.Flags().Set("repo", "https://github.com/org/repo")
	gitValidateCmd.Flags().Set("branch", "main")
	t.Cleanup(func() {
		gitValidateCmd.Flags().Set("repo", "")
		gitValidateCmd.Flags().Set("branch", "")
	})

	err := gitValidateCmd.RunE(gitValidateCmd, []string{})
	require.NoError(t, err)

	var result types.GitValidateResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.True(t, result.Valid)
	assert.Equal(t, "main", result.Branch)
}

func TestGitValidateCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.GitValidateResponse{
			Valid:   true,
			Branch:  "main",
			Message: "",
		})
	}))
	defer server.Close()

	buf := setupGitTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	gitValidateCmd.Flags().Set("repo", "https://github.com/org/repo")
	gitValidateCmd.Flags().Set("branch", "main")
	t.Cleanup(func() {
		gitValidateCmd.Flags().Set("repo", "")
		gitValidateCmd.Flags().Set("branch", "")
	})

	err := gitValidateCmd.RunE(gitValidateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "valid: true")
	assert.Contains(t, out, "branch: main")
}
