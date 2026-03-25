package cmd

import (
	"fmt"
	"strconv"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Git repository operations",
	Long:  "List branches and validate branch names for git repositories.",
}

var gitBranchesCmd = &cobra.Command{
	Use:   "branches",
	Short: "List branches for a git repository",
	Long: `List branches for a git repository.

Examples:
  stackctl git branches --repo https://github.com/org/repo
  stackctl git branches --repo https://github.com/org/repo -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")

		c, err := newClient()
		if err != nil {
			return err
		}

		branches, err := c.ListGitBranches(repo)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, b := range branches {
				fmt.Fprintln(printer.Writer, b.Name)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(branches)
		case output.FormatYAML:
			return printer.PrintYAML(branches)
		default:
			headers := []string{"NAME", "HEAD"}
			rows := make([][]string, len(branches))
			for i, b := range branches {
				head := ""
				if b.IsHead {
					head = "*"
				}
				rows[i] = []string{b.Name, head}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var gitValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a branch in a git repository",
	Long: `Validate whether a branch exists in a git repository.

Examples:
  stackctl git validate --repo https://github.com/org/repo --branch main
  stackctl git validate --repo https://github.com/org/repo --branch feature/xyz -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		branch, _ := cmd.Flags().GetString("branch")

		c, err := newClient()
		if err != nil {
			return err
		}

		resp, err := c.ValidateGitBranch(repo, branch)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, strconv.FormatBool(resp.Valid))
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			valid := "true"
			if !resp.Valid {
				valid = "false"
			}
			headers := []string{"BRANCH", "VALID", "MESSAGE"}
			rows := [][]string{
				{resp.Branch, valid, resp.Message},
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

func init() {
	gitBranchesCmd.Flags().String("repo", "", "Git repository URL (required)")
	_ = gitBranchesCmd.MarkFlagRequired("repo")

	gitValidateCmd.Flags().String("repo", "", "Git repository URL (required)")
	_ = gitValidateCmd.MarkFlagRequired("repo")
	gitValidateCmd.Flags().String("branch", "", "Branch name to validate (required)")
	_ = gitValidateCmd.MarkFlagRequired("branch")

	gitCmd.AddCommand(gitBranchesCmd)
	gitCmd.AddCommand(gitValidateCmd)
	rootCmd.AddCommand(gitCmd)
}
