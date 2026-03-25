package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for stackctl.

To load completions:

Bash:
  source <(stackctl completion bash)

Zsh:
  stackctl completion zsh > "${fpath[1]}/_stackctl"

Fish:
  stackctl completion fish | source

PowerShell:
  stackctl completion powershell | Out-String | Invoke-Expression`,
	Args:         cobra.ExactArgs(1),
	ValidArgs:    []string{"bash", "zsh", "fish", "powershell"},
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return rootCmd.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		default:
			return fmt.Errorf("unsupported shell: %s", args[0])
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
