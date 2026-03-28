package cmd

import (
	"fmt"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// SetVersionInfo sets the build-time version info from main.go ldflags.
func SetVersionInfo(version, commit, date string) {
	buildVersion = version
	buildCommit = commit
	buildDate = date
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long: `Print the stackctl version, git commit, and build date.

This command works without any configuration and can be used to verify
that stackctl is installed correctly.

Examples:
  stackctl version
  stackctl version -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		versionInfo := map[string]string{
			"version": buildVersion,
			"commit":  buildCommit,
			"date":    buildDate,
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(versionInfo)
		case output.FormatYAML:
			return printer.PrintYAML(versionInfo)
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "stackctl %s (commit: %s, built: %s)\n", buildVersion, buildCommit, buildDate)
			return nil
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
