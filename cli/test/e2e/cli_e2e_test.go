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
	"strconv"
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
	_, _, err = runStackctl(t, dir, "config", "use-context", "production")
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
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
				return
			}
			if body["username"] == "e2euser" && body["password"] == "e2epass" {
				w.WriteHeader(http.StatusOK)
				resp := map[string]interface{}{
					"token":      "e2e-jwt-token",
					"expires_at": "2099-01-01T00:00:00Z",
					"user": map[string]interface{}{
						"id":         "1",
						"username":   "e2euser",
						"role":       "admin",
						"created_at": "2025-01-01T00:00:00Z",
						"updated_at": "2025-01-01T00:00:00Z",
						"version":    "1",
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
					"id":         "1",
					"username":   "e2euser",
					"role":       "admin",
					"created_at": "2025-01-01T00:00:00Z",
					"updated_at": "2025-01-01T00:00:00Z",
					"version":    "1",
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

	// Test quiet output (prints user ID per global --quiet contract)
	stdout, _, err = runStackctl(t, dir, "whoami", "--quiet")
	require.NoError(t, err)
	assert.Equal(t, "1\n", stdout)

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

// ---------- Stack E2E Mock Server ----------

// startE2EStackMockServer starts a mock API with stack endpoints for e2e tests.
func startE2EStackMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	nextLogID := 100
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// List stacks
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodGet:
			resp := map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id": "1", "name": "e2e-stack-1", "status": "running",
						"owner": "admin", "branch": "main", "namespace": "ns-1",
						"stack_definition_id": "1", "cluster_name": "dev",
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
					{
						"id": "2", "name": "e2e-stack-2", "status": "stopped",
						"owner": "dev", "branch": "feature/x", "namespace": "ns-2",
						"stack_definition_id": "2", "cluster_name": "staging",
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
				},
				"total": 2, "page": 1, "page_size": 20, "total_pages": 1,
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// Create stack
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodPost:
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			resp := map[string]interface{}{
				"id": "10", "name": body["name"], "status": "draft",
				"stack_definition_id": body["stack_definition_id"],
				"branch":              body["branch"],
				"owner":               "admin",
				"namespace":           "ns-new",
				"created_at":          "2025-06-01T00:00:00Z",
				"updated_at":          "2025-06-01T00:00:00Z",
				"version":             "1",
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(resp)

		// Get stack
		case r.URL.Path == "/api/v1/stack-instances/10" && r.Method == http.MethodGet:
			resp := map[string]interface{}{
				"id": "10", "name": "e2e-new-stack", "status": "draft",
				"stack_definition_id": "1", "owner": "admin", "branch": "main",
				"namespace":  "ns-new",
				"created_at": "2025-06-01T00:00:00Z", "updated_at": "2025-06-01T00:00:00Z", "version": "1",
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// Deploy stack
		case r.URL.Path == "/api/v1/stack-instances/10/deploy" && r.Method == http.MethodPost:
			nextLogID++
			resp := map[string]interface{}{
				"id": strconv.Itoa(nextLogID), "instance_id": "10", "action": "deploy", "status": "started",
				"created_at": "2025-06-01T00:00:00Z", "updated_at": "2025-06-01T00:00:00Z", "version": "1",
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// Delete stack
		case r.URL.Path == "/api/v1/stack-instances/10" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)

		// Delete non-existent
		case r.URL.Path == "/api/v1/stack-instances/999" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "instance not found"})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

// setupE2EStackContext sets up a context pointing at the mock server.
func setupE2EStackContext(t *testing.T, dir, serverURL string) {
	t.Helper()
	_, _, err := runStackctl(t, dir, "config", "use-context", "e2estack")
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "config", "set", "api-url", serverURL)
	require.NoError(t, err)
	_, _, err = runStackctl(t, dir, "config", "set", "api-key", "sk_e2e_test_key")
	require.NoError(t, err)
}

func TestE2E_StackCreateDeployDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EStackMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// 1. Create
	stdout, _, err := runStackctl(t, dir, "stack", "create", "--name", "e2e-new-stack", "--definition", "1", "--branch", "main")
	require.NoError(t, err)
	assert.Contains(t, stdout, "10")
	assert.Contains(t, stdout, "e2e-new-stack")
	assert.Contains(t, stdout, "draft")

	// 2. Deploy
	stdout, _, err = runStackctl(t, dir, "stack", "deploy", "10")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Deploying stack 10")

	// 3. Delete with --yes
	stdout, _, err = runStackctl(t, dir, "stack", "delete", "10", "--yes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Deleted stack 10")
}

func TestE2E_StackListOutputFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EStackMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// Table (default)
	stdout, _, err := runStackctl(t, dir, "stack", "list")
	require.NoError(t, err)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "e2e-stack-1")
	assert.Contains(t, stdout, "e2e-stack-2")

	// JSON
	stdout, _, err = runStackctl(t, dir, "stack", "list", "--output", "json")
	require.NoError(t, err)
	var jsonResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &jsonResult))
	data, ok := jsonResult["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, data, 2)

	// YAML
	stdout, _, err = runStackctl(t, dir, "stack", "list", "--output", "yaml")
	require.NoError(t, err)
	assert.Contains(t, stdout, "name: e2e-stack-1")
	assert.Contains(t, stdout, "name: e2e-stack-2")

	// Quiet
	stdout, _, err = runStackctl(t, dir, "stack", "list", "--quiet")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"1", "2"}, lines)
}

func TestE2E_StackDeleteConfirmation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EStackMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// Without --yes flag, the command expects interactive input.
	// Since we can't provide stdin easily in e2e, we verify --yes works.
	stdout, _, err := runStackctl(t, dir, "stack", "delete", "10", "--yes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Deleted stack 10")

	// Deleting a non-existent instance should fail
	_, _, err = runStackctl(t, dir, "stack", "delete", "999", "--yes")
	assert.Error(t, err)
}

// ---------- Template & Definition E2E Mock Server ----------

func startE2ETemplateDefMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// List templates
		case r.URL.Path == "/api/v1/templates" && r.Method == http.MethodGet:
			resp := map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id": "1", "name": "web-template", "description": "Web app stack",
						"published": true, "owner": "admin", "charts": []map[string]interface{}{
							{"id": "1", "name": "frontend", "repo_url": "https://charts.example.com", "chart_version": "1.0.0"},
						},
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
					{
						"id": "2", "name": "api-template", "description": "API stack",
						"published": false, "owner": "admin", "charts": []map[string]interface{}{},
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
				},
				"total": 2, "page": 1, "page_size": 20, "total_pages": 1,
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// List definitions
		case r.URL.Path == "/api/v1/stack-definitions" && r.Method == http.MethodGet:
			resp := map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id": "1", "name": "my-definition", "description": "Test definition",
						"owner": "admin", "default_branch": "main",
						"charts":     []map[string]interface{}{},
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
				},
				"total": 1, "page": 1, "page_size": 20, "total_pages": 1,
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// Export definition
		case r.URL.Path == "/api/v1/stack-definitions/1/export" && r.Method == http.MethodGet:
			exportData := map[string]interface{}{
				"name": "my-definition", "description": "Test definition",
				"charts": []interface{}{},
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(exportData)

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

func TestE2E_TemplateListOutputFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2ETemplateDefMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// Table (default)
	stdout, _, err := runStackctl(t, dir, "template", "list")
	require.NoError(t, err)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "PUBLISHED")
	assert.Contains(t, stdout, "web-template")
	assert.Contains(t, stdout, "api-template")

	// Quiet
	stdout, _, err = runStackctl(t, dir, "template", "list", "--quiet")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"1", "2"}, lines)

	// JSON
	stdout, _, err = runStackctl(t, dir, "template", "list", "--output", "json")
	require.NoError(t, err)
	var jsonResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &jsonResult))
	data, ok := jsonResult["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, data, 2)
}

func TestE2E_DefinitionListOutputFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2ETemplateDefMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// Table (default)
	stdout, _, err := runStackctl(t, dir, "definition", "list")
	require.NoError(t, err)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "OWNER")
	assert.Contains(t, stdout, "my-definition")

	// Quiet
	stdout, _, err = runStackctl(t, dir, "definition", "list", "--quiet")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 1)
	assert.Equal(t, "1", lines[0])

	// JSON
	stdout, _, err = runStackctl(t, dir, "definition", "list", "--output", "json")
	require.NoError(t, err)
	var jsonResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &jsonResult))
	data, ok := jsonResult["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, data, 1)
}

func TestE2E_DefinitionExportToFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2ETemplateDefMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	outFile := filepath.Join(dir, "exported.json")
	stdout, _, err := runStackctl(t, dir, "definition", "export", "1", "--output-file", outFile)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Exported definition 1")

	// Verify file was written
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-definition")
}

// ---------- Override E2E Mock Server ----------

func startE2EOverrideMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// List value overrides
		case r.URL.Path == "/api/v1/stack-instances/1/overrides" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "10", "instance_id": "1", "chart_id": "5", "values": `{"replicas":3}`, "version": "1",
					"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z"},
			})

		// Get quota override
		case r.URL.Path == "/api/v1/stack-instances/1/quota-overrides" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"instance_id": "1", "cpu_request": "100m", "cpu_limit": "500m",
				"memory_request": "128Mi", "memory_limit": "512Mi", "updated_at": "2025-01-01T00:00:00Z",
			})

		// List branch overrides
		case r.URL.Path == "/api/v1/stack-instances/1/branches" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "20", "instance_id": "1", "chart_id": "5", "branch": "feature/test", "version": "1",
					"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z"},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

func TestE2E_OverrideListJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EOverrideMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	stdout, _, err := runStackctl(t, dir, "override", "list", "1", "--output", "json")
	require.NoError(t, err)
	var result []interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Len(t, result, 1)
}

func TestE2E_OverrideListTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EOverrideMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	stdout, _, err := runStackctl(t, dir, "override", "list", "1")
	require.NoError(t, err)
	assert.Contains(t, stdout, "CHART ID")
	assert.Contains(t, stdout, "HAS VALUES")
}

func TestE2E_OverrideQuotaGet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EOverrideMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	stdout, _, err := runStackctl(t, dir, "override", "quota", "get", "1", "--output", "json")
	require.NoError(t, err)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, "100m", result["cpu_request"])
	assert.Equal(t, "512Mi", result["memory_limit"])
}

func TestE2E_OverrideBranchListJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EOverrideMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	stdout, _, err := runStackctl(t, dir, "override", "branch", "list", "1", "--output", "json")
	require.NoError(t, err)
	var result []interface{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Len(t, result, 1)
}

func TestE2E_OverrideInvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EOverrideMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	_, _, err := runStackctl(t, dir, "override", "list", "abc")
	assert.Error(t, err)
}

// ---------- Quiet Piping Workflow E2E ----------

// startE2EQuietPipingMockServer starts a mock server that handles stack list and bulk endpoints.
func startE2EQuietPipingMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		// List stacks — returns 3 stacks with IDs 1, 2, 3
		case r.URL.Path == "/api/v1/stack-instances" && r.Method == http.MethodGet:
			resp := map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id": "1", "name": "pipe-stack-1", "status": "running",
						"owner": "admin", "branch": "main", "namespace": "ns-1",
						"stack_definition_id": "1", "cluster_name": "dev",
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
					{
						"id": "2", "name": "pipe-stack-2", "status": "running",
						"owner": "admin", "branch": "main", "namespace": "ns-2",
						"stack_definition_id": "1", "cluster_name": "dev",
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
					{
						"id": "3", "name": "pipe-stack-3", "status": "stopped",
						"owner": "admin", "branch": "feature/x", "namespace": "ns-3",
						"stack_definition_id": "2", "cluster_name": "staging",
						"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-01T00:00:00Z", "version": "1",
					},
				},
				"total": 3, "page": 1, "page_size": 20, "total_pages": 1,
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)

		// Bulk deploy
		case r.URL.Path == "/api/v1/stack-instances/bulk/deploy" && r.Method == http.MethodPost:
			var req struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "ids required"})
				return
			}
			var results []map[string]interface{}
			for _, id := range req.IDs {
				results = append(results, map[string]interface{}{"id": id, "success": true})
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"results": results})

		// Bulk stop
		case r.URL.Path == "/api/v1/stack-instances/bulk/stop" && r.Method == http.MethodPost:
			var req struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "ids required"})
				return
			}
			var results []map[string]interface{}
			for _, id := range req.IDs {
				results = append(results, map[string]interface{}{"id": id, "success": true})
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"results": results})

		// Bulk delete
		case r.URL.Path == "/api/v1/stack-instances/bulk/delete" && r.Method == http.MethodPost:
			var req struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "ids required"})
				return
			}
			var results []map[string]interface{}
			for _, id := range req.IDs {
				results = append(results, map[string]interface{}{"id": id, "success": true})
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"results": results})

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
}

func TestE2E_QuietPipingWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	server := startE2EQuietPipingMockServer(t)
	defer server.Close()

	dir := t.TempDir()
	setupE2EStackContext(t, dir, server.URL)

	// 1. Run stack list --quiet and verify output is "1\n2\n3\n"
	stdout, _, err := runStackctl(t, dir, "stack", "list", "--quiet")
	require.NoError(t, err)
	assert.Equal(t, "1\n2\n3\n", stdout)

	// 2. Parse the quiet output to extract IDs — validates the format
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 3)
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
		assert.Regexp(t, `^\d+$`, lines[i], "each line should be a numeric ID only")
	}

	// 3. Build the --ids argument from the quiet output to simulate piping workflow
	idsCSV := strings.Join(lines, ",")

	// 4. Run bulk deploy --ids <derived> → verify success
	stdout, _, err = runStackctl(t, dir, "bulk", "deploy", "--ids", idsCSV)
	require.NoError(t, err)
	for _, id := range lines {
		assert.Contains(t, stdout, id)
	}

	// 5. Run bulk stop --ids <derived> → verify success
	stdout, _, err = runStackctl(t, dir, "bulk", "stop", "--ids", idsCSV)
	require.NoError(t, err)
	for _, id := range lines {
		assert.Contains(t, stdout, id)
	}

	// 6. Run bulk delete --ids <derived> --yes → verify success (needs --yes to skip confirmation)
	stdout, _, err = runStackctl(t, dir, "bulk", "delete", "--ids", idsCSV, "--yes")
	require.NoError(t, err)
	for _, id := range lines {
		assert.Contains(t, stdout, id)
	}
}

func TestE2E_QuietOutputFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Use the quiet piping mock for stacks, and the template/def mock for templates and definitions.
	stackServer := startE2EQuietPipingMockServer(t)
	defer stackServer.Close()

	templateDefServer := startE2ETemplateDefMockServer(t)
	defer templateDefServer.Close()

	// --- stack list --quiet ---
	dir1 := t.TempDir()
	setupE2EStackContext(t, dir1, stackServer.URL)

	stdout, _, err := runStackctl(t, dir1, "stack", "list", "--quiet")
	require.NoError(t, err)
	// Verify exact format: numeric IDs only, one per line, no headers, no extra whitespace
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "1", lines[0])
	assert.Equal(t, "2", lines[1])
	assert.Equal(t, "3", lines[2])

	// --- template list --quiet ---
	dir2 := t.TempDir()
	setupE2EStackContext(t, dir2, templateDefServer.URL)

	stdout, _, err = runStackctl(t, dir2, "template", "list", "--quiet")
	require.NoError(t, err)
	lines = strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "1", lines[0])
	assert.Equal(t, "2", lines[1])
	// Verify no table headers or extra output
	assert.NotContains(t, stdout, "ID")
	assert.NotContains(t, stdout, "NAME")

	// --- definition list --quiet ---
	stdout, _, err = runStackctl(t, dir2, "definition", "list", "--quiet")
	require.NoError(t, err)
	lines = strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 1)
	assert.Equal(t, "1", lines[0])
	// Verify no table headers or extra output
	assert.NotContains(t, stdout, "ID")
	assert.NotContains(t, stdout, "NAME")
}
