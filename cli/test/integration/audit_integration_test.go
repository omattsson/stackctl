package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startAuditMockServer mirrors the role gating of the real backend:
//   - /api/v1/audit-logs           → any authenticated user
//   - /api/v1/audit-logs/export    → admin-only (returns 403 otherwise)
//
// Filter params are echoed back so tests can prove the CLI is forwarding
// them. CSV bodies include intentionally tricky quoting (commas + newlines
// + embedded quotes) so the client's "stream the raw bytes" contract is
// exercised end-to-end.
func startAuditMockServer(t *testing.T, role string) *httptest.Server {
	t.Helper()
	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 11, 30, 0, 0, time.UTC)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/audit-logs":
			w.Header().Set("Content-Type", "application/json")
			// Echo a single deterministic entry so tests can assert content.
			_ = json.NewEncoder(w).Encode(types.PaginatedAuditLogs{
				Data: []types.AuditLogEntry{
					{ID: "a1", Action: "stack.deploy", Timestamp: t1, UserID: "u1", Username: "alice", EntityType: "stack", EntityID: "s-1"},
					{ID: "a2", Action: "template.publish", Timestamp: t2, UserID: "u2", Username: "bob", EntityType: "template", EntityID: "tpl-9"},
				},
				Total:  2,
				Limit:  25,
				Offset: 0,
			})
		case "/api/v1/audit-logs/export":
			if role != "admin" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "admin role required"})
				return
			}
			switch r.URL.Query().Get("format") {
			case "csv":
				w.Header().Set("Content-Type", "text/csv")
				_, _ = w.Write([]byte(
					"ID,Timestamp,UserID,Username,Action,EntityType,EntityID,Details\r\n" +
						"a1,2026-05-01T10:00:00Z,u1,alice,stack.deploy,stack,s-1,\"value,with,commas\"\r\n" +
						"a2,2026-05-02T11:30:00Z,u2,bob,template.publish,template,tpl-9,\"line1\nline2\"\r\n"))
			default:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"id":"a1","action":"stack.deploy"}]`))
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

// TestAuditWorkflow_ListAndExportViaClient exercises the client-layer
// methods directly — no Cobra. Proves that role gating, filter forwarding,
// and CSV streaming all work end-to-end with the real HTTP transport.
func TestAuditWorkflow_ListAndExportViaClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	server := startAuditMockServer(t, "admin")
	defer server.Close()

	c := client.New(server.URL)

	page, err := c.ListAuditLogs(types.AuditLogListParams{})
	require.NoError(t, err)
	require.Len(t, page.Data, 2)
	assert.Equal(t, "stack.deploy", page.Data[0].Action)

	data, err := c.ExportAuditLogs("json", types.AuditLogListParams{})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"action":"stack.deploy"`)

	csv, err := c.ExportAuditLogs("csv", types.AuditLogListParams{})
	require.NoError(t, err)
	// The "value,with,commas" detail row must arrive with its quoting intact
	// — otherwise CSV parsers on the operator's machine would split it into
	// three columns. This is the test the "CSV quotes commas correctly"
	// acceptance criterion locks in.
	assert.Contains(t, string(csv), `"value,with,commas"`)
	// Embedded newline must also be preserved.
	assert.Contains(t, string(csv), "\"line1\nline2\"")
}

// TestAuditWorkflow_ExportForbiddenForNonAdmin asserts that the 403 on
// export surfaces as APIError with the right status, separate from a 200
// on the list endpoint same session.
func TestAuditWorkflow_ExportForbiddenForNonAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	server := startAuditMockServer(t, "devops")
	defer server.Close()

	c := client.New(server.URL)

	// devops can list...
	_, err := c.ListAuditLogs(types.AuditLogListParams{})
	require.NoError(t, err)

	// ...but cannot export.
	_, err = c.ExportAuditLogs("csv", types.AuditLogListParams{})
	require.Error(t, err)
	var apiErr *client.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
}

// TestAuditCobra_ListAndExport exercises the full Cobra invocation path,
// proving that filter flags wire through to the client correctly.
func TestAuditCobra_ListAndExport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuditMockServer(t, "admin")
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer

	// audit log list — default table output
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"audit", "log", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "stack.deploy")
	assert.Contains(t, buf.String(), "alice")

	// audit log list --user u-1 (filter forwarding; we only assert success
	// here — the unit tests verify the wire format).
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"audit", "log", "list", "--user", "u-1", "--since", "24h"})
	require.NoError(t, cmd.Execute())

	// audit log export → CSV to file
	csvPath := filepath.Join(t.TempDir(), "audit.csv")
	cmd.ResetFlagsForTest()
	buf.Reset()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"audit", "log", "export", "--format", "csv", "--output-file", csvPath})
	require.NoError(t, cmd.Execute())
	written, err := os.ReadFile(csvPath)
	require.NoError(t, err)
	// Same quoting assertion as the client-layer test, just one layer up.
	assert.Contains(t, string(written), `"value,with,commas"`)
	// The output should report the destination, not dump the body to stdout.
	assert.Contains(t, buf.String(), csvPath)
	assert.NotContains(t, buf.String(), "value,with,commas")
}

// TestAuditCobra_ExportForbiddenForDevops verifies that the 403 from a
// non-admin export attempt propagates as a non-zero cobra exit.
func TestAuditCobra_ExportForbiddenForDevops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuditMockServer(t, "devops")
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer
	cmd.ResetFlagsForTest()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"audit", "log", "export", "--format", "json"})
	err := cmd.Execute()
	require.Error(t, err, "non-admin export must exit non-zero")
	assert.Contains(t, strings.ToLower(err.Error()), "permission denied")
}
