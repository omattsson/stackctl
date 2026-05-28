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
			{InstanceID: "1", Status: "success"},
			{InstanceID: "2", Status: "success"},
			{InstanceID: "3", Status: "error", Error: "not found"},
		},
	}
}

// ---------- bulk deploy ----------

func TestBulkDeployCmd_TableOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/stack-instances/bulk/deploy", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.BulkInstancesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.InstanceIDs)

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
	assert.True(t, result.Results[0].Success())
	assert.False(t, result.Results[2].Success())
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
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
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
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
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
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
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
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
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

		var body types.BulkInstancesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.InstanceIDs)

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
		var body types.BulkInstancesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.InstanceIDs)

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

// ---- Dry-run Tests ----

func TestBulkDeleteCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkDeleteCmd.Flags().Set("ids", "10,20,30")
	bulkDeleteCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		bulkDeleteCmd.Flags().Set("ids", "")
		bulkDeleteCmd.Flags().Set("dry-run", "false")
	})

	err := bulkDeleteCmd.RunE(bulkDeleteCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would delete 3 stacks")
	assert.Contains(t, buf.String(), "10, 20, 30")
}

func TestBulkCleanCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkCleanCmd.Flags().Set("ids", "5,6")
	bulkCleanCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		bulkCleanCmd.Flags().Set("ids", "")
		bulkCleanCmd.Flags().Set("dry-run", "false")
	})

	err := bulkCleanCmd.RunE(bulkCleanCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would clean 2 stacks")
}

// ---------- bulk template publish ----------

func TestBulkTemplatePublishCmd_TableOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/bulk/publish", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var body types.BulkTemplatesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"1", "2", "3"}, body.TemplateIDs)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplatePublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplatePublishCmd.Flags().Set("ids", "") })

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "success")
	assert.Contains(t, out, "not found")
}

func TestBulkTemplatePublishCmd_JSONOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	bulkTemplatePublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplatePublishCmd.Flags().Set("ids", "") })

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.NoError(t, err)

	var result types.BulkResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result.Results, 3)
}

func TestBulkTemplatePublishCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkTemplatePublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplatePublishCmd.Flags().Set("ids", "") })

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
}

func TestBulkTemplatePublishCmd_QuietOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Quiet = true

	bulkTemplatePublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplatePublishCmd.Flags().Set("ids", "") })

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.NoError(t, err)

	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n2", lines)
}

func TestBulkTemplatePublishCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "unauthorized"})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkTemplatePublishCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkTemplatePublishCmd.Flags().Set("ids", "") })

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

// ---------- bulk template unpublish ----------

func TestBulkTemplateUnpublishCmd_TableOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/templates/bulk/unpublish", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateUnpublishCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkTemplateUnpublishCmd.Flags().Set("ids", "") })

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ID")
	assert.Contains(t, buf.String(), "STATUS")
}

func TestBulkTemplateUnpublishCmd_JSONOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	bulkTemplateUnpublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplateUnpublishCmd.Flags().Set("ids", "") })

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.NoError(t, err)

	var result types.BulkResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result.Results, 3)
}

func TestBulkTemplateUnpublishCmd_QuietOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Quiet = true

	bulkTemplateUnpublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplateUnpublishCmd.Flags().Set("ids", "") })

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.NoError(t, err)
	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n2", lines)
}

func TestBulkTemplateUnpublishCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkTemplateUnpublishCmd.Flags().Set("ids", "1,2,3")
	t.Cleanup(func() { bulkTemplateUnpublishCmd.Flags().Set("ids", "") })

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
}

func TestBulkTemplateUnpublishCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "permission denied"})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkTemplateUnpublishCmd.Flags().Set("ids", "1,2")
	t.Cleanup(func() { bulkTemplateUnpublishCmd.Flags().Set("ids", "") })

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestBulkTemplateUnpublishCmd_PositionalArgs(t *testing.T) {
	resp := sampleBulkResponse()
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/templates/bulk/unpublish" {
			json.NewEncoder(w).Encode(resp)
			return
		}
		// name→ID resolution: return numeric ID for the name
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:     []types.StackTemplate{{Base: types.Base{ID: "1"}, Name: r.URL.Query().Get("name"), Owner: "admin"}},
			Total:    1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{"my-template"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ID")
	// resolution + bulk call were made
	assert.GreaterOrEqual(t, callCount, 2)
}

// ---------- bulk template delete (destructive) ----------

func TestBulkTemplateDeleteCmd_WithConfirmation(t *testing.T) {
	called := false
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.Equal(t, "/api/v1/templates/bulk/delete", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
		bulkTemplateDeleteCmd.SetIn(nil)
		bulkTemplateDeleteCmd.SetErr(nil)
	})

	bulkTemplateDeleteCmd.SetIn(strings.NewReader("y\n"))
	bulkTemplateDeleteCmd.SetErr(&bytes.Buffer{})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

func TestBulkTemplateDeleteCmd_Declined(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called when user declines")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
		bulkTemplateDeleteCmd.SetIn(nil)
		bulkTemplateDeleteCmd.SetErr(nil)
	})

	bulkTemplateDeleteCmd.SetIn(strings.NewReader("n\n"))
	bulkTemplateDeleteCmd.SetErr(&bytes.Buffer{})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Aborted")
}

func TestBulkTemplateDeleteCmd_WithYesFlag(t *testing.T) {
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

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2")
	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, buf.String(), "ID")
}

func TestBulkTemplateDeleteCmd_JSONOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatJSON

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)

	var result types.BulkResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result.Results, 3)
}

func TestBulkTemplateDeleteCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "permission denied"})
	}))
	defer server.Close()

	_ = setupBulkTestCmd(t, server.URL)

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2")
	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestBulkTemplateDeleteCmd_YAMLOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Format = output.FormatYAML

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "instance_id: \"1\"")
	assert.Contains(t, out, "status: success")
}

func TestBulkTemplateDeleteCmd_QuietOutput(t *testing.T) {
	resp := sampleBulkResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)
	printer.Quiet = true

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2,3")
	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("yes", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	lines := strings.TrimSpace(buf.String())
	assert.Equal(t, "1\n2", lines)
}

func TestBulkTemplateDeleteCmd_PositionalArgs(t *testing.T) {
	resp := sampleBulkResponse()
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/v1/templates/bulk/delete" {
			json.NewEncoder(w).Encode(resp)
			return
		}
		// name→ID resolution
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:     []types.StackTemplate{{Base: types.Base{ID: "1"}, Name: r.URL.Query().Get("name"), Owner: "admin"}},
			Total:    1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateDeleteCmd.Flags().Set("yes", "true")
	t.Cleanup(func() { bulkTemplateDeleteCmd.Flags().Set("yes", "false") })

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{"my-template"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ID")
	assert.GreaterOrEqual(t, callCount, 2)
}

// ---------- resolveBulkTemplateIDs ----------

func TestResolveBulkTemplateIDs_NumericIDs(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,2,3")

	ids, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)
}

func TestResolveBulkTemplateIDs_UUIDs(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "550e8400-e29b-41d4-a716-446655440000,660e8400-e29b-41d4-a716-446655440001")

	ids, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"550e8400-e29b-41d4-a716-446655440000", "660e8400-e29b-41d4-a716-446655440001"}, ids)
}

func TestResolveBulkTemplateIDs_TemplateNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		id := "resolved-" + name
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:  []types.StackTemplate{{Base: types.Base{ID: id}, Name: name, Owner: "admin"}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "my-template,other-template")

	ids, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"resolved-my-template", "resolved-other-template"}, ids)
}

func TestResolveBulkTemplateIDs_MixedNamesAndIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:  []types.StackTemplate{{Base: types.Base{ID: "99"}, Name: name, Owner: "admin"}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "1,my-template")

	ids, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "99"}, ids)
}

func TestResolveBulkTemplateIDs_Dedup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data:  []types.StackTemplate{{Base: types.Base{ID: "42"}, Name: r.URL.Query().Get("name"), Owner: "admin"}},
			Total: 1, Page: 1, PageSize: 1,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "my-template,42")

	ids, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"42"}, ids)
}

func TestResolveBulkTemplateIDs_UnknownName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.ListResponse[types.StackTemplate]{
			Data: []types.StackTemplate{}, Total: 0, Page: 1, PageSize: 0,
		})
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	cmd.Flags().Set("ids", "nonexistent")

	_, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no template found")
}

func TestResolveBulkTemplateIDs_TooMany(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")
	parts := make([]string, 51)
	for i := range parts {
		parts[i] = strconv.Itoa(i + 1)
	}
	cmd.Flags().Set("ids", strings.Join(parts, ","))

	_, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum 50")
}

func TestResolveBulkTemplateIDs_Empty(t *testing.T) {
	c := client.New("http://unused")
	cmd := &cobra.Command{}
	cmd.Flags().String("ids", "", "")

	_, err := resolveBulkTemplateIDs(c, cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one template name or ID")
}

// ---------- bulk template dry-run ----------

func TestBulkTemplatePublishCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplatePublishCmd.Flags().Set("ids", "10,20,30")
	bulkTemplatePublishCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		bulkTemplatePublishCmd.Flags().Set("ids", "")
		bulkTemplatePublishCmd.Flags().Set("dry-run", "false")
	})

	err := bulkTemplatePublishCmd.RunE(bulkTemplatePublishCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would publish 3 templates")
	assert.Contains(t, buf.String(), "10, 20, 30")
}

func TestBulkTemplateUnpublishCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateUnpublishCmd.Flags().Set("ids", "5,6")
	bulkTemplateUnpublishCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		bulkTemplateUnpublishCmd.Flags().Set("ids", "")
		bulkTemplateUnpublishCmd.Flags().Set("dry-run", "false")
	})

	err := bulkTemplateUnpublishCmd.RunE(bulkTemplateUnpublishCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would unpublish 2 templates")
}

func TestBulkTemplateDeleteCmd_DryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should NOT be called in dry-run mode")
	}))
	defer server.Close()

	buf := setupBulkTestCmd(t, server.URL)

	bulkTemplateDeleteCmd.Flags().Set("ids", "1,2")
	bulkTemplateDeleteCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		bulkTemplateDeleteCmd.Flags().Set("ids", "")
		bulkTemplateDeleteCmd.Flags().Set("dry-run", "false")
	})

	err := bulkTemplateDeleteCmd.RunE(bulkTemplateDeleteCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Would delete 2 templates")
}
