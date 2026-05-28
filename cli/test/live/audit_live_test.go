//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAuditLog_List verifies the audit log read contract. The backend
// is expected to always have at least the login event from our own auth
// call earlier in the suite.
func TestLiveAuditLog_List(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	resp, err := c.ListAuditLogs(types.AuditLogListParams{Limit: 5})
	require.NoError(t, err, "list audit logs")
	// We expect at least one entry (our auth call generated one). If the
	// backend has audit disabled the list will be empty; that's a config
	// issue we don't want to fail the suite on.
	if len(resp.Data) == 0 {
		t.Skip("audit log empty — likely disabled on this backend")
	}

	first := resp.Data[0]
	assert.NotEmpty(t, first.Action, "audit entry must have an action")
	assert.False(t, first.Timestamp.IsZero(), "audit entry must have a timestamp")
}

// TestLiveAuditLog_FilterByEntityType locks the filter wire contract.
// The persistent flags on `stackctl audit log` map to these query params;
// if backend names drift, lists silently return un-filtered data.
func TestLiveAuditLog_FilterByEntityType(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	resp, err := c.ListAuditLogs(types.AuditLogListParams{
		EntityType: "template",
		Limit:      10,
	})
	require.NoError(t, err, "list audit logs filtered by entity_type")

	// All returned entries must match the filter — verifies the backend
	// actually applied it. Empty result is fine (no template ops yet).
	for _, e := range resp.Data {
		assert.Equal(t, "template", e.EntityType,
			"filter must be applied server-side, got entry with entity_type=%q", e.EntityType)
	}
}

// TestLiveAuditLog_ExportJSON locks the export format wire contract.
// Backend serves the export as a streamed JSON body; client wraps it
// into ExportAuditLogs which returns raw bytes.
func TestLiveAuditLog_ExportJSON(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	out, err := c.ExportAuditLogs("json", types.AuditLogListParams{Limit: 5})
	if err != nil {
		// Export is admin-gated — skip if the API key doesn't have it.
		if strings.Contains(err.Error(), "Permission denied") || strings.Contains(err.Error(), "forbidden") {
			t.Skipf("audit log export is admin-gated: %v", err)
		}
		require.NoError(t, err, "export audit logs")
	}
	assert.NotEmpty(t, out, "export must return non-empty bytes")
	// Should look like JSON — start with [ or { after optional whitespace.
	trimmed := strings.TrimSpace(string(out))
	require.NotEmpty(t, trimmed)
	first := trimmed[0]
	assert.True(t, first == '[' || first == '{',
		"JSON export must begin with [ or {, got %q", first)
}
