package cmd

import (
	"fmt"
	"math"
	"strconv"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

var analyticsCmd = &cobra.Command{
	Use:   "analytics",
	Short: "Show platform usage analytics (devops/admin)",
	Long: `Read-only aggregate counts and per-resource usage statistics.

  overview   — platform-wide counts (templates, definitions, instances, deploys, users)
  templates  — per-template usage (definition / instance / deploy counts, success rate)
  users      — per-user usage (instance / deploy counts, last active); admin-only

All endpoints are devops-gated; users is additionally admin-gated. Server-side
responses are cached for ~30s, so consecutive calls may return identical
snapshots. JSON/YAML output is byte-stable across runs (alphabetical key
order, struct-defined field order in Go) — safe to diff in CI.`,
}

var analyticsOverviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Show platform-wide aggregate counts",
	Long: `Show high-level platform counts: templates, definitions, instances,
running instances, total deploys, users.

Examples:
  stackctl analytics overview
  stackctl analytics overview -o json
  stackctl analytics overview -o yaml`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		overview, err := c.GetAnalyticsOverview()
		if err != nil {
			return err
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(overview)
		case output.FormatYAML:
			return printer.PrintYAML(overview)
		default:
			// Table mode renders one KPI per row — the natural shape for a
			// scalar-only resource. Quiet mode is intentionally NOT routed
			// through PrintIDs (there is no list of identifiers); it falls
			// through to the same KV rendering so the operator always sees
			// the numbers. Override by piping through -o json | jq if a
			// machine-readable subset is needed.
			return printer.PrintSingle(overview, []output.KeyValue{
				{Key: "Total templates", Value: strconv.Itoa(overview.TotalTemplates)},
				{Key: "Total definitions", Value: strconv.Itoa(overview.TotalDefinitions)},
				{Key: "Total instances", Value: strconv.Itoa(overview.TotalInstances)},
				{Key: "Running instances", Value: strconv.Itoa(overview.RunningInstances)},
				{Key: "Total deploys", Value: strconv.Itoa(overview.TotalDeploys)},
				{Key: "Total users", Value: strconv.Itoa(overview.TotalUsers)},
			})
		}
	},
}

var analyticsTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "Show per-template usage statistics",
	Long: `Show usage statistics for each stack template: definition count,
instance count, deploy count, error count, success rate.

Examples:
  stackctl analytics templates
  stackctl analytics templates -o json
  stackctl analytics templates --quiet  (template IDs, one per line)`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		stats, err := c.GetAnalyticsTemplates()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, s := range stats {
				fmt.Fprintln(printer.Writer, s.TemplateID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(stats)
		case output.FormatYAML:
			return printer.PrintYAML(stats)
		default:
			if len(stats) == 0 {
				printer.PrintMessage("No templates found.")
				return nil
			}
			headers := []string{"ID", "NAME", "CATEGORY", "PUBLISHED", "DEFS", "INSTANCES", "DEPLOYS", "SUCCESS", "SUCCESS RATE"}
			rows := make([][]string, len(stats))
			for i, s := range stats {
				rows[i] = []string{
					s.TemplateID,
					s.TemplateName,
					s.Category,
					strconv.FormatBool(s.IsPublished),
					strconv.Itoa(s.DefinitionCount),
					strconv.Itoa(s.InstanceCount),
					strconv.Itoa(s.DeployCount),
					strconv.Itoa(s.SuccessCount),
					formatPercent(s.SuccessRate),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var analyticsUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Show per-user usage statistics (admin only)",
	Long: `Show per-user usage statistics: instance count, deploy count,
last active timestamp.

This endpoint is admin-gated; non-admin (including devops) callers receive
a 403 surfaced as a command error.

Examples:
  stackctl analytics users
  stackctl analytics users -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		stats, err := c.GetAnalyticsUsers()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, s := range stats {
				fmt.Fprintln(printer.Writer, s.UserID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(stats)
		case output.FormatYAML:
			return printer.PrintYAML(stats)
		default:
			if len(stats) == 0 {
				printer.PrintMessage("No users found.")
				return nil
			}
			headers := []string{"ID", "USERNAME", "INSTANCES", "DEPLOYS", "LAST ACTIVE"}
			rows := make([][]string, len(stats))
			for i, s := range stats {
				rows[i] = []string{
					s.UserID,
					s.Username,
					strconv.Itoa(s.InstanceCount),
					strconv.Itoa(s.DeployCount),
					formatTime(s.LastActive),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

// formatPercent renders a 0–100 success-rate float with one decimal place so
// table output is stable across Go versions and minor float drift (the
// underlying value is success_count/deploy_count*100 server-side).
//
// NaN/±Inf render as "-" rather than "NaN%" / "+Inf%" — the backend
// documents that deploy_count==0 yields 0.0, but a defensive guard
// avoids cosmetic regressions if that ever changes.
func formatPercent(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}

func init() {
	analyticsCmd.AddCommand(analyticsOverviewCmd)
	analyticsCmd.AddCommand(analyticsTemplatesCmd)
	analyticsCmd.AddCommand(analyticsUsersCmd)
	rootCmd.AddCommand(analyticsCmd)
}
