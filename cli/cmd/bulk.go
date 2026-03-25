package cmd

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var bulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Bulk operations on stack instances",
	Long:  "Deploy, stop, clean, or delete multiple stack instances at once.",
}

var bulkDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy multiple stack instances",
	Long: `Deploy multiple stack instances at once.

Examples:
  stackctl bulk deploy --ids 1,2,3
  stackctl bulk deploy --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd)
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
	Use:   "stop",
	Short: "Stop multiple stack instances",
	Long: `Stop multiple stack instances at once.

Examples:
  stackctl bulk stop --ids 1,2,3
  stackctl bulk stop --ids 1,2,3 -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd)
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
	Use:   "clean",
	Short: "Clean multiple stack instances",
	Long: `Undeploy and remove namespaces for multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl bulk clean --ids 1,2,3
  stackctl bulk clean --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd)
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Fprintf(cmd.ErrOrStderr(), "This will clean %d stack instances. Continue? (y/n): ", len(ids))
			reader := bufio.NewReader(cmd.InOrStdin())
			answer, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				printer.PrintMessage("Aborted.")
				return nil
			}
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
	Use:   "delete",
	Short: "Delete multiple stack instances",
	Long: `Permanently delete multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl bulk delete --ids 1,2,3
  stackctl bulk delete --ids 1,2,3 --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := parseBulkIDs(cmd)
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Fprintf(cmd.ErrOrStderr(), "This will permanently delete %d stack instances. Continue? (y/n): ", len(ids))
			reader := bufio.NewReader(cmd.InOrStdin())
			answer, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			if strings.TrimSpace(strings.ToLower(answer)) != "y" {
				printer.PrintMessage("Aborted.")
				return nil
			}
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
	bulkDeployCmd.Flags().String("ids", "", "Comma-separated list of instance IDs (required)")
	_ = bulkDeployCmd.MarkFlagRequired("ids")

	bulkStopCmd.Flags().String("ids", "", "Comma-separated list of instance IDs (required)")
	_ = bulkStopCmd.MarkFlagRequired("ids")

	bulkCleanCmd.Flags().String("ids", "", "Comma-separated list of instance IDs (required)")
	_ = bulkCleanCmd.MarkFlagRequired("ids")
	bulkCleanCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	bulkDeleteCmd.Flags().String("ids", "", "Comma-separated list of instance IDs (required)")
	_ = bulkDeleteCmd.MarkFlagRequired("ids")
	bulkDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	bulkCmd.AddCommand(bulkDeployCmd)
	bulkCmd.AddCommand(bulkStopCmd)
	bulkCmd.AddCommand(bulkCleanCmd)
	bulkCmd.AddCommand(bulkDeleteCmd)
	rootCmd.AddCommand(bulkCmd)
}

func parseBulkIDs(cmd *cobra.Command) ([]uint, error) {
	idsStr, _ := cmd.Flags().GetString("ids")
	if idsStr == "" {
		return nil, fmt.Errorf("--ids is required")
	}

	parts := strings.Split(idsStr, ",")
	if len(parts) > 50 {
		return nil, fmt.Errorf("maximum 50 IDs allowed, got %d", len(parts))
	}

	seen := make(map[uint]bool)
	ids := make([]uint, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseUint(p, 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid ID %q: must be a positive integer", p)
		}
		if seen[uint(id)] {
			continue
		}
		seen[uint(id)] = true
		ids = append(ids, uint(id))
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("--ids must contain at least one valid ID")
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
				strconv.FormatUint(uint64(r.ID), 10),
				printer.StatusColor(status),
				r.Error,
			}
		}
		return printer.PrintTable(headers, rows)
	}
}
