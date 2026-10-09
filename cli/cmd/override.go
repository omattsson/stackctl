package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Use:   "list <name|id>",
	Short: "List value overrides for a stack instance",
	Long: `List all value overrides for a stack instance.

Examples:
  stackctl override list my-stack
  stackctl override list my-stack -o json
  stackctl override list my-stack -q`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		overrides, err := c.ListValueOverrides(instanceID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, o := range overrides {
				fmt.Fprintln(printer.Writer, o.ChartConfigID)
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
				if strings.TrimSpace(o.Values) != "" {
					hasValues = "true"
				}
				rows[i] = []string{
					o.ChartConfigID,
					o.StackInstanceID,
					hasValues,
					o.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var overrideGetCmd = &cobra.Command{
	Use:   "get <name|id> <chart>",
	Short: "Show the value override of a chart",
	Long: `Show the value override of one chart in a stack instance.

<chart> is a chart name or a chart ID of the stack's definition.

Examples:
  stackctl override get my-stack my-chart
  stackctl override get my-stack 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		chartID, err := resolveChartID(c, instanceID, args[1])
		if err != nil {
			return err
		}

		override, err := c.GetValueOverride(instanceID, chartID)
		if err != nil {
			return overrideError(err, "value", args[1], args[0])
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, override.ChartConfigID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(override)
		case output.FormatYAML:
			return printer.PrintYAML(override)
		default:
			fields := []output.KeyValue{
				{Key: "Chart ID", Value: override.ChartConfigID},
				{Key: "Instance ID", Value: override.StackInstanceID},
				{Key: "Updated At", Value: override.UpdatedAt.Format(time.RFC3339)},
			}
			if err := printer.PrintSingle(override, fields); err != nil {
				return err
			}
			printer.PrintMessage("Values:")
			return writeYAMLDoc([]byte(override.Values))
		}
	},
}

var overrideSetCmd = &cobra.Command{
	Use:   "set <name|id> <chart>",
	Short: "Set value overrides for a chart",
	Long: `Set the value override of one chart in a stack instance.

<chart> is a chart name or a chart ID of the stack's definition.

--set key=value (repeatable) changes one key and keeps the other keys of
the current override, like helm --set. Use dots for nested keys
(image.tag=v2); escape a dot in a key with a backslash
(podAnnotations.prometheus\.io/scrape=true). Values are parsed like helm
--set: true/false/null (any case) become booleans and null, integers that
do not start with "0" become numbers ("+1" and "-01" too), and everything
else ("1.10", "0123", "1e3") stays a string.

Differences from helm --set: one key per --set (no a=1,b=2; a comma is
part of the value), no {a,b} list syntax, and no list indexes (a[0]=x).
Use --file for lists.

--file (JSON or YAML) replaces the whole override with the file content.
--set keys given together with --file apply on top of the file.

--replace starts from an empty override: the override becomes exactly the
--set keys (and the --file content, if given).

The command prints the keys that change. Key order and comments of the
current override are not kept. An empty result (for example an empty
--file) removes the override; the command asks for confirmation first
unless --yes is given. To remove single keys, use "stackctl override unset".

Examples:
  stackctl override set my-stack my-chart --set replicas=3 --set image.tag=v2
  stackctl override set my-stack 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --set replicas=3
  stackctl override set my-stack my-chart --file values.yaml
  stackctl override set my-stack my-chart --file values.json --set replicas=5
  stackctl override set my-stack my-chart --replace --set replicas=1`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		setFlags, _ := cmd.Flags().GetStringArray("set")
		replace, _ := cmd.Flags().GetBool("replace")

		if file == "" && len(setFlags) == 0 {
			return fmt.Errorf("at least one of --file or --set is required")
		}

		var fileValues map[string]interface{}
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
			fileValues, err = parseValuesDocument(data)
			if err != nil {
				return fmt.Errorf("invalid JSON/YAML in file %s: %w", file, err)
			}
		}

		sets, err := parseSetFlags(setFlags)
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
		chartID, err := resolveChartID(c, instanceID, args[1])
		if err != nil {
			return err
		}

		existing, current, err := currentOverrideValues(c, instanceID, chartID, args[1], args[0])
		if err != nil {
			return err
		}

		var values map[string]interface{}
		switch {
		case fileValues != nil:
			values = fileValues
		case replace:
			values = map[string]interface{}{}
		default:
			values = deepCopyMap(current)
		}
		for _, kv := range sets {
			setNestedPath(values, kv.path, kv.value)
		}

		if len(values) == 0 && existing != nil {
			confirmed, err := confirmAction(cmd, fmt.Sprintf("The new values are empty. This will remove the value override of chart %s on instance %s. Continue? (y/n): ", args[1], instanceID))
			if err != nil {
				return err
			}
			if !confirmed {
				printer.PrintMessage(msgAborted)
				return nil
			}
		}

		return writeValueOverride(c, instanceID, chartID, args[1], existing, current, values)
	},
}

var overrideUnsetCmd = &cobra.Command{
	Use:   "unset <name|id> <chart> <key>...",
	Short: "Remove keys from the value override of a chart",
	Long: `Remove one or more keys from the value override of one chart.

<chart> is a chart name or a chart ID of the stack's definition. Use dots
for nested keys (image.tag) and a backslash to escape a dot in a key
(podAnnotations.prometheus\.io/scrape). The other keys stay. When no key
is left, the override is removed.

Examples:
  stackctl override unset my-stack my-chart replicas
  stackctl override unset my-stack my-chart image.tag resources.limits.memory`,
	Args:         cobra.MinimumNArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		chartID, err := resolveChartID(c, instanceID, args[1])
		if err != nil {
			return err
		}

		override, err := c.GetValueOverride(instanceID, chartID)
		if err != nil {
			return overrideError(err, "value", args[1], args[0])
		}
		current, err := parseOverrideValues(override.Values)
		if err != nil {
			return err
		}

		paths := make([][]string, len(args)-2)
		for i, key := range args[2:] {
			if paths[i], err = splitKeyPath(key); err != nil {
				return err
			}
		}

		values := deepCopyMap(current)
		for i, key := range args[2:] {
			if !unsetNestedPath(values, paths[i]) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: key %q is not set in the override\n", key)
			}
		}

		return writeValueOverride(c, instanceID, chartID, args[1], override, current, values)
	},
}

// currentOverrideValues returns the value override of a chart and its
// parsed values. Without an override it returns nil and an empty map.
func currentOverrideValues(c *client.Client, instanceID, chartID, chartArg, stackArg string) (*types.ValueOverride, map[string]interface{}, error) {
	override, err := c.GetValueOverride(instanceID, chartID)
	if err != nil {
		if isAPINotFound(err, "override not found") {
			return nil, map[string]interface{}{}, nil
		}
		return nil, nil, overrideError(err, "value", chartArg, stackArg)
	}
	values, err := parseOverrideValues(override.Values)
	return override, values, err
}

// parseOverrideValues parses the YAML of a value override into a map.
func parseOverrideValues(raw string) (map[string]interface{}, error) {
	values, err := parseValuesDocument([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("the current override is not a YAML mapping (use --replace or --file): %w", err)
	}
	return values, nil
}

// writeValueOverride prints the keys that change from old to values and
// writes values as the override of the chart. It skips the API call when
// nothing changes. An empty map removes the override. existing is the
// current override (nil if none); -o json/yaml print it when nothing changes.
func writeValueOverride(c *client.Client, instanceID, chartID, chartArg string, existing *types.ValueOverride, old, values map[string]interface{}) error {
	changes := diffValueKeys(old, values)

	if len(changes) == 0 {
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, chartID)
			return nil
		}
		if printer.Format == output.FormatTable {
			printer.PrintMessage("No changes to the value override of chart %s on instance %s", chartArg, instanceID)
			return nil
		}
	}

	var req types.SetValueOverrideRequest
	if len(values) > 0 {
		yamlBytes, err := yaml.Marshal(values)
		if err != nil {
			return fmt.Errorf("serializing values to YAML: %w", err)
		}
		req.Values = string(yamlBytes)
	}

	override := &types.ValueOverride{StackInstanceID: instanceID, ChartConfigID: chartID, Values: req.Values}
	if len(changes) == 0 && existing != nil {
		override = existing
	}
	if len(changes) > 0 {
		saved, err := c.SetValueOverride(instanceID, chartID, &req)
		if err != nil {
			return overrideError(err, "value", chartArg, instanceID)
		}
		if saved != nil && saved.ID != "" {
			override = saved
		}
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, chartID)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(override)
	case output.FormatYAML:
		return printer.PrintYAML(override)
	default:
		if len(values) == 0 {
			printer.PrintMessage("Removed the value override of chart %s on instance %s (no keys left)", chartArg, instanceID)
		} else {
			printer.PrintMessage("Set value override for chart %s on instance %s", chartArg, instanceID)
		}
		for _, ch := range changes {
			printer.PrintMessage("  %s", ch)
		}
		return nil
	}
}

// diffValueKeys returns one line per leaf key that is added, changed or
// removed between old and new, sorted by key. Only key names are shown,
// not values (values can be secrets).
func diffValueKeys(oldValues, newValues map[string]interface{}) []string {
	oldFlat := map[string]interface{}{}
	newFlat := map[string]interface{}{}
	flattenValues(oldValues, "", oldFlat)
	flattenValues(newValues, "", newFlat)

	keys := make([]string, 0, len(oldFlat)+len(newFlat))
	seen := map[string]bool{}
	for k := range oldFlat {
		keys = append(keys, k)
		seen[k] = true
	}
	for k := range newFlat {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		ov, inOld := oldFlat[k]
		nv, inNew := newFlat[k]
		switch {
		case inOld && !inNew:
			lines = append(lines, "- "+k+" (removed)")
		case !inOld && inNew:
			lines = append(lines, "+ "+k+" (added)")
		case !sameYAMLValue(ov, nv):
			lines = append(lines, "~ "+k+" (changed)")
		}
	}
	return lines
}

// flattenValues maps every leaf of a nested map to its dot path. Lists and
// empty maps are leaves.
func flattenValues(m map[string]interface{}, prefix string, out map[string]interface{}) {
	for k, v := range m {
		key := strings.ReplaceAll(k, ".", `\.`)
		if prefix != "" {
			key = prefix + "." + key
		}
		if sub, ok := v.(map[string]interface{}); ok && len(sub) > 0 {
			flattenValues(sub, key, out)
			continue
		}
		out[key] = v
	}
}

// sameYAMLValue compares two values by their YAML form, so int and int64
// (YAML decode vs. --set parse) compare equal.
func sameYAMLValue(a, b interface{}) bool {
	ya, errA := yaml.Marshal(a)
	yb, errB := yaml.Marshal(b)
	return errA == nil && errB == nil && string(ya) == string(yb)
}

// deepCopyMap copies a nested map so the original stays unchanged.
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]interface{}); ok {
			out[k] = deepCopyMap(sub)
			continue
		}
		out[k] = v
	}
	return out
}

// isAPINotFound reports whether err is an API 404 whose server message
// contains substr (case-insensitive).
func isAPINotFound(err error, substr string) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(apiErr.Message), strings.ToLower(substr))
}

// overrideError turns the 404 responses of the per-chart override routes
// into clear messages: an unknown chart, or a chart without an override.
func overrideError(err error, kind, chart, stack string) error {
	switch {
	case isAPINotFound(err, "chart not found"):
		return fmt.Errorf("chart %s is not part of the definition of stack %s", chart, stack)
	case isAPINotFound(err, "override not found"):
		return fmt.Errorf("chart %s on stack %s has no %s override", chart, stack, kind)
	default:
		return err
	}
}

var overrideDeleteCmd = &cobra.Command{
	Use:   "delete <name|id> <chart>",
	Short: "Delete a value override",
	Long: `Delete the value override of one chart in a stack instance.

<chart> is a chart name or a chart ID of the stack's definition.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override delete my-stack my-chart
  stackctl override delete my-stack 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --yes`,
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
	Use:   "list <name|id>",
	Short: "List branch overrides for a stack instance",
	Long: `List all branch overrides for a stack instance.

Examples:
  stackctl override branch list my-stack
  stackctl override branch list my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		overrides, err := c.ListBranchOverrides(instanceID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, o := range overrides {
				fmt.Fprintln(printer.Writer, o.ChartConfigID)
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
					o.ChartConfigID,
					o.StackInstanceID,
					o.Branch,
					o.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var overrideBranchSetCmd = &cobra.Command{
	Use:   "set <name|id> <chart> <branch>",
	Short: "Set a branch override for a chart",
	Long: `Set a branch override for one chart in a stack instance.

<chart> is a chart name or a chart ID of the stack's definition.

Examples:
  stackctl override branch set my-stack my-chart feature/my-branch
  stackctl override branch set my-stack 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b main -o json`,
	Args:         cobra.ExactArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[2]

		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		chartID, err := resolveChartID(c, instanceID, args[1])
		if err != nil {
			return err
		}

		override, err := c.SetBranchOverride(instanceID, chartID, &types.SetBranchOverrideRequest{
			Branch: branch,
		})
		if err != nil {
			return overrideError(err, "branch", args[1], args[0])
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, override.ChartConfigID)
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
	Use:   "delete <name|id> <chart>",
	Short: "Delete a branch override",
	Long: `Delete the branch override of one chart in a stack instance.

<chart> is a chart name or a chart ID of the stack's definition.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override branch delete my-stack my-chart
  stackctl override branch delete my-stack 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b --yes`,
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
	Use:   "get <name|id>",
	Short: "Get quota override for a stack instance",
	Long: `Get the resource quota override for a stack instance.

Examples:
  stackctl override quota get my-stack
  stackctl override quota get my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
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
				{Key: "Instance ID", Value: quota.StackInstanceID},
				{Key: "CPU Request", Value: quota.CPURequest},
				{Key: "CPU Limit", Value: quota.CPULimit},
				{Key: "Memory Request", Value: quota.MemRequest},
				{Key: "Memory Limit", Value: quota.MemLimit},
				{Key: "Storage Limit", Value: quota.StorageLimit},
				{Key: "Pod Limit", Value: formatOptionalInt(quota.PodLimit)},
			}
			return printer.PrintSingle(quota, fields)
		}
	},
}

var overrideQuotaSetCmd = &cobra.Command{
	Use:   "set <name|id>",
	Short: "Set quota override for a stack instance",
	Long: `Set the resource quota override for a stack instance.

Specify at least one quota flag. By default the command reads the current
override and changes only the given fields; the other fields keep their
values. An empty value (for example --cpu-limit "") clears a CPU, memory
or storage field. --replace starts from an empty override: the override
becomes exactly the given fields. The API (PUT) replaces the whole
override, so a field that is not sent is cleared. The command refuses an
empty result; use "stackctl override quota delete" to remove the override.

--pod-limit 0 means no pod limit. An empty value cannot clear pod_limit;
use --replace without --pod-limit, or "stackctl override quota delete".

The read and the write are two requests. A change that another user (for
example an admin) makes between them is overwritten. Run "stackctl
override quota get" afterwards when other users change the same override.

Who can set what:
  - The stack owner, admin and devops can set a quota override.
  - A user without the admin or devops role cannot set a value above the
    cluster quota of the stack's cluster. The server then returns 403
    (k8s-stack-manager v0.7.0 or later). --pod-limit 0 (no limit) counts
    as above a cluster pod limit.
  - A value equal to the stored value is allowed. An owner can change one
    field and keep a higher value that an admin set on another field.
  - Admin and devops can set values above the cluster quota.

Examples:
  stackctl override quota set my-stack --cpu-request 100m --cpu-limit 500m
  stackctl override quota set my-stack --memory-request 128Mi --memory-limit 512Mi
  stackctl override quota set my-stack --storage-limit 10Gi --pod-limit 20
  stackctl override quota set my-stack --cpu-limit ""
  stackctl override quota set my-stack --replace --memory-limit 1Gi`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		replace, _ := flags.GetBool("replace")
		var podLimit *int
		if flags.Changed("pod-limit") {
			v, _ := flags.GetInt("pod-limit")
			if v < 0 {
				return fmt.Errorf("--pod-limit must not be negative")
			}
			podLimit = &v
		}

		changed := podLimit != nil
		for _, name := range quotaStringFlags {
			if flags.Changed(name) {
				changed = true
			}
		}
		if !changed {
			return fmt.Errorf("at least one of --cpu-request, --cpu-limit, --memory-request, --memory-limit, --storage-limit, or --pod-limit is required")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		req := &types.SetQuotaOverrideRequest{}
		if !replace {
			current, err := c.GetQuotaOverride(instanceID)
			switch {
			case err == nil:
				req = quotaRequestFrom(current)
			case isAPINotFound(err, "quota override not found"):
				// No override yet: the server answers 404 "Instance quota
				// override not found" (mapError with
				// entityInstanceQuotaOverride). Start from an empty
				// override. Other 404s (for example a missing stack) are
				// errors.
			default:
				return err
			}
		}
		applyQuotaFlags(cmd, req, podLimit)
		if *req == (types.SetQuotaOverrideRequest{}) {
			return fmt.Errorf("the quota override would be empty; to remove it, run: stackctl override quota delete %s", args[0])
		}

		quota, err := c.SetQuotaOverride(instanceID, req)
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

// quotaStringFlags are the string flags of "override quota set".
var quotaStringFlags = []string{"cpu-request", "cpu-limit", "memory-request", "memory-limit", "storage-limit"}

// quotaRequestFrom returns a request that keeps every field of the stored
// override q.
func quotaRequestFrom(q *types.QuotaOverride) *types.SetQuotaOverrideRequest {
	req := &types.SetQuotaOverrideRequest{
		CPURequest:   q.CPURequest,
		CPULimit:     q.CPULimit,
		MemRequest:   q.MemRequest,
		MemLimit:     q.MemLimit,
		StorageLimit: q.StorageLimit,
	}
	if q.PodLimit != nil {
		v := *q.PodLimit
		req.PodLimit = &v
	}
	return req
}

// applyQuotaFlags sets the fields of req whose flags are given. An empty
// string flag clears the field.
func applyQuotaFlags(cmd *cobra.Command, req *types.SetQuotaOverrideRequest, podLimit *int) {
	targets := map[string]*string{
		"cpu-request":    &req.CPURequest,
		"cpu-limit":      &req.CPULimit,
		"memory-request": &req.MemRequest,
		"memory-limit":   &req.MemLimit,
		"storage-limit":  &req.StorageLimit,
	}
	for _, name := range quotaStringFlags {
		if cmd.Flags().Changed(name) {
			v, _ := cmd.Flags().GetString(name)
			*targets[name] = strings.TrimSpace(v)
		}
	}
	if podLimit != nil {
		req.PodLimit = podLimit
	}
}

var overrideQuotaDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete quota override for a stack instance",
	Long: `Delete the resource quota override for a stack instance.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl override quota delete my-stack
  stackctl override quota delete my-stack --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if isDryRun(cmd, "Would delete quota override for instance %s", args[0]) {
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		instanceID, err := resolveStackID(c, args[0])
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
	if isDryRun(cmd, "Would delete %s override for chart %s on instance %s", kind, args[1], args[0]) {
		return nil
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	instanceID, err := resolveStackID(c, args[0])
	if err != nil {
		return err
	}
	chartID, err := resolveChartID(c, instanceID, args[1])
	if err != nil {
		return err
	}

	confirmed, err := confirmAction(cmd, fmt.Sprintf("This will delete the %s override for chart %s on instance %s. Continue? (y/n): ", kind, args[1], instanceID))
	if err != nil {
		return err
	}
	if !confirmed {
		printer.PrintMessage(msgAborted)
		return nil
	}

	if err := deleteFn(c, instanceID, chartID); err != nil {
		return overrideError(err, kind, args[1], args[0])
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, chartID)
		return nil
	}

	printer.PrintMessage("Deleted %s override for chart %s on instance %s", kind, args[1], instanceID)
	return nil
}

// formatOptionalInt formats an optional integer, or "-" when it is nil.
func formatOptionalInt(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

func init() {
	// override set flags
	overrideSetCmd.Flags().String("file", "", "JSON or YAML file with values")
	overrideSetCmd.Flags().StringArray("set", nil, "Set a value (key=value, dots for nested keys, \\. for a literal dot), repeatable; keeps the other keys")
	overrideSetCmd.Flags().Bool("replace", false, "Replace the whole override instead of merging --set into it")
	overrideSetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation when the new values are empty (the override is removed)")

	// override delete flags
	overrideDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	overrideDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// branch delete flags
	overrideBranchDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	overrideBranchDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// quota set flags
	overrideQuotaSetCmd.Flags().String("cpu-request", "", "CPU request (e.g. 100m)")
	overrideQuotaSetCmd.Flags().String("cpu-limit", "", "CPU limit (e.g. 500m)")
	overrideQuotaSetCmd.Flags().String("memory-request", "", "Memory request (e.g. 128Mi)")
	overrideQuotaSetCmd.Flags().String("memory-limit", "", "Memory limit (e.g. 512Mi)")
	overrideQuotaSetCmd.Flags().String("storage-limit", "", "Storage limit (e.g. 10Gi)")
	overrideQuotaSetCmd.Flags().Int("pod-limit", 0, "Maximum number of pods")
	overrideQuotaSetCmd.Flags().Bool("replace", false, "Replace the whole override instead of merging the given fields into it")

	// quota delete flags
	overrideQuotaDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	overrideQuotaDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

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
	overrideCmd.AddCommand(overrideGetCmd)
	overrideCmd.AddCommand(overrideSetCmd)
	overrideCmd.AddCommand(overrideUnsetCmd)
	overrideCmd.AddCommand(overrideDeleteCmd)
	overrideCmd.AddCommand(overrideBranchCmd)
	overrideCmd.AddCommand(overrideQuotaCmd)
	rootCmd.AddCommand(overrideCmd)
}
