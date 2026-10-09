package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// followLogs streams deployment logs via WebSocket until a terminal status is
// received. Returns an error if the deployment ended in error status.
//
// Installs an os.Interrupt signal handler for Ctrl-C; writes log lines to
// os.Stdout and warnings to os.Stderr. Test code should call followLogsCtx
// directly to inject a context + writers.
func followLogs(c *client.Client, instanceID string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return followLogsCtx(ctx, c, instanceID, os.Stdout, os.Stderr)
}

// followLogsCtx is the testable core of followLogs. It does NOT install a
// signal handler — the caller owns ctx and is responsible for cancelling
// it on shutdown. Returns nil when:
//   - the deployment reaches a terminal non-error status (running,
//     stopped, draft, "unknown" on graceful server close);
//   - ctx is cancelled before any other error surfaces (Ctrl-C path).
//
// Returns a wrapped error for terminal "error" status, or the underlying
// network/parse error otherwise.
func followLogsCtx(ctx context.Context, c *client.Client, instanceID string, out, warn io.Writer) error {
	result, err := c.StreamDeploymentLogs(ctx, instanceID, out, warn)
	if err != nil {
		if ctx.Err() != nil {
			// Ctrl-C / parent cancellation — surface as clean exit so
			// the operator's intentional interrupt doesn't look like a
			// command failure.
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
  stackctl stack list --status running --cluster 5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e
  stackctl stack list --definition example-dev
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
				params["pageSize"] = strconv.Itoa(pageSize)
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

The table output shows the expiry time and, for a running stack, the
access URLs (from the stack status).

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

		// Access URLs come from the status endpoint. Only a running stack
		// has them; the call is best-effort: one request, no retry, a short
		// timeout, and any error omits the URLs.
		var urls []string
		if printer.Format == output.FormatTable && !printer.Quiet && strings.EqualFold(instance.Status, "running") {
			if status, statusErr := bestEffortClient(c).GetStackStatus(id); statusErr == nil {
				urls = ingressURLs(status)
			}
		}

		return printInstanceWithURLs(instance, urls)
	},
}

var stackCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new stack instance",
	Long: `Create a new stack instance from a definition.

The --definition flag accepts either a definition name or ID.

Examples:
  stackctl stack create --name my-stack --definition example-dev
  stackctl stack create --name my-stack --definition e9af3b10-4633-436b-a131-975a3b598e3e
  stackctl stack create --name my-stack --definition example-dev --branch feature/xyz --cluster 5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e --ttl 120`,
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
		if isDryRun(cmd, "Would deploy stack %s", args[0]) {
			return nil
		}

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
		if isDryRun(cmd, "Would stop stack %s", args[0]) {
			return nil
		}

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
		if isDryRun(cmd, "Would clean stack %s (undeploy + remove namespace)", args[0]) {
			return nil
		}

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
	Long: `Show the current status and the pods of a stack instance, per chart.

The table shows one row per pod. A chart without pods (for example a
stopped chart) shows one row with the chart status. Access URLs are listed
below the table. -o json and -o yaml print the full API response.

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
			return printStatusTable(status)
		}
	},
}

// nowFunc returns the current time. Tests replace it for stable output.
var nowFunc = time.Now

// bestEffortTimeout is the timeout of optional extra calls (see
// bestEffortClient). A variable so tests can shorten it.
var bestEffortTimeout = 5 * time.Second

// bestEffortClient returns a client for an optional extra call: same server
// and credentials as c, one attempt (no retry), no session renewal, and a
// timeout of bestEffortTimeout.
func bestEffortClient(c *client.Client) *client.Client {
	q := client.New(c.BaseURL)
	q.APIKey = c.APIKey
	q.Token = c.Token
	q.Debug = c.Debug
	q.DebugWriter = c.DebugWriter
	q.RetryBackoff = []time.Duration{} // non-nil and empty: no retry
	q.HTTPClient = &http.Client{Timeout: bestEffortTimeout}
	if c.HTTPClient != nil {
		q.HTTPClient.Transport = c.HTTPClient.Transport
	}
	return q
}

// printStatusTable prints the per-chart pod table of a stack status.
func printStatusTable(status *types.InstanceStatus) error {
	printer.PrintMessage("Status: %s", printer.StatusColor(status.Status))
	podCount := 0
	for _, ch := range status.Charts {
		podCount += len(ch.Pods)
	}
	if podCount == 0 {
		printer.PrintMessage("No pods found.")
	} else {
		headers := []string{"CHART", "POD", "PHASE", "READY", "RESTARTS", "AGE", "IMAGE"}
		var rows [][]string
		now := nowFunc()
		for _, ch := range status.Charts {
			if len(ch.Pods) == 0 {
				rows = append(rows, []string{ch.ChartName, "-", printer.StatusColor(ch.Status), "-", "-", "-", "-"})
				continue
			}
			for _, p := range ch.Pods {
				rows = append(rows, []string{
					ch.ChartName,
					p.Name,
					printer.StatusColor(p.Phase),
					strconv.FormatBool(p.Ready),
					strconv.Itoa(int(p.RestartCount)),
					formatAge(p.StartTime, now),
					p.Image,
				})
			}
		}
		if err := printer.PrintTable(headers, rows); err != nil {
			return err
		}
	}
	if urls := ingressURLs(status); len(urls) > 0 {
		printer.PrintMessage("URLs:")
		for _, u := range urls {
			printer.PrintMessage("  %s", u)
		}
	}
	return nil
}

// ingressURLs returns the unique access URLs of a stack status, in API order.
func ingressURLs(status *types.InstanceStatus) []string {
	if status == nil {
		return nil
	}
	seen := map[string]bool{}
	var urls []string
	for _, ing := range status.Ingresses {
		if ing.URL == "" || seen[ing.URL] {
			continue
		}
		seen[ing.URL] = true
		urls = append(urls, ing.URL)
	}
	return urls
}

// formatAge returns the age of start relative to now in kubectl style
// (45s, 12m, 3h, 2d), or "-" when start is unknown.
func formatAge(start *time.Time, now time.Time) string {
	if start == nil || start.IsZero() {
		return "-"
	}
	d := now.Sub(*start)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
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
	Short: "Add N minutes to the expiry of a stack instance",
	Long: `Add N minutes to the stack's expiry (never shortens it).

--minutes N adds N minutes to the current expiry of the stack, or to now
when the stack has already expired. The server never makes the expiry
earlier, does not change the TTL of the stack, and caps the new expiry at
30 days from now. The output shows the old and the new expiry.

--reset-ttl M is deprecated. It uses the old behaviour of the API: the TTL
of the stack becomes M minutes and the expiry becomes now + M minutes. This
can make the expiry earlier. The server answers with a deprecation warning,
which the command writes to stderr.

--yes is accepted for compatibility with older scripts and has no effect.

Examples:
  stackctl stack extend my-stack --minutes 60
  stackctl stack extend my-stack --minutes 120 -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		minutes, _ := cmd.Flags().GetInt("minutes")
		resetTTL, _ := cmd.Flags().GetInt("reset-ttl")
		useMinutes := cmd.Flags().Changed("minutes")
		useReset := cmd.Flags().Changed("reset-ttl")
		switch {
		case useMinutes && useReset:
			return fmt.Errorf("--minutes and --reset-ttl cannot be used together")
		case !useMinutes && !useReset:
			return fmt.Errorf("--minutes is required")
		case useMinutes && minutes <= 0:
			return fmt.Errorf("--minutes must be a positive integer")
		case useReset && resetTTL <= 0:
			return fmt.Errorf("--reset-ttl must be a positive integer")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		c.WarnWriter = cmd.ErrOrStderr()

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		current, err := c.GetStack(id)
		if err != nil {
			return err
		}
		oldExpiry := current.ExpiresAt

		var updated *types.StackInstance
		if useReset {
			updated, err = c.ResetStackTTL(id, resetTTL)
		} else {
			updated, err = c.ExtendStack(id, minutes)
		}
		if err != nil {
			return err
		}

		// A server without support for "minutes" (before v0.6.0) ignores
		// the field and resets the expiry to now + TTL. Detect that after
		// the output, so the result is still shown, and exit non-zero.
		var notAdded error
		if !useReset {
			notAdded = checkMinutesAdded(minutes, oldExpiry, updated)
		}

		if err := printExtendResult(id, updated, oldExpiry, useReset, minutes, resetTTL); err != nil {
			return err
		}
		return notAdded
	},
}

// printExtendResult prints the result of stack extend in the output format.
func printExtendResult(id string, updated *types.StackInstance, oldExpiry *time.Time, useReset bool, minutes, resetTTL int) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, id)
		return nil
	}

	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(updated)
	case output.FormatYAML:
		return printer.PrintYAML(updated)
	default:
		if useReset {
			printer.PrintMessage("Reset the TTL of stack %s to %d minutes", id, resetTTL)
		} else {
			printer.PrintMessage("Extended stack %s by %d minutes", id, minutes)
		}
		printer.PrintMessage("Old expiry: %s", formatTime(oldExpiry))
		printer.PrintMessage("New expiry: %s", formatTime(updated.ExpiresAt))
		return nil
	}
}

// extendTolerance covers the time between the GET of the old expiry and
// the extend call, and rounding on the server.
const extendTolerance = 2 * time.Minute

// maxExtendMinutes is the cap of the server: the new expiry is at most
// now + 30 days.
const maxExtendMinutes = 43200

// checkMinutesAdded returns an error when the server did not add minutes
// to the expiry: the new expiry is earlier than max(old expiry, server
// time) + minutes (minus extendTolerance) and is not at the 30-day cap. The
// server time is updated_at of the response (the server sets it on extend),
// else the local clock.
func checkMinutesAdded(minutes int, oldExpiry *time.Time, updated *types.StackInstance) error {
	serverNow := updated.UpdatedAt
	if serverNow.IsZero() {
		serverNow = nowFunc()
	}
	base := serverNow
	if oldExpiry != nil && oldExpiry.After(base) {
		base = *oldExpiry
	}
	expected := base.Add(time.Duration(minutes) * time.Minute)
	capAt := serverNow.Add(maxExtendMinutes * time.Minute)

	newExpiry := updated.ExpiresAt
	if newExpiry != nil && (!newExpiry.Before(expected.Add(-extendTolerance)) || !newExpiry.Before(capAt.Add(-extendTolerance))) {
		return nil
	}
	return fmt.Errorf("the server did not add %d minutes (new expiry %s, expected about %s). "+
		"The server does not support --minutes; upgrade k8s-stack-manager to v0.6.0 or later",
		minutes, formatTime(newExpiry), formatTime(&expected))
}

var stackValuesCmd = &cobra.Command{
	Use:   "values <name|id>",
	Short: "Show merged Helm values for a stack instance",
	Long: `Show the merged Helm values of a stack instance, as deploy uses them.

Without --chart the command prints the values.yaml of every chart of the
stack's definition. Each chart starts with a "# chart: <name>" header.
--chart takes a chart name or ID and prints the YAML of that chart only.

--output-file writes the values to a new file instead of stdout. Without
--chart the file is the ZIP archive from the API (one values.yaml per
chart). With --chart the file is the YAML of that chart. The file gets mode
0600 (values can hold secrets). An existing file is not overwritten unless
--force is given; --force replaces the file (a symlink is replaced, not
followed).

-o json and -o yaml print a map of chart name to values.

Examples:
  stackctl stack values my-stack
  stackctl stack values my-stack --chart my-chart
  stackctl stack values my-stack --chart 3f2b8c1e-5a4d-4e6f-9a7b-1c2d3e4f5a6b
  stackctl stack values my-stack --output-file my-stack-values.zip
  stackctl stack values my-stack --chart my-chart --output-file my-chart.yaml --force
  stackctl stack values my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		chart, _ := cmd.Flags().GetString("chart")
		outputFile, _ := cmd.Flags().GetString("output-file")
		force, _ := cmd.Flags().GetBool("force")
		if outputFile != "" {
			if err := checkNoPathTraversal(outputFile); err != nil {
				return err
			}
			outputFile = filepath.Clean(outputFile)
			// Check before the API calls; writeFileAtomic checks again.
			if err := checkOutputFile(outputFile, force); err != nil {
				return err
			}
		} else if force {
			return fmt.Errorf("--force needs --output-file")
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		if outputFile != "" && chart == "" {
			data, _, err := c.ExportValues(id)
			if err != nil {
				return err
			}
			return writeValuesFile(id, outputFile, data, force)
		}

		charts, err := stackCharts(c, id)
		if err != nil {
			return err
		}
		if chart != "" {
			ch, err := matchChart(charts, strings.TrimSpace(chart), "the definition of stack "+id)
			if err != nil {
				return err
			}
			charts = []types.ChartConfig{*ch}
		}

		docs := make([]chartValuesDoc, 0, len(charts))
		for _, ch := range charts {
			data, err := c.GetChartValues(id, ch.ID)
			if err != nil {
				return fmt.Errorf("reading values of chart %s: %w", ch.ChartName, err)
			}
			docs = append(docs, chartValuesDoc{Name: ch.ChartName, YAML: data})
		}

		if outputFile != "" {
			return writeValuesFile(id, outputFile, docs[0].YAML, force)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON, output.FormatYAML:
			byChart := make(map[string]interface{}, len(docs))
			for _, d := range docs {
				var v interface{}
				if err := yaml.Unmarshal(d.YAML, &v); err != nil {
					return fmt.Errorf("parsing values of chart %s: %w", d.Name, err)
				}
				byChart[d.Name] = normalizeYAML(v)
			}
			if printer.Format == output.FormatJSON {
				return printer.PrintJSON(byChart)
			}
			return printer.PrintYAML(byChart)
		default:
			if len(docs) == 0 {
				printer.PrintMessage("Stack %s has no charts.", id)
				return nil
			}
			if chart != "" {
				return writeYAMLDoc(docs[0].YAML)
			}
			for i, d := range docs {
				if i > 0 {
					fmt.Fprintln(printer.Writer, "---")
				}
				fmt.Fprintf(printer.Writer, "# chart: %s\n", d.Name)
				if err := writeYAMLDoc(d.YAML); err != nil {
					return err
				}
			}
			return nil
		}
	},
}

// checkOutputFile refuses an existing path unless force is set. It does not
// follow a symlink (Lstat).
func checkOutputFile(path string, force bool) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("%s is a directory", path)
	case err == nil && !force:
		return fmt.Errorf("%s already exists; use --force to replace it", path)
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("checking %s: %w", path, err)
	}
	return nil
}

// writeFileAtomic writes data to a new temp file with mode 0600 in the
// directory of path, then renames it to path. The rename replaces path
// itself: the mode of an existing file is not kept and a symlink is
// replaced, not followed. An existing path is refused unless force is set.
func writeFileAtomic(path string, data []byte, force bool) error {
	if err := checkOutputFile(path, force); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing file %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("writing file %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing file %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing file %s: %w", path, err)
	}
	if !force {
		// Link fails when path appeared since the check: no overwrite.
		if err := os.Link(tmpName, path); err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("%s already exists; use --force to replace it", path)
			}
			// The file system has no hard links: check again and rename.
			if err := checkOutputFile(path, false); err != nil {
				return err
			}
		} else {
			return nil
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing file %s: %w", path, err)
	}
	return nil
}

// chartValuesDoc is the merged values.yaml of one chart.
type chartValuesDoc struct {
	Name string
	YAML []byte
}

// writeYAMLDoc writes a YAML document to the printer, ending with a newline.
func writeYAMLDoc(data []byte) error {
	if _, err := printer.Writer.Write(data); err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		_, err := fmt.Fprintln(printer.Writer)
		return err
	}
	return nil
}

// writeValuesFile writes exported values to path (see writeFileAtomic)
// and prints a confirmation.
func writeValuesFile(id, path string, data []byte, force bool) error {
	if err := writeFileAtomic(path, data, force); err != nil {
		return err
	}
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, id)
		return nil
	}
	printer.PrintMessage("Wrote values of stack %s to %s", id, path)
	return nil
}

var stackCompareCmd = &cobra.Command{
	Use:   "compare <name|id> <name|id>",
	Short: "Compare two stack instances",
	Long: `Compare the merged Helm values of two stack instances, per chart.

The table shows for each chart whether the merged values differ. -o json
and -o yaml print the full API response, with the merged values of both
sides.

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
			return printCompareTable(result)
		}
	},
}

// printCompareTable prints one row per chart: "differs", "identical", or
// the side the chart is missing from.
func printCompareTable(result *types.CompareResult) error {
	printer.PrintMessage("Left:  %s (%s, branch %s, owner %s)", result.Left.Name, result.Left.ID, result.Left.Branch, result.Left.Owner)
	printer.PrintMessage("Right: %s (%s, branch %s, owner %s)", result.Right.Name, result.Right.ID, result.Right.Branch, result.Right.Owner)
	if len(result.Charts) == 0 {
		printer.PrintMessage("No charts to compare.")
		return nil
	}
	headers := []string{"CHART", "RESULT"}
	rows := make([][]string, len(result.Charts))
	differing := 0
	for i, ch := range result.Charts {
		res := "identical"
		switch {
		case ch.LeftValues == nil && ch.RightValues != nil:
			res = "only in right"
		case ch.LeftValues != nil && ch.RightValues == nil:
			res = "only in left"
		case ch.HasDifferences:
			res = "differs"
		}
		if ch.HasDifferences || res != "identical" {
			differing++
		}
		rows[i] = []string{ch.ChartName, res}
	}
	if err := printer.PrintTable(headers, rows); err != nil {
		return err
	}
	if differing == 0 {
		printer.PrintMessage("No differences found.")
	} else {
		printer.PrintMessage("%d of %d charts differ. Use -o json or -o yaml to see the merged values of both sides.", differing, len(result.Charts))
	}
	return nil
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
		if isDryRun(cmd, "Would rollback stack %s", args[0]) {
			return nil
		}

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

	// --dry-run flags
	stackDeployCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	stackStopCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	stackCleanCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	stackDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	stackRollbackCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	// stack clean flags
	stackCleanCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	// stack delete flags
	stackDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	// stack extend flags
	stackExtendCmd.Flags().Int("minutes", 0, "Add N minutes to the stack's expiry (never shortens it)")
	stackExtendCmd.Flags().Int("reset-ttl", 0, "Deprecated: set the TTL to M minutes and the expiry to now + M (can shorten the expiry)")
	// --yes had a confirmation to skip in older versions; it has no effect now.
	stackExtendCmd.Flags().BoolP("yes", "y", false, "No effect; kept for compatibility with older scripts")
	_ = stackExtendCmd.Flags().MarkHidden("yes")

	// stack values flags
	stackValuesCmd.Flags().String("chart", "", "Chart name or ID (default: all charts)")
	stackValuesCmd.Flags().String("output-file", "", "Write the values to a new file (mode 0600): the ZIP export of all charts, or the YAML of --chart")
	stackValuesCmd.Flags().Bool("force", false, "Replace an existing --output-file")

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
	stackCmd.AddCommand(stackWatchCmd)

	// stack watch flags
	// Plain String (not StringSlice): pflag's StringSlice.Set APPENDS after
	// the first call rather than replacing, so test-cleanup `Set("")` is a
	// silent no-op and flag values leak across in-process invocations.
	// Manual splitting in RunE keeps the test-reset story clean.
	stackWatchCmd.Flags().String("id", "", "Filter to one or more instance IDs (comma-separated); exits when all listed IDs reach a terminal status (running or failed)")
	stackWatchCmd.Flags().String("owner", "", "Filter to a single owner — NOT YET SUPPORTED (the /ws payload does not carry owner_id; see stackctl#75 follow-up)")
	stackWatchCmd.Flags().String("status", "", "Filter to a single status value (e.g. running, deploying, failed); streams until Ctrl-C")

	rootCmd.AddCommand(stackCmd)
}

// stackWatchTerminalStatuses lists the status values that count as a
// terminal outcome for `stack watch --id`. Mirrors backend deployer state
// transitions: "running" / "stopped" / "draft" → success-ish; "error" /
// "failed" → failure (the backend uses "error" but issue #75 documents
// "failed" — we accept both).
var stackWatchTerminalStatuses = map[string]bool{
	"running": true,
	"stopped": true,
	"draft":   true,
	"error":   true,
	"failed":  true,
}

// stackWatchFailedStatuses lists the terminal statuses that should cause
// `stack watch --id` to exit with a non-zero exit code.
var stackWatchFailedStatuses = map[string]bool{
	"error":  true,
	"failed": true,
}

var stackWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch stack lifecycle events over WebSocket",
	Long: `Subscribe to /ws and render deploy/stop/clean/status events as they
arrive.

  --id <id1,id2,...>   Wait for the listed instance IDs to reach a
                       terminal status (running/stopped/draft → exit 0;
                       error/failed → exit 1). Exits as soon as every
                       listed ID has reported terminal.
  --status <state>     Filter to events with the given status; streams
                       until Ctrl-C.
  --owner <user>       NOT YET SUPPORTED — the /ws status payload does
                       not carry owner_id, so a per-event REST lookup
                       would be required. Returns an explicit error.

--id takes precedence over --status: if both are set, --status is
ignored (with a stderr warning) so the watch sees every status
transition for the listed instances and can detect opposite-terminal
states like "error" / "failed" reliably.

Authentication: reuses the HTTP client auth chain. JWT goes via the
Authorization: Bearer header; if the WS upgrade is rejected with HTTP
401 (e.g. a reverse proxy stripped the Authorization header), the
dialer transparently retries with ?token=<jwt> in the query string,
which the backend WS handler also accepts. API keys are sent as
X-API-Key but the backend WS endpoint currently only honours JWTs;
operators using --api-key should expect a 401.

Ctrl-C exits cleanly with no orphan goroutines.

Examples:
  stackctl stack deploy 6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d && stackctl stack watch --id 6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d
  stackctl stack watch --id 6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d,7b2e3d4c-5f6a-4b7c-9d8e-0f1a2b3c4d5e   # exits when both reach terminal
  stackctl stack watch --status deploying
  stackctl stack watch -o json         # one JSON object per event`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		idsRaw, _ := cmd.Flags().GetString("id")
		owner, _ := cmd.Flags().GetString("owner")
		statusFilter, _ := cmd.Flags().GetString("status")

		if owner != "" {
			return fmt.Errorf("--owner is not yet supported; see stackctl#75 follow-up")
		}

		// Split + normalise --id values (trim whitespace, drop empties).
		var normIDs []string
		for _, raw := range strings.Split(idsRaw, ",") {
			id := strings.TrimSpace(raw)
			if id != "" {
				normIDs = append(normIDs, id)
			}
		}
		idMode := len(normIDs) > 0

		// In --id mode the watch needs to see EVERY status transition for
		// the listed instances so the terminal-status detector can drain
		// `pending`. A --status filter (even one matching a terminal value
		// like "running") would drop the opposite-terminal events
		// (error/failed) and the watch would hang forever. Drop the
		// filter and warn — surprising the user is better than hanging.
		if idMode && statusFilter != "" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: ignoring --status %q because --id is set; --id mode shows every status transition so the watch can detect terminal states reliably\n",
				statusFilter)
			statusFilter = ""
		}

		pending := map[string]bool{}
		for _, id := range normIDs {
			pending[id] = true
		}

		c, err := newClient()
		if err != nil {
			return err
		}

		// cmd.Context() returns nil when RunE is invoked directly (e.g. in
		// unit tests that bypass Execute()). Fall back to Background so the
		// signal-trap context is always rooted in a valid parent.
		parentCtx := cmd.Context()
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt)
		defer stop()

		events, err := c.WatchEvents(ctx, client.WatchFilter{
			InstanceIDs: normIDs,
			Status:      statusFilter,
		})
		if err != nil {
			return err
		}

		var failedIDs []string
		for ev := range events {
			if err := printWatchEvent(ev); err != nil {
				return err
			}
			if !idMode {
				continue
			}
			if stackWatchTerminalStatuses[ev.Status] {
				if stackWatchFailedStatuses[ev.Status] {
					failedIDs = append(failedIDs, ev.InstanceID)
				}
				delete(pending, ev.InstanceID)
				if len(pending) == 0 {
					stop()
					break
				}
			}
		}

		if len(failedIDs) > 0 {
			return fmt.Errorf("instances reached a failed terminal status: %s", strings.Join(failedIDs, ", "))
		}
		return nil
	},
}

// printWatchEvent renders one event in the operator's chosen output mode.
// For table mode each event is one line ("[TIMESTAMP] INSTANCE STATUS …");
// JSON/YAML emit one structured object per event (no array wrapper, so
// the stream is grep- and jq-friendly).
func printWatchEvent(ev types.WatchEvent) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, ev.InstanceID)
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(ev)
	case output.FormatYAML:
		return printer.PrintYAML(ev)
	default:
		msg := fmt.Sprintf("[%s] %s %s", ev.Timestamp.UTC().Format(time.RFC3339), ev.InstanceID, ev.Status)
		if ev.ErrorMessage != "" {
			msg += " — " + ev.ErrorMessage
		}
		fmt.Fprintln(printer.Writer, msg)
		return nil
	}
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
	return printInstanceWithURLs(instance, nil)
}

// printInstanceWithURLs prints a stack instance; the table output adds
// one "URL" line per access URL.
func printInstanceWithURLs(instance *types.StackInstance, urls []string) error {
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
		if instance.ErrorMessage != "" {
			fields = append(fields, output.KeyValue{Key: "Error", Value: instance.ErrorMessage})
		}
		for _, u := range urls {
			fields = append(fields, output.KeyValue{Key: "URL", Value: u})
		}
		return printer.PrintSingle(instance, fields)
	}
}
