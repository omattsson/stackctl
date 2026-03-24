package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests exercise the config package against a real filesystem.
// Run with: go test ./test/integration/ -v

func TestConfigWorkflow_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)
	configPath := filepath.Join(dir, "config.yaml")

	// 1. Load config from nonexistent file → empty config
	cfg, err := config.LoadFrom(configPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.CurrentContext)
	assert.Empty(t, cfg.Contexts)

	// 2. Create and switch to "local" context
	cfg.Contexts["local"] = &config.Context{}
	cfg.CurrentContext = "local"
	require.NoError(t, cfg.SaveTo(configPath))

	// 3. Set values on local context
	require.NoError(t, cfg.SetContextValue("api-url", "http://localhost:8081"))
	require.NoError(t, cfg.SetContextValue("api-key", "sk_local_test"))
	require.NoError(t, cfg.SetContextValue("insecure", "true"))
	require.NoError(t, cfg.SaveTo(configPath))

	// 4. Reload and verify
	reloaded, err := config.LoadFrom(configPath)
	require.NoError(t, err)
	assert.Equal(t, "local", reloaded.CurrentContext)
	local := reloaded.Contexts["local"]
	require.NotNil(t, local)
	assert.Equal(t, "http://localhost:8081", local.APIURL)
	assert.Equal(t, "sk_local_test", local.APIKey)
	assert.True(t, local.Insecure)

	// 5. Add a production context
	reloaded.Contexts["production"] = &config.Context{
		APIURL: "https://prod.example.com",
		APIKey: "sk_prod_abc",
	}
	reloaded.CurrentContext = "production"
	require.NoError(t, reloaded.SaveTo(configPath))

	// 6. Reload and verify both contexts survive
	final, err := config.LoadFrom(configPath)
	require.NoError(t, err)
	assert.Equal(t, "production", final.CurrentContext)
	assert.Len(t, final.Contexts, 2)
	assert.Equal(t, "http://localhost:8081", final.Contexts["local"].APIURL)
	assert.Equal(t, "https://prod.example.com", final.Contexts["production"].APIURL)

	// 7. Delete a context
	delete(final.Contexts, "production")
	final.CurrentContext = "local"
	require.NoError(t, final.SaveTo(configPath))

	afterDelete, err := config.LoadFrom(configPath)
	require.NoError(t, err)
	assert.Len(t, afterDelete.Contexts, 1)
	assert.NotContains(t, afterDelete.Contexts, "production")
}

func TestTokenWorkflow_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	contextName := "test-ctx"

	// 1. Get token path
	tokenPath, err := config.TokenPath(contextName)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "tokens", contextName+".json"), tokenPath)

	// 2. Create token directory and write a token file
	tokenDir := filepath.Dir(tokenPath)
	require.NoError(t, os.MkdirAll(tokenDir, 0700))
	require.NoError(t, os.WriteFile(tokenPath, []byte(`{"token":"test-jwt","expires_at":"2099-01-01T00:00:00Z"}`), 0600))

	// 3. Verify file exists with correct permissions
	info, err := os.Stat(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// 4. Verify directory permissions
	dirInfo, err := os.Stat(tokenDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	// 5. Remove token
	require.NoError(t, os.Remove(tokenPath))
	_, err = os.Stat(tokenPath)
	assert.True(t, os.IsNotExist(err))
}

func TestConfigFilePermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg := &config.Config{
		CurrentContext: "test",
		Contexts:       map[string]*config.Context{"test": {APIURL: "http://test"}},
	}
	require.NoError(t, cfg.SaveTo(configPath))

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "config file should be owner-only readable/writable")
	}
}

func TestConfigDirectoryCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	deepPath := filepath.Join(dir, "deeply", "nested", "dir", "config.yaml")

	cfg := &config.Config{
		CurrentContext: "test",
		Contexts:       map[string]*config.Context{"test": {}},
	}
	require.NoError(t, cfg.SaveTo(deepPath))

	// Verify all intermediate directories were created
	_, err := os.Stat(filepath.Dir(deepPath))
	require.NoError(t, err)
}

func TestMultipleContextSwitching(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"dev":     {APIURL: "http://dev:8081"},
			"staging": {APIURL: "https://staging.example.com"},
			"prod":    {APIURL: "https://prod.example.com", APIKey: "sk_prod"},
		},
	}

	// Switch between contexts and verify GetContextValue works for each
	for _, ctx := range []string{"dev", "staging", "prod"} {
		cfg.CurrentContext = ctx
		require.NoError(t, cfg.SaveTo(configPath))

		reloaded, err := config.LoadFrom(configPath)
		require.NoError(t, err)
		assert.Equal(t, ctx, reloaded.CurrentContext)

		url, err := reloaded.GetContextValue("api-url")
		require.NoError(t, err)
		assert.Equal(t, cfg.Contexts[ctx].APIURL, url)
	}
}
