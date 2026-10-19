package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Show the overview: failing and expiring stacks, recent deployments, clusters",
	Long: `Show the overview of the landing page of the web UI (GET /api/v1/dashboard).
It covers the stacks of all users, not only your own:

  - Failing stacks (status error) with the error message.
  - Stacks that expire within the next hour, with the time left.
  - Recent deployments (deploy, stop, clean, rollback).
  - Cluster health (devops and admin only).

An empty section is not shown. -q prints the IDs of the failing stacks and
of the stacks that expire soon, one per line. These stacks can belong to
other users. -o json and -o yaml print the full response.

Examples:
  stackctl dashboard
  stackctl dashboard -o json
  stackctl dashboard -q`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		d, err := c.GetDashboard()
		if err != nil {
			return err
		}
		if printer.Quiet {
			seen := map[string]bool{}
			for _, f := range d.FailingInstances {
				if !seen[f.ID] {
					seen[f.ID] = true
					fmt.Fprintln(printer.Writer, f.ID)
				}
			}
			for _, e := range d.ExpiringSoon {
				if !seen[e.ID] {
					seen[e.ID] = true
					fmt.Fprintln(printer.Writer, e.ID)
				}
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(d)
		case output.FormatYAML:
			return printer.PrintYAML(d)
		default:
			return printDashboard(d)
		}
	},
}

// printDashboard prints the sections of the dashboard that have entries.
func printDashboard(d *types.Dashboard) error {
	now := nowFunc()
	s := client.SanitizeServerText
	printed := false
	section := func(title string) {
		if printed {
			printer.PrintMessage("")
		}
		printer.PrintMessage("%s", title)
		printed = true
	}

	if len(d.FailingInstances) > 0 {
		section("Failing stacks:")
		rows := make([][]string, len(d.FailingInstances))
		for i, f := range d.FailingInstances {
			rows[i] = []string{f.ID, s(f.Name), printer.StatusColor(s(f.Status)), valueOrDash(s(f.ErrorMessage))}
		}
		if err := printer.PrintTable([]string{"ID", "NAME", "STATUS", "ERROR"}, rows); err != nil {
			return err
		}
	}
	if len(d.ExpiringSoon) > 0 {
		section("Expiring soon:")
		rows := make([][]string, len(d.ExpiringSoon))
		for i, e := range d.ExpiringSoon {
			rows[i] = []string{e.ID, s(e.Name), printer.StatusColor(s(e.Status)), formatTime(e.ExpiresAt), timeLeft(e.ExpiresAt, now)}
		}
		if err := printer.PrintTable([]string{"ID", "NAME", "STATUS", "EXPIRES AT", "LEFT"}, rows); err != nil {
			return err
		}
	}
	if len(d.RecentDeployments) > 0 {
		section("Recent deployments:")
		rows := make([][]string, len(d.RecentDeployments))
		for i, r := range d.RecentDeployments {
			start := r.StartedAt
			rows[i] = []string{
				formatAge(&start, now), s(r.InstanceName), s(r.Action), printer.StatusColor(s(r.Status)), valueOrDash(s(r.OwnerUsername)),
			}
		}
		if err := printer.PrintTable([]string{"AGE", "STACK", "ACTION", "STATUS", "OWNER"}, rows); err != nil {
			return err
		}
	}
	// The role user gets the clusters without health data: no section then.
	var clusters []types.DashboardCluster
	for _, cl := range d.Clusters {
		if cl.HealthStatus != "" {
			clusters = append(clusters, cl)
		}
	}
	if len(clusters) > 0 {
		section("Clusters:")
		rows := make([][]string, len(clusters))
		for i, cl := range clusters {
			nodes := "-"
			if cl.NodeCount != nil {
				ready := "?"
				if cl.ReadyNodeCount != nil {
					ready = strconv.Itoa(*cl.ReadyNodeCount)
				}
				nodes = ready + "/" + strconv.Itoa(*cl.NodeCount)
			}
			namespaces := "-"
			if cl.NamespaceCount != nil {
				namespaces = strconv.Itoa(*cl.NamespaceCount)
			}
			rows[i] = []string{s(cl.Name), printer.StatusColor(s(cl.HealthStatus)), nodes, namespaces}
		}
		if err := printer.PrintTable([]string{"CLUSTER", "HEALTH", "NODES READY", "NAMESPACES"}, rows); err != nil {
			return err
		}
	}
	if !printed {
		printer.PrintMessage("Nothing to show: no failing or expiring stacks and no recent deployments.")
	}
	return nil
}

// timeLeft returns the time until t in kubectl style (45s, 12m, 3h, 2d),
// "expired" for a time in the past, or "-".
func timeLeft(t *time.Time, now time.Time) string {
	if t == nil {
		return "-"
	}
	if !t.After(now) {
		return "expired"
	}
	start := now.Add(-t.Sub(now))
	return formatAge(&start, now)
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
}
