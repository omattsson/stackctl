package cmd

import (
	"fmt"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var orphanedCmd = &cobra.Command{
	Use:   "orphaned",
	Short: "Manage orphaned Kubernetes namespaces",
	Long:  "List and clean up namespaces that have the stack-manager label but no matching database record.",
}

var orphanedListCmd = &cobra.Command{
	Use:   "list",
	Short: "List orphaned namespaces",
	Long: `List Kubernetes namespaces that have the stack-manager label but no matching stack instance in the database.

Examples:
  stackctl orphaned list
  stackctl orphaned list -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		namespaces, err := c.ListOrphanedNamespaces()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, ns := range namespaces {
				fmt.Fprintln(printer.Writer, ns.Namespace)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(namespaces)
		case output.FormatYAML:
			return printer.PrintYAML(namespaces)
		default:
			if len(namespaces) == 0 {
				printer.PrintMessage("No orphaned namespaces found.")
				return nil
			}
			headers := []string{"NAMESPACE", "CLUSTER", "CREATED"}
			rows := make([][]string, len(namespaces))
			for i, ns := range namespaces {
				rows[i] = []string{ns.Namespace, ns.Cluster, ns.CreatedAt}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var orphanedDeleteCmd = &cobra.Command{
	Use:   "delete <namespace>",
	Short: "Delete an orphaned namespace",
	Long: `Remove an orphaned Kubernetes namespace.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl orphaned delete stack-old-namespace
  stackctl orphaned delete stack-old-namespace --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace := strings.TrimSpace(args[0])
		if namespace == "" {
			return fmt.Errorf("namespace must not be empty")
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will delete orphaned namespace %q. Continue? (y/n): ", namespace))
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

		if err := c.DeleteOrphanedNamespace(namespace); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, namespace)
			return nil
		}

		printer.PrintMessage("Deleted orphaned namespace %q", namespace)
		return nil
	},
}

func init() {
	orphanedDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	orphanedCmd.AddCommand(orphanedListCmd)
	orphanedCmd.AddCommand(orphanedDeleteCmd)
	rootCmd.AddCommand(orphanedCmd)
}
