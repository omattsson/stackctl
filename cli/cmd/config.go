package cmd

import (
	"fmt"
	"sort"

	"github.com/omattsson/stackctl/pkg/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration and contexts",
	Long: `Manage stackctl configuration, including named contexts for different environments.

Configuration is stored in ~/.stackmanager/config.yaml.`,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value in the current context",
	Long: `Set a configuration value in the current context.

Available keys:
  api-url    API server URL
  api-key    API key for authentication
  insecure   Skip TLS verification (true/false)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]
		if err := cfg.SetContextValue(key, value); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		printer.PrintMessage("Set %s = %s in context %q", key, value, cfg.CurrentContext)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a config value from the current context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value, err := cfg.GetContextValue(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), value)
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all contexts and their configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(cfg.Contexts) == 0 {
			printer.PrintMessage("No contexts configured. Run 'stackctl config use-context <name>' to create one.")
			return nil
		}

		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)

		headers := []string{"", "CONTEXT", "API URL", "API KEY", "INSECURE"}
		var rows [][]string
		for _, name := range names {
			ctx := cfg.Contexts[name]
			marker := " "
			if name == cfg.CurrentContext {
				marker = "*"
			}
			apiKey := ""
			if ctx.APIKey != "" {
				// Mask API key, show only last 4 chars
				if len(ctx.APIKey) > 4 {
					apiKey = "***" + ctx.APIKey[len(ctx.APIKey)-4:]
				} else {
					apiKey = "***"
				}
			}
			insecure := ""
			if ctx.Insecure {
				insecure = "true"
			}
			rows = append(rows, []string{marker, name, ctx.APIURL, apiKey, insecure})
		}
		printer.PrintTable(headers, rows)
		return nil
	},
}

var configUseContextCmd = &cobra.Command{
	Use:   "use-context <name>",
	Short: "Switch to a named context",
	Long: `Switch to a named context. Creates the context if it doesn't exist.

Examples:
  stackctl config use-context local
  stackctl config use-context production`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, ok := cfg.Contexts[name]; !ok {
			cfg.Contexts[name] = &config.Context{}
		}
		cfg.CurrentContext = name
		if err := cfg.Save(); err != nil {
			return err
		}
		printer.PrintMessage("Switched to context %q", name)
		return nil
	},
}

var configCurrentContextCmd = &cobra.Command{
	Use:   "current-context",
	Short: "Show the current context name",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.CurrentContext == "" {
			printer.PrintMessage("No current context set.")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), cfg.CurrentContext)
		return nil
	},
}

var configDeleteContextCmd = &cobra.Command{
	Use:   "delete-context <name>",
	Short: "Delete a named context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, ok := cfg.Contexts[name]; !ok {
			return fmt.Errorf("context %q not found", name)
		}
		delete(cfg.Contexts, name)
		if cfg.CurrentContext == name {
			cfg.CurrentContext = ""
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		printer.PrintMessage("Deleted context %q", name)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configUseContextCmd)
	configCmd.AddCommand(configCurrentContextCmd)
	configCmd.AddCommand(configDeleteContextCmd)
	rootCmd.AddCommand(configCmd)
}
