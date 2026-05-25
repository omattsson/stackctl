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

var cleanupPolicyCmd = &cobra.Command{
	Use:   "cleanup-policy",
	Short: "Manage automated cleanup policies (admin only)",
	Long: `Manage cleanup policies that the scheduler applies to stack instances.

A policy combines an Action (stop/clean/delete), a Condition (e.g. idle_days:7),
a Schedule (cron expression), and a ClusterID ("all" for every cluster).
Policies are evaluated by the backend scheduler on the configured cadence.

All cleanup-policy commands require admin role.`,
}

var cleanupPolicyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cleanup policies",
	Long: `List every cleanup policy configured on the server.

Examples:
  stackctl cleanup-policy list
  stackctl cleanup-policy list -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		policies, err := c.ListCleanupPolicies()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, p := range policies {
				fmt.Fprintln(printer.Writer, p.ID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(policies)
		case output.FormatYAML:
			return printer.PrintYAML(policies)
		default:
			if len(policies) == 0 {
				printer.PrintMessage("No cleanup policies found.")
				return nil
			}
			headers := []string{"ID", "NAME", "CLUSTER", "ACTION", "CONDITION", "SCHEDULE", "ENABLED"}
			rows := make([][]string, len(policies))
			for i, p := range policies {
				rows[i] = []string{
					p.ID,
					p.Name,
					p.ClusterID,
					p.Action,
					p.Condition,
					p.Schedule,
					strconv.FormatBool(p.Enabled),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var cleanupPolicyGetCmd = &cobra.Command{
	Use:   "get <id-or-name>",
	Short: "Show a single cleanup policy",
	Long: `Show a single cleanup policy by ID or name.

The backend does not expose a GET-by-ID endpoint for cleanup policies, so this
command fetches the full list and filters client-side.

Arguments that look like an integer or a UUID are treated as IDs; rename a
policy if you need name-based access to it.

Examples:
  stackctl cleanup-policy get 1
  stackctl cleanup-policy get nightly-stop
  stackctl cleanup-policy get 1 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		nameOrID := strings.TrimSpace(args[0])
		if nameOrID == "" {
			return fmt.Errorf("policy ID or name must not be empty")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		policy, err := findCleanupPolicy(c, nameOrID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, policy.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(policy)
		case output.FormatYAML:
			return printer.PrintYAML(policy)
		default:
			return printer.PrintSingle(policy, []output.KeyValue{
				{Key: "ID", Value: policy.ID},
				{Key: "Name", Value: policy.Name},
				{Key: "Cluster", Value: policy.ClusterID},
				{Key: "Action", Value: policy.Action},
				{Key: "Condition", Value: policy.Condition},
				{Key: "Schedule", Value: policy.Schedule},
				{Key: "Enabled", Value: strconv.FormatBool(policy.Enabled)},
				{Key: "Dry Run", Value: strconv.FormatBool(policy.DryRun)},
				{Key: "Last Run", Value: formatTime(policy.LastRunAt)},
			})
		}
	},
}

var cleanupPolicyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new cleanup policy (admin only)",
	Long: `Create a new cleanup policy from a JSON or YAML payload.

The file must contain a CreateCleanupPolicyRequest — see the schema in
cli/pkg/types/types.go.

Examples:
  stackctl cleanup-policy create --from-file policy.json
  stackctl cleanup-policy create --from-file policy.yaml -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString(flagFromFile)
		if fromFile == "" {
			return fmt.Errorf("--from-file is required")
		}

		var req types.CreateCleanupPolicyRequest
		if err := readPolicyFile(fromFile, &req); err != nil {
			return err
		}
		if err := validateCleanupPolicyPayload(req.Name, req.ClusterID, req.Action, req.Condition, req.Schedule); err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		policy, err := c.CreateCleanupPolicy(&req)
		if err != nil {
			return err
		}

		return printCleanupPolicy(policy)
	},
}

var cleanupPolicyUpdateCmd = &cobra.Command{
	Use:   "update <id-or-name>",
	Short: "Update an existing cleanup policy (admin only)",
	Long: `Replace an existing cleanup policy with the payload in a JSON or YAML file.

PUT is a full upsert — the file must contain every field. To partially modify
a policy, first 'cleanup-policy get <id> -o json' to a file, edit it, then
'cleanup-policy update <id> --from-file <file>'.

When the argument is a name (not an ID), this command issues a list+filter
round-trip to resolve the ID before sending the PUT. Pass the numeric ID
directly in scripts to skip the extra request.

Examples:
  stackctl cleanup-policy update 1 --from-file policy.json
  stackctl cleanup-policy update nightly-stop --from-file policy.yaml`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString(flagFromFile)
		if fromFile == "" {
			return fmt.Errorf("--from-file is required")
		}

		var req types.UpdateCleanupPolicyRequest
		if err := readPolicyFile(fromFile, &req); err != nil {
			return err
		}
		if err := validateCleanupPolicyPayload(req.Name, req.ClusterID, req.Action, req.Condition, req.Schedule); err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveCleanupPolicyID(c, args[0])
		if err != nil {
			return err
		}

		policy, err := c.UpdateCleanupPolicy(id, &req)
		if err != nil {
			return err
		}

		return printCleanupPolicy(policy)
	},
}

var cleanupPolicyDeleteCmd = &cobra.Command{
	Use:   "delete <id-or-name>",
	Short: "Delete a cleanup policy (admin only)",
	Long: `Delete a cleanup policy.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

When the argument is a name (not an ID), this command issues a list+filter
round-trip to resolve the ID before sending the DELETE.

Examples:
  stackctl cleanup-policy delete 1
  stackctl cleanup-policy delete nightly-stop --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will delete cleanup policy %s. Continue? (y/n): ",
			resolveCleanupPolicyID,
			func(c *client.Client, id string) error { return c.DeleteCleanupPolicy(id) },
			"Deleted cleanup policy %s",
		)
	},
}

var cleanupPolicyRunCmd = &cobra.Command{
	Use:   "run <id-or-name>",
	Short: "Execute a cleanup policy immediately (admin only)",
	Long: `Trigger a one-off run of a cleanup policy, outside its normal schedule.

Use --dry-run to preview the affected stack instances without applying the
policy's action (stop/clean/delete). Without --dry-run the backend applies
the action and returns one result per matched instance.

The command exits non-zero when the backend reports any per-instance error,
even if other instances succeeded — partial failures are surfaced so scripts
can branch on the exit code.

When the argument is a name (not an ID), this command issues a list+filter
round-trip to resolve the ID before sending the run request.

Examples:
  stackctl cleanup-policy run 1 --dry-run
  stackctl cleanup-policy run nightly-stop
  stackctl cleanup-policy run 1 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveCleanupPolicyID(c, args[0])
		if err != nil {
			return err
		}

		results, err := c.RunCleanupPolicy(id, dryRun)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, r := range results {
				fmt.Fprintln(printer.Writer, r.InstanceID)
			}
			return runExitOnPartialFailure(results)
		}

		switch printer.Format {
		case output.FormatJSON:
			if err := printer.PrintJSON(results); err != nil {
				return err
			}
		case output.FormatYAML:
			if err := printer.PrintYAML(results); err != nil {
				return err
			}
		default:
			if len(results) == 0 {
				printer.PrintMessage("No instances matched policy %s.", args[0])
			} else {
				headers := []string{"INSTANCE ID", "INSTANCE", "NAMESPACE", "OWNER", "ACTION", "STATUS", "ERROR"}
				rows := make([][]string, len(results))
				for i, r := range results {
					rows[i] = []string{r.InstanceID, r.InstanceName, r.Namespace, r.OwnerID, r.Action, r.Status, r.Error}
				}
				if err := printer.PrintTable(headers, rows); err != nil {
					return err
				}
			}
			success, errored, dry := cleanupResultCounts(results)
			printer.PrintMessage("Summary: %d success, %d error, %d dry-run.", success, errored, dry)
		}

		return runExitOnPartialFailure(results)
	},
}

// cleanupResultCounts buckets the results by Status. Anything outside the
// three documented statuses ("success", "error", "dry_run") is ignored for
// counting — unknown statuses still appear in the table.
func cleanupResultCounts(results []types.CleanupResult) (success, errored, dryRun int) {
	for _, r := range results {
		switch r.Status {
		case "success":
			success++
		case "error":
			errored++
		case "dry_run":
			dryRun++
		}
	}
	return
}

// runExitOnPartialFailure returns a non-nil error (which Cobra turns into a
// non-zero exit code) when at least one result reports Status == "error".
// SilenceUsage on the command keeps cobra from printing the usage banner.
func runExitOnPartialFailure(results []types.CleanupResult) error {
	n := 0
	for _, r := range results {
		if r.Status == "error" {
			n++
		}
	}
	if n > 0 {
		return fmt.Errorf("cleanup policy reported %d per-instance failure(s); see results above", n)
	}
	return nil
}

// readPolicyFile loads a JSON or YAML file containing a cleanup-policy payload.
// `..` segments in the path are rejected to match the convention used by
// `cluster create`, `cluster update`, `definition create`, etc.
func readPolicyFile(path string, out interface{}) error {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == ".." {
			return errors.New(msgPathTraversal)
		}
	}
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return readFileErr(path, err)
	}
	if jsonErr := json.Unmarshal(data, out); jsonErr != nil {
		if yamlErr := yaml.Unmarshal(data, out); yamlErr != nil {
			return fmt.Errorf("invalid JSON/YAML in file %s (json: %v): %w", path, jsonErr, yamlErr)
		}
	}
	return nil
}

// validateCleanupPolicyPayload checks the required fields on a create/update
// request before sending it to the server. The server validates too, but a
// client-side check produces clearer errors and avoids round-trips.
func validateCleanupPolicyPayload(name, clusterID, action, condition, schedule string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("'name' field is required in the policy file")
	}
	if strings.TrimSpace(clusterID) == "" {
		return fmt.Errorf("'cluster_id' field is required (use \"all\" for every cluster)")
	}
	if strings.TrimSpace(action) == "" {
		return fmt.Errorf("'action' field is required (one of: stop, clean, delete)")
	}
	if strings.TrimSpace(condition) == "" {
		return fmt.Errorf("'condition' field is required")
	}
	if strings.TrimSpace(schedule) == "" {
		return fmt.Errorf("'schedule' field is required (cron expression)")
	}
	return nil
}

// findCleanupPolicy returns the policy matching nameOrID. Because the server
// has no GET-by-ID endpoint, the lookup goes via the list and filters
// client-side. Numeric / UUID-shaped strings are matched on ID; everything
// else is matched on Name (case-insensitive, exact match).
func findCleanupPolicy(c *client.Client, nameOrID string) (*types.CleanupPolicy, error) {
	policies, err := c.ListCleanupPolicies()
	if err != nil {
		return nil, err
	}

	if looksLikeID(nameOrID) {
		for i := range policies {
			if policies[i].ID == nameOrID {
				return &policies[i], nil
			}
		}
		return nil, fmt.Errorf("no cleanup policy found with ID %q", nameOrID)
	}

	var matches []types.CleanupPolicy
	for i := range policies {
		if strings.EqualFold(policies[i].Name, nameOrID) {
			matches = append(matches, policies[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no cleanup policy found with name %q", nameOrID)
	case 1:
		return &matches[0], nil
	default:
		msg := fmt.Sprintf("multiple cleanup policies match name %q — use the ID instead:\n", nameOrID)
		for _, p := range matches {
			msg += fmt.Sprintf("  %s  (action: %s, schedule: %s)\n", p.ID, p.Action, p.Schedule)
		}
		return nil, fmt.Errorf("%s", msg)
	}
}

func resolveCleanupPolicyID(c *client.Client, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("policy ID or name must not be empty")
	}
	if looksLikeID(nameOrID) {
		return nameOrID, nil
	}
	policy, err := findCleanupPolicy(c, nameOrID)
	if err != nil {
		return "", err
	}
	return policy.ID, nil
}

func printCleanupPolicy(policy *types.CleanupPolicy) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, policy.ID)
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(policy)
	case output.FormatYAML:
		return printer.PrintYAML(policy)
	default:
		return printer.PrintSingle(policy, []output.KeyValue{
			{Key: "ID", Value: policy.ID},
			{Key: "Name", Value: policy.Name},
			{Key: "Cluster", Value: policy.ClusterID},
			{Key: "Action", Value: policy.Action},
			{Key: "Condition", Value: policy.Condition},
			{Key: "Schedule", Value: policy.Schedule},
			{Key: "Enabled", Value: strconv.FormatBool(policy.Enabled)},
			{Key: "Dry Run", Value: strconv.FormatBool(policy.DryRun)},
			{Key: "Last Run", Value: formatTime(policy.LastRunAt)},
		})
	}
}

func init() {
	cleanupPolicyCreateCmd.Flags().String(flagFromFile, "", "JSON or YAML file with the CreateCleanupPolicyRequest payload (required)")
	_ = cleanupPolicyCreateCmd.MarkFlagRequired(flagFromFile)
	cleanupPolicyUpdateCmd.Flags().String(flagFromFile, "", "JSON or YAML file with the UpdateCleanupPolicyRequest payload (required)")
	_ = cleanupPolicyUpdateCmd.MarkFlagRequired(flagFromFile)
	cleanupPolicyDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	cleanupPolicyDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	cleanupPolicyRunCmd.Flags().Bool("dry-run", false, "Preview the affected stack instances without applying the policy's action")

	cleanupPolicyCmd.AddCommand(cleanupPolicyListCmd)
	cleanupPolicyCmd.AddCommand(cleanupPolicyGetCmd)
	cleanupPolicyCmd.AddCommand(cleanupPolicyCreateCmd)
	cleanupPolicyCmd.AddCommand(cleanupPolicyUpdateCmd)
	cleanupPolicyCmd.AddCommand(cleanupPolicyDeleteCmd)
	cleanupPolicyCmd.AddCommand(cleanupPolicyRunCmd)

	rootCmd.AddCommand(cleanupPolicyCmd)
}
