package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBulkTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	return setupStackTestCmd(t, apiURL)
}

func sampleBulkResponse() types.BulkResponse {
	return types.BulkResponse{
		Results: []types.BulkOperationResult{
			{ID: "1", Success: true},
			{ID: "2", Success: true},
			{ID: "3", Success: false, Error: "not found"},
		},
	}
}

// ---------- bulk deploy ----------

func TestBulkDeployCmd_TableOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/bulk/deploy", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.BulkRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.IDs)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeployCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "success")
	assert.Contains(t, out, "not found")
}

func TestBulkDeployCmd_JSONOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	bulkDeployCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.NoError(t, err)

	var result types.BulkResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result.Results, 3)
	assert.True(t, result.Results[0].Success)
	assert.False(t, result.Results[2].Success)
}

func TestBulkDeployCmd_QuietOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Quiet = true

	bulkDeployCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n2", lines)
}

// ---------- bulk stop ----------

func TestBulkStopCmd_Success(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/bulk/stop", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkStopCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkStopCmd.Flags().Set("ids", "") })

	err := bulkStopCmd.RunE(bulkStopCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STATUS")
}

// ---------- bulk clean (destructive) ----------

func TestBulkCleanCmd_WithConfirmation(t *testing.T) {
	called := false
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/bulk/clean", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkCleanCmd.Flags().Set("ids", "1,2,3")
	bulkCleanCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkCleanCmd.Flags().Set("ids", "")
		bulkCleanCmd.Flags().Set("yes", "false")
		bulkCleanCmd.SetIn(nil)
		bulkCleanCmd.SetErr(nil)
	})

	bulkCleanCmd.SetIn(strings.NewReader("y\n"))
	bulkCleanCmd.SetErr(&bytes.Buffer{})

	err := bulkCleanCmd.RunE(bulkCleanCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

func TestBulkCleanCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkCleanCmd.Flags().Set("ids", "1,2,3")
	bulkCleanCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkCleanCmd.Flags().Set("ids", "")
		bulkCleanCmd.Flags().Set("yes", "false")
		bulkCleanCmd.SetIn(nil)
		bulkCleanCmd.SetErr(nil)
	})

	bulkCleanCmd.SetIn(strings.NewReader("n\n"))
	bulkCleanCmd.SetErr(&bytes.Buffer{})

	err := bulkCleanCmd.RunE(bulkCleanCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestBulkCleanCmd_WithYesFlag(t *testing.T) {
	called := false
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkCleanCmd.Flags().Set("ids", "1,2")
	bulkCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkCleanCmd.Flags().Set("ids", "")
		bulkCleanCmd.Flags().Set("yes", "false")
	})

	err := bulkCleanCmd.RunE(bulkCleanCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

// ---------- bulk delete (destructive) ----------

func TestBulkDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/stack-instances/bulk/delete", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkDeleteCmd.Flags().Set("ids", "")
		bulkDeleteCmd.Flags().Set("yes", "false")
		bulkDeleteCmd.SetIn(nil)
		bulkDeleteCmd.SetErr(nil)
	})

	bulkDeleteCmd.SetIn(strings.NewReader("y\n"))
	bulkDeleteCmd.SetErr(&bytes.Buffer{})

	err := bulkDeleteCmd.RunE(bulkDeleteCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

func TestBulkDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkDeleteCmd.Flags().Set("ids", "")
		bulkDeleteCmd.Flags().Set("yes", "false")
		bulkDeleteCmd.SetIn(nil)
		bulkDeleteCmd.SetErr(nil)
	})

	bulkDeleteCmd.SetIn(strings.NewReader("n\n"))
	bulkDeleteCmd.SetErr(&bytes.Buffer{})

	err := bulkDeleteCmd.RunE(bulkDeleteCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestBulkDeleteCmd_WithYesFlag(t *testing.T) {
	called := false
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeleteCmd.Flags().Set("ids", "1,2")
	bulkDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkDeleteCmd.Flags().Set("ids", "")
		bulkDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkDeleteCmd.RunE(bulkDeleteCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

// ---------- resolveBulkIDs ----------

func TestResolveBulkIDs_NumericIDs(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,2,3")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

func TestResolveBulkIDs_UUIDs(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "550e8400-e29b-41d4-a716-446655440000,660e8400-e29b-41d4-a716-446655440001")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"550e8400-e29b-41d4-a716-446655440000", "660e8400-e29b-41d4-a716-446655440001"}, ids)
}

func TestResolveBulkIDs_StackNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		id := "resolved-" + name
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:  []types.StackInstance{{Base: types.Base{ID: id}, Name: name}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "my-stack,other-stack")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"resolved-my-stack", "resolved-other-stack"}, ids)
}

func TestResolveBulkIDs_MixedNamesAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:  []types.StackInstance{{Base: types.Base{ID: "99"}, Name: name}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,my-stack")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "99"}, ids)
}

func TestResolveBulkIDs_NameDedup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data:  []types.StackInstance{{Base: types.Base{ID: "42"}, Name: r.URL.Query().Get("name")}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "my-stack,42")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"42"}, ids)
}

func TestResolveBulkIDs_UnknownName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackInstance]{
			Data: []types.StackInstance{}, Total: 0, Page: 1, PageSize: 0,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "nonexistent")

	_, err := resolveBulkIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stack found")
}

func TestResolveBulkIDs_TooMany(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	parts := make([]string, 51)
	for i := range parts {
		parts[i] = strconv.Itoa(i + 1)
	}
	cmd.Flags().Set("ids", strings.Join(parts, ","))

	_, err := resolveBulkIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum 50")
}

func TestResolveBulkIDs_Empty(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")

	_, err := resolveBulkIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one stack name or ID")
}

func TestBulkDeployCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unauthorized"})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkDeployCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

func TestBulkDeployCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkDeployCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "id: \"1\"")
	assert.Contains(t, out, "success: true")
	assert.Contains(t, out, "error: not found")
}

// ---------- bulk stop additional output modes ----------

func TestBulkStopCmd_JSONOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/bulk/stop", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	bulkStopCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkStopCmd.Flags().Set("ids", "") })

	err := bulkStopCmd.RunE(bulkStopCmd, []string{})
	require.NoError(t, err)

	var result types.BulkResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result.Results, 3)
}

func TestBulkStopCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkStopCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkStopCmd.Flags().Set("ids", "") })

	err := bulkStopCmd.RunE(bulkStopCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "id: \"1\"")
	assert.Contains(t, out, "success: true")
}

func TestBulkStopCmd_QuietOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Quiet = true

	bulkStopCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkStopCmd.Flags().Set("ids", "") })

	err := bulkStopCmd.RunE(bulkStopCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n2", lines)
}

func TestBulkStopCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "server error"})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkStopCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkStopCmd.Flags().Set("ids", "") })

	err := bulkStopCmd.RunE(bulkStopCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
}

// ---------- bulk clean additional output modes ----------

func TestBulkCleanCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkCleanCmd.Flags().Set("ids", "1,2")
	bulkCleanCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkCleanCmd.Flags().Set("ids", "")
		bulkCleanCmd.Flags().Set("yes", "false")
	})

	err := bulkCleanCmd.RunE(bulkCleanCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "id: \"1\"")
	assert.Contains(t, out, "success: true")
}

// ---------- bulk delete additional output modes ----------

func TestBulkDeleteCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkDeleteCmd.Flags().Set("ids", "1,2")
	bulkDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkDeleteCmd.Flags().Set("ids", "")
		bulkDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkDeleteCmd.RunE(bulkDeleteCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "id: \"1\"")
	assert.Contains(t, out, "success: true")
}

// ---------- resolveBulkIDs edge cases ----------

func TestResolveBulkIDs_OnlyCommas(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", ",,,")

	_, err := resolveBulkIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one stack name or ID")
}

func TestResolveBulkIDs_WhitespaceHandling(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", " 1 , 2 , 3 ")

	ids, err := resolveBulkIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

// ---------- positional and mixed args ----------

func TestResolveBulkIDs_PositionalArgs(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")

	ids, err := resolveBulkIDs(c, cmd, []string{"1", "2", "3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

func TestResolveBulkIDs_MixedFlagAndPositional(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,2")

	ids, err := resolveBulkIDs(c, cmd, []string{"3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

func TestResolveBulkIDs_MixedDedup(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,2")

	ids, err := resolveBulkIDs(c, cmd, []string{"2", "3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

func TestBulkDeployCmd_PositionalArgs(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/bulk/deploy", r.URL.Path)

		var body types.BulkRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.IDs)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{"1", "2", "3"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STATUS")
}

func TestBulkDeployCmd_MixedArgs(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body types.BulkRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.IDs)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeployCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{"3"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
}

func TestBulkDeployCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkDeployCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Server error")
}

func TestBulkDeployCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkDeployCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkDeployCmd.Flags().Set("ids", "") })

	err := bulkDeployCmd.RunE(bulkDeployCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}
