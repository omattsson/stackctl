package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// followLogs streams deployment logs via WebSocket until a terminal status is
// received. Returns an error if the deployment ended in error status.
func followLogs(c *client.Client, instanceID string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	result, err := c.StreamDeploymentLogs(ctx, instanceID, os.Stdout)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	if result.Status == "error" {
		if result.ErrorMessage != "" {
			return fmt.Errorf("deployment failed: %s", result.ErrorMessage)
		}
		return fmt.Errorf("deployment failed")
	}
	return nil
}

const flagPageSize = "page-size"

var stackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Manage stack instances",
	Long: `Create, deploy, monitor, and manage stack instances.

Most commands accept a stack name or UUID as the argument. Purely numeric
values (e.g. "42") are always treated as IDs, not names.`,
}

var stackListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stack instances",
	Long: `List stack instances with optional filtering.

The --definition flag accepts either a definition name or ID.

Examples:
  stackctl stack list
  stackctl stack list --mine
  stackctl stack list --status running --cluster 1
  stackctl stack list --definition klaravik-dev
  stackctl stack list -o json
  stackctl stack list -q | xargs -I{} stackctl stack deploy {}`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		params := map[string]string{}

		mine, _ := cmd.Flags().GetBool("mine")
		if mine {
			params["owner"] = "me"
		}
		if owner, _ := cmd.Flags().GetString("owner"); owner != "" {
			params["owner"] = owner
		}
		if status, _ := cmd.Flags().GetString("status"); status != "" {
			params["status"] = status
		}
		if cluster, _ := cmd.Flags().GetString("cluster"); cluster != "" {
			params["cluster_id"] = cluster
		}
		if def, _ := cmd.Flags().GetString("definition"); def != "" {
			defID, err := resolveDefinitionID(c, def)
			if err != nil {
				return err
			}
			params["definition_id"] = defID
		}
		if cmd.Flags().Changed("page") {
			page, _ := cmd.Flags().GetInt("page")
			if page > 0 {
				params["page"] = strconv.Itoa(page)
			}
		}
		if cmd.Flags().Changed(flagPageSize) {
			pageSize, _ := cmd.Flags().GetInt(flagPageSize)
			if pageSize > 0 {
				params["page_size"] = strconv.Itoa(pageSize)
			}
		}

		resp, err := c.ListStacks(params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(resp.Data))
			for i, s := range resp.Data {
				ids[i] = s.ID
			}
			printer.PrintIDs(ids)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			headers := []string{"ID", "NAME", "STATUS", "OWNER", "BRANCH", "CLUSTER", "DEPLOYED AT"}
			rows := make([][]string, len(resp.Data))
			for i, s := range resp.Data {
				cluster := s.ClusterName
				if cluster == "" && s.ClusterID != nil {
					cluster = *s.ClusterID
				}
				rows[i] = []string{
					s.ID,
					s.Name,
					printer.StatusColor(s.Status),
					s.Owner,
					s.Branch,
					cluster,
					formatTime(s.DeployedAt),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var stackGetCmd = &cobra.Command{
	Use:   "get <name|id>",
	Short: "Show stack instance details",
	Long: `Show detailed information about a stack instance.

Examples:
  stackctl stack get my-stack
  stackctl stack get 550e8400-e29b-41d4-a716-446655440000
  stackctl stack get my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		instance, err := c.GetStack(id)
		if err != nil {
			return err
		}

		return printInstance(instance)
	},
}

var stackCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new stack instance",
	Long: `Create a new stack instance from a definition.

The --definition flag accepts either a definition name or ID.

Examples:
  stackctl stack create --name my-stack --definition klaravik-dev
  stackctl stack create --name my-stack --definition e9af3b10-4633-436b-a131-975a3b598e3e
  stackctl stack create --name my-stack --definition klaravik-dev --branch feature/xyz --cluster 2 --ttl 120`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		defNameOrID, _ := cmd.Flags().GetString("definition")
		branch, _ := cmd.Flags().GetString("branch")
		clusterID, _ := cmd.Flags().GetString("cluster")
		ttl, _ := cmd.Flags().GetInt("ttl")
		if ttl < 0 {
			return fmt.Errorf("--ttl must be a non-negative integer (0 means no TTL)")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		defID, err := resolveDefinitionID(c, defNameOrID)
		if err != nil {
			return err
		}

		req := &types.CreateStackRequest{
			Name:              name,
			StackDefinitionID: defID,
			Branch:            branch,
			ClusterID:         clusterID,
			TTLMinutes:        ttl,
		}

		created, err := c.CreateStack(req)
		if err != nil {
			return err
		}

		return printInstance(created)
	},
}

var stackDeployCmd = &cobra.Command{
	Use:   "deploy <name|id>",
	Short: "Deploy a stack instance",
	Long: `Trigger a deployment for a stack instance.

Use --follow to stream deployment logs in real-time until completion.

Examples:
  stackctl stack deploy my-stack
  stackctl stack deploy my-stack --follow
  stackctl stack deploy 550e8400-e29b-41d4-a716-446655440000`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		resp, err := c.DeployStack(id)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followLogs(c, id)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.LogID)
			return nil
		}

		printer.PrintMessage("Deploying stack %s... (log ID: %s)", id, resp.LogID)
		return nil
	},
}

var stackStopCmd = &cobra.Command{
	Use:   "stop <name|id>",
	Short: "Stop a stack instance",
	Long: `Stop a running stack instance.

Use --follow to stream logs in real-time until completion.

Examples:
  stackctl stack stop my-stack
  stackctl stack stop my-stack --follow`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		resp, err := c.StopStack(id)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followLogs(c, id)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.LogID)
			return nil
		}

		printer.PrintMessage("Stopping stack %s... (log ID: %s)", id, resp.LogID)
		return nil
	},
}

var stackCleanCmd = &cobra.Command{
	Use:   "clean <name|id>",
	Short: "Undeploy and remove namespace for a stack instance",
	Long: `Undeploy a stack instance and remove its namespace.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified. Use --follow to stream logs in real-time.

Examples:
  stackctl stack clean my-stack
  stackctl stack clean my-stack --yes
  stackctl stack clean my-stack --yes --follow`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will undeploy and remove the namespace for stack %s. Continue? (y/n): ", id))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		resp, err := c.CleanStack(id)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followLogs(c, id)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.LogID)
			return nil
		}

		printer.PrintMessage("Cleaning stack %s... (log ID: %s)", id, resp.LogID)
		return nil
	},
}

var stackDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a stack instance",
	Long: `Permanently delete a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl stack delete my-stack
  stackctl stack delete my-stack --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete stack %s. Continue? (y/n): ",
			resolveStackID,
			func(c *client.Client, id string) error { return c.DeleteStack(id) },
			"Deleted stack %s",
		)
	},
}

var stackStatusCmd = &cobra.Command{
	Use:   "status <name|id>",
	Short: "Show pod status for a stack instance",
	Long: `Show the current status and pod states for a stack instance.

Examples:
  stackctl stack status my-stack
  stackctl stack status my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		status, err := c.GetStackStatus(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(status)
		case output.FormatYAML:
			return printer.PrintYAML(status)
		default:
			printer.PrintMessage("Status: %s", printer.StatusColor(status.Status))
			if len(status.Pods) == 0 {
				printer.PrintMessage("No pods found.")
				return nil
			}
			headers := []string{"NAME", "STATUS", "READY", "RESTARTS", "AGE"}
			rows := make([][]string, len(status.Pods))
			for i, p := range status.Pods {
				ready := "false"
				if p.Ready {
					ready = "true"
				}
				rows[i] = []string{
					p.Name,
					printer.StatusColor(p.Status),
					ready,
					strconv.Itoa(p.Restarts),
					p.Age,
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var stackLogsCmd = &cobra.Command{
	Use:   "logs <name|id>",
	Short: "Show latest deployment log for a stack instance",
	Long: `Show the latest deployment log for a stack instance.

Use --follow to stream logs from an active deployment in real-time.

Examples:
  stackctl stack logs my-stack
  stackctl stack logs my-stack --follow
  stackctl stack logs my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followLogs(c, id)
		}

		log, err := c.GetStackLogs(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, log.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(log)
		case output.FormatYAML:
			return printer.PrintYAML(log)
		default:
			fields := []output.KeyValue{
				{Key: "Log ID", Value: log.ID},
				{Key: "Action", Value: log.Action},
				{Key: "Status", Value: printer.StatusColor(log.Status)},
				{Key: "Output", Value: log.Output},
			}
			return printer.PrintSingle(log, fields)
		}
	},
}

var stackCloneCmd = &cobra.Command{
	Use:   "clone <name|id>",
	Short: "Clone a stack instance",
	Long: `Clone a stack instance, creating a new instance with the same configuration.

Examples:
  stackctl stack clone my-stack
  stackctl stack clone my-stack -q`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		instance, err := c.CloneStack(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, instance.ID)
			return nil
		}

		printer.PrintMessage("Cloned stack %s → new stack %s", id, instance.ID)
		return nil
	},
}

var stackExtendCmd = &cobra.Command{
	Use:   "extend <name|id>",
	Short: "Extend the TTL of a stack instance",
	Long: `Extend the time-to-live of a stack instance by the specified number of minutes.

Examples:
  stackctl stack extend my-stack --minutes 60
  stackctl stack extend my-stack --minutes 120`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		minutes, _ := cmd.Flags().GetInt("minutes")
		if minutes <= 0 {
			return fmt.Errorf("--minutes must be a positive integer")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		_, err = c.ExtendStack(id, minutes)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		printer.PrintMessage("Extended stack %s TTL by %d minutes", id, minutes)
		return nil
	},
}

var stackValuesCmd = &cobra.Command{
	Use:   "values <name|id>",
	Short: "Show merged Helm values for a stack instance",
	Long: `Show the fully merged Helm values for a stack instance.

Nested values are displayed as JSON by default. Use -o yaml for YAML format.

Examples:
  stackctl stack values my-stack
  stackctl stack values my-stack --chart my-chart
  stackctl stack values my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		chart, _ := cmd.Flags().GetString("chart")

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		values, err := c.GetMergedValues(id, chart)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(values)
		case output.FormatYAML:
			return printer.PrintYAML(values)
		default:
			return printer.PrintJSON(values)
		}
	},
}

var stackCompareCmd = &cobra.Command{
	Use:   "compare <name|id> <name|id>",
	Short: "Compare two stack instances",
	Long: `Compare two stack instances and show their differences.

Examples:
  stackctl stack compare my-stack other-stack
  stackctl stack compare my-stack other-stack -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		leftID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		rightID, err := resolveStackID(c, args[1])
		if err != nil {
			return err
		}

		if leftID == rightID {
			return fmt.Errorf("cannot compare an instance with itself (both IDs are %s)", leftID)
		}

		result, err := c.CompareInstances(leftID, rightID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, leftID)
			fmt.Fprintln(printer.Writer, rightID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(result)
		case output.FormatYAML:
			return printer.PrintYAML(result)
		default:
			headers := []string{"FIELD", "LEFT", "RIGHT"}
			var rows [][]string
			fields := make([]string, 0, len(result.Diffs))
			for field := range result.Diffs {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for _, field := range fields {
				val := result.Diffs[field]
				if diffMap, ok := val.(map[string]interface{}); ok {
					left := fmt.Sprintf("%v", diffMap["left"])
					right := fmt.Sprintf("%v", diffMap["right"])
					rows = append(rows, []string{field, left, right})
				}
			}
			if len(rows) == 0 {
				printer.PrintMessage("No differences found between stack %s and %s", leftID, rightID)
				return nil
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var stackHistoryCmd = &cobra.Command{
	Use:   "history <name|id>",
	Short: "Show deployment history for a stack instance",
	Long: `Show the deployment history for a stack instance.

Examples:
  stackctl stack history my-stack
  stackctl stack history my-stack --limit 20
  stackctl stack history my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		params := map[string]string{}
		if limit > 0 {
			params["limit"] = strconv.Itoa(limit)
		}

		resp, err := c.GetDeploymentHistory(id, params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(resp.Data))
			for i, d := range resp.Data {
				ids[i] = d.ID
			}
			printer.PrintIDs(ids)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			if len(resp.Data) == 0 {
				printer.PrintMessage("No deployment history for stack %s", id)
				return nil
			}
			headers := []string{"LOG ID", "ACTION", "STATUS", "STARTED", "COMPLETED"}
			rows := make([][]string, len(resp.Data))
			for i, d := range resp.Data {
				rows[i] = []string{
					d.ID,
					d.Action,
					printer.StatusColor(d.Status),
					formatTime(d.StartedAt),
					formatTime(d.CompletedAt),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var stackRollbackCmd = &cobra.Command{
	Use:   "rollback <name|id>",
	Short: "Rollback a stack instance to the previous deployment",
	Long: `Rollback all Helm releases in a stack instance to their previous revision.

This is a potentially disruptive operation. You will be prompted for
confirmation unless --yes is specified. Use --follow to stream logs in real-time.

Optionally specify --target-log to rollback to a specific past deployment.

Examples:
  stackctl stack rollback my-stack
  stackctl stack rollback my-stack --yes --follow
  stackctl stack rollback my-stack --target-log abc-123`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will rollback stack %s. Continue? (y/n): ", id))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		targetLog, _ := cmd.Flags().GetString("target-log")
		req := &types.RollbackRequest{TargetLogID: targetLog}

		resp, err := c.RollbackStack(id, req)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followLogs(c, id)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.LogID)
			return nil
		}

		printer.PrintMessage("Rollback started for stack %s (log ID: %s)", id, resp.LogID)
		return nil
	},
}

var stackHistoryValuesCmd = &cobra.Command{
	Use:   "history-values <name|id> <log-id>",
	Short: "Show values used in a past deployment",
	Long: `Show the merged Helm values that were used in a specific deployment.

Examples:
  stackctl stack history-values my-stack abc-123
  stackctl stack history-values my-stack abc-123 -o yaml`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		logID, err := parseID(args[1])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		resp, err := c.GetDeployLogValues(instanceID, logID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.LogID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			return printer.PrintJSON(resp)
		}
	},
}

func init() {
	// stack list flags
	stackListCmd.Flags().Bool("mine", false, "Show only my stacks")
	stackListCmd.Flags().String("owner", "", "Filter by owner")
	stackListCmd.Flags().String("status", "", "Filter by status")
	stackListCmd.Flags().String("cluster", "", "Filter by cluster ID")
	stackListCmd.Flags().String("definition", "", "Filter by definition name or ID")
	stackListCmd.Flags().Int("page", 0, "Page number")
	stackListCmd.Flags().Int(flagPageSize, 0, "Page size")
	stackListCmd.MarkFlagsMutuallyExclusive("mine", "owner")

	// stack create flags
	stackCreateCmd.Flags().String("name", "", "Stack instance name (required)")
	stackCreateCmd.Flags().String("definition", "", "Stack definition name or ID (required)")
	stackCreateCmd.Flags().String("branch", "", "Git branch")
	stackCreateCmd.Flags().String("cluster", "", "Target cluster ID")
	stackCreateCmd.Flags().Int("ttl", 0, "Time to live in minutes")
	_ = stackCreateCmd.MarkFlagRequired("name")
	_ = stackCreateCmd.MarkFlagRequired("definition")

	// --follow flags
	stackDeployCmd.Flags().BoolP("follow", "f", false, "Stream deployment logs until completion")
	stackStopCmd.Flags().BoolP("follow", "f", false, "Stream logs until completion")
	stackCleanCmd.Flags().BoolP("follow", "f", false, "Stream logs until completion")
	stackLogsCmd.Flags().BoolP("follow", "f", false, "Stream logs from active deployment")
	stackRollbackCmd.Flags().BoolP("follow", "f", false, "Stream logs until completion")

	// stack clean flags
	stackCleanCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	// stack delete flags
	stackDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	// stack extend flags
	stackExtendCmd.Flags().Int("minutes", 0, "Number of minutes to extend TTL by (required)")
	_ = stackExtendCmd.MarkFlagRequired("minutes")

	// stack values flags
	stackValuesCmd.Flags().String("chart", "", "Filter by chart name")

	// stack history flags
	stackHistoryCmd.Flags().Int("limit", 20, "Maximum number of entries to show")

	// stack rollback flags
	stackRollbackCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	stackRollbackCmd.Flags().String("target-log", "", "Target deployment log ID to rollback to")

	// Wire up subcommands
	stackCmd.AddCommand(stackListCmd)
	stackCmd.AddCommand(stackGetCmd)
	stackCmd.AddCommand(stackCreateCmd)
	stackCmd.AddCommand(stackDeployCmd)
	stackCmd.AddCommand(stackStopCmd)
	stackCmd.AddCommand(stackCleanCmd)
	stackCmd.AddCommand(stackDeleteCmd)
	stackCmd.AddCommand(stackStatusCmd)
	stackCmd.AddCommand(stackLogsCmd)
	stackCmd.AddCommand(stackCloneCmd)
	stackCmd.AddCommand(stackExtendCmd)
	stackCmd.AddCommand(stackValuesCmd)
	stackCmd.AddCommand(stackCompareCmd)
	stackCmd.AddCommand(stackHistoryCmd)
	stackCmd.AddCommand(stackRollbackCmd)
	stackCmd.AddCommand(stackHistoryValuesCmd)
	rootCmd.AddCommand(stackCmd)
}

// parseID parses a string argument as a uint ID.
func parseID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("invalid ID: must not be empty")
	}
	return s, nil
}

// formatTime formats a *time.Time as RFC3339 or returns "-" if nil.
func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// printInstance prints a stack instance in the configured output format.
func printInstance(instance *types.StackInstance) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, instance.ID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(instance)
	case output.FormatYAML:
		return printer.PrintYAML(instance)
	default:
		clusterID := "-"
		if instance.ClusterID != nil {
			clusterID = *instance.ClusterID
		}
		fields := []output.KeyValue{
			{Key: "ID", Value: instance.ID},
			{Key: "Name", Value: instance.Name},
			{Key: "Status", Value: printer.StatusColor(instance.Status)},
			{Key: "Owner", Value: instance.Owner},
			{Key: "Branch", Value: instance.Branch},
			{Key: "Namespace", Value: instance.Namespace},
			{Key: "Cluster ID", Value: clusterID},
			{Key: "Definition ID", Value: instance.StackDefinitionID},
			{Key: "TTL", Value: strconv.Itoa(instance.TTLMinutes) + " minutes"},
			{Key: "Expires At", Value: formatTime(instance.ExpiresAt)},
			{Key: "Deployed At", Value: formatTime(instance.DeployedAt)},
			{Key: "Created At", Value: instance.CreatedAt.Format(time.RFC3339)},
		}
		return printer.PrintSingle(instance, fields)
	}
}
