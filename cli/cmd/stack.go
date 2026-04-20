package cmd

import (
	"fmt"
	"strings"
	"sort"
	"strconv"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

const flagPageSize = "page-size"

var stackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Manage stack instances",
	Long:  "Create, deploy, monitor, and manage stack instances.",
}

var stackListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stack instances",
	Long: `List stack instances with optional filtering.

Examples:
  stackctl stack list
  stackctl stack list --mine
  stackctl stack list --status running --cluster 1
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
			params["definition_id"] = def
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
	Use:   "get <id>",
	Short: "Show stack instance details",
	Long: `Show detailed information about a stack instance.

Examples:
  stackctl stack get 42
  stackctl stack get 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
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

Examples:
  stackctl stack create --name my-stack --definition 1
  stackctl stack create --name my-stack --definition 1 --branch feature/xyz --cluster 2 --ttl 120`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		defID, _ := cmd.Flags().GetString("definition")
		branch, _ := cmd.Flags().GetString("branch")
		clusterID, _ := cmd.Flags().GetString("cluster")
		ttl, _ := cmd.Flags().GetInt("ttl")
		if ttl < 0 {
			return fmt.Errorf("--ttl must be a non-negative integer (0 means no TTL)")
		}

		req := &types.CreateStackRequest{
			Name:              name,
			StackDefinitionID: defID,
			Branch:            branch,
			ClusterID:         clusterID,
			TTLMinutes:        ttl,
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		created, err := c.CreateStack(req)
		if err != nil {
			return err
		}

		return printInstance(created)
	},
}

var stackDeployCmd = &cobra.Command{
	Use:   "deploy <id>",
	Short: "Deploy a stack instance",
	Long: `Trigger a deployment for a stack instance.

Examples:
  stackctl stack deploy 42`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		log, err := c.DeployStack(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, log.ID)
			return nil
		}

		printer.PrintMessage("Deploying stack %s... (log ID: %s)", id, log.ID)
		return nil
	},
}

var stackStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a stack instance",
	Long: `Stop a running stack instance.

Examples:
  stackctl stack stop 42`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		log, err := c.StopStack(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, log.ID)
			return nil
		}

		printer.PrintMessage("Stopping stack %s... (log ID: %s)", id, log.ID)
		return nil
	},
}

var stackCleanCmd = &cobra.Command{
	Use:   "clean <id>",
	Short: "Undeploy and remove namespace for a stack instance",
	Long: `Undeploy a stack instance and remove its namespace.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl stack clean 42
  stackctl stack clean 42 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
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

		c, err := newClient()
		if err != nil {
			return err
		}

		log, err := c.CleanStack(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, log.ID)
			return nil
		}

		printer.PrintMessage("Cleaning stack %s... (log ID: %s)", id, log.ID)
		return nil
	},
}

var stackDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a stack instance",
	Long: `Permanently delete a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl stack delete 42
  stackctl stack delete 42 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete stack %s. Continue? (y/n): ",
			func(c *client.Client, id string) error { return c.DeleteStack(id) },
			"Deleted stack %s",
		)
	},
}

var stackStatusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "Show pod status for a stack instance",
	Long: `Show the current status and pod states for a stack instance.

Examples:
  stackctl stack status 42
  stackctl stack status 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
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
	Use:   "logs <id>",
	Short: "Show latest deployment log for a stack instance",
	Long: `Show the latest deployment log for a stack instance.

Examples:
  stackctl stack logs 42
  stackctl stack logs 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
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
	Use:   "clone <id>",
	Short: "Clone a stack instance",
	Long: `Clone a stack instance, creating a new instance with the same configuration.

Examples:
  stackctl stack clone 42
  stackctl stack clone 42 -q`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
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
	Use:   "extend <id>",
	Short: "Extend the TTL of a stack instance",
	Long: `Extend the time-to-live of a stack instance by the specified number of minutes.

Examples:
  stackctl stack extend 42 --minutes 60
  stackctl stack extend 42 --minutes 120`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		minutes, _ := cmd.Flags().GetInt("minutes")
		if minutes <= 0 {
			return fmt.Errorf("--minutes must be a positive integer")
		}

		c, err := newClient()
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
	Use:   "values <id>",
	Short: "Show merged Helm values for a stack instance",
	Long: `Show the fully merged Helm values for a stack instance.

Nested values are displayed as JSON by default. Use -o yaml for YAML format.

Examples:
  stackctl stack values 1
  stackctl stack values 1 --chart my-chart
  stackctl stack values 1 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		chart, _ := cmd.Flags().GetString("chart")

		c, err := newClient()
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
	Use:   "compare <id1> <id2>",
	Short: "Compare two stack instances",
	Long: `Compare two stack instances and show their differences.

Examples:
  stackctl stack compare 42 43
  stackctl stack compare 42 43 -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		leftID, err := parseID(args[0])
		if err != nil {
			return err
		}
		rightID, err := parseID(args[1])
		if err != nil {
			return err
		}

		if leftID == rightID {
			return fmt.Errorf("cannot compare an instance with itself (both IDs are %s)", leftID)
		}

		c, err := newClient()
		if err != nil {
			return err
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
	Use:   "history <id>",
	Short: "Show deployment history for a stack instance",
	Long: `Show the deployment history for a stack instance.

Examples:
  stackctl stack history 42
  stackctl stack history 42 --limit 20
  stackctl stack history 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		limit, _ := cmd.Flags().GetInt("limit")

		c, err := newClient()
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
	Use:   "rollback <id>",
	Short: "Rollback a stack instance to the previous deployment",
	Long: `Rollback all Helm releases in a stack instance to their previous revision.

This is a potentially disruptive operation. You will be prompted for
confirmation unless --yes is specified.

Optionally specify --target-log to rollback to a specific past deployment.

Examples:
  stackctl stack rollback 42
  stackctl stack rollback 42 --yes
  stackctl stack rollback 42 --target-log abc-123`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
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

		c, err := newClient()
		if err != nil {
			return err
		}

		targetLog, _ := cmd.Flags().GetString("target-log")
		req := &types.RollbackRequest{TargetLogID: targetLog}

		resp, err := c.RollbackStack(id, req)
		if err != nil {
			return err
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
	Use:   "history-values <instance-id> <log-id>",
	Short: "Show values used in a past deployment",
	Long: `Show the merged Helm values that were used in a specific deployment.

Examples:
  stackctl stack history-values 42 abc-123
  stackctl stack history-values 42 abc-123 -o yaml`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}
		logID, err := parseID(args[1])
		if err != nil {
			return err
		}

		c, err := newClient()
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
	stackListCmd.Flags().String("definition", "", "Filter by definition ID")
	stackListCmd.Flags().Int("page", 0, "Page number")
	stackListCmd.Flags().Int(flagPageSize, 0, "Page size")
	stackListCmd.MarkFlagsMutuallyExclusive("mine", "owner")

	// stack create flags
	stackCreateCmd.Flags().String("name", "", "Stack instance name (required)")
	stackCreateCmd.Flags().String("definition", "", "Stack definition ID (required)")
	stackCreateCmd.Flags().String("branch", "", "Git branch")
	stackCreateCmd.Flags().String("cluster", "", "Target cluster ID")
	stackCreateCmd.Flags().Int("ttl", 0, "Time to live in minutes")
	_ = stackCreateCmd.MarkFlagRequired("name")
	_ = stackCreateCmd.MarkFlagRequired("definition")

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
