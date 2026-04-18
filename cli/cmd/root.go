package cmd

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	flagOutput   string
	flagQuiet    bool
	flagNoColor  bool
	flagAPIURL   string
	flagAPIKey   string
	flagInsecure bool

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
  stackctl version`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Initialize printer with Cobra's configured output writer
		printer = output.NewPrinter(flagOutput, flagQuiet, flagNoColor)
		printer.Writer = cmd.OutOrStdout()

		// Skip config loading for commands that should work without a config file
		name := cmd.Name()
		if name == "version" || name == "completion" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
			cfg = &config.Config{Contexts: map[string]*config.Context{}}
			return nil
		}

		// Load config
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}

		// Warn when TLS verification is disabled
		insecure := flagInsecure
		if !insecure && cfg.CurrentCtx() != nil {
			insecure = cfg.CurrentCtx().Insecure
		}
		if insecure {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: TLS certificate verification is disabled")
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "Output format: table, json, yaml")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Output only IDs (one per line)")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().StringVar(&flagAPIURL, "api-url", "", "API server URL (overrides config)")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification (use with caution)")
}

// Execute runs the root command.
func Execute() error {
	// Discover external plugins from $PATH and register any that do not
	// collide with built-in commands. Ignoring collisions rather than
	// erroring keeps the CLI usable if a user happens to have a
	// `stackctl-<builtin>` binary lying around.
	registerPlugins(rootCmd, os.Getenv("PATH"))
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

	applyInsecureTLS(c)

	// JWT token from stored token file (only if no API key)
	if c.APIKey == "" {
		token, err := loadToken()
		if err != nil && !flagQuiet {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		c.Token = token
	}

	return c, nil
}

// newUnauthenticatedClient creates a client without loading any credentials.
// Used for login where we don't need (and don't want warnings about) existing tokens.
func newUnauthenticatedClient() (*client.Client, error) {
	apiURL := resolveAPIURL()
	if apiURL == "" {
		return nil, errNoAPIURL
	}

	c := client.New(apiURL)
	applyInsecureTLS(c)

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

// applyInsecureTLS configures the client to skip TLS verification if the insecure flag is set.
func applyInsecureTLS(c *client.Client) {
	insecure := flagInsecure
	if !insecure && cfg.CurrentCtx() != nil {
		insecure = cfg.CurrentCtx().Insecure
	}
	if insecure {
		// Clone the default transport to preserve proxies, timeouts, and keep-alives.
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			clone := t.Clone()
			clone.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user-requested
			c.HTTPClient.Transport = clone
		} else {
			c.HTTPClient.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // user-requested
			}
		}
	}
}

var errNoAPIURL = &configError{msg: "no API URL configured. Run 'stackctl config set api-url <url>' or use --api-url"}

type configError struct {
	msg string
}

func (e *configError) Error() string {
	return e.msg
}

// confirmAction prompts the user for confirmation unless --yes is set.
// Returns true if the action should proceed, false if aborted.
func confirmAction(cmd *cobra.Command, message string) (bool, error) {
	yes, _ := cmd.Flags().GetBool("yes")
	if yes {
		return true, nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), message)
	reader := bufio.NewReader(cmd.InOrStdin())
	answer, err := reader.ReadString('\n')
	if err != nil && (err != io.EOF || answer == "") {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(answer)) != "y" {
		return false, nil
	}
	return true, nil
}

func deleteByID(cmd *cobra.Command, args []string, promptFmt string, deleteFn func(*client.Client, string) error, successFmt string) error {
	id, err := parseID(args[0])
	if err != nil {
		return err
	}

	confirmed, err := confirmAction(cmd, fmt.Sprintf(promptFmt, id))
	if err != nil {
		return err
	}
	if !confirmed {
		printer.PrintMessage("Aborted.")
		return nil
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	if err := deleteFn(c, id); err != nil {
		return err
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, id)
		return nil
	}

	printer.PrintMessage(successFmt, id)
	return nil
}
