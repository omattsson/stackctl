package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Token tests mutate package-level cfg global, so they cannot run in parallel.

func TestSaveToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	expires := time.Now().Add(24 * time.Hour)
	err := saveToken("jwt-token-123", "admin", expires)
	require.NoError(t, err)

	// Verify file exists with correct permissions
	path := filepath.Join(dir, "tokens", "test.json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Verify content
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored storedToken
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, "jwt-token-123", stored.Token)
	assert.Equal(t, "admin", stored.Username)
	assert.WithinDuration(t, expires, stored.ExpiresAt, time.Second)
}

func TestSaveToken_NoCurrentContext(t *testing.T) {
	cfg = &config.Config{Contexts: map[string]*config.Context{}}
	err := saveToken("token", "user", time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no current context")
}

func TestLoadToken_Valid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	// Write a valid token file
	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	stored := storedToken{
		Token:     "valid-jwt",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Username:  "admin",
	}
	data, _ := json.Marshal(stored)
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), data, 0600))

	token, warning, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "valid-jwt", token)
	assert.Empty(t, warning)
}

func TestLoadToken_Expired(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	stored := storedToken{
		Token:     "expired-jwt",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	data, _ := json.Marshal(stored)
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), data, 0600))

	_, _, err := loadToken()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token expired")
}

func TestLoadToken_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	token, _, err := loadToken()
	require.NoError(t, err)
	assert.Empty(t, token)
}

func TestLoadToken_NoCurrentContext(t *testing.T) {
	cfg = &config.Config{Contexts: map[string]*config.Context{}}

	token, _, err := loadToken()
	require.NoError(t, err)
	assert.Empty(t, token)
}

func TestLoadToken_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), []byte("not json"), 0600))

	_, _, err := loadToken()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing token file")
}

func TestLoadToken_ZeroExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	stored := storedToken{
		Token: "no-expiry-jwt",
		// ExpiresAt zero value — should not expire
	}
	data, _ := json.Marshal(stored)
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), data, 0600))

	token, warning, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "no-expiry-jwt", token)
	assert.Empty(t, warning)
}

func TestDeleteToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	// Create token file
	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	path := filepath.Join(tokenDir, "test.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"token":"x"}`), 0600))

	err := deleteToken()
	require.NoError(t, err)

	// File should be gone
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteToken_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	err := deleteToken()
	require.NoError(t, err, "deleting nonexistent token should not error")
}

func TestDeleteToken_NoCurrentContext(t *testing.T) {
	cfg = &config.Config{Contexts: map[string]*config.Context{}}

	err := deleteToken()
	require.NoError(t, err)
}

func TestLoadToken_NearExpiryWarning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	stored := storedToken{
		Token:     "expiring-soon-jwt",
		ExpiresAt: time.Now().Add(3 * time.Minute),
	}
	data, _ := json.Marshal(stored)
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), data, 0600))

	token, warning, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "expiring-soon-jwt", token)
	assert.Contains(t, warning, "Warning: token expires in")
	assert.Contains(t, warning, "stackctl login")
}

func TestLoadToken_NoWarningWhenFarFromExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "test", Contexts: map[string]*config.Context{"test": {}}}

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	stored := storedToken{
		Token:     "valid-jwt",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	data, _ := json.Marshal(stored)
	require.NoError(t, os.WriteFile(filepath.Join(tokenDir, "test.json"), data, 0600))

	token, warning, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "valid-jwt", token)
	assert.Empty(t, warning)
}

func TestSaveAndLoadToken_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{CurrentContext: "roundtrip", Contexts: map[string]*config.Context{"roundtrip": {}}}

	expires := time.Now().Add(2 * time.Hour)
	require.NoError(t, saveToken("my-jwt-token", "testuser", expires))

	token, _, err := loadToken()
	require.NoError(t, err)
	assert.Equal(t, "my-jwt-token", token)
}
