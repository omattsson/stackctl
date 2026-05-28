//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveFavorite_AddListRemove locks the full favorite wire contract.
// Field-name drift here would break stackctl's `favorite add/remove`
// commands silently.
func TestLiveFavorite_AddListRemove(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	def := requireDefinition(t, c)
	const entityType = "definition"

	// Ensure clean state — backend treats add as idempotent, but Remove
	// before is cheap and confirms the wire contract too.
	_ = c.RemoveFavorite(entityType, def.ID)

	// Add
	fav, err := c.AddFavorite(types.AddFavoriteRequest{
		EntityType: entityType,
		EntityID:   def.ID,
	})
	require.NoError(t, err, "add favorite")
	assert.Equal(t, def.ID, fav.EntityID, "response must echo entity_id")
	assert.Equal(t, entityType, fav.EntityType, "response must echo entity_type")

	// Always best-effort remove so the test leaves no trace.
	t.Cleanup(func() {
		_ = c.RemoveFavorite(entityType, def.ID)
	})

	// List — our new favorite must show up.
	list, err := c.ListFavorites()
	require.NoError(t, err, "list favorites")

	var found bool
	for _, f := range list {
		if f.EntityType == entityType && f.EntityID == def.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "newly added favorite (%s/%s) must appear in list", entityType, def.ID)

	// Remove — explicit call (cleanup is the safety net).
	require.NoError(t, c.RemoveFavorite(entityType, def.ID), "remove favorite")

	// Confirm gone.
	list2, err := c.ListFavorites()
	require.NoError(t, err)
	for _, f := range list2 {
		assert.False(t, f.EntityType == entityType && f.EntityID == def.ID,
			"favorite must be gone after RemoveFavorite, still found %+v", f)
	}
}
