package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var stackPreviewCmd = &cobra.Command{
	Use:   "preview <name|id>",
	Short: "Show the Helm values that the next deploy uses",
	Long: `Show the merged Helm values that the next deploy of a stack uses, for
each chart, without a deploy (GET /api/v1/stack-instances/:id/deploy-preview).

The values are the result of the full merge: shared values, chart
defaults, instance overrides and locked template values, with the template
variables substituted.

The table output writes YAML to stdout: one document for each chart, with a
"# chart: <name>" comment line that says whether the values differ from the
last deploy. With --chart, stdout has only the YAML of that chart, so you
can write it to a file. Warnings go to stderr: after a rollback, the stored
overrides can differ from the running values (values_drift), and the next
deploy applies the stored overrides again.

-q prints the names of the charts whose values differ from the last deploy.
-o json and -o yaml print the response, with the values of the last deploy
(previous_values) and the next deploy (pending_values). With --chart, the
response has only that chart.

Examples:
  stackctl stack preview my-stack
  stackctl stack preview my-stack --chart backend > backend-values.yaml
  stackctl stack preview my-stack -q`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		chart, _ := cmd.Flags().GetString("chart")

		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		preview, err := c.GetDeployPreview(id)
		if err != nil {
			return err
		}
		if chart != "" {
			if preview.Charts, err = selectPreviewChart(preview.Charts, chart); err != nil {
				return err
			}
		}
		if preview.ValuesDrift {
			warning := preview.Warning
			if strings.TrimSpace(warning) == "" {
				warning = types.ValuesDriftWarning
			}
			printServerWarning(cmd, warning)
		}

		if printer.Quiet {
			for _, ch := range preview.Charts {
				if ch.HasChanges {
					fmt.Fprintln(printer.Writer, ch.ChartName)
				}
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(preview)
		case output.FormatYAML:
			return printer.PrintYAML(preview)
		}

		if len(preview.Charts) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "The stack has no charts.")
			return nil
		}
		if chart != "" {
			fmt.Fprint(printer.Writer, withNewline(preview.Charts[0].PendingValues))
			return nil
		}
		for _, ch := range preview.Charts {
			change := "no change since the last deploy"
			switch {
			case ch.PreviousValues == "":
				change = "no previous deploy"
			case ch.HasChanges:
				change = "changed since the last deploy"
			}
			fmt.Fprintf(printer.Writer, "---\n# chart: %s (%s)\n%s", ch.ChartName, change, withNewline(ch.PendingValues))
		}
		return nil
	},
}

// selectPreviewChart returns the preview of the chart with the given name.
func selectPreviewChart(charts []types.ChartDeployPreview, name string) ([]types.ChartDeployPreview, error) {
	names := make([]string, 0, len(charts))
	for _, ch := range charts {
		if ch.ChartName == name {
			return []types.ChartDeployPreview{ch}, nil
		}
		names = append(names, ch.ChartName)
	}
	return nil, fmt.Errorf("chart %q is not in the stack (charts: %s)", name, strings.Join(names, ", "))
}

// withNewline returns s with a final newline (empty values stay empty).
func withNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

var stackPodsCmd = &cobra.Command{
	Use:   "pods <name|id>",
	Short: "Show the live pods and the warning events of a stack",
	Long: `Show the pods of a stack namespace with their status, and the Warning
events of the namespace (GET /api/v1/stack-instances/:id/pods).

Unlike "stack status", the server reads the cluster directly (not its
cache) and adds the events. The REASON column shows why a container waits
or stopped, for example CrashLoopBackOff or ImagePullBackOff.

The last column, EXEC, lists the containers of the pod that
'stackctl stack exec' accepts with -c; "*" marks the container that it
uses without -c (for example "web*, worker"). A container is shown only on
the pod that 'stack exec' picks for it: the pod that the server names
(k8s-stack-manager v0.17.0 or later). With an older server, stackctl picks
the first pod by name that is Running and Ready and runs the container;
this can be wrong for a pod that is shutting down, or for an init
container with the name of an exec container. A pod shows "-" when the
command picks it for no container: for example a finished Job pod, a pod
that is not ready, a pod without such a container, or a chart where no
container accepts exec or the server could not check them (for example a
failed read or a check over 3 s). A container that no listed pod gets is
shown on a line under the table. The column appears only when
the server sends exec hints: ` + client.ExecServerVersion + `,
a user who may change the stack, exec turned on, a cluster that allows
exec and, with an API key, a server that allows API keys for exec
(STACK_STREAM_ALLOW_API_KEYS). The hint can leave out a container that
works, but never lists one that the server refuses. Only Kubernetes
container names (lower-case letters, digits and '-', at most 63
characters) are shown; any other name from the server is left out.

--all-events also shows the Normal events. -q prints the pod names (and
does not ask for the exec hints). -o json and -o yaml print the full
response, with the container states, the pod conditions, all events and
the exec hints as received (exec_hints, exec_containers,
exec_default_container, exec_pods).

Examples:
  stackctl stack pods my-stack
  stackctl stack pods my-stack --all-events
  stackctl stack pods my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		allEvents, _ := cmd.Flags().GetBool("all-events")

		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}

		// -q prints only the pod names: no exec hints (extra server reads).
		status, err := c.GetStackPods(id, !printer.Quiet)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, ch := range status.Charts {
				for _, p := range ch.Pods {
					fmt.Fprintln(printer.Writer, p.Name)
				}
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(status)
		case output.FormatYAML:
			return printer.PrintYAML(status)
		default:
			return printPodsTable(status, allEvents)
		}
	},
}

// printPodsTable prints the pods and the events of "stack pods".
func printPodsTable(status *types.InstanceStatus, allEvents bool) error {
	now := nowFunc()
	var rows [][]string
	var unplaced []string
	for _, ch := range status.Charts {
		var execCells []string
		if status.ExecHints {
			var rest []string
			execCells, rest = execColumn(ch)
			if len(rest) > 0 && len(ch.Pods) > 0 {
				unplaced = append(unplaced, "  "+client.SanitizeServerText(ch.ChartName)+": "+strings.Join(rest, ", "))
			}
		}
		for i, p := range ch.Pods {
			row := []string{
				client.SanitizeServerText(ch.ChartName),
				client.SanitizeServerText(p.Name),
				printer.StatusColor(client.SanitizeServerText(p.Phase)),
				readyText(p),
				strconv.Itoa(int(p.RestartCount)),
				formatAge(p.StartTime, now),
				client.SanitizeServerText(podReason(p)),
			}
			if status.ExecHints {
				row = append(row, execCells[i])
			}
			rows = append(rows, row)
		}
	}
	// EXEC goes last, so the other columns keep their positions.
	headers := []string{"CHART", "POD", "PHASE", "READY", "RESTARTS", "AGE", "REASON"}
	if status.ExecHints {
		headers = append(headers, "EXEC")
	}
	if len(rows) == 0 {
		printer.PrintMessage("No pods found in namespace %s.", client.SanitizeServerText(status.Namespace))
	} else if err := printer.PrintTable(headers, rows); err != nil {
		return err
	}
	if len(unplaced) > 0 {
		printer.PrintMessage("")
		printer.PrintMessage("Exec containers on no listed pod ('stack exec -c' can still work):")
		for _, l := range unplaced {
			printer.PrintMessage("%s", l)
		}
	}

	events := make([]types.PodEvent, 0, len(status.Events))
	for _, ev := range status.Events {
		if allEvents || ev.Type != "Normal" {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		return nil
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].LastSeen.After(events[j].LastSeen) })
	evRows := make([][]string, len(events))
	for i, ev := range events {
		last := ev.LastSeen
		evRows[i] = []string{
			// Event fields have no fixed format: sanitize all of them.
			formatAge(&last, now),
			client.SanitizeServerText(ev.Type),
			client.SanitizeServerText(ev.Reason),
			client.SanitizeServerText(ev.Object),
			strconv.Itoa(int(ev.Count)),
			client.SanitizeServerText(ev.Message),
		}
	}
	printer.PrintMessage("")
	printer.PrintMessage("Events:")
	return printer.PrintTable([]string{"LAST SEEN", "TYPE", "REASON", "OBJECT", "COUNT", "MESSAGE"}, evRows)
}

// execColumn returns the EXEC cell of each pod of a chart, in the order of
// ch.Pods, and the valid hint names that no pod gets. Each container goes
// only to the pod that "stack exec -c <name>" picks (#186): the pod named in
// exec_pods (#189), or, from an older server, the first pod by name that is
// Running and Ready and runs the container. The default container gets a
// trailing "*"; a pod without a container gets "-".
func execColumn(ch types.ChartStatus) (cells, unplaced []string) {
	pick := clientExecPod(ch)
	if ch.ExecPods != nil {
		pick = serverExecPod(ch)
	}

	byPod := make([][]string, len(ch.Pods))
	for _, name := range client.ExecContainerNames(ch.ExecContainers) {
		label := name
		if name == ch.ExecDefaultContainer {
			label += "*"
		}
		if i, ok := pick(name); ok {
			byPod[i] = append(byPod[i], label)
		} else {
			unplaced = append(unplaced, label)
		}
	}
	cells = make([]string, len(ch.Pods))
	for i, names := range byPod {
		cells[i] = "-"
		if len(names) > 0 {
			cells[i] = strings.Join(names, ", ")
		}
	}
	return cells, unplaced
}

// serverExecPod returns the index in ch.Pods of the pod that exec_pods
// names for a container.
func serverExecPod(ch types.ChartStatus) func(string) (int, bool) {
	return func(name string) (int, bool) {
		pod, ok := ch.ExecPods[name]
		if !ok || pod == "" {
			return 0, false
		}
		for i, p := range ch.Pods {
			if p.Name == pod {
				return i, true
			}
		}
		return 0, false
	}
}

// clientExecPod returns the index in ch.Pods of the first pod by name that
// is Running and Ready and runs the container. It cannot see a pod that is
// shutting down or tell init containers apart (servers without exec_pods).
func clientExecPod(ch types.ChartStatus) func(string) (int, bool) {
	ready := make([]int, 0, len(ch.Pods))
	for i, p := range ch.Pods {
		if podExecReady(p) {
			ready = append(ready, i)
		}
	}
	sort.SliceStable(ready, func(a, b int) bool { return ch.Pods[ready[a]].Name < ch.Pods[ready[b]].Name })
	return func(name string) (int, bool) {
		for _, i := range ready {
			if podRunsContainer(ch.Pods[i], name) {
				return i, true
			}
		}
		return 0, false
	}
}

// podExecReady reports whether "stack exec" can pick the pod: phase Running
// and the Ready condition True, as the server checks it.
func podExecReady(p types.PodStatus) bool {
	if p.Phase != "Running" {
		return false
	}
	for _, c := range p.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}

// podRunsContainer reports whether the pod has a container status of that name.
func podRunsContainer(p types.PodStatus, name string) bool {
	for _, cs := range p.ContainerStates {
		if cs.Name == name {
			return true
		}
	}
	return false
}

// readyText returns "ready/total" containers of a pod, or the pod Ready
// flag when the server sends no container states.
func readyText(p types.PodStatus) string {
	if len(p.ContainerStates) == 0 {
		return strconv.FormatBool(p.Ready)
	}
	ready := 0
	for _, cs := range p.ContainerStates {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(p.ContainerStates))
}

// podReason returns the first container reason of a pod (for example
// CrashLoopBackOff), or "-".
func podReason(p types.PodStatus) string {
	for _, cs := range p.ContainerStates {
		if cs.Reason != "" {
			return cs.Reason
		}
	}
	return "-"
}

var stackRecentCmd = &cobra.Command{
	Use:   "recent",
	Short: "List your 5 most recently updated stack instances",
	Long: `List the 5 stack instances that you own and that were updated last
(GET /api/v1/stack-instances/recent).

Examples:
  stackctl stack recent
  stackctl stack recent -q`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		items, err := c.ListRecentStacks()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, s := range items {
				fmt.Fprintln(printer.Writer, s.ID)
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(items)
		case output.FormatYAML:
			return printer.PrintYAML(items)
		default:
			if len(items) == 0 {
				printer.PrintMessage("You have no stack instances.")
				return nil
			}
			return printStackTable(items)
		}
	},
}

func init() {
	stackPreviewCmd.Flags().String("chart", "", "Show only this chart (the chart name); stdout is then plain YAML")
	stackPodsCmd.Flags().Bool("all-events", false, "Also show the Normal events")

	stackCmd.AddCommand(stackPreviewCmd)
	stackCmd.AddCommand(stackPodsCmd)
	stackCmd.AddCommand(stackRecentCmd)
}
