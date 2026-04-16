package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

const flagDescIDs = "Comma-separated list of instance IDs"

var bulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Bulk operations on stack instances",
	Long:  "Deploy, stop, clean, or delete multiple stack instances at once.",
}

var bulkDeployCmd = &cobra.Command{
	Use:   "deploy [IDs...]",
	Short: "Deploy multiple stack instances",
	Long: `Deploy multiple stack instances at once.

IDs can be provided via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk deploy --ids 1,2,3
  stackctl bulk deploy 1 2 3
  stackctl bulk deploy --ids 1,2 3
  stackctl bulk deploy --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd, args)
		if err != nil {
			return err
		}

		c, err := newClient()
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
	Use:   "stop [IDs...]",
	Short: "Stop multiple stack instances",
	Long: `Stop multiple stack instances at once.

IDs can be provided via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk stop --ids 1,2,3
  stackctl bulk stop 1 2 3
  stackctl bulk stop --ids 1,2 3
  stackctl bulk stop --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd, args)
		if err != nil {
			return err
		}

		c, err := newClient()
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
	Use:   "clean [IDs...]",
	Short: "Clean multiple stack instances",
	Long: `Undeploy and remove namespaces for multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

IDs can be provided via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk clean --ids 1,2,3
  stackctl bulk clean 1 2 3
  stackctl bulk clean --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd, args)
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

		c, err := newClient()
		if err != nil {
			return err
		}

		resp, err := c.BulkClean(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp)
	},
}

var bulkDeleteCmd = &cobra.Command{
	Use:   "delete [IDs...]",
	Short: "Delete multiple stack instances",
	Long: `Permanently delete multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

IDs can be provided via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk delete --ids 1,2,3
  stackctl bulk delete 1 2 3
  stackctl bulk delete --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd, args)
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

		c, err := newClient()
		if err != nil {
			return err
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

func parseBulkIDs(cmd *cobra.Command, args []string) ([]string, error) {
	var rawParts []string

	idsStr, _ := cmd.Flags().GetString("ids")
	if idsStr != "" {
		rawParts = append(rawParts, strings.Split(idsStr, ",")...)
	}
	rawParts = append(rawParts, args...)

	seen := make(map[string]bool)
	ids := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseUint(p, 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid ID %q: must be a positive integer", p)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		ids = append(ids, p)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one instance ID is required (use --ids or positional arguments)")
	}

	if len(ids) > 50 {
		return nil, fmt.Errorf("maximum 50 IDs allowed, got %d", len(ids))
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
