//go:build live

package live

import (
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAPIKey_CRUD locks the API-key wire contract end-to-end:
// create → list → revoke. Catches drift on the highest-blast-radius
// surface — a field-name regression here would mask every other
// authenticated test (CI mints its key via this exact flow).
//
// We scope every key to the calling user (whoami) and revoke it on
// cleanup so the test leaves no debris in the user's keyring.
func TestLiveAPIKey_CRUD(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	me, err := c.Whoami()
	require.NoError(t, err, "whoami")
	require.NotEmpty(t, me.ID, "whoami must return a populated user id")

	prefix := liveResourcePrefix()
	days := 1

	// Create
	created, err := c.CreateAPIKey(me.ID, &types.CreateAPIKeyRequest{
		Name:          prefix + "-apikey",
		ExpiresInDays: &days,
	})
	require.NoError(t, err, "create api key")
	require.NotEmpty(t, created.ID, "created key must have an ID")
	require.NotEmpty(t, created.RawKey, "raw_key must be populated on create (this is the only time it's returned)")
	assert.Truef(t, len(created.RawKey) > len("sk_"), "raw_key %q must be longer than the sk_ prefix", created.RawKey)
	assert.Equal(t, "sk_", created.RawKey[:3], "raw_key must carry the sk_ prefix")
	assert.NotEmpty(t, created.Prefix, "prefix must be set so the key shows up in list")
	require.NotNil(t, created.ExpiresAt, "expires_at must echo back on create when expires_in_days was set")
	assert.True(t, created.ExpiresAt.After(time.Now()), "expires_at must be in the future")

	// Always best-effort revoke so a failed assertion doesn't leave the
	// key around the calling user's keyring.
	t.Cleanup(func() {
		_ = c.DeleteAPIKey(me.ID, created.ID)
	})

	// List — the new key must be visible. Raw key MUST NOT come back on list.
	keys, err := c.ListAPIKeys(me.ID)
	require.NoError(t, err, "list api keys")
	var found *types.APIKey
	for i := range keys {
		if keys[i].ID == created.ID {
			found = &keys[i]
			break
		}
	}
	require.NotNilf(t, found, "newly-created key %s must appear in list", created.ID)
	assert.Equal(t, created.Prefix, found.Prefix, "prefix must match between create and list responses")
	assert.Equal(t, me.ID, found.UserID, "list entry must echo back the owning user id")

	// Revoke (explicit — cleanup is the safety net).
	require.NoError(t, c.DeleteAPIKey(me.ID, created.ID), "revoke api key")

	// Confirm gone.
	after, err := c.ListAPIKeys(me.ID)
	require.NoError(t, err, "list api keys after revoke")
	for _, k := range after {
		assert.NotEqualf(t, created.ID, k.ID,
			"revoked key %s must not appear in list", created.ID)
	}
}
