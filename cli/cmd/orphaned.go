package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var orphanedCmd = &cobra.Command{
	Use:   "orphaned",
	Short: "Manage orphaned Kubernetes namespaces",
	Long: `List and delete stack-* namespaces that have no matching stack instance.

These commands use the admin routes /api/v1/admin/orphaned-namespaces.
They need the admin role.`,
}

var orphanedListCmd = &cobra.Command{
	Use:   "list",
	Short: "List orphaned namespaces",
	Long: `List the stack-* Kubernetes namespaces that have no matching stack instance.

The MANAGED column is "yes" when the namespace has the label
managed-by=k8s-stack-manager. To delete a namespace without this label,
use 'stackctl orphaned delete <namespace> --confirm <namespace>'.

Use --details to add the Helm releases and the resource counts. This is slower.

Examples:
  stackctl orphaned list
  stackctl orphaned list --details
  stackctl orphaned list -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		details, _ := cmd.Flags().GetBool("details")

		c, err := newClient()
		if err != nil {
			return err
		}

		namespaces, err := c.ListOrphanedNamespaces(details)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, ns := range namespaces {
				fmt.Fprintln(printer.Writer, ns.Name)
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
			headers := []string{"NAMESPACE", "PHASE", "MANAGED", "CREATED"}
			if details {
				headers = append(headers, "RELEASES", "PODS", "DEPLOYMENTS", "SERVICES")
			}
			rows := make([][]string, len(namespaces))
			for i, ns := range namespaces {
				managed := "no"
				if ns.Managed {
					managed = "yes"
				}
				row := []string{ns.Name, ns.Phase, managed, ns.CreatedAt}
				if details {
					releases := strings.Join(ns.HelmReleases, ",")
					if releases == "" {
						releases = "-"
					}
					pods, deployments, services := "-", "-", "-"
					if rc := ns.ResourceCounts; rc != nil {
						pods = strconv.Itoa(rc.Pods)
						deployments = strconv.Itoa(rc.Deployments)
						services = strconv.Itoa(rc.Services)
					}
					row = append(row, releases, pods, deployments, services)
				}
				rows[i] = row
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var orphanedDeleteCmd = &cobra.Command{
	Use:   "delete <namespace>",
	Short: "Delete an orphaned namespace",
	Long: `Uninstall the Helm releases of an orphaned namespace and delete the namespace.

This is a destructive operation. The command asks for confirmation.
Use --yes to skip the prompt.

A namespace without the label managed-by=k8s-stack-manager can belong to
another team or tool. The server deletes it only when --confirm is set to
the full namespace name. Without --confirm, the server refuses the delete
(409 Conflict). 'stackctl orphaned list' shows the label in the MANAGED column.

Examples:
  stackctl orphaned delete stack-old-namespace
  stackctl orphaned delete stack-old-namespace --yes
  stackctl orphaned delete stack-other-tool --confirm stack-other-tool`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace := strings.TrimSpace(args[0])
		if namespace == "" {
			return fmt.Errorf("namespace must not be empty")
		}
		confirmName, _ := cmd.Flags().GetString("confirm")
		confirmName = strings.TrimSpace(confirmName)
		if cmd.Flags().Changed("confirm") && confirmName != namespace {
			return fmt.Errorf("--confirm must be the full namespace name %q", namespace)
		}

		if isDryRun(cmd, "Would delete orphaned namespace %q", namespace) {
			return nil
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

		if err := c.DeleteOrphanedNamespace(namespace, confirmName); err != nil {
			return orphanedDeleteError(err, namespace, confirmName)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, namespace)
			return nil
		}

		printer.PrintMessage("Deleted orphaned namespace %q", namespace)
		return nil
	},
}

// orphanedDeleteError adds the next command to the 409 the server returns
// for a namespace without the managed-by label (k8s-stack-manager v0.8.0+).
func orphanedDeleteError(err error, namespace, confirmName string) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || confirmName != "" {
		return err
	}
	if !strings.Contains(strings.ToLower(apiErr.Message), "not managed") {
		return err
	}
	return fmt.Errorf("%s\nThe namespace has no label managed-by=k8s-stack-manager. Make sure that no other team or tool uses it. To delete it, run:\n  stackctl orphaned delete %s --confirm %s",
		apiErr.Error(), namespace, namespace)
}

func init() {
	orphanedListCmd.Flags().Bool("details", false, "Add the Helm releases and the resource counts (slower)")

	orphanedDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	orphanedDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	orphanedDeleteCmd.Flags().String("confirm", "", "Full namespace name; needed to delete a namespace without the label managed-by=k8s-stack-manager")

	orphanedCmd.AddCommand(orphanedListCmd)
	orphanedCmd.AddCommand(orphanedDeleteCmd)
	rootCmd.AddCommand(orphanedCmd)
}
