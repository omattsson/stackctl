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
const flagDescTemplateIDs = "Comma-separated list of template names or IDs"

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

The LOG ID column shows the deployment log of each started operation.
-o json and -o yaml print the full response, with log_id per item.
Run 'stackctl stack logs <id> --follow' to follow one operation.

Examples:
  stackctl bulk deploy --ids my-stack,other-stack
  stackctl bulk deploy my-stack other-stack
  stackctl bulk deploy --ids my-stack,6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d 7b2e3d4c-5f6a-4b7c-9d8e-0f1a2b3c4d5e
  stackctl bulk deploy --ids my-stack,other-stack -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would deploy %d stacks: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

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

		return printBulkResults(resp, "")
	},
}

var bulkStopCmd = &cobra.Command{
	Use:   "stop [name|ID...]",
	Short: "Stop multiple stack instances",
	Long: `Stop multiple stack instances at once.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

The LOG ID column shows the deployment log of each started operation.
-o json and -o yaml print the full response, with log_id per item.
Run 'stackctl stack logs <id> --follow' to follow one operation.

Examples:
  stackctl bulk stop --ids my-stack,other-stack
  stackctl bulk stop my-stack other-stack
  stackctl bulk stop --ids my-stack,6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d 7b2e3d4c-5f6a-4b7c-9d8e-0f1a2b3c4d5e
  stackctl bulk stop --ids my-stack,other-stack -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would stop %d stacks: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

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

		return printBulkResults(resp, "")
	},
}

var bulkCleanCmd = &cobra.Command{
	Use:   "clean [name|ID...]",
	Short: "Clean multiple stack instances",
	Long: `Undeploy and remove namespaces for multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

The LOG ID column shows the deployment log of each started operation.
-o json and -o yaml print the full response, with log_id per item.
Run 'stackctl stack logs <id> --follow' to follow one operation.

Examples:
  stackctl bulk clean --ids my-stack,other-stack
  stackctl bulk clean my-stack other-stack
  stackctl bulk clean --ids my-stack,other-stack --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would clean %d stacks: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

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

		return printBulkResults(resp, "")
	},
}

var bulkDeleteCmd = &cobra.Command{
	Use:   "delete [name|ID...]",
	Short: "Delete multiple stack instances",
	Long: `Permanently delete multiple stack instances.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Stacks can be specified by name or ID via --ids flag, positional arguments, or both.

With k8s-stack-manager v0.8.0 or later, a stack with cluster resources
(status running, partial, stopped or error) is cleaned first: the server
uninstalls the Helm releases, deletes the namespace, and deletes the stack
when the clean completes. Its result is "success" with a log ID: "success"
then means that the clean started, not that the stack is deleted. A failed
clean keeps the stack with status error. A draft stack is deleted at once
and has no log ID.
The LOG ID column shows the log of the clean. -o json and -o yaml print
the full response, with log_id per item. Run 'stackctl stack get <id>'
later: "not found" means that the delete is complete.

Examples:
  stackctl bulk delete --ids my-stack,other-stack
  stackctl bulk delete my-stack other-stack
  stackctl bulk delete --ids my-stack,other-stack --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would delete %d stacks: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

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

		return printBulkResults(resp, bulkDeleteLogNote)
	},
}

var bulkTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Bulk operations on stack templates",
	Long:  "Publish, unpublish, or delete multiple stack templates at once.",
}

var bulkTemplatePublishCmd = &cobra.Command{
	Use:   "publish [name|ID...]",
	Short: "Publish multiple stack templates",
	Long: `Publish multiple stack templates at once.

Templates can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk template publish --ids my-template,other-template
  stackctl bulk template publish my-template other-template
  stackctl bulk template publish --ids my-template,8c9d0e1f-2a3b-4c5d-9e6f-7a8b9c0d1e2f 9f0a1b2c-3d4e-4f5a-8b6c-7d8e9f0a1b2c`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would publish %d templates: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkTemplateIDs(c, cmd, args)
		if err != nil {
			return err
		}

		resp, err := c.BulkPublishTemplates(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp, "")
	},
}

var bulkTemplateUnpublishCmd = &cobra.Command{
	Use:   "unpublish [name|ID...]",
	Short: "Unpublish multiple stack templates",
	Long: `Unpublish multiple stack templates at once.

Templates can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk template unpublish --ids my-template,other-template
  stackctl bulk template unpublish my-template other-template`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would unpublish %d templates: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkTemplateIDs(c, cmd, args)
		if err != nil {
			return err
		}

		resp, err := c.BulkUnpublishTemplates(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp, "")
	},
}

var bulkTemplateDeleteCmd = &cobra.Command{
	Use:   "delete [name|ID...]",
	Short: "Delete multiple stack templates",
	Long: `Permanently delete multiple stack templates.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Templates can be specified by name or ID via --ids flag, positional arguments, or both.

Examples:
  stackctl bulk template delete --ids my-template,other-template
  stackctl bulk template delete my-template other-template
  stackctl bulk template delete --ids my-template,other-template --yes`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if raw := rawBulkArgs(cmd, args); isDryRun(cmd, "Would delete %d templates: %s", len(raw), strings.Join(raw, ", ")) {
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		ids, err := resolveBulkTemplateIDs(c, cmd, args)
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will permanently delete %d template(s). Continue? (y/n): ", len(ids)))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		resp, err := c.BulkDeleteTemplates(ids)
		if err != nil {
			return err
		}

		return printBulkResults(resp, "")
	},
}

func init() {
	bulkDeployCmd.Flags().String("ids", "", flagDescIDs)
	bulkDeployCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	bulkStopCmd.Flags().String("ids", "", flagDescIDs)
	bulkStopCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	bulkCleanCmd.Flags().String("ids", "", flagDescIDs)
	bulkCleanCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	bulkCleanCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	bulkDeleteCmd.Flags().String("ids", "", flagDescIDs)
	bulkDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	bulkDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	bulkCmd.AddCommand(bulkDeployCmd)
	bulkCmd.AddCommand(bulkStopCmd)
	bulkCmd.AddCommand(bulkCleanCmd)
	bulkCmd.AddCommand(bulkDeleteCmd)

	bulkTemplatePublishCmd.Flags().String("ids", "", flagDescTemplateIDs)
	bulkTemplatePublishCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	bulkTemplateUnpublishCmd.Flags().String("ids", "", flagDescTemplateIDs)
	bulkTemplateUnpublishCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	bulkTemplateDeleteCmd.Flags().String("ids", "", flagDescTemplateIDs)
	bulkTemplateDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	bulkTemplateDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	bulkTemplateCmd.AddCommand(bulkTemplatePublishCmd)
	bulkTemplateCmd.AddCommand(bulkTemplateUnpublishCmd)
	bulkTemplateCmd.AddCommand(bulkTemplateDeleteCmd)
	bulkCmd.AddCommand(bulkTemplateCmd)

	rootCmd.AddCommand(bulkCmd)
}

func rawBulkArgs(cmd *cobra.Command, args []string) []string {
	var parts []string
	if idsStr, _ := cmd.Flags().GetString("ids"); idsStr != "" {
		parts = append(parts, strings.Split(idsStr, ",")...)
	}
	parts = append(parts, args...)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolveBulkTemplateIDs(c *client.Client, cmd *cobra.Command, args []string) ([]string, error) {
	var rawParts []string

	idsStr, _ := cmd.Flags().GetString("ids")
	if idsStr != "" {
		rawParts = append(rawParts, strings.Split(idsStr, ",")...)
	}
	rawParts = append(rawParts, args...)

	// Normalize: trim whitespace and drop empty tokens before enforcing the cap.
	norm := rawParts[:0]
	for _, p := range rawParts {
		if t := strings.TrimSpace(p); t != "" {
			norm = append(norm, t)
		}
	}
	rawParts = norm

	if len(rawParts) > 50 {
		return nil, fmt.Errorf("maximum 50 templates allowed, got %d", len(rawParts))
	}

	seen := make(map[string]bool)
	ids := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		resolved, err := resolveTemplateID(c, p)
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
		return nil, fmt.Errorf("at least one template name or ID is required (use --ids or positional arguments)")
	}

	return ids, nil
}

func resolveBulkIDs(c *client.Client, cmd *cobra.Command, args []string) ([]string, error) {
	var rawParts []string

	idsStr, _ := cmd.Flags().GetString("ids")
	if idsStr != "" {
		rawParts = append(rawParts, strings.Split(idsStr, ",")...)
	}
	rawParts = append(rawParts, args...)

	// Normalize: trim whitespace and drop empty tokens before enforcing the cap.
	norm := rawParts[:0]
	for _, p := range rawParts {
		if t := strings.TrimSpace(p); t != "" {
			norm = append(norm, t)
		}
	}
	rawParts = norm

	if len(rawParts) > 50 {
		return nil, fmt.Errorf("maximum 50 stacks allowed, got %d", len(rawParts))
	}

	seen := make(map[string]bool)
	ids := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
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

	return ids, nil
}

// bulkDeleteLogNote explains a bulk delete result with a log ID.
const bulkDeleteLogNote = `Note: "success" with a log ID means that the clean started. The server deletes the stack when the clean completes. A failed clean keeps the stack with status error. Run 'stackctl stack get <id>' later: "not found" means that the delete is complete.`

// printBulkResults prints the per-item results. In table mode it prints
// logNote after the table when a successful result has a log ID.
func printBulkResults(resp *types.BulkResponse, logNote string) error {
	if printer.Quiet {
		for _, r := range resp.Results {
			if r.Success() {
				fmt.Fprintln(printer.Writer, r.ID())
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
		// The LOG ID column is shown only when the server returned a log
		// ID (instance operations); template operations have none.
		withLogID := false
		for _, r := range resp.Results {
			if r.LogID != "" {
				withLogID = true
				break
			}
		}
		headers := []string{"ID", "STATUS", "ERROR"}
		if withLogID {
			headers = []string{"ID", "STATUS", "LOG ID", "ERROR"}
		}
		rows := make([][]string, len(resp.Results))
		for i, r := range resp.Results {
			if withLogID {
				rows[i] = []string{r.ID(), printer.StatusColor(r.Status), r.LogID, r.Error}
			} else {
				rows[i] = []string{r.ID(), printer.StatusColor(r.Status), r.Error}
			}
		}
		if err := printer.PrintTable(headers, rows); err != nil {
			return err
		}
		if logNote != "" && hasStartedOperation(resp) {
			printer.PrintMessage("%s", logNote)
		}
		return nil
	}
}

// hasStartedOperation reports whether a successful result has a log ID,
// that is, the server started an operation that continues after the
// response.
func hasStartedOperation(resp *types.BulkResponse) bool {
	for _, r := range resp.Results {
		if r.Success() && r.LogID != "" {
			return true
		}
	}
	return false
}
