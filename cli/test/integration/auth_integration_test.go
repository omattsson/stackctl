package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for the authentication flow.
// These exercise the HTTP client, token storage, and config together
// against an httptest mock server, not a real API.
// Run with: go test ./test/integration/ -v

func startAuthMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost:
			var req types.LoginRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid request body"})
				return
			}
			if req.Username == "admin" && req.Password == "correct" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.LoginResponse{
					Token:     "integration-jwt-token",
					ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
					User: types.User{
						Base:     types.Base{ID: "1", CreatedAt: time.Now().UTC()},
						Username: "admin",
						Role:     "admin",
					},
				})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid credentials"})

		case r.URL.Path == "/api/v1/auth/me" && r.Method == http.MethodGet:
			auth := r.Header.Get("Authorization")
			apiKey := r.Header.Get("X-API-Key")
			if auth == "Bearer integration-jwt-token" || apiKey == "sk_integration_key" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.User{
					Base:     types.Base{ID: "1", CreatedAt: time.Now().UTC()},
					Username: "admin",
					Role:     "admin",
				})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Not authenticated. Run 'stackctl login' first."})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(types.ErrorResponse{Error: "not found"})
		}
	}))
}

func TestAuthWorkflow_LoginWhoamiLogout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuthMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	c := client.New(server.URL)

	// 1. Login
	resp, err := c.Login("admin", "correct")
	require.NoError(t, err)
	assert.Equal(t, "integration-jwt-token", resp.Token)
	assert.Equal(t, "admin", resp.User.Username)
	assert.Equal(t, "admin", resp.User.Role)

	// Client token should be set after login
	assert.Equal(t, "integration-jwt-token", c.Token)

	// Simulate token storage (as the cmd layer would do)
	expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	require.NoError(t, err)
	tokenPath := filepath.Join(dir, "tokens", "integration.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
	tokenData, err := json.Marshal(map[string]interface{}{
		"token":      resp.Token,
		"expires_at": expiresAt,
		"username":   resp.User.Username,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenPath, tokenData, 0600))

	// Verify file permissions
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tokenPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}

	// Verify token file can be loaded back
	var persisted struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		Username  string    `json:"username"`
	}
	readData, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(readData, &persisted))
	assert.Equal(t, resp.Token, persisted.Token)
	assert.Equal(t, resp.User.Username, persisted.Username)

	// Whoami with a fresh client using the persisted token
	freshClient := client.New(server.URL)
	freshClient.Token = persisted.Token
	freshUser, err := freshClient.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "admin", freshUser.Username)
	assert.Equal(t, "admin", freshUser.Role)

	// 2. Whoami (using the token set by Login)
	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "admin", user.Username)
	assert.Equal(t, "admin", user.Role)
	assert.Equal(t, "1", user.ID)

	// 3. Logout (remove token file and clear token)
	require.NoError(t, os.Remove(tokenPath))
	_, err = os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(err))

	// 4. Whoami without token should fail
	noAuthClient := client.New(server.URL)
	_, err = noAuthClient.Whoami()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
}

func TestAuthWorkflow_LoginWithAPIKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuthMockServer(t)
	defer server.Close()

	// When using API key, whoami should work without JWT token
	c := client.New(server.URL)
	c.APIKey = "sk_integration_key"

	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "admin", user.Username)
	assert.Equal(t, "admin", user.Role)
}

func TestAuthWorkflow_APIKeyTakesPrecedenceOverToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuthMockServer(t)
	defer server.Close()

	// API key should be used even if a (potentially invalid) token is set
	c := client.New(server.URL)
	c.Token = "invalid-token"
	c.APIKey = "sk_integration_key"

	user, err := c.Whoami()
	require.NoError(t, err)
	assert.Equal(t, "admin", user.Username)
}

func TestAuthWorkflow_InvalidCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server := startAuthMockServer(t)
	defer server.Close()

	c := client.New(server.URL)
	_, err := c.Login("admin", "wrong-password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid credentials")

	// Token should NOT be set on failure
	assert.Empty(t, c.Token)
}

func TestAuthWorkflow_ExpiredTokenDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	// Simulate an expired token on disk
	tokenPath := filepath.Join(dir, "tokens", "expired-ctx.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(tokenPath), 0700))
	tokenData, err := json.Marshal(map[string]interface{}{
		"token":      "expired-jwt",
		"expires_at": time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		"username":   "admin",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenPath, tokenData, 0600))

	// Read the token back and verify it's expired
	data, err := os.ReadFile(tokenPath)
	require.NoError(t, err)

	var stored struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.True(t, time.Now().After(stored.ExpiresAt), "token should be detected as expired")
	assert.Equal(t, "expired-jwt", stored.Token)
}

func TestAuthWorkflow_TokenFileSecurityPermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not applicable on Windows")
	}

	dir := t.TempDir()

	tokenDir := filepath.Join(dir, "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))

	tokenPath := filepath.Join(tokenDir, "secure-test.json")
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{"token":"secure"}`), 0600))

	// Verify directory permissions
	dirInfo, err := os.Stat(tokenDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm(), "token directory should be 0700")

	// Verify file permissions
	fileInfo, err := os.Stat(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm(), "token file should be 0600")

	// Verify file is NOT world-readable or group-readable
	perm := fileInfo.Mode().Perm()
	assert.Zero(t, perm&0077, "token file should not be readable by group or others")
}

func TestAuthWorkflow_LoginResponseExpiryPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tests := []struct {
		name      string
		expiresAt string
		wantErr   bool
	}{
		{
			name:      "RFC3339 format",
			expiresAt: "2099-01-01T00:00:00Z",
			wantErr:   false,
		},
		{
			name:      "RFC3339 with offset",
			expiresAt: "2099-01-01T00:00:00+05:30",
			wantErr:   false,
		},
		{
			name:      "empty expiry",
			expiresAt: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.LoginResponse{
					Token:     "tok",
					ExpiresAt: tt.expiresAt,
					User:      types.User{Username: "u"},
				})
			}))
			defer server.Close()

			c := client.New(server.URL)
			resp, err := c.Login("u", "p")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "tok", resp.Token)
				assert.Equal(t, tt.expiresAt, resp.ExpiresAt)
			}
		})
	}
}
