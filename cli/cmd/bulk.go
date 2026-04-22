package cmd

import (
	"fmt"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

const flagDescIDs = "Comma-separated list of stack names or IDs"

var bulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Bulk operations on stack instances",
	Long:  "Deploy, stop, clean, or delete multiple stack instances at once.",
}

var bulkDeployCmd = &cobra.Command{
	Use:   "deploy [name|ID...]",
	Short: "Deploy multiple stack instances",
	Long: `Deploy multiple stack instances at once.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk deploy --ids 1,2,3
  stackctl bulk deploy my-stack other-stack
  stackctl bulk deploy --ids my-stack,2 3
  stackctl bulk deploy --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkIDs(c, cmd, args)
		if err != nil {
			return err
		}

		resp, err := c.BulkDeploy(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp)
	},
}

var bulkStopCmd = &cobra.Command{
	Use:   "stop [name|ID...]",
	Short: "Stop multiple stack instances",
	Long: `Stop multiple stack instances at once.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk stop --ids 1,2,3
  stackctl bulk stop my-stack other-stack
  stackctl bulk stop --ids my-stack,2 3
  stackctl bulk stop --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkIDs(c, cmd, args)
		if err != nil {
			return err
		}

		resp, err := c.BulkStop(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp)
	},
}

var bulkCleanCmd = &cobra.Command{
	Use:   "clean [name|ID...]",
	Short: "Clean multiple stack instances",
	Long: `Undeploy and remove namespaces for multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk clean --ids 1,2,3
  stackctl bulk clean my-stack other-stack
  stackctl bulk clean --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkIDs(c, cmd, args)
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will clean %d stack instances. Continue? (y/n): ", len(ids)))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		resp, err := c.BulkClean(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp)
	},
}

var bulkDeleteCmd = &cobra.Command{
	Use:   "delete [name|ID...]",
	Short: "Delete multiple stack instances",
	Long: `Permanently delete multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk delete --ids 1,2,3
  stackctl bulk delete my-stack other-stack
  stackctl bulk delete --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkIDs(c, cmd, args)
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will permanently delete %d stack instances. Continue? (y/n): ", len(ids)))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		resp, err := c.BulkDelete(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp)
	},
}

func init() {
	bulkDeployCmd.Flags().String("ids", "", flagDescIDs)

	bulkStopCmd.Flags().String("ids", "", flagDescIDs)

	bulkCleanCmd.Flags().String("ids", "", flagDescIDs)
	bulkCleanCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	bulkDeleteCmd.Flags().String("ids", "", flagDescIDs)
	bulkDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	bulkCmd.AddCommand(bulkDeployCmd)
	bulkCmd.AddCommand(bulkStopCmd)
	bulkCmd.AddCommand(bulkCleanCmd)
	bulkCmd.AddCommand(bulkDeleteCmd)
	rootCmd.AddCommand(bulkCmd)
}

func resolveBulkIDs(c *client.Client, cmd *cobra.Command, args []string) ([]string, error) {
	var rawParts []string

	idsStr, _ := cmd.Flags().GetString("ids")
	if idsStr != "" {
		rawParts = append(rawParts, strings.Split(idsStr, ",")...)
	}
	rawParts = append(rawParts, args...)

	if len(rawParts) > 50 {
		return nil, fmt.Errorf("maximum 50 stacks allowed, got %d", len(rawParts))
	}

	seen := make(map[string]bool)
	ids := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		resolved, err := resolveStackID(c, p)
		if err != nil {
			return nil, err
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		ids = append(ids, resolved)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one stack name or ID is required (use --ids or positional arguments)")
	}

	if len(ids) > 50 {
		return nil, fmt.Errorf("maximum 50 stacks allowed, got %d", len(ids))
	}

	return ids, nil
}

func printBulkResults(resp *types.BulkResponse) error {
	if printer.Quiet {
		for _, r := range resp.Results {
			if r.Success {
				fmt.Fprintln(printer.Writer, r.ID)
			}
		}
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(resp)
	case output.FormatYAML:
		return printer.PrintYAML(resp)
	default:
		headers := []string{"ID", "STATUS", "ERROR"}
		rows := make([][]string, len(resp.Results))
		for i, r := range resp.Results {
			status := "success"
			if !r.Success {
				status = "failed"
			}
			rows[i] = []string{
				r.ID,
				printer.StatusColor(status),
				r.Error,
			}
		}
		return printer.PrintTable(headers, rows)
	}
}
