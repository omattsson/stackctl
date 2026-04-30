package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage clusters",
	Long:  "List and inspect registered Kubernetes clusters.",
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List clusters",
	Long: `List registered Kubernetes clusters.

Examples:
  stackctl cluster list
  stackctl cluster list -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		clusters, err := c.ListClusters()
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]string, len(clusters))
			for i, cl := range clusters {
				ids[i] = cl.ID
			}
			printer.PrintIDs(ids)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(clusters)
		case output.FormatYAML:
			return printer.PrintYAML(clusters)
		default:
			headers := []string{"ID", "NAME", "STATUS", "DEFAULT", "NODES"}
			rows := make([][]string, len(clusters))
			for i, cl := range clusters {
				isDefault := "false"
				if cl.IsDefault {
					isDefault = "true"
				}
				rows[i] = []string{
					cl.ID,
					cl.Name,
					printer.StatusColor(cl.Status),
					isDefault,
					strconv.Itoa(cl.NodeCount),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var clusterGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show cluster details",
	Long: `Show detailed information about a cluster, including health status.

Examples:
  stackctl cluster get 1
  stackctl cluster get 1 -o json`,
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

		cluster, err := c.GetCluster(id)
		if err != nil {
			return err
		}

		var health *types.ClusterHealthSummary
		if !printer.Quiet {
			h, err := c.GetClusterHealth(id)
			if err != nil {
				// Degrade gracefully for expected unavailability; propagate other errors
				var apiErr *client.APIError
				if !errors.As(err, &apiErr) || (apiErr.StatusCode != 404 && apiErr.StatusCode != 503) {
					return err
				}
			} else {
				health = h
			}
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, cluster.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			combined := map[string]interface{}{
				"cluster": cluster,
			}
			if health != nil {
				combined["health"] = health
			}
			return printer.PrintJSON(combined)
		case output.FormatYAML:
			combined := map[string]interface{}{
				"cluster": cluster,
			}
			if health != nil {
				combined["health"] = health
			}
			return printer.PrintYAML(combined)
		default:
			isDefault := "false"
			if cluster.IsDefault {
				isDefault = "true"
			}
			fields := []output.KeyValue{
				{Key: "ID", Value: cluster.ID},
				{Key: "Name", Value: cluster.Name},
				{Key: "Description", Value: cluster.Description},
				{Key: "Status", Value: printer.StatusColor(cluster.Status)},
				{Key: "Default", Value: isDefault},
				{Key: "Nodes", Value: strconv.Itoa(cluster.NodeCount)},
			}
			if health != nil {
				fields = append(fields,
					output.KeyValue{Key: "Health Status", Value: printer.StatusColor(health.Status)},
					output.KeyValue{Key: "CPU Usage", Value: health.CPUUsage},
					output.KeyValue{Key: "CPU Total", Value: health.CPUTotal},
					output.KeyValue{Key: "Memory Usage", Value: health.MemUsage},
					output.KeyValue{Key: "Memory Total", Value: health.MemTotal},
				)
			} else {
				fields = append(fields,
					output.KeyValue{Key: "Health Status", Value: "unavailable"},
				)
			}
			return printer.PrintSingle(cluster, fields)
		}
	},
}

// --- Shared Values ---

var clusterSharedValuesCmd = &cobra.Command{
	Use:   "shared-values",
	Short: "Manage cluster-level shared Helm values",
	Long:  "List, create, and delete shared Helm values that apply to all deployments on a cluster.",
}

var clusterSharedValuesListCmd = &cobra.Command{
	Use:   "list <cluster-id>",
	Short: "List shared values for a cluster",
	Long: `List all shared Helm values configured for a cluster.

Examples:
  stackctl cluster shared-values list 1
  stackctl cluster shared-values list 1 -o json`,
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

		svList, err := c.ListSharedValues(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, sv := range svList {
				fmt.Fprintln(printer.Writer, sv.ID)
			}
			return nil
		}

		if len(svList) == 0 {
			printer.PrintMessage("No shared values found for cluster %s", id)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(svList)
		case output.FormatYAML:
			return printer.PrintYAML(svList)
		default:
			headers := []string{"ID", "NAME", "PRIORITY", "HAS VALUES", "UPDATED AT"}
			rows := make([][]string, len(svList))
			for i, sv := range svList {
				hasValues := "false"
				if sv.Values != "" {
					hasValues = "true"
				}
				rows[i] = []string{
					sv.ID,
					sv.Name,
					strconv.Itoa(sv.Priority),
					hasValues,
					sv.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var clusterSharedValuesSetCmd = &cobra.Command{
	Use:   "set <cluster-id>",
	Short: "Create or update shared values for a cluster",
	Long: `Create or update shared Helm values for a cluster.

Values are provided via --file (JSON or YAML) and/or --set key=value flags,
following the same syntax as 'override set'.

Examples:
  stackctl cluster shared-values set 1 --name "local-dev-defaults" --file values.yaml
  stackctl cluster shared-values set 1 --name "local-dev-defaults" --set persistence.storageClass=local-path
  stackctl cluster shared-values set 1 --name "local-dev-defaults" --file values.yaml --priority 10`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		file, _ := cmd.Flags().GetString("file")
		setFlags, _ := cmd.Flags().GetStringSlice("set")
		priority, _ := cmd.Flags().GetInt("priority")

		if file == "" && len(setFlags) == 0 {
			return fmt.Errorf("at least one of --file or --set is required")
		}

		values := map[string]interface{}{}

		if file != "" {
			for _, segment := range strings.Split(filepath.ToSlash(file), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			file = filepath.Clean(file)
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", file, err)
			}
			if err := json.Unmarshal(data, &values); err != nil {
				if yamlErr := yaml.Unmarshal(data, &values); yamlErr != nil {
					return fmt.Errorf("invalid JSON/YAML in file %s (json: %v): %w", file, err, yamlErr)
				}
			}
		}

		for _, kv := range setFlags {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --set format %q: expected key=value", kv)
			}
			setNestedValue(values, parts[0], parseScalarValue(parts[1]))
		}

		yamlBytes, err := yaml.Marshal(values)
		if err != nil {
			return fmt.Errorf("serializing values to YAML: %w", err)
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		sv, err := c.SetSharedValues(id, &types.SetSharedValuesRequest{
			Name:     name,
			Values:   string(yamlBytes),
			Priority: priority,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, sv.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(sv)
		case output.FormatYAML:
			return printer.PrintYAML(sv)
		default:
			printer.PrintMessage("Set shared values %q for cluster %s", sv.Name, id)
			return nil
		}
	},
}

var clusterSharedValuesDeleteCmd = &cobra.Command{
	Use:   "delete <cluster-id> <shared-values-id>",
	Short: "Delete shared values from a cluster",
	Long: `Delete shared Helm values from a cluster.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl cluster shared-values delete 1 5
  stackctl cluster shared-values delete 1 5 --yes`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := parseID(args[0])
		if err != nil {
			return err
		}
		svID, err := parseID(args[1])
		if err != nil {
			return err
		}

		if isDryRun(cmd, "Would delete shared values %s from cluster %s", svID, clusterID) {
			return nil
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will delete shared values %s from cluster %s. Continue? (y/n): ", svID, clusterID))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage(msgAborted)
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		if err := c.DeleteSharedValues(clusterID, svID); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, svID)
			return nil
		}

		printer.PrintMessage("Deleted shared values %s from cluster %s", svID, clusterID)
		return nil
	},
}

func init() {
	// shared-values set flags
	clusterSharedValuesSetCmd.Flags().String("name", "", "Name for the shared values entry (required)")
	clusterSharedValuesSetCmd.Flags().String("file", "", "JSON or YAML file with values")
	clusterSharedValuesSetCmd.Flags().StringSlice("set", nil, "Set a value (key=value), repeatable")
	clusterSharedValuesSetCmd.Flags().Int("priority", 0, "Merge priority (higher = applied later)")
	_ = clusterSharedValuesSetCmd.MarkFlagRequired("name")

	// shared-values delete flags
	clusterSharedValuesDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	clusterSharedValuesDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// Wire up shared-values subcommands
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesListCmd)
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesSetCmd)
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesDeleteCmd)

	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterSharedValuesCmd)
	rootCmd.AddCommand(clusterCmd)
}
