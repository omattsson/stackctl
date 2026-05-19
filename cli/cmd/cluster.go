package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
					output.KeyValue{Key: "Health Status", Value: printer.StatusColor(deriveHealthStatus(health))},
					output.KeyValue{Key: "Ready Nodes", Value: fmt.Sprintf("%d/%d", health.ReadyNodeCount, health.NodeCount)},
					output.KeyValue{Key: "CPU Allocatable", Value: health.AllocatableCPU},
					output.KeyValue{Key: "CPU Total", Value: health.TotalCPU},
					output.KeyValue{Key: "Memory Allocatable", Value: health.AllocatableMemory},
					output.KeyValue{Key: "Memory Total", Value: health.TotalMemory},
					output.KeyValue{Key: "Namespaces", Value: strconv.Itoa(health.NamespaceCount)},
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

// deriveHealthStatus collapses the cluster health summary into a single status
// label suitable for the table/quiet output modes. `unknown` covers the case
// where no nodes were reported (registry hasn't seen any, or the cluster is
// brand new) — distinct from `degraded`, which implies known-but-bad.
func deriveHealthStatus(h *types.ClusterHealthSummary) string {
	switch {
	case h == nil || h.NodeCount == 0:
		return "unknown"
	case h.ReadyNodeCount == h.NodeCount:
		return "healthy"
	default:
		return "degraded"
	}
}

// printCluster renders a single cluster in the configured output format.
func printCluster(cluster *types.Cluster) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, cluster.ID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(cluster)
	case output.FormatYAML:
		return printer.PrintYAML(cluster)
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
		return printer.PrintSingle(cluster, fields)
	}
}

var clusterCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a new cluster",
	Long: `Register a new Kubernetes cluster.

Provide either --from-file with a JSON/YAML file, or use --name and --kubeconfig-data / --kubeconfig-path.

Examples:
  stackctl cluster create --from-file cluster.json
  stackctl cluster create --name prod --kubeconfig-path ~/.kube/config`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString("from-file")

		var req types.CreateClusterRequest

		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", fromFile, err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				if yamlErr := yaml.Unmarshal(data, &req); yamlErr != nil {
					return fmt.Errorf("invalid JSON/YAML in file %s (json: %v): %w", fromFile, err, yamlErr)
				}
			}
			if req.Name == "" {
				return fmt.Errorf("name is required in the cluster file")
			}
			if req.KubeconfigData != "" && req.KubeconfigPath != "" {
				return fmt.Errorf("file %s must not specify both kubeconfig_data and kubeconfig_path", fromFile)
			}
			// Resolve kubeconfig_path in the file to kubeconfig_data client-side.
			// Relative paths are resolved relative to the directory of fromFile.
			if req.KubeconfigPath != "" {
				for _, segment := range strings.Split(filepath.ToSlash(req.KubeconfigPath), "/") {
					if segment == ".." {
						return fmt.Errorf("kubeconfig_path in file must not contain '..' segments")
					}
				}
				kpPath := req.KubeconfigPath
				if !filepath.IsAbs(kpPath) {
					kpPath = filepath.Join(filepath.Dir(fromFile), kpPath)
				}
				content, err := os.ReadFile(filepath.Clean(kpPath))
				if err != nil {
					return fmt.Errorf("reading kubeconfig file %s: %w", req.KubeconfigPath, err)
				}
				req.KubeconfigData = string(content)
				req.KubeconfigPath = ""
			}
		} else {
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				return fmt.Errorf("--name is required (or use --from-file)")
			}
			kubeconfigData, _ := cmd.Flags().GetString("kubeconfig-data")
			kubeconfigPath, _ := cmd.Flags().GetString("kubeconfig-path")
			if kubeconfigData != "" && kubeconfigPath != "" {
				return fmt.Errorf("--kubeconfig-data and --kubeconfig-path are mutually exclusive")
			}
			req.Name = name
			req.Description, _ = cmd.Flags().GetString("description")
			req.KubeconfigData = kubeconfigData
			if kubeconfigPath != "" {
				content, err := os.ReadFile(kubeconfigPath)
				if err != nil {
					return fmt.Errorf("reading kubeconfig file %s: %w", kubeconfigPath, err)
				}
				req.KubeconfigData = string(content)
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		cluster, err := c.CreateCluster(&req)
		if err != nil {
			return err
		}

		return printCluster(cluster)
	},
}

var clusterUpdateCmd = &cobra.Command{
	Use:          "update <id>",
	Short:        "Update cluster configuration",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		fromFile, _ := cmd.Flags().GetString("from-file")
		nameChanged := cmd.Flags().Changed("name")
		descChanged := cmd.Flags().Changed("description")
		kubeconfigDataChanged := cmd.Flags().Changed("kubeconfig-data")
		kubeconfigPathChanged := cmd.Flags().Changed("kubeconfig-path")
		defaultChanged := cmd.Flags().Changed("default")

		if fromFile == "" && !nameChanged && !descChanged && !kubeconfigDataChanged && !kubeconfigPathChanged && !defaultChanged {
			return fmt.Errorf("at least one of --name, --description, --kubeconfig-data, --kubeconfig-path, --default, or --from-file must be specified")
		}

		var req types.UpdateClusterRequest

		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", fromFile, err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				if yamlErr := yaml.Unmarshal(data, &req); yamlErr != nil {
					return fmt.Errorf("invalid JSON/YAML in file %s (json: %v): %w", fromFile, err, yamlErr)
				}
			}
			// Resolve kubeconfig_path in the file to kubeconfig_data client-side.
			// Relative paths are resolved relative to the directory of fromFile.
			if req.KubeconfigData != nil && *req.KubeconfigData != "" && req.KubeconfigPath != nil && *req.KubeconfigPath != "" {
				return fmt.Errorf("file %s must not specify both kubeconfig_data and kubeconfig_path", fromFile)
			}
			// Normalize kubeconfig_path: "" to nil so the empty-payload check below is accurate.
			if req.KubeconfigPath != nil && *req.KubeconfigPath == "" {
				req.KubeconfigPath = nil
			}
			if req.KubeconfigPath != nil && *req.KubeconfigPath != "" {
				for _, segment := range strings.Split(filepath.ToSlash(*req.KubeconfigPath), "/") {
					if segment == ".." {
						return fmt.Errorf("kubeconfig_path in file must not contain '..' segments")
					}
				}
				kpPath := *req.KubeconfigPath
				if !filepath.IsAbs(kpPath) {
					kpPath = filepath.Join(filepath.Dir(fromFile), kpPath)
				}
				content, err := os.ReadFile(filepath.Clean(kpPath))
				if err != nil {
					return fmt.Errorf("reading kubeconfig file %s: %w", *req.KubeconfigPath, err)
				}
				s := string(content)
				req.KubeconfigData = &s
				req.KubeconfigPath = nil
			}
			// Ensure the file contained at least one update field.
			if b, _ := json.Marshal(req); string(b) == "{}" {
				return fmt.Errorf("file %s specifies no update fields: at least one field must be provided", fromFile)
			}
		} else {
			if kubeconfigDataChanged && kubeconfigPathChanged {
				return fmt.Errorf("--kubeconfig-data and --kubeconfig-path are mutually exclusive")
			}
			if nameChanged {
				v, _ := cmd.Flags().GetString("name")
				req.Name = &v
			}
			if descChanged {
				v, _ := cmd.Flags().GetString("description")
				req.Description = &v
			}
			if kubeconfigDataChanged {
				v, _ := cmd.Flags().GetString("kubeconfig-data")
				req.KubeconfigData = &v
			}
			if kubeconfigPathChanged {
				v, _ := cmd.Flags().GetString("kubeconfig-path")
				content, err := os.ReadFile(v)
				if err != nil {
					return fmt.Errorf("reading kubeconfig file %s: %w", v, err)
				}
				s := string(content)
				req.KubeconfigData = &s
			}
			if defaultChanged {
				v, _ := cmd.Flags().GetBool("default")
				req.IsDefault = &v
			}
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		cluster, err := c.UpdateCluster(id, &req)
		if err != nil {
			return err
		}

		return printCluster(cluster)
	},
}

var clusterDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a registered cluster",
	Long: `Permanently delete a registered cluster.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl cluster delete 1
  stackctl cluster delete 1 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete cluster %s. Continue? (y/n): ",
			passthroughID,
			func(c *client.Client, id string) error { return c.DeleteCluster(id) },
			"Deleted cluster %s",
		)
	},
}

var clusterSetDefaultCmd = &cobra.Command{
	Use:          "set-default <id>",
	Short:        "Mark a cluster as the default",
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

		if err := c.SetDefaultCluster(id); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}
		printer.PrintMessage("Cluster %s set as default", id)
		return nil
	},
}

// --- Health & connectivity ---

var clusterTestConnectionCmd = &cobra.Command{
	Use:          "test-connection <id>",
	Short:        "Test connectivity to a cluster's API server",
	Long:         "Issue a lightweight call to the cluster's API server to verify it is reachable.",
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
		result, err := c.TestClusterConnection(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, result.Status)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(result)
		case output.FormatYAML:
			return printer.PrintYAML(result)
		default:
			return printer.PrintSingle(result, []output.KeyValue{
				{Key: "Status", Value: printer.StatusColor(result.Status)},
				{Key: "Message", Value: result.Message},
				{Key: "Server Version", Value: result.ServerVersion},
			})
		}
	},
}

var clusterHealthCmd = &cobra.Command{
	Use:          "health <id>",
	Short:        "Show the cluster health summary",
	Long:         "Show node counts, ready-node ratio, and aggregate CPU/memory capacity for the cluster.",
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
		health, err := c.GetClusterHealth(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, deriveHealthStatus(health))
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(health)
		case output.FormatYAML:
			return printer.PrintYAML(health)
		default:
			return printer.PrintSingle(health, []output.KeyValue{
				{Key: "Health Status", Value: printer.StatusColor(deriveHealthStatus(health))},
				{Key: "Ready Nodes", Value: fmt.Sprintf("%d/%d", health.ReadyNodeCount, health.NodeCount)},
				{Key: "CPU Allocatable", Value: health.AllocatableCPU},
				{Key: "CPU Total", Value: health.TotalCPU},
				{Key: "Memory Allocatable", Value: health.AllocatableMemory},
				{Key: "Memory Total", Value: health.TotalMemory},
				{Key: "Namespaces", Value: strconv.Itoa(health.NamespaceCount)},
			})
		}
	},
}

var clusterNodesCmd = &cobra.Command{
	Use:          "nodes <id>",
	Short:        "List nodes in a cluster with their status",
	Long:         "List per-node health, pod count, and allocatable CPU/memory for the cluster.",
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
		nodes, err := c.GetClusterNodes(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			names := make([]string, len(nodes))
			for i, n := range nodes {
				names[i] = n.Name
			}
			printer.PrintIDs(names)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(nodes)
		case output.FormatYAML:
			return printer.PrintYAML(nodes)
		default:
			headers := []string{"NAME", "STATUS", "PODS", "CPU", "MEMORY"}
			rows := make([][]string, len(nodes))
			for i, n := range nodes {
				rows[i] = []string{
					n.Name,
					printer.StatusColor(n.Status),
					strconv.Itoa(n.PodCount),
					n.Allocatable.CPU,
					n.Allocatable.Memory,
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var clusterNamespacesCmd = &cobra.Command{
	Use:          "namespaces <id>",
	Short:        "List stack-* namespaces present in a cluster",
	Long:         "List namespaces whose names start with stack-, along with their phase and creation time.",
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
		namespaces, err := c.GetClusterNamespaces(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			names := make([]string, len(namespaces))
			for i, n := range namespaces {
				names[i] = n.Name
			}
			printer.PrintIDs(names)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(namespaces)
		case output.FormatYAML:
			return printer.PrintYAML(namespaces)
		default:
			headers := []string{"NAME", "PHASE", "CREATED"}
			rows := make([][]string, len(namespaces))
			for i, n := range namespaces {
				created := ""
				if n.CreatedAt != nil {
					created = n.CreatedAt.UTC().Format("2006-01-02 15:04:05")
				}
				rows[i] = []string{n.Name, printer.StatusColor(n.Phase), created}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var clusterUtilizationCmd = &cobra.Command{
	Use:          "utilization <id>",
	Short:        "Show per-namespace resource utilization",
	Long:         "Show aggregated CPU/memory/pod usage per stack-* namespace in the cluster.",
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
		util, err := c.GetClusterUtilization(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			names := make([]string, len(util.Namespaces))
			for i, ns := range util.Namespaces {
				names[i] = ns.Namespace
			}
			printer.PrintIDs(names)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(util)
		case output.FormatYAML:
			return printer.PrintYAML(util)
		default:
			headers := []string{"NAMESPACE", "CPU USED", "CPU LIMIT", "MEM USED", "MEM LIMIT", "PODS"}
			rows := make([][]string, len(util.Namespaces))
			for i, ns := range util.Namespaces {
				podCol := strconv.Itoa(ns.PodCount)
				if ns.PodLimit > 0 {
					podCol = fmt.Sprintf("%d/%d", ns.PodCount, ns.PodLimit)
				}
				rows[i] = []string{
					ns.Namespace,
					ns.CPUUsed,
					ns.CPULimit,
					ns.MemoryUsed,
					ns.MemoryLimit,
					podCol,
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

// --- Quotas ---

var clusterQuotaCmd = &cobra.Command{
	Use:   "quota",
	Short: "Manage cluster-level resource quotas",
	Long:  "Get, set, and delete the resource-quota config applied to stack-* namespaces on a cluster.",
}

var clusterQuotaGetCmd = &cobra.Command{
	Use:          "get <cluster-id>",
	Short:        "Show the resource-quota config for a cluster",
	Long:         "Show the resource-quota config that the backend applies to stack-* namespaces on this cluster. Returns a not-found error when no quota is configured.",
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
		quota, err := c.GetClusterQuota(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, quota.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(quota)
		case output.FormatYAML:
			return printer.PrintYAML(quota)
		default:
			return printer.PrintSingle(quota, []output.KeyValue{
				{Key: "ID", Value: quota.ID},
				{Key: "Cluster ID", Value: quota.ClusterID},
				{Key: "CPU Request", Value: quota.CPURequest},
				{Key: "CPU Limit", Value: quota.CPULimit},
				{Key: "Memory Request", Value: quota.MemoryRequest},
				{Key: "Memory Limit", Value: quota.MemoryLimit},
				{Key: "Storage Limit", Value: quota.StorageLimit},
				{Key: "Pod Limit", Value: strconv.Itoa(quota.PodLimit)},
			})
		}
	},
}

var clusterQuotaSetCmd = &cobra.Command{
	Use:   "set <cluster-id>",
	Short: "Create or update the resource-quota config for a cluster (admin only)",
	Long: `Create or update the resource-quota config the backend applies to stack-* namespaces on this cluster.

Provide the payload via --from-file (JSON or YAML), or via individual flags
(--cpu-request, --cpu-limit, --memory-request, --memory-limit,
--storage-limit, --pod-limit). When both are provided, individual flags
override the file.

The backend PUT is a full upsert — any field absent from the request body is
treated as cleared (string fields) or "no limit" (--pod-limit 0). To preserve
unspecified fields when using individual flags, the command first fetches the
existing quota and uses its values as defaults; you only need to specify the
fields you want to change. With --from-file, the file contents are sent
verbatim — no merge — so include every field you want to keep.

Examples:
  stackctl cluster quota set 1 --from-file quota.json
  stackctl cluster quota set 1 --cpu-limit 8 --memory-limit 16Gi --pod-limit 50
  stackctl cluster quota set 1 --pod-limit 100   # other fields preserved from current quota`,
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

		req := types.SetClusterQuotaRequest{}
		fromFile, _ := cmd.Flags().GetString("from-file")

		if fromFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(fromFile), "/") {
				if segment == ".." {
					return fmt.Errorf("file path must not contain '..' segments")
				}
			}
			fromFile = filepath.Clean(fromFile)
			data, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", fromFile, err)
			}
			if err := json.Unmarshal(data, &req); err != nil {
				if yamlErr := yaml.Unmarshal(data, &req); yamlErr != nil {
					return fmt.Errorf("invalid JSON/YAML in file %s (json: %v): %w", fromFile, err, yamlErr)
				}
			}
		} else {
			// PUT is a full upsert — pre-fetch the existing quota so omitted
			// flags preserve their current values instead of silently clearing
			// them. A 404 means "no quota yet" — start from a zero value.
			existing, getErr := c.GetClusterQuota(id)
			if getErr == nil {
				req = types.SetClusterQuotaRequest{
					CPURequest:    existing.CPURequest,
					CPULimit:      existing.CPULimit,
					MemoryRequest: existing.MemoryRequest,
					MemoryLimit:   existing.MemoryLimit,
					StorageLimit:  existing.StorageLimit,
					PodLimit:      existing.PodLimit,
				}
			} else {
				var apiErr *client.APIError
				if !errors.As(getErr, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
					return getErr
				}
			}
		}

		// Individual flags override file/existing values when set on the command line.
		if cmd.Flags().Changed("cpu-request") {
			req.CPURequest, _ = cmd.Flags().GetString("cpu-request")
		}
		if cmd.Flags().Changed("cpu-limit") {
			req.CPULimit, _ = cmd.Flags().GetString("cpu-limit")
		}
		if cmd.Flags().Changed("memory-request") {
			req.MemoryRequest, _ = cmd.Flags().GetString("memory-request")
		}
		if cmd.Flags().Changed("memory-limit") {
			req.MemoryLimit, _ = cmd.Flags().GetString("memory-limit")
		}
		if cmd.Flags().Changed("storage-limit") {
			req.StorageLimit, _ = cmd.Flags().GetString("storage-limit")
		}
		if cmd.Flags().Changed("pod-limit") {
			req.PodLimit, _ = cmd.Flags().GetInt("pod-limit")
		}

		quota, err := c.SetClusterQuota(id, &req)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, quota.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(quota)
		case output.FormatYAML:
			return printer.PrintYAML(quota)
		default:
			// Render the persisted quota so the user can confirm the server's
			// view (post-validation, with timestamps).
			return printer.PrintSingle(quota, []output.KeyValue{
				{Key: "ID", Value: quota.ID},
				{Key: "Cluster ID", Value: quota.ClusterID},
				{Key: "CPU Request", Value: quota.CPURequest},
				{Key: "CPU Limit", Value: quota.CPULimit},
				{Key: "Memory Request", Value: quota.MemoryRequest},
				{Key: "Memory Limit", Value: quota.MemoryLimit},
				{Key: "Storage Limit", Value: quota.StorageLimit},
				{Key: "Pod Limit", Value: strconv.Itoa(quota.PodLimit)},
			})
		}
	},
}

var clusterQuotaDeleteCmd = &cobra.Command{
	Use:   "delete <cluster-id>",
	Short: "Delete the resource-quota config for a cluster (admin only)",
	Long: `Remove the resource-quota config so the backend no longer applies limits to stack-* namespaces on this cluster.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl cluster quota delete 1
  stackctl cluster quota delete 1 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will remove the resource-quota config for cluster %s. Continue? (y/n): ",
			passthroughID,
			func(c *client.Client, id string) error { return c.DeleteClusterQuota(id) },
			"Deleted resource-quota config for cluster %s",
		)
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

	// cluster create flags
	clusterCreateCmd.Flags().String("from-file", "", "JSON or YAML file with cluster registration payload")
	clusterCreateCmd.Flags().String("name", "", "Cluster name")
	clusterCreateCmd.Flags().String("description", "", "Cluster description")
	clusterCreateCmd.Flags().String("kubeconfig-data", "", "Raw kubeconfig data (base64 or YAML string)")
	clusterCreateCmd.Flags().String("kubeconfig-path", "", "Path to kubeconfig file")

	// cluster update flags
	clusterUpdateCmd.Flags().String("from-file", "", "JSON or YAML file with update payload")
	clusterUpdateCmd.Flags().String("name", "", "New cluster name")
	clusterUpdateCmd.Flags().String("description", "", "New cluster description")
	clusterUpdateCmd.Flags().String("kubeconfig-data", "", "New kubeconfig data")
	clusterUpdateCmd.Flags().String("kubeconfig-path", "", "New path to kubeconfig file")
	clusterUpdateCmd.Flags().Bool("default", false, "Mark cluster as default")

	// cluster delete flags
	clusterDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	clusterDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// Wire up shared-values subcommands
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesListCmd)
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesSetCmd)
	clusterSharedValuesCmd.AddCommand(clusterSharedValuesDeleteCmd)

	// cluster quota set flags
	clusterQuotaSetCmd.Flags().String("from-file", "", "JSON or YAML file with the SetClusterQuotaRequest payload")
	clusterQuotaSetCmd.Flags().String("cpu-request", "", "CPU request limit (Kubernetes resource notation, e.g. \"500m\")")
	clusterQuotaSetCmd.Flags().String("cpu-limit", "", "CPU limit")
	clusterQuotaSetCmd.Flags().String("memory-request", "", "Memory request limit (e.g. \"2Gi\")")
	clusterQuotaSetCmd.Flags().String("memory-limit", "", "Memory limit")
	clusterQuotaSetCmd.Flags().String("storage-limit", "", "Storage limit")
	clusterQuotaSetCmd.Flags().Int("pod-limit", 0, "Pod-count limit (0 = no limit)")

	// cluster quota delete flags
	clusterQuotaDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	clusterQuotaDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// Wire up quota subcommands
	clusterQuotaCmd.AddCommand(clusterQuotaGetCmd)
	clusterQuotaCmd.AddCommand(clusterQuotaSetCmd)
	clusterQuotaCmd.AddCommand(clusterQuotaDeleteCmd)

	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterSharedValuesCmd)
	clusterCmd.AddCommand(clusterCreateCmd)
	clusterCmd.AddCommand(clusterUpdateCmd)
	clusterCmd.AddCommand(clusterDeleteCmd)
	clusterCmd.AddCommand(clusterSetDefaultCmd)
	clusterCmd.AddCommand(clusterTestConnectionCmd)
	clusterCmd.AddCommand(clusterHealthCmd)
	clusterCmd.AddCommand(clusterNodesCmd)
	clusterCmd.AddCommand(clusterNamespacesCmd)
	clusterCmd.AddCommand(clusterUtilizationCmd)
	clusterCmd.AddCommand(clusterQuotaCmd)
	rootCmd.AddCommand(clusterCmd)
}
