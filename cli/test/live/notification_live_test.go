//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveNotification_List ensures the list endpoint returns the
// PaginatedNotifications envelope shape — `notifications`, `total`,
// `unread_count`. A drift here would silently truncate the response.
func TestLiveNotification_List(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	resp, err := c.ListNotifications(client.NotificationListParams{Limit: 5})
	require.NoError(t, err, "list notifications")

	// Counters must decode even when there are no notifications.
	assert.GreaterOrEqual(t, resp.Total, int64(0))
	assert.GreaterOrEqual(t, resp.UnreadCount, int64(0))
	// Notifications slice may be empty but must not be nil.
	assert.NotNil(t, resp.Notifications)
}

// TestLiveNotification_PreferencesRoundTrip locks the prefs update wire
// contract. We read current prefs, write them back unchanged, and verify
// the response matches input — any field-name drift would cause the
// backend to ignore values.
func TestLiveNotification_PreferencesRoundTrip(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefs, err := c.GetNotificationPreferences()
	require.NoError(t, err, "get notification preferences")

	// If empty, backend has no prefs configured yet — seed one entry to
	// exercise the write path. We can't restore to empty afterwards
	// because the backend rejects "at least one preference is required"
	// (verified live 2026-05-28); leaving the seeded pref in place is
	// safe — subsequent runs hit the round-trip branch below.
	if len(prefs) == 0 {
		seeded := []types.NotificationPreference{
			{EventType: "stack.deployed", Enabled: true},
		}
		got, err := c.UpdateNotificationPreferences(seeded)
		require.NoError(t, err, "seed notification preference")
		require.Len(t, got, 1)
		assert.Equal(t, "stack.deployed", got[0].EventType)
		assert.True(t, got[0].Enabled)
		return
	}

	// Otherwise echo current prefs back and verify response matches input.
	got, err := c.UpdateNotificationPreferences(prefs)
	require.NoError(t, err, "update notification preferences (echo)")
	require.Len(t, got, len(prefs), "response length must match input")
	for i := range prefs {
		assert.Equal(t, prefs[i].EventType, got[i].EventType, "event_type[%d]", i)
		assert.Equal(t, prefs[i].Enabled, got[i].Enabled, "enabled[%d]", i)
	}
}
