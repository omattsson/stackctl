package cmd

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/config"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	// Global flags
	flagOutput   string
	flagQuiet    bool
	flagNoColor  bool
	flagAPIURL   string
	flagAPIKey   string
	flagInsecure bool
	flagDebug    bool

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
  stackctl version

AI agents and scripts: run 'stackctl agent-guide' first.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Initialize printer with Cobra's configured output writer. Colors
		// only go to a terminal: not with --no-color, NO_COLOR
		// (https://no-color.org) or a stdout that is a pipe or a file.
		printer = output.NewPrinter(flagOutput, flagQuiet, flagNoColor || noColorEnv() || !isTerminalWriter(cmd.OutOrStdout()))
		printer.Writer = cmd.OutOrStdout()
		printer.ErrWriter = cmd.ErrOrStderr()

		// Skip config loading for commands that should work without a config file
		if skipsConfig(cmd) {
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
		if effectiveInsecure() {
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
	rootCmd.PersistentFlags().BoolVar(&flagDebug, "debug", false, "Log HTTP requests and responses to stderr")
}

// Execute runs the root command.
func Execute() error {
	// Discover external plugins from $PATH and register any that do not
	// collide with built-in commands. Ignoring collisions rather than
	// erroring keeps the CLI usable if a user happens to have a
	// `stackctl-<builtin>` binary lying around.
	registerPlugins(rootCmd, os.Getenv("PATH"))
	args := os.Args[1:]
	if testArgs != nil {
		args = testArgs
	}
	// For a plugin, the root command parses the global flags before the
	// plugin name (TraverseChildren). The plugin (DisableFlagParsing) then
	// gets only the arguments after its name, and pluginEnv passes the flag
	// values as STACKCTL_* variables: `stackctl --no-color refresh-db
	// status x` runs the plugin with "status x" and STACKCTL_NO_COLOR=1
	// (stackctl#144). Built-in commands keep the default flag parsing.
	rootCmd.TraverseChildren = targetsPlugin(rootCmd, args)
	return rootCmd.Execute()
}

// testArgs holds the arguments of SetArgs (nil: os.Args[1:]).
var testArgs []string

// SetArgs overrides the command-line arguments for the next Execute call.
// Intended for integration tests.
func SetArgs(args []string) {
	testArgs = args
	rootCmd.SetArgs(args)
}

// SetOut redirects the root command output writer.
// PersistentPreRunE passes this through to printer.Writer, so all command
// output is captured. Intended for integration tests.
func SetOut(w io.Writer) {
	rootCmd.SetOut(w)
}

// SetIn redirects the root command input reader. Subcommands that read
// passwords or confirmation responses use cmd.InOrStdin(), which inherits
// the root setting. Intended for integration tests.
func SetIn(r io.Reader) {
	rootCmd.SetIn(r)
}

// ResetFlagsForTest resets all persistent flag variables to their default
// values. Cobra does not reset flags between in-process Execute calls, so
// integration tests that mix flag values across subtests must call this
// before each invocation to prevent flag leakage.
func ResetFlagsForTest() {
	flagOutput = "table"
	flagQuiet = false
	flagNoColor = false
	flagAPIURL = ""
	flagAPIKey = ""
	flagInsecure = false
	flagDebug = false
}

// newClient creates an API client from the current config and flags.
func newClient() (*client.Client, error) {
	apiURL := resolveAPIURL()
	if apiURL == "" {
		return nil, errNoAPIURL
	}

	c := client.New(apiURL)

	// Debug: explicit flag overrides env var
	if rootCmd.PersistentFlags().Changed("debug") {
		c.Debug = flagDebug
	} else if os.Getenv("STACKCTL_DEBUG") == "1" {
		c.Debug = true
	}
	if c.Debug {
		c.DebugWriter = os.Stderr
	}

	c.APIKey = resolveAPIKey()

	applyInsecureTLS(c)

	// JWT token from stored token file (only if no API key)
	if c.APIKey == "" {
		token, warning, err := loadToken()
		if err != nil && !flagQuiet {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		c.Token = token
		if warning != "" && !flagQuiet {
			fmt.Fprintln(os.Stderr, warning)
		}
		// Renew a username/password session automatically (see client/auth.go).
		if token != "" {
			if store := tokenStore(); store != nil {
				c.Tokens = store
			}
		}
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

// resolveAPIKey determines the API key from flags, env, or config.
func resolveAPIKey() string {
	if flagAPIKey != "" {
		return flagAPIKey
	}
	if envKey := os.Getenv("STACKCTL_API_KEY"); envKey != "" {
		return envKey
	}
	if ctx := cfg.CurrentCtx(); ctx != nil {
		return ctx.APIKey
	}
	return ""
}

// effectiveInsecure reports whether TLS verification is off: --insecure, or
// the insecure setting of the current context. The warning, the transport
// (applyInsecureTLS) and the stream check (checkStreamTransport) all use it,
// so they cannot disagree.
func effectiveInsecure() bool {
	if flagInsecure {
		return true
	}
	ctx := cfg.CurrentCtx()
	return ctx != nil && ctx.Insecure
}

// applyInsecureTLS configures the client to skip TLS verification when
// effectiveInsecure reports true (the flag or the context setting).
func applyInsecureTLS(c *client.Client) {
	if effectiveInsecure() {
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

// noColorEnv reports whether NO_COLOR is set to a non-empty value.
func noColorEnv() bool {
	return os.Getenv("NO_COLOR") != ""
}

// isTerminalWriter reports whether w is a terminal.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// skipConfigAnnotation marks a Cobra command that works without a config
// file (version, agent-guide, completion). It also applies to the
// subcommands of a marked command.
const skipConfigAnnotation = "stackctl.skip-config"

// skipsConfig reports whether cmd or one of its parents has
// skipConfigAnnotation.
func skipsConfig(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[skipConfigAnnotation] == "true" {
			return true
		}
	}
	return false
}

// errAborted is returned when the user declines a confirmation prompt: the
// command exits with code 1, so a script can tell that nothing changed.
var errAborted = errors.New("aborted: not confirmed, nothing changed")

// confirmAction prompts the user for confirmation unless --yes is set.
// It returns nil if the action should proceed and errAborted if the user
// declined, so callers only pass the error on.
func confirmAction(cmd *cobra.Command, message string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if yes {
		return nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), message)
	reader := bufio.NewReader(cmd.InOrStdin())
	answer, err := reader.ReadString('\n')
	if err != nil && (err != io.EOF || answer == "") {
		if err == io.EOF {
			// stdin closed without an answer: a non-interactive shell.
			return fmt.Errorf("no answer to the confirmation prompt (non-interactive shell?): use --yes to confirm")
		}
		return fmt.Errorf("reading confirmation: %w (use --yes to skip the prompt)", err)
	}
	if strings.TrimSpace(strings.ToLower(answer)) != "y" {
		return errAborted
	}
	return nil
}

func deleteByID(cmd *cobra.Command, args []string, promptFmt string, resolveFn func(*client.Client, string) (string, error), deleteFn func(*client.Client, string) error, successFmt string) error {
	if isDryRun(cmd, "Would delete %s", args[0]) {
		return nil
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	id, err := resolveFn(c, args[0])
	if err != nil {
		return err
	}

	if err := confirmAction(cmd, fmt.Sprintf(promptFmt, id)); err != nil {
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

func isDryRun(cmd *cobra.Command, format string, args ...interface{}) bool {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		printer.PrintMessage(format, args...)
		return true
	}
	return false
}

func passthroughID(_ *client.Client, id string) (string, error) {
	return parseID(id)
}
