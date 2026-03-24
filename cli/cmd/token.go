package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omattsson/stackctl/pkg/config"
)

// storedToken represents a JWT token stored on disk.
type storedToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Username  string    `json:"username,omitempty"`
}

// saveToken writes a JWT token to disk for the current context.
func saveToken(token, username string, expiresAt time.Time) error {
	if cfg.CurrentContext == "" {
		return fmt.Errorf("no current context set")
	}

	path, err := config.TokenPath(cfg.CurrentContext)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating token directory: %w", err)
	}

	data, err := json.Marshal(storedToken{
		Token:     token,
		ExpiresAt: expiresAt,
		Username:  username,
	})
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	return nil
}

// loadToken reads the JWT token for the current context.
// Returns empty string if no token exists or token is expired.
func loadToken() (string, error) {
	if cfg.CurrentContext == "" {
		return "", nil
	}

	path, err := config.TokenPath(cfg.CurrentContext)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading token file: %w", err)
	}

	var t storedToken
	if err := json.Unmarshal(data, &t); err != nil {
		return "", fmt.Errorf("parsing token file: %w", err)
	}

	if !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) {
		return "", fmt.Errorf("token expired. Run 'stackctl login' to re-authenticate")
	}

	return t.Token, nil
}

// deleteToken removes the token file for the current context.
func deleteToken() error {
	if cfg.CurrentContext == "" {
		return nil
	}

	path, err := config.TokenPath(cfg.CurrentContext)
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing token file: %w", err)
	}
	return nil
}
