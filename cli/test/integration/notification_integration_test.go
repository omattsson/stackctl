package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/cmd"
	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notificationState mirrors the backend's notification store closely enough
// to round-trip the read-all + count workflow: an atomic counter for unread,
// plus a slice of preferences that GET and PUT share.
type notificationState struct {
	unread int64
	prefs  []types.NotificationPreference
}

// startNotificationMockServer returns a mock that respects the unread counter
// (so `count` → `read-all` → `count` truly drops from N→0) and persists the
// preference array across GET/PUT.
func startNotificationMockServer(t *testing.T, state *notificationState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications":
			_ = json.NewEncoder(w).Encode(types.PaginatedNotifications{
				Notifications: []types.Notification{
					{ID: "n1", Type: "stack.deploy.failed", Title: "Deploy failed", CreatedAt: time.Now().UTC()},
				},
				Total:       1,
				UnreadCount: atomic.LoadInt64(&state.unread),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications/count":
			_ = json.NewEncoder(w).Encode(types.UnreadCountResponse{
				UnreadCount: atomic.LoadInt64(&state.unread),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/notifications/read-all":
			atomic.StoreInt64(&state.unread, 0)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications/preferences":
			_ = json.NewEncoder(w).Encode(state.prefs)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/notifications/preferences":
			var got []types.NotificationPreference
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: err.Error()})
				return
			}
			// Backend defaults empty channel to "in_app" on store.
			for i := range got {
				if got[i].Channel == "" {
					got[i].Channel = "in_app"
				}
			}
			state.prefs = got
			_ = json.NewEncoder(w).Encode(state.prefs)
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

// TestNotificationWorkflow_CountReadAllRoundTrip exercises the issue's
// acceptance criterion via the client: "Unread count matches server after
// read-all". count → 3 → read-all → count → 0.
func TestNotificationWorkflow_CountReadAllRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := &notificationState{unread: 3}
	server := startNotificationMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	got, err := c.CountUnreadNotifications()
	require.NoError(t, err)
	assert.Equal(t, int64(3), got)

	require.NoError(t, c.MarkAllNotificationsAsRead())

	got, err = c.CountUnreadNotifications()
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "read-all must drop the unread count to zero")
}

// TestNotificationWorkflow_PrefsRoundTrip exercises the issue's other
// acceptance criterion: "prefs get | jq … | prefs set round-trip works".
// GET → mutate → PUT → GET should observe the mutation.
func TestNotificationWorkflow_PrefsRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	t.Parallel()

	state := &notificationState{
		prefs: []types.NotificationPreference{
			{EventType: "stack.deploy.failed", Enabled: true, Channel: "in_app"},
			{EventType: "stack.deploy.succeeded", Enabled: true, Channel: "in_app"},
		},
	}
	server := startNotificationMockServer(t, state)
	defer server.Close()

	c := client.New(server.URL)

	got, err := c.GetNotificationPreferences()
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Mutate: disable succeeded.
	for i := range got {
		if got[i].EventType == "stack.deploy.succeeded" {
			got[i].Enabled = false
		}
	}
	updated, err := c.UpdateNotificationPreferences(got)
	require.NoError(t, err)

	// Verify the mutation persisted.
	var found bool
	for _, p := range updated {
		if p.EventType == "stack.deploy.succeeded" {
			assert.False(t, p.Enabled, "PUT must persist the Enabled=false mutation")
			found = true
		}
	}
	assert.True(t, found, "PUT response must include the mutated preference")

	// Re-fetch and confirm.
	again, err := c.GetNotificationPreferences()
	require.NoError(t, err)
	for _, p := range again {
		if p.EventType == "stack.deploy.succeeded" {
			assert.False(t, p.Enabled, "second GET must reflect the mutation")
		}
	}
}

// TestNotificationCobra_FullWorkflow exercises the Cobra command surface
// end-to-end: list → count → read-all → count and the prefs file workflow.
// Verifies that the notification subcommand flag vars don't leak between
// in-process Execute calls.
func TestNotificationCobra_FullWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	state := &notificationState{
		unread: 5,
		prefs: []types.NotificationPreference{
			{EventType: "stack.deploy.failed", Enabled: true, Channel: "in_app"},
		},
	}
	server := startNotificationMockServer(t, state)
	defer server.Close()

	t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKCTL_API_URL", server.URL)

	var buf bytes.Buffer
	resetAll := func() {
		cmd.ResetFlagsForTest()
		cmd.ResetNotificationFlagsForTest()
		buf.Reset()
		cmd.SetOut(&buf)
	}

	// count → 5
	resetAll()
	cmd.SetArgs([]string{"notification", "count"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "5")

	// list — verify entries surface
	resetAll()
	cmd.SetArgs([]string{"notification", "list"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "stack.deploy.failed")

	// read-all
	resetAll()
	cmd.SetArgs([]string{"notification", "read-all"})
	require.NoError(t, cmd.Execute())

	// count → 0
	resetAll()
	cmd.SetArgs([]string{"notification", "count"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "0", strings.TrimSpace(buf.String()))

	// prefs get | mutate | prefs set
	resetAll()
	cmd.SetArgs([]string{"notification", "prefs", "get", "-o", "json"})
	require.NoError(t, cmd.Execute())

	var prefs []types.NotificationPreference
	require.NoError(t, json.Unmarshal(buf.Bytes(), &prefs))
	require.Len(t, prefs, 1)
	prefs[0].Enabled = false

	mutated, err := json.Marshal(prefs)
	require.NoError(t, err)
	prefsPath := filepath.Join(t.TempDir(), "prefs.json")
	require.NoError(t, os.WriteFile(prefsPath, mutated, 0600))

	resetAll()
	cmd.SetArgs([]string{"notification", "prefs", "set", "--from-file", prefsPath})
	require.NoError(t, cmd.Execute())

	resetAll()
	cmd.SetArgs([]string{"notification", "prefs", "get", "-o", "json"})
	require.NoError(t, cmd.Execute())
	var after []types.NotificationPreference
	require.NoError(t, json.Unmarshal(buf.Bytes(), &after))
	require.Len(t, after, 1)
	assert.False(t, after[0].Enabled, "round-trip must persist Enabled=false")
}

