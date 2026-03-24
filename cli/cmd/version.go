package cmd

import (
	"fmt"

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
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagOutput == "json" {
			return printer.PrintJSON(map[string]string{
				"version": buildVersion,
				"commit":  buildCommit,
				"date":    buildDate,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stackctl %s (commit: %s, built: %s)\n", buildVersion, buildCommit, buildDate)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
