package cmd

import (
	"encoding/json"
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

const (
	msgAborted          = "Aborted."
	flagDescSkipConfirm = "Skip confirmation prompt"
)

var overrideCmd = &cobra.Command{
	Use:   "override",
	Short: "Manage value, branch, and quota overrides",
	Long:  "Manage per-chart value overrides, branch overrides, and quota overrides for stack instances.",
}

// --- Value Overrides ---

var overrideListCmd = &cobra.Command{
	Use:   "list <instance-id>",
	Short: "List value overrides for a stack instance",
	Long: `List all value overrides for a stack instance.

Examples:
  stackctl override list 42
  stackctl override list 42 -o json
  stackctl override list 42 -q`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		overrides, err := c.ListValueOverrides(instanceID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, o := range overrides {
				fmt.Fprintln(printer.Writer, o.ChartID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(overrides)
		case output.FormatYAML:
			return printer.PrintYAML(overrides)
		default:
			headers := []string{"CHART ID", "INSTANCE ID", "HAS VALUES", "UPDATED AT"}
			rows := make([][]string, len(overrides))
			for i, o := range overrides {
				hasValues := "false"
				if o.Values != "" {
					hasValues = "true"
				}
				rows[i] = []string{
					o.ChartID,
					o.InstanceID,
					hasValues,
					o.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var overrideSetCmd = &cobra.Command{
	Use:   "set <instance-id> <chart-id>",
	Short: "Set value overrides for a chart",
	Long: `Set value overrides for a specific chart in a stack instance.

Provide values via --file (JSON or YAML file) or --set key=value (repeatable).
At least one of --file or --set is required.

Examples:
  stackctl override set 42 1 --file values.json
  stackctl override set 42 1 --file values.yaml
  stackctl override set 42 1 --set replicas=3 --set image.tag=v2
  stackctl override set 42 1 --file values.json --set replicas=5`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}
		chartID, err := parseID(args[1])
		if err != nil {
			return err
		}

		file, _ := cmd.Flags().GetString("file")
		setFlags, _ := cmd.Flags().GetStringSlice("set")

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
			// Try JSON first, then YAML
			if err := json.Unmarshal(data, &values); err != nil {
				// Try YAML
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

		c, err := newClient()
		if err != nil {
			return err
		}

		override, err := c.SetValueOverride(instanceID, chartID, &types.SetValueOverrideRequest{
			Values: values,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, override.ChartID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(override)
		case output.FormatYAML:
			return printer.PrintYAML(override)
		default:
			printer.PrintMessage("Set value override for chart %s on instance %s", chartID, instanceID)
			return nil
		}
	},
}

var overrideDeleteCmd = &cobra.Command{
	Use:   "delete <instance-id> <chart-id>",
	Short: "Delete a value override",
	Long: `Delete a value override for a specific chart in a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override delete 42 1
  stackctl override delete 42 1 --yes`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteChartOverride(cmd, args, "value", func(c *client.Client, instanceID, chartID string) error {
			return c.DeleteValueOverride(instanceID, chartID)
		})
	},
}

// --- Branch Overrides ---

var overrideBranchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Manage branch overrides",
	Long:  "Manage per-chart branch overrides for stack instances.",
}

var overrideBranchListCmd = &cobra.Command{
	Use:   "list <instance-id>",
	Short: "List branch overrides for a stack instance",
	Long: `List all branch overrides for a stack instance.

Examples:
  stackctl override branch list 42
  stackctl override branch list 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		overrides, err := c.ListBranchOverrides(instanceID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, o := range overrides {
				fmt.Fprintln(printer.Writer, o.ChartID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(overrides)
		case output.FormatYAML:
			return printer.PrintYAML(overrides)
		default:
			headers := []string{"CHART ID", "INSTANCE ID", "BRANCH", "UPDATED AT"}
			rows := make([][]string, len(overrides))
			for i, o := range overrides {
				rows[i] = []string{
					o.ChartID,
					o.InstanceID,
					o.Branch,
					o.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var overrideBranchSetCmd = &cobra.Command{
	Use:   "set <instance-id> <chart-id> <branch>",
	Short: "Set a branch override for a chart",
	Long: `Set a branch override for a specific chart in a stack instance.

Examples:
  stackctl override branch set 42 1 feature/my-branch
  stackctl override branch set 42 1 main -o json`,
	Args:         cobra.ExactArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}
		chartID, err := parseID(args[1])
		if err != nil {
			return err
		}
		branch := args[2]

		c, err := newClient()
		if err != nil {
			return err
		}

		override, err := c.SetBranchOverride(instanceID, chartID, &types.SetBranchOverrideRequest{
			Branch: branch,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, override.ChartID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(override)
		case output.FormatYAML:
			return printer.PrintYAML(override)
		default:
			printer.PrintMessage("Set branch override %q for chart %s on instance %s", branch, chartID, instanceID)
			return nil
		}
	},
}

var overrideBranchDeleteCmd = &cobra.Command{
	Use:   "delete <instance-id> <chart-id>",
	Short: "Delete a branch override",
	Long: `Delete a branch override for a specific chart in a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override branch delete 42 1
  stackctl override branch delete 42 1 --yes`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteChartOverride(cmd, args, "branch", func(c *client.Client, instanceID, chartID string) error {
			return c.DeleteBranchOverride(instanceID, chartID)
		})
	},
}

// --- Quota Overrides ---

var overrideQuotaCmd = &cobra.Command{
	Use:   "quota",
	Short: "Manage quota overrides",
	Long:  "Manage per-instance resource quota overrides.",
}

var overrideQuotaGetCmd = &cobra.Command{
	Use:   "get <instance-id>",
	Short: "Get quota override for a stack instance",
	Long: `Get the resource quota override for a stack instance.

Examples:
  stackctl override quota get 42
  stackctl override quota get 42 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		quota, err := c.GetQuotaOverride(instanceID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, instanceID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(quota)
		case output.FormatYAML:
			return printer.PrintYAML(quota)
		default:
			fields := []output.KeyValue{
				{Key: "Instance ID", Value: quota.InstanceID},
				{Key: "CPU Request", Value: quota.CPURequest},
				{Key: "CPU Limit", Value: quota.CPULimit},
				{Key: "Memory Request", Value: quota.MemRequest},
				{Key: "Memory Limit", Value: quota.MemLimit},
			}
			return printer.PrintSingle(quota, fields)
		}
	},
}

var overrideQuotaSetCmd = &cobra.Command{
	Use:   "set <instance-id>",
	Short: "Set quota override for a stack instance",
	Long: `Set resource quota overrides for a stack instance.

At least one of the quota flags must be specified.

Examples:
  stackctl override quota set 42 --cpu-request 100m --cpu-limit 500m
  stackctl override quota set 42 --memory-request 128Mi --memory-limit 512Mi
  stackctl override quota set 42 --cpu-request 200m --memory-limit 1Gi`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}

		cpuReq, _ := cmd.Flags().GetString("cpu-request")
		cpuLim, _ := cmd.Flags().GetString("cpu-limit")
		memReq, _ := cmd.Flags().GetString("memory-request")
		memLim, _ := cmd.Flags().GetString("memory-limit")

		if cpuReq == "" && cpuLim == "" && memReq == "" && memLim == "" {
			return fmt.Errorf("at least one of --cpu-request, --cpu-limit, --memory-request, or --memory-limit is required")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		quota, err := c.SetQuotaOverride(instanceID, &types.SetQuotaOverrideRequest{
			CPURequest: cpuReq,
			CPULimit:   cpuLim,
			MemRequest: memReq,
			MemLimit:   memLim,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, instanceID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(quota)
		case output.FormatYAML:
			return printer.PrintYAML(quota)
		default:
			printer.PrintMessage("Set quota override for instance %s", instanceID)
			return nil
		}
	},
}

var overrideQuotaDeleteCmd = &cobra.Command{
	Use:   "delete <instance-id>",
	Short: "Delete quota override for a stack instance",
	Long: `Delete the resource quota override for a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override quota delete 42
  stackctl override quota delete 42 --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceID, err := parseID(args[0])
		if err != nil {
			return err
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will delete the quota override for instance %s. Continue? (y/n): ", instanceID))
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

		if err := c.DeleteQuotaOverride(instanceID); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, instanceID)
			return nil
		}

		printer.PrintMessage("Deleted quota override for instance %s", instanceID)
		return nil
	},
}

func deleteChartOverride(cmd *cobra.Command, args []string, kind string, deleteFn func(*client.Client, string, string) error) error {
	instanceID, err := parseID(args[0])
	if err != nil {
		return err
	}
	chartID, err := parseID(args[1])
	if err != nil {
		return err
	}

	confirmed, err := confirmAction(cmd, fmt.Sprintf("This will delete the %s override for chart %s on instance %s. Continue? (y/n): ", kind, chartID, instanceID))
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

	if err := deleteFn(c, instanceID, chartID); err != nil {
		return err
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, chartID)
		return nil
	}

	printer.PrintMessage("Deleted %s override for chart %s on instance %s", kind, chartID, instanceID)
	return nil
}

// parseScalarValue converts a string value to the appropriate Go type.
func parseScalarValue(s string) interface{} {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if s == "null" || s == "" {
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// setNestedValue sets a value in a nested map using a dot-separated key path.
func setNestedValue(m map[string]interface{}, key string, value interface{}) {
	parts := strings.Split(key, ".")
	for i, part := range parts {
		if i == len(parts)-1 {
			m[part] = value
			return
		}
		next, ok := m[part]
		if !ok {
			next = map[string]interface{}{}
			m[part] = next
		}
		nextMap, ok := next.(map[string]interface{})
		if !ok {
			nextMap = map[string]interface{}{}
			m[part] = nextMap
		}
		m = nextMap
	}
}

func init() {
	// override set flags
	overrideSetCmd.Flags().String("file", "", "JSON or YAML file with values")
	overrideSetCmd.Flags().StringSlice("set", nil, "Set a value (key=value), repeatable")

	// override delete flags
	overrideDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)

	// branch delete flags
	overrideBranchDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)

	// quota set flags
	overrideQuotaSetCmd.Flags().String("cpu-request", "", "CPU request (e.g. 100m)")
	overrideQuotaSetCmd.Flags().String("cpu-limit", "", "CPU limit (e.g. 500m)")
	overrideQuotaSetCmd.Flags().String("memory-request", "", "Memory request (e.g. 128Mi)")
	overrideQuotaSetCmd.Flags().String("memory-limit", "", "Memory limit (e.g. 512Mi)")

	// quota delete flags
	overrideQuotaDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)

	// Wire up branch subcommands
	overrideBranchCmd.AddCommand(overrideBranchListCmd)
	overrideBranchCmd.AddCommand(overrideBranchSetCmd)
	overrideBranchCmd.AddCommand(overrideBranchDeleteCmd)

	// Wire up quota subcommands
	overrideQuotaCmd.AddCommand(overrideQuotaGetCmd)
	overrideQuotaCmd.AddCommand(overrideQuotaSetCmd)
	overrideQuotaCmd.AddCommand(overrideQuotaDeleteCmd)

	// Wire up override subcommands
	overrideCmd.AddCommand(overrideListCmd)
	overrideCmd.AddCommand(overrideSetCmd)
	overrideCmd.AddCommand(overrideDeleteCmd)
	overrideCmd.AddCommand(overrideBranchCmd)
	overrideCmd.AddCommand(overrideQuotaCmd)
	rootCmd.AddCommand(overrideCmd)
}
