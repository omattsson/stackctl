package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionWithoutConfig(t *testing.T) {
	// version should work even with no config file
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"version"})
	err := rootCmd.Execute()
	assert.NoError(t, err)
}

func TestVersionWithInvalidConfig(t *testing.T) {
	// version should work even with a corrupted/invalid config file
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)

	invalidConfig := []byte(":\n- invalid\n")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), invalidConfig, 0600))

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"version"})
	err := rootCmd.Execute()
	assert.NoError(t, err)
}

func TestCompletionWithoutConfig(t *testing.T) {
	// completion should work even with no config file
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"completion", "bash"})
	err := rootCmd.Execute()
	assert.NoError(t, err)
	assert.Contains(t, stdout.String(), "bash")
}

func TestInsecureWarning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)
	configContent := []byte("current-context: test\ncontexts:\n  test:\n    api-url: http://localhost:8081\n")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configContent, 0600))

	// Ensure we start from a known flag state; it persists between test runs.
	flagInsecure = false
	defer func() { flagInsecure = false }()

	// version --insecure should NOT emit the warning because version skips config loading.
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"version", "--insecure"})

	err := rootCmd.Execute()
	assert.NoError(t, err)
	assert.NotContains(t, stderr.String(), "WARNING: TLS certificate verification is disabled")

	// A config-loading command with --insecure SHOULD emit the warning.
	stderr.Reset()
	rootCmd.SetErr(&stderr)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"config", "list", "--insecure"})

	err = rootCmd.Execute()
	assert.NoError(t, err)
	assert.Contains(t, stderr.String(), "WARNING: TLS certificate verification is disabled")
}

func TestInsecureWarningFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)
	configContent := []byte("current-context: test\ncontexts:\n  test:\n    api-url: http://localhost:8081\n    insecure: true\n")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configContent, 0600))

	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetOut(&bytes.Buffer{})
	// Use "config list" as it loads config but doesn't need API
	rootCmd.SetArgs([]string{"config", "list"})
	flagInsecure = false

	err := rootCmd.Execute()
	assert.NoError(t, err)
	assert.Contains(t, stderr.String(), "WARNING: TLS certificate verification is disabled")
}
