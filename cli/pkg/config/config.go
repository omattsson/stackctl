package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigDir  = ".stackmanager"
	DefaultConfigFile = "config.yaml"
	TokenDir          = "tokens"
)

// Config represents the full configuration file.
type Config struct {
	CurrentContext string              `yaml:"current-context"`
	Contexts       map[string]*Context `yaml:"contexts"`
}

// Context represents a named configuration context (e.g., local, production).
type Context struct {
	APIURL   string `yaml:"api-url,omitempty"`
	APIKey   string `yaml:"api-key,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

// ConfigDir returns the configuration directory path.
// Checks STACKCTL_CONFIG_DIR, then XDG_CONFIG_HOME, then falls back to ~/.stackmanager.
func ConfigDir() (string, error) {
	if dir := os.Getenv("STACKCTL_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "stackmanager"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, DefaultConfigDir), nil
}

// ConfigPath returns the full path to the config file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultConfigFile), nil
}

// TokenPath returns the path to the token file for a given context.
func TokenPath(contextName string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, TokenDir, contextName+".json"), nil
}

// Load reads the config file from disk. Returns a default config if the file doesn't exist.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config from a specific path.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				Contexts: make(map[string]*Context),
			}, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]*Context)
	}
	return &cfg, nil
}

// Save writes the config to disk.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}

// SaveTo writes the config to a specific path.
func (c *Config) SaveTo(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	// Enforce 0700 even if directory already existed with broader permissions.
	// On Windows, Chmod is best-effort since POSIX permissions don't apply.
	if err := os.Chmod(dir, 0700); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("setting config directory permissions: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	// Enforce 0600 even if file already existed with broader permissions.
	// On Windows, Chmod is best-effort since POSIX permissions don't apply.
	if err := os.Chmod(path, 0600); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("setting config file permissions: %w", err)
	}
	return nil
}

// CurrentCtx returns the active context, or nil if none is set.
func (c *Config) CurrentCtx() *Context {
	if c.CurrentContext == "" {
		return nil
	}
	return c.Contexts[c.CurrentContext]
}

// SetContextValue sets a key-value pair on the current context, creating it if needed.
func (c *Config) SetContextValue(key, value string) error {
	if c.CurrentContext == "" {
		return fmt.Errorf("no current context set; run 'stackctl config use-context <name>' first")
	}
	ctx, ok := c.Contexts[c.CurrentContext]
	if !ok {
		ctx = &Context{}
		c.Contexts[c.CurrentContext] = ctx
	}
	switch key {
	case "api-url":
		ctx.APIURL = value
	case "api-key":
		ctx.APIKey = value
	case "insecure":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid value for insecure: %q (use true or false)", value)
		}
		ctx.Insecure = b
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// GetContextValue gets a config value from the current context.
func (c *Config) GetContextValue(key string) (string, error) {
	if c.CurrentContext == "" {
		return "", fmt.Errorf("no current context set")
	}
	ctx := c.Contexts[c.CurrentContext]
	if ctx == nil {
		return "", fmt.Errorf("context %q not found", c.CurrentContext)
	}
	switch key {
	case "api-url":
		return ctx.APIURL, nil
	case "api-key":
		return ctx.APIKey, nil
	case "insecure":
		if ctx.Insecure {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
}
