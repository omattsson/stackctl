package cmd

import (
	"bytes"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestCmd initializes global state for command tests.
// cmd tests mutate package-level globals (cfg, printer) so they cannot run in parallel.
func setupTestCmd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", dir)

	cfg = &config.Config{
		CurrentContext: "test",
		Contexts: map[string]*config.Context{
			"test": {APIURL: "http://localhost:8081"},
		},
	}
	printer = output.NewPrinter("table", false, true)
}

func TestConfigSetCmd(t *testing.T) {
	setupTestCmd(t)

	configSetCmd.SetArgs([]string{"api-url", "http://new-url:9090"})
	var buf bytes.Buffer
	configSetCmd.SetOut(&buf)
	printer.Writer = &buf

	err := configSetCmd.RunE(configSetCmd, []string{"api-url", "http://new-url:9090"})
	require.NoError(t, err)
	assert.Equal(t, "http://new-url:9090", cfg.Contexts["test"].APIURL)
}

func TestConfigSetCmd_InvalidKey(t *testing.T) {
	setupTestCmd(t)

	err := configSetCmd.RunE(configSetCmd, []string{"invalid-key", "value"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown config key")
}

func TestConfigGetCmd(t *testing.T) {
	setupTestCmd(t)

	var buf bytes.Buffer
	configGetCmd.SetOut(&buf)
	err := configGetCmd.RunE(configGetCmd, []string{"api-url"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "http://localhost:8081")
}

func TestConfigGetCmd_InvalidKey(t *testing.T) {
	setupTestCmd(t)

	err := configGetCmd.RunE(configGetCmd, []string{"bogus"})
	assert.Error(t, err)
}

func TestConfigListCmd_WithContexts(t *testing.T) {
	setupTestCmd(t)
	cfg.Contexts["production"] = &config.Context{APIURL: "https://prod.example.com", APIKey: "sk_prod_123456"}

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configListCmd.RunE(configListCmd, []string{})
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "test")
	assert.Contains(t, output, "production")
	assert.Contains(t, output, "http://localhost:8081")
	assert.Contains(t, output, "https://prod.example.com")
	// API key should be masked
	assert.Contains(t, output, "***")
	assert.NotContains(t, output, "sk_prod_123456")
}

func TestConfigListCmd_Empty(t *testing.T) {
	setupTestCmd(t)
	cfg.Contexts = map[string]*config.Context{}

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configListCmd.RunE(configListCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No contexts configured")
}

func TestConfigUseContextCmd_NewContext(t *testing.T) {
	setupTestCmd(t)

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configUseContextCmd.RunE(configUseContextCmd, []string{"staging"})
	require.NoError(t, err)
	assert.Equal(t, "staging", cfg.CurrentContext)
	assert.Contains(t, cfg.Contexts, "staging")
	assert.Contains(t, buf.String(), "staging")
}

func TestConfigUseContextCmd_ExistingContext(t *testing.T) {
	setupTestCmd(t)
	cfg.Contexts["production"] = &config.Context{APIURL: "https://prod.example.com"}

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configUseContextCmd.RunE(configUseContextCmd, []string{"production"})
	require.NoError(t, err)
	assert.Equal(t, "production", cfg.CurrentContext)
	// Existing context should not be overwritten
	assert.Equal(t, "https://prod.example.com", cfg.Contexts["production"].APIURL)
}

func TestConfigCurrentContextCmd(t *testing.T) {
	setupTestCmd(t)

	var buf bytes.Buffer
	configCurrentContextCmd.SetOut(&buf)

	err := configCurrentContextCmd.RunE(configCurrentContextCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test")
}

func TestConfigCurrentContextCmd_NoContext(t *testing.T) {
	setupTestCmd(t)
	cfg.CurrentContext = ""

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configCurrentContextCmd.RunE(configCurrentContextCmd, []string{})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No current context")
}

func TestConfigDeleteContextCmd(t *testing.T) {
	setupTestCmd(t)
	cfg.Contexts["staging"] = &config.Context{APIURL: "https://staging.example.com"}

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configDeleteContextCmd.RunE(configDeleteContextCmd, []string{"staging"})
	require.NoError(t, err)
	assert.NotContains(t, cfg.Contexts, "staging")
	assert.Contains(t, buf.String(), "Deleted context")
}

func TestConfigDeleteContextCmd_CurrentContext(t *testing.T) {
	setupTestCmd(t)

	var buf bytes.Buffer
	printer.Writer = &buf

	err := configDeleteContextCmd.RunE(configDeleteContextCmd, []string{"test"})
	require.NoError(t, err)
	assert.Empty(t, cfg.CurrentContext, "current context should be cleared when deleted")
	assert.NotContains(t, cfg.Contexts, "test")
}

func TestConfigDeleteContextCmd_NotFound(t *testing.T) {
	setupTestCmd(t)

	err := configDeleteContextCmd.RunE(configDeleteContextCmd, []string{"nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestConfigUseContextCmd_EmptyName(t *testing.T) {
	setupTestCmd(t)

	var buf bytes.Buffer
	printer.Writer = &buf

	// Empty string is passed as the context name argument.
	// ValidateContextName rejects it because the regex requires at least one alphanumeric char.
	err := configUseContextCmd.RunE(configUseContextCmd, []string{""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid context name")
}
