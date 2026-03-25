package cmd

import (
	"fmt"
	"strconv"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
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

		resp, err := c.ListClusters()
		if err != nil {
			return err
		}

		if printer.Quiet {
			ids := make([]uint, len(resp.Data))
			for i, cl := range resp.Data {
				ids[i] = cl.ID
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
			headers := []string{"ID", "NAME", "STATUS", "DEFAULT", "NODES"}
			rows := make([][]string, len(resp.Data))
			for i, cl := range resp.Data {
				isDefault := "false"
				if cl.IsDefault {
					isDefault = "true"
				}
				rows[i] = []string{
					strconv.FormatUint(uint64(cl.ID), 10),
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
		var healthErr error
		if !printer.Quiet {
			health, healthErr = c.GetClusterHealth(id)
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
			if healthErr == nil {
				combined["health"] = health
			}
			return printer.PrintJSON(combined)
		case output.FormatYAML:
			combined := map[string]interface{}{
				"cluster": cluster,
			}
			if healthErr == nil {
				combined["health"] = health
			}
			return printer.PrintYAML(combined)
		default:
			isDefault := "false"
			if cluster.IsDefault {
				isDefault = "true"
			}
			fields := []output.KeyValue{
				{Key: "ID", Value: strconv.FormatUint(uint64(cluster.ID), 10)},
				{Key: "Name", Value: cluster.Name},
				{Key: "Description", Value: cluster.Description},
				{Key: "Status", Value: printer.StatusColor(cluster.Status)},
				{Key: "Default", Value: isDefault},
				{Key: "Nodes", Value: strconv.Itoa(cluster.NodeCount)},
			}
			if healthErr == nil {
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

func init() {
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	rootCmd.AddCommand(clusterCmd)
}
