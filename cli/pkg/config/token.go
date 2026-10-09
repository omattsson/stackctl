package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// StoredToken is the content of tokens/<context>.json.
//
// RefreshToken holds the value of the refresh_token cookie from a
// username/password login. It is empty for SSO logins and for token files
// written by older stackctl versions; such tokens are not renewed.
type StoredToken struct {
	ExpiresAt    time.Time `json:"expires_at"`
	Token        string    `json:"token"`
	Username     string    `json:"username,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	// APIURL is the API URL of the login. The refresh token is only sent
	// to this URL. Empty in files from older versions (see
	// TokenStore.DefaultAPIURL).
	APIURL string `json:"api_url,omitempty"`
}

// TokenStore reads and writes the token file of one context.
type TokenStore struct {
	// Context is the name of the context. It selects tokens/<Context>.json.
	Context string
	// Dir overrides the token directory (<config dir>/tokens). Tests use it.
	Dir string
	// DefaultAPIURL is the API URL of the context. Load uses it when the
	// file has no api_url (files from older versions).
	DefaultAPIURL string
}

// NewTokenStore returns the token store for a context.
func NewTokenStore(contextName string) *TokenStore {
	return &TokenStore{Context: contextName}
}

// Path returns the path of the token file.
func (s *TokenStore) Path() (string, error) {
	return s.file(".json")
}

// LockPath returns the path of the lock file next to the token file.
func (s *TokenStore) LockPath() (string, error) {
	return s.file(".lock")
}

func (s *TokenStore) file(ext string) (string, error) {
	if err := ValidateContextName(s.Context); err != nil {
		return "", err
	}
	dir := s.Dir
	if dir == "" {
		cfgDir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cfgDir, TokenDir)
	}
	return filepath.Join(dir, s.Context+ext), nil
}

// Load reads the token file. It returns nil and no error when the file does
// not exist.
func (s *TokenStore) Load() (*StoredToken, error) {
	path, err := s.Path()
	if err != nil {
		return nil, err
	}
	data, err := readWithRetry(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading token file: %w", err)
	}
	var t StoredToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parsing token file: %w", err)
	}
	if t.APIURL == "" {
		t.APIURL = s.DefaultAPIURL
	}
	return &t, nil
}

// Save writes the token file with mode 0600 in a directory with mode 0700.
// It writes a temporary file and renames it, so a reader never sees a
// partial file.
func (s *TokenStore) Save(t *StoredToken) error {
	path, err := s.Path()
	if err != nil {
		return err
	}
	dir, err := ensureTokenDir(path)
	if err != nil {
		return err
	}

	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+s.Context+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	// CreateTemp uses mode 0600; set it again to be explicit.
	if err := tmp.Chmod(0600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return fmt.Errorf("setting token file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	if err := renameWithRetry(tmpName, path); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	// Enforce 0600 even if the file system ignored the mode of the temp file.
	// On Windows, Chmod is best-effort since POSIX permissions don't apply.
	if err := os.Chmod(path, 0600); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("setting token file permissions: %w", err)
	}
	return nil
}

// Delete removes the token file. A missing file is not an error. The lock
// file stays, because another process can hold a lock on it.
func (s *TokenStore) Delete() error {
	path, err := s.Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing token file: %w", err)
	}
	return nil
}

// LockTimeout is the longest time Lock waits for another process.
var LockTimeout = 45 * time.Second

// lockPollInterval is the time between two lock attempts.
const lockPollInterval = 50 * time.Millisecond

// Lock takes an exclusive lock on tokens/<context>.lock. Use it around a
// token renewal, so that two stackctl processes do not both send the same
// refresh token (the server revokes the session when a used refresh token
// comes back). Call the returned function to release the lock.
//
// The lock is a flock(2) lock on Unix and a LockFileEx lock on Windows. The
// operating system releases it when the process exits.
func (s *TokenStore) Lock() (func(), error) {
	path, err := s.LockPath()
	if err != nil {
		return nil, err
	}
	if _, err := ensureTokenDir(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening token lock file: %w", err)
	}

	deadline := time.Now().Add(LockTimeout)
	for {
		locked, err := tryLockFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("locking token file: %w", err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("locking token file: timed out after %s (another stackctl process holds %s)", LockTimeout, path)
		}
		time.Sleep(lockPollInterval)
	}

	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// fileRetries and fileRetryDelay bound the retries of a token file read or
// rename. On Windows, a rename fails while another process has the target
// open, and a read can fail during a rename.
const (
	fileRetries    = 5
	fileRetryDelay = 50 * time.Millisecond
)

// retryTransient calls op up to fileRetries times while it fails with an
// error that can be transient (see isTransientFileError).
func retryTransient(op func() error) error {
	var err error
	for i := 0; i < fileRetries; i++ {
		if err = op(); err == nil || !isTransientFileError(err) {
			return err
		}
		if i < fileRetries-1 {
			time.Sleep(fileRetryDelay)
		}
	}
	return err
}

func renameWithRetry(from, to string) error {
	return retryTransient(func() error { return os.Rename(from, to) })
}

func readWithRetry(path string) ([]byte, error) {
	var data []byte
	err := retryTransient(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

// ensureTokenDir creates the directory of path with mode 0700 and returns it.
func ensureTokenDir(path string) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating token directory: %w", err)
	}
	// Enforce 0700 even if directory already existed with broader permissions.
	// On Windows, Chmod is best-effort since POSIX permissions don't apply.
	if err := os.Chmod(dir, 0700); err != nil && runtime.GOOS != "windows" {
		return "", fmt.Errorf("setting token directory permissions: %w", err)
	}
	return dir, nil
}
