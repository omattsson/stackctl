package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
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
			Action:     "stack.deploy",
			Details:    "deployed via UI",
			EntityID:   "stack-1",
			EntityType: "stack",
			ID:         "a1",
			Timestamp:  t1,
			UserID:     "u1",
			Username:   "alice",
		},
		{
			Action:     "template.publish",
			Details:    "",
			EntityID:   "tpl-9",
			EntityType: "template",
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
	auditFlagAction = "stack.deploy"
	auditFlagEntityType = "stack"
	auditFlagEntityID = "stack-1"
	auditFlagSince = "2026-05-01T00:00:00Z"
	auditFlagUntil = "2026-05-02T00:00:00Z"
	auditFlagLimit = 50
	auditFlagOffset = 10
	auditFlagCursor = "c-abc"

	p, err := buildAuditListParams()
	require.NoError(t, err)
	assert.Equal(t, "u1", p.UserID)
	assert.Equal(t, "stack.deploy", p.Action)
	assert.Equal(t, "stack", p.EntityType)
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
	assert.Contains(t, out, "stack.deploy")
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
	auditFlagAction = "stack.deploy"
	auditFlagEntityType = "stack"
	auditFlagEntityID = "s-1"
	auditFlagSince = "2026-05-01T00:00:00Z"
	auditFlagUntil = "2026-05-02T00:00:00Z"
	auditFlagLimit = 50
	auditFlagOffset = 10

	require.NoError(t, auditLogListCmd.RunE(auditLogListCmd, []string{}))

	assert.Contains(t, gotQuery, "user_id=u-42")
	assert.Contains(t, gotQuery, "action=stack.deploy")
	assert.Contains(t, gotQuery, "entity_type=stack")
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
	assert.Contains(t, out, "action: stack.deploy")
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
	want := []byte(`[{"id":"a1","action":"stack.deploy"}]`)
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

