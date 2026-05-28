//go:build live

package live

import (
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveUser_RegisterListDisableEnable creates a throwaway user via
// /auth/register, exercises the admin user-management endpoints
// against it, then deletes it. Never operates on the calling user
// (admin) — locking the admin out would break the rest of the suite.
//
// The backend requires SELF_REGISTRATION=true for the register
// endpoint to be open without admin credentials; the CI compose
// stack ships with that default. We never assert role/serviceaccount
// because /auth/register forces role=user server-side.
func TestLiveUser_RegisterListDisableEnable(t *testing.T) {
	c := newLiveClient(t)
	login(t, c)

	prefix := liveResourcePrefix()
	username := prefix + "-user"

	// 1. Register
	created, err := c.Register(&types.RegisterRequest{
		Username:    username,
		Password:    "ci-throwaway-password-1",
		DisplayName: "CI throwaway",
	})
	require.NoError(t, err, "register throwaway user")
	require.NotEmpty(t, created.ID, "registered user must have an ID")
	assert.Equal(t, username, created.Username, "response must echo the username")

	// Always delete on cleanup so the suite doesn't accumulate users.
	t.Cleanup(func() {
		_ = c.DeleteUser(created.ID)
	})

	// 2. List — registered user must be visible to the admin caller.
	users, err := c.ListUsers()
	require.NoError(t, err, "list users (admin)")
	var found *types.User
	for i := range users {
		if users[i].ID == created.ID {
			found = &users[i]
			break
		}
	}
	require.NotNilf(t, found, "newly-registered user %s must appear in list", username)
	assert.False(t, found.Disabled, "freshly-registered user must not be disabled")

	// 3. Disable → re-list and verify the flag flipped.
	require.NoError(t, c.DisableUser(created.ID), "disable user")
	after, err := c.ListUsers()
	require.NoError(t, err, "list users after disable")
	for _, u := range after {
		if u.ID == created.ID {
			assert.True(t, u.Disabled, "user must be marked disabled after DisableUser")
		}
	}

	// 4. Enable → re-list and verify the flag flipped back.
	require.NoError(t, c.EnableUser(created.ID), "enable user")
	after2, err := c.ListUsers()
	require.NoError(t, err, "list users after enable")
	for _, u := range after2 {
		if u.ID == created.ID {
			assert.False(t, u.Disabled, "user must be re-enabled after EnableUser")
		}
	}

	// 5. ResetUserPassword — admin path. We don't try to authenticate as the
	// new user, just verify the endpoint returns 204 for a local-provider
	// account. A rejection would surface a 400 (length) or 403 (auth) — both
	// would fail this assertion.
	require.NoError(t, c.ResetUserPassword(created.ID, "ci-rotated-password-2"),
		"reset user password")

	// 6. Explicit delete (cleanup is the safety net) — confirms gone.
	require.NoError(t, c.DeleteUser(created.ID), "delete throwaway user")
	final, err := c.ListUsers()
	require.NoError(t, err, "list users after delete")
	for _, u := range final {
		assert.NotEqualf(t, created.ID, u.ID,
			"deleted user %s must not appear in list", created.ID)
	}
}
