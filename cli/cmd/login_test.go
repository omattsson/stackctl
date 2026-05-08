package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLoginTestCmd initialises globals and returns a buffer for captured output.
func setupLoginTestCmd(t *testing.T, apiURL string) *bytes.Buffer {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	t.Cleanup(func() {
		loginCmd.Flags().Set("username", "")
		loginCmd.Flags().Set("password", "")
	})

	cfg = &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {APIURL: apiURL},
		},
	}
	printer = output.NewPrinter("table", false, true)

	var buf bytes.Buffer
	printer.Writer = &buf

	// Reset global flags that newClient reads.
	flagAPIURL = apiURL
	flagAPIKey = ""
	flagInsecure = false
	flagQuiet = false

	return &buf
}

// ---------- Login command tests ----------

func TestLoginCmd_WithFlags(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/login", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var req types.LoginRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "admin", req.Username)
		assert.Equal(t, "secret123", req.Password)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "jwt-test-token",
			ExpiresAt: expiresAt.Format(time.RFC3339),
			User:      types.User{Base: types.Base{ID: "1"}, Username: "admin", Role: "admin"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "admin")
	loginCmd.Flags().Set("password", "secret123")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Logged in as admin")

	// Verify token was saved
	tokenPath := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens", "test.json")
	data, err := os.ReadFile(tokenPath)
	require.NoError(t, err)

	var stored storedToken
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, "jwt-test-token", stored.Token)
	assert.Equal(t, "admin", stored.Username)

	// Verify token file has secure permissions
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tokenPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

func TestLoginCmd_WithStdinInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "jwt-stdin-token",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			User:      types.User{Username: "stdinuser"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	// Provide username via flag and password via stdin
	loginCmd.Flags().Set("username", "stdinuser")
	loginCmd.Flags().Set("password", "")
	loginCmd.SetIn(strings.NewReader("stdinpass\n"))
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Logged in as stdinuser")
}

func TestLoginCmd_EmptyUsername(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when username is empty")
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "")
	loginCmd.Flags().Set("password", "")
	loginCmd.SetIn(strings.NewReader("\npassword\n"))
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username is required")
}

func TestLoginCmd_EmptyPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API should not be called when password is empty")
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	// Provide username via flag, empty password via stdin
	loginCmd.Flags().Set("username", "admin")
	loginCmd.Flags().Set("password", "")
	loginCmd.SetIn(strings.NewReader("\n"))
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password is required")
}

func TestLoginCmd_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "invalid credentials"})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "bad")
	loginCmd.Flags().Set("password", "wrong")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid credentials")

	// Verify no token was saved
	tokenPath := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens", "test.json")
	_, statErr := os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(statErr), "token should not be saved on auth failure")
}

func TestLoginCmd_InvalidExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "jwt-token",
			"expires_at": "not-a-date",
			"user":       map[string]interface{}{"username": "admin"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "admin")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing token expiry")
}

func TestLoginCmd_PasswordNotInOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "jwt-token",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			User:      types.User{Username: "admin"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "admin")
	loginCmd.Flags().Set("password", "supersecretpassword")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "supersecretpassword")
	assert.NotContains(t, buf.String(), "jwt-token")
}

func TestLoginCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "internal server error"})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "admin")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
}

func TestLoginCmd_UserFromResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "jwt-token",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			User:      types.User{Username: "display-name"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "input-name")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Logged in as display-name")
}

func TestLoginCmd_EmptyUserFallsBackToInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "jwt-token",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			User:      types.User{Username: ""},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "typed-user")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Logged in as typed-user")
}

// ---------- Logout command tests ----------

func TestLogoutCmd_Success(t *testing.T) {
	buf := setupLoginTestCmd(t, "http://unused")

	// Create a token file first
	tokenDir := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens")
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	tokenPath := filepath.Join(tokenDir, "test.json")
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{"token":"old-jwt"}`), 0600))

	logoutCmd.SetOut(buf)
	err := logoutCmd.RunE(logoutCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Logged out")
	assert.Contains(t, buf.String(), "test")

	// Verify token file is deleted
	_, statErr := os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(statErr), "token file should be removed after logout")
}

func TestLogoutCmd_NoToken(t *testing.T) {
	buf := setupLoginTestCmd(t, "http://unused")

	logoutCmd.SetOut(buf)
	err := logoutCmd.RunE(logoutCmd, []string{})
	require.NoError(t, err, "logout without existing token should succeed gracefully")
	assert.Contains(t, buf.String(), "Logged out")
}

func TestLogoutCmd_DefaultContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{
		CurrentContext: "",
		Contexts:       map[string]*config.Context{},
	}
	printer = output.NewPrinter("table", false, true)

	var buf bytes.Buffer
	printer.Writer = &buf
	logoutCmd.SetOut(&buf)

	err := logoutCmd.RunE(logoutCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "default")
}

// ---------- Whoami command tests ----------

func TestWhoamiCmd_TableOutput(t *testing.T) {
	createdAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/me", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: "1", CreatedAt: createdAt},
			Username: "admin",
			Role:     "admin",
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Username")
	assert.Contains(t, out, "admin")
	assert.Contains(t, out, "Role")
	assert.Contains(t, out, "Created")
	assert.Contains(t, out, "2025-01-15")
}

func TestWhoamiCmd_JSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: "42"},
			Username: "jsonuser",
			Role:     "viewer",
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	printer = output.NewPrinter("json", false, true)
	printer.Writer = buf

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.NoError(t, err)

	var user types.User
	require.NoError(t, json.Unmarshal(buf.Bytes(), &user))
	assert.Equal(t, "jsonuser", user.Username)
	assert.Equal(t, "viewer", user.Role)
	assert.Equal(t, "42", user.ID)
}

func TestWhoamiCmd_YAMLOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: "7"},
			Username: "yamluser",
			Role:     "operator",
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	printer = output.NewPrinter("yaml", false, true)
	printer.Writer = buf

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "username: yamluser")
	assert.Contains(t, out, "role: operator")
}

func TestWhoamiCmd_QuietOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: "1"},
			Username: "quietuser",
			Role:     "admin",
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	printer = output.NewPrinter("table", true, true)
	printer.Writer = buf

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.NoError(t, err)

	out := strings.TrimSpace(buf.String())
	assert.Equal(t, "1", out, "quiet mode should output only user ID")
	assert.NotContains(t, out, "Role")
}

func TestWhoamiCmd_NotAuthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "Not authenticated. Run 'stackctl login' first."})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not authenticated")
}

func TestWhoamiCmd_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(types.ErrorResponse{Error: "access denied"})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestWhoamiCmd_TokenNotInOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.User{
			Base:     types.Base{ID: "1"},
			Username: "admin",
			Role:     "admin",
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	flagAPIKey = "sk_secret_key"

	whoamiCmd.SetOut(buf)
	err := whoamiCmd.RunE(whoamiCmd, []string{})
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "sk_secret_key")
}

// ---------- Whoami output modes table-driven ----------

func TestWhoamiCmd_OutputModes(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		quiet      bool
		wantSubstr []string
		wantAbsent []string
	}{
		{
			name:       "table format",
			format:     "table",
			quiet:      false,
			wantSubstr: []string{"Username", "admin", "Role", "Created"},
		},
		{
			name:       "json format",
			format:     "json",
			quiet:      false,
			wantSubstr: []string{`"username"`, `"admin"`, `"role"`},
		},
		{
			name:       "yaml format",
			format:     "yaml",
			quiet:      false,
			wantSubstr: []string{"username: admin", "role: admin"},
		},
		{
			name:       "quiet mode",
			format:     "table",
			quiet:      true,
			wantSubstr: []string{"1"},
			wantAbsent: []string{"Role", "Created", "Username:"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			createdAt := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(types.User{
					Base:     types.Base{ID: "1", CreatedAt: createdAt},
					Username: "admin",
					Role:     "admin",
				})
			}))
			defer server.Close()

			buf := setupLoginTestCmd(t, server.URL)
			printer = output.NewPrinter(tt.format, tt.quiet, true)
			printer.Writer = buf

			whoamiCmd.SetOut(buf)
			err := whoamiCmd.RunE(whoamiCmd, []string{})
			require.NoError(t, err)

			out := buf.String()
			for _, s := range tt.wantSubstr {
				assert.Contains(t, out, s, "expected %q in output", s)
			}
			for _, s := range tt.wantAbsent {
				assert.NotContains(t, out, s, "unexpected %q in output", s)
			}
		})
	}
}

// ---------- Login + Logout round-trip ----------

func TestLoginLogout_RoundTrip(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(types.LoginResponse{
				Token:     "roundtrip-jwt",
				ExpiresAt: expires,
				User:      types.User{Username: "testuser"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)
	configDir := os.Getenv("STACKCTL_CONFIG_DIR")

	// Login
	loginCmd.Flags().Set("username", "testuser")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)
	require.NoError(t, loginCmd.RunE(loginCmd, []string{}))

	// Verify token exists
	tokenPath := filepath.Join(configDir, "tokens", "test.json")
	_, err := os.Stat(tokenPath)
	require.NoError(t, err, "token file should exist after login")

	// Logout
	buf.Reset()
	logoutCmd.SetOut(buf)
	require.NoError(t, logoutCmd.RunE(logoutCmd, []string{}))

	// Verify token is gone
	_, err = os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(err), "token file should be removed after logout")
}

func TestLoginCmd_EmptyTokenFromServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(types.LoginResponse{
			Token:     "",
			ExpiresAt: "2030-01-01T00:00:00Z",
			User:      types.User{Base: types.Base{ID: "1"}, Username: "test", Role: "admin"},
		})
	}))
	defer server.Close()

	buf := setupLoginTestCmd(t, server.URL)

	loginCmd.Flags().Set("username", "test")
	loginCmd.Flags().Set("password", "pass")
	loginCmd.SetOut(buf)

	err := loginCmd.RunE(loginCmd, []string{})

	// The login command should treat an empty token as an error and avoid writing a token file.
	require.Error(t, err, "login should fail when server returns an empty token")
	assert.Contains(t, err.Error(), "empty token")

	tokenPath := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens", "test.json")
	_, statErr := os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(statErr), "token file should not exist when server returns empty token")
}

// ---------- SSO login tests ----------

func setupSSOTestCmd(t *testing.T, apiURL string) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	t.Cleanup(func() {
		loginCmd.Flags().Set("sso", "false")
		loginCmd.Flags().Set("username", "")
		loginCmd.Flags().Set("password", "")
		loginCmd.SetIn(nil)
		loginCmd.SetOut(nil)
		loginCmd.SetErr(nil)
		ssoPollInterval = 3 * time.Second
	})

	cfg = &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {APIURL: apiURL},
		},
	}
	printer = output.NewPrinter("table", false, true)

	var outBuf bytes.Buffer
	printer.Writer = &outBuf

	var errBuf bytes.Buffer

	flagAPIURL = apiURL
	flagAPIKey = ""
	flagInsecure = false
	flagQuiet = false

	return &outBuf, &errBuf
}

func TestLoginSSO_OIDCDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/oidc/config":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"enabled":            false,
				"provider_name":      "",
				"local_auth_enabled": true,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	outBuf, errBuf := setupSSOTestCmd(t, server.URL)

	loginCmd.Flags().Set("sso", "true")
	loginCmd.SetOut(outBuf)
	loginCmd.SetErr(errBuf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSO is not enabled")
}

func TestLoginSSO_InitiateSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/oidc/config":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"enabled":            true,
				"provider_name":      "azure-ad",
				"local_auth_enabled": true,
			})
		case "/api/v1/auth/oidc/cli-auth":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"session_id": "sess-abc",
				"login_url":  "https://login.example.com/auth?session=sess-abc",
				"expires_in": 1, // 1 second to trigger quick timeout
			})
		case "/api/v1/auth/oidc/cli-token":
			// Always return pending to trigger timeout
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "pending",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	outBuf, errBuf := setupSSOTestCmd(t, server.URL)

	loginCmd.Flags().Set("sso", "true")
	loginCmd.SetOut(outBuf)
	loginCmd.SetErr(errBuf)

	err := loginCmd.RunE(loginCmd, []string{})
	// It will timeout since we never complete, but we can verify the login URL was printed
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, errBuf.String(), "https://login.example.com/auth?session=sess-abc")
}

func TestLoginSSO_PollCompleted(t *testing.T) {
	// Build a JWT with an exp claim for testing
	// Header: {"alg":"HS256","typ":"JWT"}
	// Payload: {"exp":4102444800,"sub":"sso-user"} (exp = 2100-01-01)
	testJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjQxMDI0NDQ4MDAsInN1YiI6InNzby11c2VyIn0.signature"

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/oidc/config":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"enabled":            true,
				"provider_name":      "azure-ad",
				"local_auth_enabled": true,
			})
		case "/api/v1/auth/oidc/cli-auth":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"session_id": "sess-poll",
				"login_url":  "https://login.example.com/auth?session=sess-poll",
				"expires_in": 30,
			})
		case "/api/v1/auth/oidc/cli-token":
			callCount++
			w.WriteHeader(http.StatusOK)
			if callCount >= 2 {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":   "completed",
					"token":    testJWT,
					"username": "sso-user",
					"user_id":  "42",
				})
			} else {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "pending",
				})
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	outBuf, errBuf := setupSSOTestCmd(t, server.URL)
	ssoPollInterval = 10 * time.Millisecond

	loginCmd.Flags().Set("sso", "true")
	loginCmd.SetOut(outBuf)
	loginCmd.SetErr(errBuf)

	err := loginCmd.RunE(loginCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "Logged in as sso-user via SSO")

	// Verify token was saved
	tokenPath := filepath.Join(os.Getenv("STACKCTL_CONFIG_DIR"), "tokens", "test.json")
	data, readErr := os.ReadFile(tokenPath)
	require.NoError(t, readErr)

	var stored storedToken
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, testJWT, stored.Token)
	assert.Equal(t, "sso-user", stored.Username)
	assert.False(t, stored.ExpiresAt.IsZero(), "expiry should be parsed from JWT")
}

func TestParseJWTExpiry(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantExp int64
		wantErr bool
	}{
		{
			name:    "valid JWT with exp",
			token:   "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjQxMDI0NDQ4MDB9.sig",
			wantExp: 4102444800,
		},
		{
			name:    "not a JWT",
			token:   "not-a-jwt",
			wantErr: true,
		},
		{
			name:    "empty token",
			token:   "",
			wantErr: true,
		},
		{
			name:    "JWT without exp",
			token:   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.sig",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseJWTExpiry(tt.token)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantExp, result.Unix())
			}
		})
	}
}
