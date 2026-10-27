package cmd

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// agentGuide is the guide for agents and scripts. {{VERSION}} is replaced
// with the build version, so the guide names the binary it came with.
//
//go:embed agent_guide.md
var agentGuide string

var agentGuideCmd = &cobra.Command{
	Use:   "agent-guide",
	Short: "Print a guide for AI agents and scripts that use stackctl",
	Long: `Print a Markdown guide for AI agents and scripts: machine-readable
output, exit codes, authentication without a prompt, confirmations,
long-running operations and common tasks.

The guide is part of the binary, so it matches this version of stackctl.
It always prints Markdown (--output has no effect). This command works
without any configuration.

Examples:
  stackctl agent-guide
  stackctl agent-guide > STACKCTL.md`,
	Annotations:  map[string]string{skipConfigAnnotation: "true"},
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), renderAgentGuide())
		return err
	},
}

// renderAgentGuide returns the guide with the build version filled in.
func renderAgentGuide() string {
	return strings.ReplaceAll(agentGuide, "{{VERSION}}", buildVersion)
}

func init() {
	rootCmd.AddCommand(agentGuideCmd)
}
