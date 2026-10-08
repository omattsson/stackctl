package cmd

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/config"
)

// storedToken represents a JWT token stored on disk (tokens/<context>.json).
type storedToken = config.StoredToken

// tokenStore returns the token store of the current context, or nil when no
// context is set.
func tokenStore() *config.TokenStore {
	if cfg == nil || cfg.CurrentContext == "" {
		return nil
	}
	store := config.NewTokenStore(cfg.CurrentContext)
	if ctx := cfg.CurrentCtx(); ctx != nil {
		// Files from older versions have no api_url: they belong to the
		// URL of the context.
		store.DefaultAPIURL = ctx.APIURL
	}
	return store
}

// saveToken writes a JWT token without a refresh token (SSO login) to disk
// for the current context.
func saveToken(token, username string, expiresAt time.Time) error {
	return saveSession(token, "", username, expiresAt)
}

// saveSession writes a login session to disk for the current context. It
// holds the token lock, so a renewal of the previous session in another
// process cannot overwrite the new login.
func saveSession(token, refreshToken, username string, expiresAt time.Time) error {
	store := tokenStore()
	if store == nil {
		return fmt.Errorf("no current context set")
	}
	unlock, err := store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.Save(&storedToken{
		Token:        token,
		ExpiresAt:    expiresAt,
		Username:     username,
		RefreshToken: refreshToken,
		APIURL:       resolveAPIURL(),
	})
}

// loadToken reads the JWT token for the current context.
// Returns the token, an optional expiry warning, and any error.
// Returns empty strings and nil error if no token exists.
//
// A username/password login stores a refresh token. The client renews its
// access token automatically, so an expired access token is not an error
// and gives no warning. The session itself ends 12 hours after the login,
// or after 30 minutes without use (server defaults); then the user must run
// 'stackctl login' again.
//
// An SSO login has no refresh token. Its token is not renewed.
func loadToken() (token string, warning string, err error) {
	store := tokenStore()
	if store == nil {
		return "", "", nil
	}

	t, err := store.Load()
	if err != nil {
		return "", "", err
	}
	if t == nil {
		return "", "", nil
	}
	if t.RefreshToken != "" {
		return t.Token, "", nil
	}

	if !t.ExpiresAt.IsZero() {
		remaining := time.Until(t.ExpiresAt)
		if remaining <= 0 {
			return "", "", fmt.Errorf("token expired. Run 'stackctl login' to re-authenticate")
		}
		if remaining < 5*time.Minute {
			mins := int(math.Ceil(remaining.Minutes()))
			if mins < 1 {
				mins = 1
			}
			unit := "minutes"
			if mins == 1 {
				unit = "minute"
			}
			warning = fmt.Sprintf("Warning: token expires in %d %s and cannot be renewed (SSO login or old token file). Run 'stackctl login' to get a new token.\n"+
				"A username/password login renews automatically until the session ends (12 hours after login, or 30 minutes idle).", mins, unit)
		}
	}

	return t.Token, warning, nil
}

// deleteToken removes the token file for the current context.
func deleteToken() error {
	store := tokenStore()
	if store == nil {
		return nil
	}
	return store.Delete()
}

// freshSessionToken returns the stored session token of the current context,
// renewed first when it expires soon. It returns "" when there is no usable
// token. Used to hand a valid STACKCTL_TOKEN to a plugin.
func freshSessionToken() string {
	token, _, err := loadToken()
	if err != nil || token == "" {
		return ""
	}
	apiURL := resolveAPIURL()
	if apiURL == "" {
		return token
	}
	c := client.New(apiURL)
	applyInsecureTLS(c)
	c.Token = token
	c.Tokens = tokenStore()
	fresh, err := c.EnsureFreshToken()
	if err != nil {
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		return ""
	}
	return fresh
}
