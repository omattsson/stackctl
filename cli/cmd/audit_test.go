package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file mutate package-level globals (printer, flagAPIURL,
// auditFlag*) and therefore do not use t.Parallel(). The test helper
// resets every audit flag before each test so flag leakage between in-
// process Cobra invocations is impossible.

func sampleAuditEntries() []types.AuditLogEntry {
	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 11, 30, 0, 0, time.UTC)
	return []types.AuditLogEntry{
		{
			Action:     "deploy",
			Details:    "deployed via UI",
			EntityID:   "stack-1",
			EntityType: "stack_instance",
			ID:         "a1",
			Timestamp:  t1,
			UserID:     "u1",
			Username:   "alice",
		},
		{
			Action:     "template.publish",
			Details:    "",
			EntityID:   "tpl-9",
			EntityType: "stack_template",
			ID:         "a2",
			Timestamp:  t2,
			UserID:     "u2",
			Username:   "bob",
		},
	}
}

func samplePaginatedAuditLogs() types.PaginatedAuditLogs {
	return types.PaginatedAuditLogs{
		Data:   sampleAuditEntries(),
		Total:  2,
		Limit:  25,
		Offset: 0,
	}
}

// ---------- parseTimeFlag ----------

func TestParseTimeFlag(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		input   string
		wantErr bool
		want    time.Time
	}{
		{name: "rfc3339_utc", input: "2026-05-01T00:00:00Z", want: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{name: "rfc3339_with_offset", input: "2026-05-01T02:00:00+02:00", want: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{name: "duration_24h", input: "24h", want: now.Add(-24 * time.Hour)},
		{name: "duration_30m", input: "30m", want: now.Add(-30 * time.Minute)},
		{name: "duration_composed", input: "2h45m", want: now.Add(-(2*time.Hour + 45*time.Minute))},
		{name: "negative_duration_rejected", input: "-1h", wantErr: true},
		{name: "days_rejected", input: "7d", wantErr: true}, // documented gotcha
		{name: "garbage", input: "yesterday", wantErr: true},
		{name: "empty_after_trim", input: "   ", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTimeFlag(tc.input, now)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, got.Equal(tc.want), "got %v want %v", got, tc.want)
		})
	}
}

// ---------- buildAuditListParams ----------

func TestBuildAuditListParams_AllFilters(t *testing.T) {
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagUser = "u1"
	auditFlagAction = "deploy"
	auditFlagEntityType = "stack_instance"
	auditFlagEntityID = "stack-1"
	auditFlagSince = "2026-05-01T00:00:00Z"
	auditFlagUntil = "2026-05-02T00:00:00Z"
	auditFlagLimit = 50
	auditFlagOffset = 10
	auditFlagCursor = "c-abc"

	p, err := buildAuditListParams()
	require.NoError(t, err)
	assert.Equal(t, "u1", p.UserID)
	assert.Equal(t, "deploy", p.Action)
	assert.Equal(t, "stack_instance", p.EntityType)
	assert.Equal(t, "stack-1", p.EntityID)
	assert.Equal(t, "c-abc", p.Cursor)
	assert.Equal(t, 50, p.Limit)
	assert.Equal(t, 10, p.Offset)
	require.NotNil(t, p.StartDate)
	require.NotNil(t, p.EndDate)
	assert.Equal(t, "2026-05-01T00:00:00Z", p.StartDate.UTC().Format(time.RFC3339))
	assert.Equal(t, "2026-05-02T00:00:00Z", p.EndDate.UTC().Format(time.RFC3339))
}

func TestBuildAuditListParams_InvalidSince(t *testing.T) {
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagSince = "garbage"
	_, err := buildAuditListParams()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--since")
}

// ---------- audit log list ----------

func TestAuditLogListCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/audit-logs", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		// No filters set → query should be empty
		assert.Empty(t, r.URL.Query().Encode())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePaginatedAuditLogs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "TIMESTAMP")
	assert.Contains(t, out, "deploy")
	assert.Contains(t, out, "template.publish")
	assert.Contains(t, out, "alice")
}

func TestAuditLogListCmd_ForwardsFiltersAsQueryParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedAuditLogs{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagUser = "u-42"
	auditFlagAction = "deploy"
	auditFlagEntityType = "stack_instance"
	auditFlagEntityID = "s-1"
	auditFlagSince = "2026-05-01T00:00:00Z"
	auditFlagUntil = "2026-05-02T00:00:00Z"
	auditFlagLimit = 50
	auditFlagOffset = 10

	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	assert.Contains(t, gotQuery, "user_id=u-42")
	assert.Contains(t, gotQuery, "action=deploy")
	assert.Contains(t, gotQuery, "entity_type=stack_instance")
	assert.Contains(t, gotQuery, "entity_id=s-1")
	assert.Contains(t, gotQuery, "start_date=2026-05-01T00%3A00%3A00Z")
	assert.Contains(t, gotQuery, "end_date=2026-05-02T00%3A00%3A00Z")
	assert.Contains(t, gotQuery, "limit=50")
	assert.Contains(t, gotQuery, "offset=10")
}

func TestAuditLogListCmd_QuietOutputIDsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePaginatedAuditLogs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	printer.Quiet = true
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	out := strings.TrimSpace(buf.String())
	assert.Equal(t, "a1\na2", out)
	assert.NotContains(t, out, "TIMESTAMP")
}

func TestAuditLogListCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePaginatedAuditLogs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	printer.Format = output.FormatJSON
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	var got types.PaginatedAuditLogs
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, int64(2), got.Total)
	assert.Len(t, got.Data, 2)
	assert.Equal(t, "a1", got.Data[0].ID)
}

func TestAuditLogListCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePaginatedAuditLogs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	printer.Format = output.FormatYAML
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "total: 2")
	assert.Contains(t, out, "action: deploy")
	assert.Contains(t, out, "id: a1")
}

func TestAuditLogListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedAuditLogs{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	assert.Contains(t, buf.String(), "No audit log entries found.")
}

func TestAuditLogListCmd_NextCursorHint(t *testing.T) {
	page := samplePaginatedAuditLogs()
	page.NextCursor = "c-next"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	assert.Contains(t, buf.String(), "--cursor c-next")
}

func TestAuditLogListCmd_InvalidSinceFlagRejectedBeforeAPICall(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagSince = "yesterday"
	err := auditLogListCmd.RunE(auditLogListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--since")
	assert.False(t, called, "API must not be called when flag parsing fails")
}

// ---------- audit log export ----------

func TestAuditLogExportCmd_JSONToStdout(t *testing.T) {
	want := []byte(`[{"id":"a1","action":"deploy"}]`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/audit-logs/export", r.URL.Path)
		assert.Equal(t, "json", r.URL.Query().Get("format"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(want)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogExportCmd.RunE(auditLogExportCmd, []string{}))
	assert.Equal(t, string(want), buf.String())
}

func TestAuditLogExportCmd_CSVQuotesCommasAndNewlines(t *testing.T) {
	// Backend-produced CSV with deliberately tricky fields. The CLI streams
	// it verbatim — proving we don't re-encode and don't drop quoting.
	csvBody := "ID,Timestamp,Details\r\n" +
		"a1,2026-05-01T10:00:00Z,\"value,with,commas\"\r\n" +
		"a2,2026-05-02T10:00:00Z,\"line1\nline2\"\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "csv", r.URL.Query().Get("format"))
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(csvBody))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagFormat = "csv"
	require.NoError(t, auditLogExportCmd.RunE(auditLogExportCmd, []string{}))
	assert.Equal(t, csvBody, buf.String(), "CSV body must be streamed byte-for-byte")
}

func TestAuditLogExportCmd_WriteToFile(t *testing.T) {
	want := []byte("ID,Timestamp\r\na1,2026-05-01T10:00:00Z\r\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(want)
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()

	dir := t.TempDir()
	dst := filepath.Join(dir, "audit.csv")
	auditFlagFormat = "csv"
	auditFlagOutputFile = dst
	require.NoError(t, auditLogExportCmd.RunE(auditLogExportCmd, []string{}))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	// Stdout should report the file write, NOT the bytes themselves.
	assert.Contains(t, buf.String(), dst)
	assert.NotContains(t, buf.String(), "a1,")
}

func TestAuditLogExportCmd_RejectsPathTraversal(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagOutputFile = "../escape.json"
	err := auditLogExportCmd.RunE(auditLogExportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
}

func TestAuditLogExportCmd_InvalidFormatRejected(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagFormat = "xml"
	err := auditLogExportCmd.RunE(auditLogExportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "json")
	assert.False(t, called, "API must not be called for invalid --format")
}

func TestAuditLogExportCmd_StripsPaginationFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server enforces its own limit/offset on export, so the client must
		// not echo user-supplied pagination — those would be silently ignored
		// and confuse the operator otherwise.
		q := r.URL.Query()
		assert.Empty(t, q.Get("limit"))
		assert.Empty(t, q.Get("offset"))
		assert.Empty(t, q.Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagLimit = 50
	auditFlagOffset = 10
	auditFlagCursor = "abc"
	require.NoError(t, auditLogExportCmd.RunE(auditLogExportCmd, []string{}))
}

// ---------- API error mapping ----------

func TestAuditLogListCmd_APIError403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"devops role required"}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	err := auditLogListCmd.RunE(auditLogListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

func TestAuditLogExportCmd_APIError403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"admin role required"}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	err := auditLogExportCmd.RunE(auditLogExportCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Permission denied")
}

func TestAuditLogListCmd_APIError500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db down"}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	err := auditLogListCmd.RunE(auditLogListCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Server error")
}

// ---------- helpers ----------

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{in: "abc", n: 5, want: "abc"},
		{in: "abcdefgh", n: 5, want: "abcd…"},
		{in: "abc", n: 0, want: ""},
		{in: "", n: 10, want: ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, truncate(tc.in, tc.n))
	}
}

func TestAuditUserLabel(t *testing.T) {
	assert.Equal(t, "alice", auditUserLabel(types.AuditLogEntry{Username: "alice", UserID: "u1"}))
	assert.Equal(t, "u1", auditUserLabel(types.AuditLogEntry{UserID: "u1"}))
	assert.Equal(t, "-", auditUserLabel(types.AuditLogEntry{}))
}

func TestAuditHelp_UsesServerNames(t *testing.T) {
	for _, cmd := range []*cobra.Command{auditLogListCmd, auditLogExportCmd} {
		assert.Contains(t, cmd.Long, "stack_instance")
		assert.Contains(t, cmd.Long, "cleanup_policy_executed")
		assert.NotContains(t, cmd.Long, "stack.deploy")
		assert.NotContains(t, cmd.Long, "--entity-type stack ")
		assert.Contains(t, cmd.Long, "k8s-stack-manager v0.7.0 or later")
		assert.Contains(t, cmd.Long, "--action create --entity-type deploy")
	}
	assert.Contains(t, auditLogCmd.PersistentFlags().Lookup("entity-type").Usage, "stack_instance")
	assert.NotContains(t, auditLogCmd.PersistentFlags().Lookup("action").Usage, "stack.deploy")
}

// ---------- k8s-stack-manager v0.18.0: new fields, filters, actions ----------

// auditPageNewAndOld is what a v0.18.0 server returns: a new entry with every
// new field (keys in a different order than the CLI prints them) and an entry
// from before the upgrade without any.
const auditPageNewAndOld = `{"total":2,"limit":25,"offset":0,"data":[
 {"username":"alice","user_id":"u1","timestamp":"2026-11-18T10:00:00Z","id":"a2","action":"deploy",
  "entity_type":"stack_instance","entity_id":"s-1","entity_name":"my-first-stack","details":"deployed via API",
  "auth_method":"api_key","api_key_id":"k-9","client_ip":"203.0.113.7","request_id":"req-1",
  "trace_id":"0af7651916cd43dd8448eb211c80319c"},
 {"id":"a1","timestamp":"2026-05-01T10:00:00Z","user_id":"u2","username":"bob","action":"create",
  "entity_type":"deploy","entity_id":"s-0","details":""}]}`

func auditServerReturning(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestBuildAuditListParams_UsernameAndAPIKeyID(t *testing.T) {
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	auditFlagUsername = "alice"
	auditFlagAPIKeyID = "k-9"

	p, err := buildAuditListParams()
	require.NoError(t, err)
	assert.Equal(t, "alice", p.Username)
	assert.Equal(t, "k-9", p.APIKeyID)
}

func TestAuditLogCmds_ForwardUsernameAndAPIKeyID(t *testing.T) {
	for _, name := range []string{"username", "api-key-id"} {
		require.NotNil(t, auditLogCmd.PersistentFlags().Lookup(name), "--%s is shared by list and export", name)
	}
	for name, cmd := range map[string]*cobra.Command{"list": auditLogListCmd, "export": auditLogExportCmd} {
		t.Run(name, func(t *testing.T) {
			var gotQuery url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			_ = setupStackTestCmd(t, server.URL)
			resetAuditFlagsForTest()
			defer resetAuditFlagsForTest()
			auditFlagUsername = "alice"
			auditFlagAPIKeyID = "k-9"

			require.NoError(t, cmd.RunE(cmd, []string{}))
			assert.Equal(t, "alice", gotQuery.Get("username"))
			assert.Equal(t, "k-9", gotQuery.Get("api_key_id"))
			assert.Empty(t, gotQuery.Get("user_id"))
		})
	}
}

func TestAuditLogListCmd_NewAndOldEntries_JSONGolden(t *testing.T) {
	server := auditServerReturning(t, auditPageNewAndOld)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	printer.Format = output.FormatJSON
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	want, err := os.ReadFile(filepath.Join("testdata", "audit_log_list.golden.json"))
	require.NoError(t, err)
	assert.Equal(t, string(want), buf.String(),
		"JSON output must match testdata/audit_log_list.golden.json byte-for-byte")
}

func TestAuditLogListCmd_NewAndOldEntries_YAMLKeepsNewFields(t *testing.T) {
	server := auditServerReturning(t, auditPageNewAndOld)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	printer.Format = output.FormatYAML
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	out := buf.String()
	for _, want := range []string{"entity_name: my-first-stack", "client_ip: 203.0.113.7", "request_id: req-1",
		"trace_id: 0af7651916cd43dd8448eb211c80319c", "auth_method: api_key", "api_key_id: k-9"} {
		assert.Contains(t, out, want)
	}
	assert.Equal(t, 1, strings.Count(out, "auth_method:"), "the old entry has no auth_method key")
}

func TestAuditLogListCmd_NewAndOldEntries_TableGolden(t *testing.T) {
	server := auditServerReturning(t, auditPageNewAndOld)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	want, err := os.ReadFile(filepath.Join("testdata", "audit_log_list_table.golden.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(want), buf.String(),
		"the table must match testdata/audit_log_list_table.golden.txt")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "AUTH")
	assert.NotContains(t, lines[0], "ENTITY ID")
	assert.Contains(t, lines[1], "my-first-stack", "the entity name is shown when it is set")
	assert.Contains(t, lines[1], "api_key")
	assert.Contains(t, lines[2], "s-0", "an entry without entity_name shows the entity ID")
	assert.NotContains(t, lines[2], "my-first-stack")
}

func TestAuditEntityAndAuthLabels(t *testing.T) {
	assert.Equal(t, "my-first-stack", auditEntityLabel(types.AuditLogEntry{EntityName: "my-first-stack", EntityID: "s-1"}))
	assert.Equal(t, "s-1", auditEntityLabel(types.AuditLogEntry{EntityID: "s-1"}))
	assert.Equal(t, "-", auditEntityLabel(types.AuditLogEntry{}))
	assert.Equal(t, "oidc", auditAuthLabel(types.AuditLogEntry{AuthMethod: "oidc"}))
	assert.Equal(t, "-", auditAuthLabel(types.AuditLogEntry{}))
}

func TestAuditLogExportCmd_JSONKeepsNewFields(t *testing.T) {
	want := `[{"action":"login","auth_method":"password","client_ip":"203.0.113.7","entity_name":"alice","id":"a3","request_id":"req-2","trace_id":"0af7651916cd43dd8448eb211c80319c"}]`
	server := auditServerReturning(t, want)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogExportCmd.RunE(auditLogExportCmd, []string{}))
	assert.Equal(t, want, buf.String(), "the export body passes through unchanged")
}

func TestAuditHelp_ListsSignInActionsAndNewFilters(t *testing.T) {
	for _, cmd := range []*cobra.Command{auditLogListCmd, auditLogExportCmd} {
		for _, action := range []string{"login", "login_failed", "oidc_login", "logout", "logout_all", "refresh_reuse_revoked"} {
			assert.Contains(t, cmd.Long, action)
		}
		assert.Contains(t, cmd.Long, "--username")
		assert.Contains(t, cmd.Long, "--api-key-id")
		assert.Contains(t, cmd.Long, "v0.18.0", "older servers ignore the new filters")
		assert.Contains(t, cmd.Long, "ignore", "the help says what an older server does with them")
	}
	for _, name := range []string{"username", "api-key-id"} {
		assert.Contains(t, auditLogCmd.PersistentFlags().Lookup(name).Usage, "v0.18.0")
	}
}

// entity_name and auth_method come from the server and can hold text a user
// chose (a template or stack name); a control sequence in them must not reach
// the terminal through the table.
func TestAuditLogListCmd_TableSanitizesNameAndAuth(t *testing.T) {
	server := auditServerReturning(t, `{"total":1,"limit":25,"offset":0,"data":[
 {"id":"a1","timestamp":"2026-11-18T10:00:00Z","user_id":"u1","username":"alice","action":"create",
  "entity_type":"stack_template","entity_id":"t-1","entity_name":"evil\u001b[2J\u001b]0;pwned\u0007name",
  "auth_method":"jwt\u001b[31m"}]}`)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "no escape sequence in the table")
	assert.NotContains(t, out, "\x07")
	assert.Contains(t, out, "evil")
	assert.Contains(t, out, "name")
}

// Entries from before v0.18.0 have no entity_name: the NAME column falls back
// to the entity ID, which an old server derived from the request route.
func TestAuditLogListCmd_TableSanitizesEntityIDFallback(t *testing.T) {
	server := auditServerReturning(t, `{"total":1,"limit":25,"offset":0,"data":[
 {"id":"a1","timestamp":"2026-05-01T10:00:00Z","user_id":"u1","username":"alice","action":"create",
  "entity_type":"deploy","entity_id":"s-\u001b[2J0"}]}`)
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetAuditFlagsForTest()
	defer resetAuditFlagsForTest()
	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))
	assert.NotContains(t, buf.String(), "\x1b")
	assert.Contains(t, buf.String(), "s-")
}
