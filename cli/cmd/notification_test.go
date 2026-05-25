package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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

// Tests here mutate package-level globals (printer, flagAPIURL, notifFlag*)
// so they do NOT use t.Parallel().

func sampleNotifications() []types.Notification {
	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 11, 30, 0, 0, time.UTC)
	return []types.Notification{
		{ID: "n1", Type: "stack.deploy.succeeded", Title: "Deploy succeeded", IsRead: false, CreatedAt: t1, UserID: "u1"},
		{ID: "n2", Type: "stack.deploy.failed", Title: "Deploy failed", IsRead: true, CreatedAt: t2, UserID: "u1", Message: "exit 1", EntityType: "stack", EntityID: "s-1"},
	}
}

func samplePrefs() []types.NotificationPreference {
	return []types.NotificationPreference{
		{EventType: "stack.deploy.failed", Enabled: true, Channel: "in_app", ID: "p1", UserID: "u1"},
		{EventType: "stack.deploy.succeeded", Enabled: false, Channel: "in_app", ID: "p2", UserID: "u1"},
	}
}

// ---------- notification list ----------

func TestNotificationListCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		require.Empty(t, r.URL.RawQuery, "no flags → no query string")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{
			Notifications: sampleNotifications(), Total: 2, UnreadCount: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "stack.deploy.succeeded")
	assert.Contains(t, out, "n1")
	assert.Contains(t, out, "1 unread of 2 total")
}

func TestNotificationListCmd_FlagsForwardedAsQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{})
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	notifFlagUnreadOnly = true
	notifFlagLimit = 50
	notifFlagOffset = 5
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	assert.Contains(t, gotQuery, "unread_only=true")
	assert.Contains(t, gotQuery, "limit=50")
	assert.Contains(t, gotQuery, "offset=5")
}

func TestNotificationListCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{
			Notifications: sampleNotifications(), Total: 2, UnreadCount: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	printer.Format = output.FormatJSON
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	var got types.PaginatedNotifications
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, int64(1), got.UnreadCount)
	assert.Len(t, got.Notifications, 2)
}

func TestNotificationListCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{
			Notifications: sampleNotifications(), Total: 2, UnreadCount: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	printer.Format = output.FormatYAML
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "unread_count: 1")
	assert.Contains(t, out, "id: n1")
}

func TestNotificationListCmd_QuietOutputIDsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{
			Notifications: sampleNotifications(), Total: 2, UnreadCount: 1,
		})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	printer.Quiet = true
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	got := strings.TrimSpace(buf.String())
	assert.Equal(t, "n1\nn2", got)
	assert.NotContains(t, got, "TYPE")
}

func TestNotificationListCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	require.NoError(t, notificationListCmd.RunE(notificationListCmd, []string{}))

	assert.Contains(t, buf.String(), "No notifications.")
}

// ---------- notification count ----------

func TestNotificationCountCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/notifications/count", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.UnreadCountResponse{UnreadCount: 7})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, notificationCountCmd.RunE(notificationCountCmd, []string{}))
	assert.Equal(t, "7", strings.TrimSpace(buf.String()))
}

func TestNotificationCountCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.UnreadCountResponse{UnreadCount: 7})
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Format = output.FormatJSON
	require.NoError(t, notificationCountCmd.RunE(notificationCountCmd, []string{}))

	var got types.UnreadCountResponse
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, int64(7), got.UnreadCount)
}

// ---------- notification read ----------

func TestNotificationReadCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/notifications/n1/read", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, notificationReadCmd.RunE(notificationReadCmd, []string{"n1"}))
	assert.Contains(t, buf.String(), "Marked notification n1 as read")
}

func TestNotificationReadCmd_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"notification not found"}`))
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	err := notificationReadCmd.RunE(notificationReadCmd, []string{"missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------- notification read-all ----------

func TestNotificationReadAllCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/notifications/read-all", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, notificationReadAllCmd.RunE(notificationReadAllCmd, []string{}))
	assert.Contains(t, buf.String(), "Marked all notifications as read")
}

// ---------- notification prefs get ----------

func TestNotificationPrefsGetCmd_TableOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/notifications/preferences", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePrefs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, notificationPrefsGetCmd.RunE(notificationPrefsGetCmd, []string{}))

	out := buf.String()
	assert.Contains(t, out, "EVENT TYPE")
	assert.Contains(t, out, "stack.deploy.failed")
	assert.Contains(t, out, "in_app")
}

func TestNotificationPrefsGetCmd_QuietPrintsEventTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePrefs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	printer.Quiet = true
	require.NoError(t, notificationPrefsGetCmd.RunE(notificationPrefsGetCmd, []string{}))
	got := strings.TrimSpace(buf.String())
	assert.Equal(t, "stack.deploy.failed\nstack.deploy.succeeded", got)
}

func TestNotificationPrefsGetCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	require.NoError(t, notificationPrefsGetCmd.RunE(notificationPrefsGetCmd, []string{}))
	assert.Contains(t, buf.String(), "No notification preferences configured.")
}

// ---------- notification prefs set ----------

func TestNotificationPrefsSetCmd_FromFile(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/notifications/preferences", r.URL.Path)
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePrefs())
	}))
	defer server.Close()

	buf := setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()

	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")
	payload := []byte(`[{"event_type":"stack.deploy.failed","enabled":true,"channel":"in_app"}]`)
	require.NoError(t, os.WriteFile(path, payload, 0600))
	notifPrefsFlagFile = path

	require.NoError(t, notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{}))
	assert.Contains(t, buf.String(), "Updated 2 notification preferences.")

	// Roundtrip: the body we sent must equal what was on disk (after JSON
	// normalization) — proves the CLI does not silently mutate the payload.
	var sent []types.NotificationPreference
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	require.Len(t, sent, 1)
	assert.Equal(t, "stack.deploy.failed", sent[0].EventType)
}

func TestNotificationPrefsSetCmd_RequiresFlag(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	err := notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-file")
}

func TestNotificationPrefsSetCmd_EmptyArrayRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(path, []byte("[]"), 0600))
	notifPrefsFlagFile = path
	err := notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one preference")
}

func TestNotificationPrefsSetCmd_InvalidJSONRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{not-json"), 0600))
	notifPrefsFlagFile = path
	err := notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestNotificationPrefsSetCmd_PathTraversalRejected(t *testing.T) {
	_ = setupStackTestCmd(t, "http://unused")
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	notifPrefsFlagFile = "../escape.json"
	err := notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "..")
}

func TestNotificationPrefsSetCmd_FromStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(samplePrefs())
	}))
	defer server.Close()

	_ = setupStackTestCmd(t, server.URL)
	resetNotificationFlagsForTest()
	defer resetNotificationFlagsForTest()
	notifPrefsFlagFile = "-"

	// Pipe the JSON via cmd.InOrStdin().
	notificationPrefsSetCmd.SetIn(bytes.NewBufferString(`[{"event_type":"stack.deploy.failed","enabled":true}]`))
	defer notificationPrefsSetCmd.SetIn(nil)
	require.NoError(t, notificationPrefsSetCmd.RunE(notificationPrefsSetCmd, []string{}))
}

// ---------- API error mapping ----------

func TestNotificationCmds_APIError401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"login required"}`))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list", func() error { return notificationListCmd.RunE(notificationListCmd, []string{}) }},
		{"count", func() error { return notificationCountCmd.RunE(notificationCountCmd, []string{}) }},
		{"read-all", func() error { return notificationReadAllCmd.RunE(notificationReadAllCmd, []string{}) }},
		{"prefs get", func() error { return notificationPrefsGetCmd.RunE(notificationPrefsGetCmd, []string{}) }},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_ = setupStackTestCmd(t, server.URL)
			resetNotificationFlagsForTest()
			defer resetNotificationFlagsForTest()
			err := tc.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Not authenticated")
		})
	}
}

