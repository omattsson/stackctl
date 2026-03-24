package cmd

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

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
		if cluster, _ := cmd.Flags().GetUint("cluster"); cluster != 0 {
			params["cluster_id"] = strconv.FormatUint(uint64(cluster), 10)
		}
		if def, _ := cmd.Flags().GetUint("definition"); def != 0 {
			params["definition_id"] = strconv.FormatUint(uint64(def), 10)
		}
		if cmd.Flags().Changed("page") {
			page, _ := cmd.Flags().GetInt("page")
			if page > 0 {
				params["page"] = strconv.Itoa(page)
			}
		}
		if cmd.Flags().Changed("page-size") {
			pageSize, _ := cmd.Flags().GetInt("page-size")
			if pageSize > 0 {
				params["page_size"] = strconv.Itoa(pageSize)
			}
		}

		resp, err := c.ListStacks(params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]uint, len(resp.Data))
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
					cluster = strconv.FormatUint(uint64(*s.ClusterID), 10)
				}
				rows[i] = []string{
					strconv.FormatUint(uint64(s.ID), 10),
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
		defID, _ := cmd.Flags().GetUint("definition")
		branch, _ := cmd.Flags().GetString("branch")
		clusterID, _ := cmd.Flags().GetUint("cluster")
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

		printer.PrintMessage("Deploying stack %d... (log ID: %d)", id, log.ID)
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

		printer.PrintMessage("Stopping stack %d... (log ID: %d)", id, log.ID)
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

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Fprintf(cmd.ErrOrStderr(), "This will undeploy and remove the namespace for stack %d. Continue? (y/n): ", id)
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

		log, err := c.CleanStack(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, log.ID)
			return nil
		}

		printer.PrintMessage("Cleaning stack %d... (log ID: %d)", id, log.ID)
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
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			fmt.Fprintf(cmd.ErrOrStderr(), "This will permanently delete stack %d. Continue? (y/n): ", id)
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

		if err := c.DeleteStack(id); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		printer.PrintMessage("Deleted stack %d", id)
		return nil
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
				{Key: "Log ID", Value: strconv.FormatUint(uint64(log.ID), 10)},
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

		printer.PrintMessage("Cloned stack %d → new stack %d", id, instance.ID)
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

		printer.PrintMessage("Extended stack %d TTL by %d minutes", id, minutes)
		return nil
	},
}

func init() {
	// stack list flags
	stackListCmd.Flags().Bool("mine", false, "Show only my stacks")
	stackListCmd.Flags().String("owner", "", "Filter by owner")
	stackListCmd.Flags().String("status", "", "Filter by status")
	stackListCmd.Flags().Uint("cluster", 0, "Filter by cluster ID")
	stackListCmd.Flags().Uint("definition", 0, "Filter by definition ID")
	stackListCmd.Flags().Int("page", 0, "Page number")
	stackListCmd.Flags().Int("page-size", 0, "Page size")
	stackListCmd.MarkFlagsMutuallyExclusive("mine", "owner")

	// stack create flags
	stackCreateCmd.Flags().String("name", "", "Stack instance name (required)")
	stackCreateCmd.Flags().Uint("definition", 0, "Stack definition ID (required)")
	stackCreateCmd.Flags().String("branch", "", "Git branch")
	stackCreateCmd.Flags().Uint("cluster", 0, "Target cluster ID")
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
	rootCmd.AddCommand(stackCmd)
}

// parseID parses a string argument as a uint ID.
func parseID(s string) (uint, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid ID %q: must be a positive integer", s)
	}
	return uint(id), nil
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
			clusterID = strconv.FormatUint(uint64(*instance.ClusterID), 10)
		}
		fields := []output.KeyValue{
			{Key: "ID", Value: strconv.FormatUint(uint64(instance.ID), 10)},
			{Key: "Name", Value: instance.Name},
			{Key: "Status", Value: printer.StatusColor(instance.Status)},
			{Key: "Owner", Value: instance.Owner},
			{Key: "Branch", Value: instance.Branch},
			{Key: "Namespace", Value: instance.Namespace},
			{Key: "Cluster ID", Value: clusterID},
			{Key: "Definition ID", Value: strconv.FormatUint(uint64(instance.StackDefinitionID), 10)},
			{Key: "TTL", Value: strconv.Itoa(instance.TTLMinutes) + " minutes"},
			{Key: "Expires At", Value: formatTime(instance.ExpiresAt)},
			{Key: "Deployed At", Value: formatTime(instance.DeployedAt)},
			{Key: "Created At", Value: instance.CreatedAt.Format(time.RFC3339)},
		}
		return printer.PrintSingle(instance, fields)
	}
}
