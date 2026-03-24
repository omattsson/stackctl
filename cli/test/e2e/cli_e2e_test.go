package e2e

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E2E tests build and run the actual stackctl binary.
// Run with: go test ./test/e2e/ -v
// These tests are skipped in short mode.

var binaryPath string

func TestMain(m *testing.M) {
	// Skip expensive binary build in short mode — all e2e tests skip anyway.
	if flag.Lookup("test.short") != nil {
		// Parse flags to check -short before building.
		flag.Parse()
		if testing.Short() {
			os.Exit(m.Run())
		}
	}

	// Build the binary before running tests
	tmpDir, err := os.MkdirTemp("", "stackctl-e2e-*")
	if err != nil {
		panic(err)
	}
	binaryName := "stackctl"
	if runtime.GOOS == "windows" {
		binaryName = "stackctl.exe"
	}
	binaryPath = filepath.Join(tmpDir, binaryName)

	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("failed to build binary: " + string(out) + ": " + err.Error())
	}

	code := m.Run()

	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// runStackctl runs the binary with args and a temp config dir.
func runStackctl(t *testing.T, configDir string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), "STACKCTL_CONFIG_DIR="+configDir)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestE2E_Version(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	stdout, _, err := runStackctl(t, dir, "version")
	require.NoError(t, err)
	assert.Contains(t, stdout, "stackctl")
}

func TestE2E_VersionJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	stdout, _, err := runStackctl(t, dir, "version", "--output", "json")
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Contains(t, result, "version")
	assert.Contains(t, result, "commit")
	assert.Contains(t, result, "date")
}

func TestE2E_Help(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	stdout, _, err := runStackctl(t, dir, "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "stackctl")
	assert.Contains(t, stdout, "config")
	assert.Contains(t, stdout, "version")
	assert.Contains(t, stdout, "--output")
	assert.Contains(t, stdout, "--quiet")
}

func TestE2E_ConfigWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()

	// 1. No context initially
	stdout, _, err := runStackctl(t, dir, "config", "current-context")
	require.NoError(t, err)
	assert.Contains(t, stdout, "No current context")

	// 2. Create and switch to local context
	stdout, _, err = runStackctl(t, dir, "config", "use-context", "local")
	require.NoError(t, err)
	assert.Contains(t, stdout, "local")

	// 3. Verify current context
	stdout, _, err = runStackctl(t, dir, "config", "current-context")
	require.NoError(t, err)
	assert.Equal(t, "local\n", stdout)

	// 4. Set api-url
	stdout, _, err = runStackctl(t, dir, "config", "set", "api-url", "http://localhost:8081")
	require.NoError(t, err)
	assert.Contains(t, stdout, "api-url")

	// 5. Get api-url
	stdout, _, err = runStackctl(t, dir, "config", "get", "api-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8081\n", stdout)

	// 6. Config file should exist with correct permissions
	configPath := filepath.Join(dir, "config.yaml")
	info, err := os.Stat(configPath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}

	// 7. Add another context
	stdout, _, err = runStackctl(t, dir, "config", "use-context", "production")
	require.NoError(t, err)

	_, _, err = runStackctl(t, dir, "config", "set", "api-url", "https://prod.example.com")
	require.NoError(t, err)

	// 8. List contexts
	stdout, _, err = runStackctl(t, dir, "config", "list")
	require.NoError(t, err)
	assert.Contains(t, stdout, "local")
	assert.Contains(t, stdout, "production")
	assert.Contains(t, stdout, "http://localhost:8081")
	assert.Contains(t, stdout, "https://prod.example.com")
	// Current context should have asterisk
	assert.Contains(t, stdout, "*")

	// 9. Switch back to local
	_, _, err = runStackctl(t, dir, "config", "use-context", "local")
	require.NoError(t, err)

	stdout, _, err = runStackctl(t, dir, "config", "get", "api-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8081\n", stdout)

	// 10. Delete production context
	stdout, _, err = runStackctl(t, dir, "config", "delete-context", "production")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Deleted")

	// 11. Verify it's gone
	stdout, _, err = runStackctl(t, dir, "config", "list")
	require.NoError(t, err)
	assert.NotContains(t, stdout, "production")
	assert.Contains(t, stdout, "local")
}

func TestE2E_ConfigSetInvalidKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "config", "use-context", "test")
	require.NoError(t, err)

	_, _, err = runStackctl(t, dir, "config", "set", "invalid-key", "value")
	assert.Error(t, err, "setting invalid key should fail")
}

func TestE2E_ConfigGetNoContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "config", "get", "api-url")
	assert.Error(t, err, "getting value without context should fail")
}

func TestE2E_ConfigDeleteNonexistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "config", "delete-context", "nonexistent")
	assert.Error(t, err)
}

func TestE2E_ConfigAPIKeyMasking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "config", "use-context", "test")
	require.NoError(t, err)

	_, _, err = runStackctl(t, dir, "config", "set", "api-key", "sk_secret_key_12345678")
	require.NoError(t, err)

	stdout, _, err := runStackctl(t, dir, "config", "list")
	require.NoError(t, err)
	// API key should be masked in list output
	assert.Contains(t, stdout, "***")
	assert.NotContains(t, stdout, "sk_secret_key_12345678")
}

func TestE2E_ConfigGetAfterSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	// Set up a context and verify get returns the set value
	_, _, err := runStackctl(t, dir, "config", "use-context", "test")
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "config", "set", "api-url", "http://config-url")
	require.NoError(t, err)

	stdout, _, err := runStackctl(t, dir, "config", "get", "api-url")
	require.NoError(t, err)
	assert.Equal(t, "http://config-url\n", stdout)
}

func TestE2E_GlobalFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()

	// --no-color should not break anything
	stdout, _, err := runStackctl(t, dir, "version", "--no-color")
	require.NoError(t, err)
	assert.Contains(t, stdout, "stackctl")

	// --quiet should not break version
	stdout, _, err = runStackctl(t, dir, "version", "--quiet")
	require.NoError(t, err)
	assert.Contains(t, stdout, "stackctl")
}

func TestE2E_CompletionBash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	stdout, _, err := runStackctl(t, dir, "completion", "bash")
	require.NoError(t, err)
	assert.Contains(t, stdout, "bash")
}

func TestE2E_CompletionZsh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	stdout, _, err := runStackctl(t, dir, "completion", "zsh")
	require.NoError(t, err)
	assert.NotEmpty(t, stdout)
}

func TestE2E_UnknownCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "nonexistent-command")
	assert.Error(t, err)
}

func TestE2E_ConfigSetMissingArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()
	_, _, err := runStackctl(t, dir, "config", "set", "api-url")
	assert.Error(t, err, "set with only one arg should fail")
}

// startE2EMockAuthServer starts a mock server for auth e2e tests.
func startE2EMockAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["username"] == "e2euser" && body["password"] == "e2epass" {
				w.WriteHeader(http.StatusOK)
				resp := map[string]interface{}{
					"token":      "e2e-jwt-token",
					"expires_at": "2099-01-01T00:00:00Z",
					"user": map[string]interface{}{
						"id":         1,
						"username":   "e2euser",
						"role":       "admin",
						"created_at": "2025-01-01T00:00:00Z",
						"updated_at": "2025-01-01T00:00:00Z",
						"version":    1,
					},
				}
				json.NewEncoder(w).Encode(resp)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})

		case r.URL.Path == "/api/v1/auth/me" && r.Method == http.MethodGet:
			auth := r.Header.Get("Authorization")
			if auth == "Bearer e2e-jwt-token" {
				resp := map[string]interface{}{
					"id":         1,
					"username":   "e2euser",
					"role":       "admin",
					"created_at": "2025-01-01T00:00:00Z",
					"updated_at": "2025-01-01T00:00:00Z",
					"version":    1,
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(resp)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Not authenticated. Run 'stackctl login' first."})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

func TestE2E_LoginLogoutWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EMockAuthServer(t)
	defer server.Close()

	dir := t.TempDir()

	// 1. Set up context with API URL pointing at mock server
	_, _, err := runStackctl(t, dir, "config", "use-context", "e2etest")
	require.NoError(t, err)

	_, _, err = runStackctl(t, dir, "config", "set", "api-url", server.URL)
	require.NoError(t, err)

	// 2. Login with flags
	stdout, _, err := runStackctl(t, dir, "login", "--username", "e2euser", "--password", "e2epass")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Logged in as e2euser")

	// 3. Verify token file exists with correct permissions
	tokenPath := filepath.Join(dir, "tokens", "e2etest.json")
	info, err := os.Stat(tokenPath)
	require.NoError(t, err, "token file should exist after login")
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}

	// 4. Whoami should succeed
	stdout, _, err = runStackctl(t, dir, "whoami")
	require.NoError(t, err)
	assert.Contains(t, stdout, "e2euser")
	assert.Contains(t, stdout, "admin")

	// 5. Verify password and token are NOT in whoami output
	assert.NotContains(t, stdout, "e2epass")
	assert.NotContains(t, stdout, "e2e-jwt-token")

	// 6. Logout
	stdout, _, err = runStackctl(t, dir, "logout")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Logged out")

	// 7. Token file should be gone
	_, err = os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(err), "token file should be removed after logout")

	// 8. Whoami should fail after logout
	_, stderr, err := runStackctl(t, dir, "whoami")
	assert.Error(t, err, "whoami should fail after logout")
	_ = stderr
}

func TestE2E_LoginInvalidCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EMockAuthServer(t)
	defer server.Close()

	dir := t.TempDir()

	_, _, err := runStackctl(t, dir, "config", "use-context", "e2etest")
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "config", "set", "api-url", server.URL)
	require.NoError(t, err)

	_, _, err = runStackctl(t, dir, "login", "--username", "bad", "--password", "wrong")
	assert.Error(t, err, "login with invalid credentials should fail")

	// Token file should not exist
	tokenPath := filepath.Join(dir, "tokens", "e2etest.json")
	_, statErr := os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(statErr), "token should not be saved on auth failure")
}

func TestE2E_WhoamiOutputFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EMockAuthServer(t)
	defer server.Close()

	dir := t.TempDir()

	// Set up and login
	_, _, err := runStackctl(t, dir, "config", "use-context", "e2etest")
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "config", "set", "api-url", server.URL)
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "login", "--username", "e2euser", "--password", "e2epass")
	require.NoError(t, err)

	// Test JSON output
	stdout, _, err := runStackctl(t, dir, "whoami", "--output", "json")
	require.NoError(t, err)
	var jsonResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &jsonResult))
	assert.Equal(t, "e2euser", jsonResult["username"])
	assert.Equal(t, "admin", jsonResult["role"])

	// Test YAML output
	stdout, _, err = runStackctl(t, dir, "whoami", "--output", "yaml")
	require.NoError(t, err)
	assert.Contains(t, stdout, "username: e2euser")
	assert.Contains(t, stdout, "role: admin")

	// Test quiet output
	stdout, _, err = runStackctl(t, dir, "whoami", "--quiet")
	require.NoError(t, err)
	assert.Equal(t, "e2euser\n", stdout)

	// Test table output (default)
	stdout, _, err = runStackctl(t, dir, "whoami")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Username")
	assert.Contains(t, stdout, "e2euser")
	assert.Contains(t, stdout, "Role")
}

func TestE2E_LogoutWithoutLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dir := t.TempDir()

	_, _, err := runStackctl(t, dir, "config", "use-context", "e2etest")
	require.NoError(t, err)

	// Logout without having logged in should succeed gracefully
	stdout, _, err := runStackctl(t, dir, "logout")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Logged out")
}
