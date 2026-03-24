package cmd

import (
	"os"

	"github.com/omattsson/stackctl/pkg/client"
	"github.com/omattsson/stackctl/pkg/config"
	"github.com/omattsson/stackctl/pkg/output"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	flagOutput  string
	flagQuiet   bool
	flagNoColor bool
	flagAPIURL  string
	flagAPIKey  string

	// Loaded at runtime
	cfg     *config.Config
	printer *output.Printer
)

var rootCmd = &cobra.Command{
	Use:   "stackctl",
	Short: "CLI for managing Kubernetes stack deployments",
	Long: `stackctl is a command-line interface for the K8s Stack Manager.

It lets you create, deploy, monitor, and manage Helm-based application
stacks across Kubernetes clusters.

Get started:
  stackctl config use-context local
  stackctl config set api-url http://localhost:8081
  stackctl login
  stackctl stack list`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Load config
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}

		// Initialize printer
		printer = output.NewPrinter(flagOutput, flagQuiet, flagNoColor)

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "Output format: table, json, yaml")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Output only IDs (one per line)")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().StringVar(&flagAPIURL, "api-url", "", "API server URL (overrides config)")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (overrides config)")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// newClient creates an API client from the current config and flags.
func newClient() (*client.Client, error) {
	apiURL := resolveAPIURL()
	if apiURL == "" {
		return nil, errNoAPIURL
	}

	c := client.New(apiURL)

	// API key: flag > env > config
	apiKey := flagAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("STACKCTL_API_KEY")
	}
	if apiKey == "" && cfg.CurrentCtx() != nil {
		apiKey = cfg.CurrentCtx().APIKey
	}
	c.APIKey = apiKey

	// JWT token from stored token file (only if no API key)
	if c.APIKey == "" {
		token, _ := loadToken()
		c.Token = token
	}

	return c, nil
}

// resolveAPIURL determines the API URL from flags, env, or config.
func resolveAPIURL() string {
	if flagAPIURL != "" {
		return flagAPIURL
	}
	if envURL := os.Getenv("STACKCTL_API_URL"); envURL != "" {
		return envURL
	}
	if ctx := cfg.CurrentCtx(); ctx != nil {
		return ctx.APIURL
	}
	return ""
}

var errNoAPIURL = &configError{msg: "no API URL configured. Run 'stackctl config set api-url <url>' or use --api-url"}

type configError struct {
	msg string
}

func (e *configError) Error() string {
	return e.msg
}
