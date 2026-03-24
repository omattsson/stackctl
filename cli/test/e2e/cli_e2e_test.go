package e2e

import (
	"encoding/json"
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
