package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFrom_NonExistentFile(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFrom("/nonexistent/path/config.yaml")
	require.NoError(t, err)
	assert.Empty(t, cfg.CurrentContext)
	assert.NotNil(t, cfg.Contexts)
	assert.Empty(t, cfg.Contexts)
}

func TestLoadFrom_ValidConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `current-context: local
contexts:
  local:
    api-url: http://localhost:8081
    api-key: sk_test_1234
    insecure: true
  production:
    api-url: https://prod.example.com
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	cfg, err := LoadFrom(path)
	require.NoError(t, err)
	assert.Equal(t, "local", cfg.CurrentContext)
	assert.Len(t, cfg.Contexts, 2)

	local := cfg.Contexts["local"]
	assert.Equal(t, "http://localhost:8081", local.APIURL)
	assert.Equal(t, "sk_test_1234", local.APIKey)
	assert.True(t, local.Insecure)

	prod := cfg.Contexts["production"]
	assert.Equal(t, "https://prod.example.com", prod.APIURL)
	assert.Empty(t, prod.APIKey)
	assert.False(t, prod.Insecure)
}

func TestLoadFrom_InvalidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: [valid: yaml"), 0600))

	_, err := LoadFrom(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoadFrom_EmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0600))

	cfg, err := LoadFrom(path)
	require.NoError(t, err)
	assert.NotNil(t, cfg.Contexts)
}

func TestSaveTo_CreatesDirectoryAndFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.yaml")

	cfg := &Config{
		CurrentContext: "test",
		Contexts: map[string]*Context{
			"test": {APIURL: "http://localhost:8081"},
		},
	}
	require.NoError(t, cfg.SaveTo(path))

	// Verify file exists
	_, err := os.Stat(path)
	require.NoError(t, err)

	// Verify content roundtrips
	loaded, err := LoadFrom(path)
	require.NoError(t, err)
	assert.Equal(t, "test", loaded.CurrentContext)
	assert.Equal(t, "http://localhost:8081", loaded.Contexts["test"].APIURL)
}

func TestSaveTo_FilePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &Config{Contexts: map[string]*Context{}}
	require.NoError(t, cfg.SaveTo(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestCurrentCtx_NoContext(t *testing.T) {
	t.Parallel()
	cfg := &Config{Contexts: map[string]*Context{}}
	assert.Nil(t, cfg.CurrentCtx())
}

func TestCurrentCtx_ContextNotFound(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		CurrentContext: "missing",
		Contexts:       map[string]*Context{},
	}
	assert.Nil(t, cfg.CurrentCtx())
}

func TestCurrentCtx_ContextExists(t *testing.T) {
	t.Parallel()
	ctx := &Context{APIURL: "http://test"}
	cfg := &Config{
		CurrentContext: "test",
		Contexts:       map[string]*Context{"test": ctx},
	}
	assert.Equal(t, ctx, cfg.CurrentCtx())
}

func TestSetContextValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		value   string
		check   func(*testing.T, *Context)
		wantErr bool
	}{
		{
			name:  "set api-url",
			key:   "api-url",
			value: "http://localhost:9090",
			check: func(t *testing.T, ctx *Context) {
				assert.Equal(t, "http://localhost:9090", ctx.APIURL)
			},
		},
		{
			name:  "set api-key",
			key:   "api-key",
			value: "sk_test_abc",
			check: func(t *testing.T, ctx *Context) {
				assert.Equal(t, "sk_test_abc", ctx.APIKey)
			},
		},
		{
			name:  "set insecure true",
			key:   "insecure",
			value: "true",
			check: func(t *testing.T, ctx *Context) {
				assert.True(t, ctx.Insecure)
			},
		},
		{
			name:  "set insecure false",
			key:   "insecure",
			value: "false",
			check: func(t *testing.T, ctx *Context) {
				assert.False(t, ctx.Insecure)
			},
		},
		{
			name:    "unknown key",
			key:     "bogus",
			value:   "val",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{
				CurrentContext: "test",
				Contexts:       map[string]*Context{"test": {}},
			}
			err := cfg.SetContextValue(tt.key, tt.value)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, cfg.Contexts["test"])
		})
	}
}

func TestSetContextValue_NoCurrentContext(t *testing.T) {
	t.Parallel()
	cfg := &Config{Contexts: map[string]*Context{}}
	err := cfg.SetContextValue("api-url", "http://test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no current context set")
}

func TestSetContextValue_CreatesContextIfMissing(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		CurrentContext: "new-ctx",
		Contexts:       map[string]*Context{},
	}
	err := cfg.SetContextValue("api-url", "http://test")
	require.NoError(t, err)
	assert.Equal(t, "http://test", cfg.Contexts["new-ctx"].APIURL)
}

func TestGetContextValue(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		CurrentContext: "test",
		Contexts: map[string]*Context{
			"test": {
				APIURL:   "http://localhost:8081",
				APIKey:   "sk_test",
				Insecure: true,
			},
		},
	}

	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{name: "get api-url", key: "api-url", want: "http://localhost:8081"},
		{name: "get api-key", key: "api-key", want: "sk_test"},
		{name: "get insecure", key: "insecure", want: "true"},
		{name: "unknown key", key: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			val, err := cfg.GetContextValue(tt.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, val)
		})
	}
}

func TestGetContextValue_NoCurrentContext(t *testing.T) {
	t.Parallel()
	cfg := &Config{Contexts: map[string]*Context{}}
	_, err := cfg.GetContextValue("api-url")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no current context set")
}

func TestGetContextValue_ContextNotFound(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		CurrentContext: "missing",
		Contexts:       map[string]*Context{},
	}
	_, err := cfg.GetContextValue("api-url")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetContextValue_InsecureFalse(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		CurrentContext: "test",
		Contexts:       map[string]*Context{"test": {Insecure: false}},
	}
	val, err := cfg.GetContextValue("insecure")
	require.NoError(t, err)
	assert.Equal(t, "false", val)
}

// These tests use t.Setenv so they cannot use t.Parallel().

func TestConfigDir_Default(t *testing.T) {
	t.Setenv("STACKCTL_CONFIG_DIR", "")
	dir, err := ConfigDir()
	require.NoError(t, err)
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, DefaultConfigDir), dir)
}

func TestConfigDir_EnvOverride(t *testing.T) {
	t.Setenv("STACKCTL_CONFIG_DIR", "/custom/path")
	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, "/custom/path", dir)
}

func TestTokenPath(t *testing.T) {
	t.Setenv("STACKCTL_CONFIG_DIR", "/tmp/stacktest")
	path, err := TokenPath("local")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/stacktest/tokens/local.json", path)
}

func TestRoundTrip_MultipleContexts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		CurrentContext: "staging",
		Contexts: map[string]*Context{
			"local":      {APIURL: "http://localhost:8081", Insecure: true},
			"staging":    {APIURL: "https://staging.example.com", APIKey: "sk_staging"},
			"production": {APIURL: "https://prod.example.com", APIKey: "sk_prod"},
		},
	}
	require.NoError(t, original.SaveTo(path))

	loaded, err := LoadFrom(path)
	require.NoError(t, err)
	assert.Equal(t, original.CurrentContext, loaded.CurrentContext)
	assert.Len(t, loaded.Contexts, 3)
	for name, origCtx := range original.Contexts {
		loadedCtx, ok := loaded.Contexts[name]
		require.True(t, ok, "missing context: %s", name)
		assert.Equal(t, origCtx.APIURL, loadedCtx.APIURL)
		assert.Equal(t, origCtx.APIKey, loadedCtx.APIKey)
		assert.Equal(t, origCtx.Insecure, loadedCtx.Insecure)
	}
}
