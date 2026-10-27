package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// Job log polling, as the web UI does it: a poll every 3 s, at once again
// after a truncated chunk, and a doubling backoff up to 30 s on 429, 502,
// 503, 504 and network errors (Retry-After when the server sends it).
const (
	jobPollInterval  = 3 * time.Second
	jobMaxBackoff    = 30 * time.Second
	jobMaxRetryAfter = 5 * time.Minute
	jobDefaultWait   = 60 * time.Minute
	actionLogHintFmt = "stackctl stack action log %s %s %s --follow"
)

// jobSleep waits between job log polls. Tests replace it.
var jobSleep = time.Sleep

// jobSuccessStatuses are the final job statuses that count as success.
// Another final status (for example failed or recovery_required) gives a
// non-zero exit code. An empty status counts as success: the subscriber
// does not report one.
var jobSuccessStatuses = map[string]bool{
	"": true, "succeeded": true, "success": true, "successful": true,
	"completed": true, "complete": true, "done": true, "ok": true, "finished": true,
}

var stackActionCmd = &cobra.Command{
	Use:   "action",
	Short: "List, run and follow the custom actions of a stack",
	Long: `The custom actions come from the hooks config of the server
(hooks.actions). An action calls a webhook of the operator, for example to
refresh a database. An asynchronous action returns a job ID, and the server
proxies its job log.

Every user who can view a stack can list its actions. Only the owner of the
stack, a devops user and an admin can run an action and read a job log.`,
}

var stackActionListCmd = &cobra.Command{
	Use:   "list <name|id>",
	Short: "List the custom actions of a stack",
	Long: `List the custom actions of a stack with their parameters.

PARAMETERS shows each declared parameter as name:type, with the options of
an enum, "*" for a required parameter and "=value" for a default. CAN RUN
says whether you can run the action on this stack. JOB LOG says whether the
server proxies the job log of the action.

Examples:
  stackctl stack action list my-stack
  stackctl stack action list my-stack -o json`,
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
		list, err := c.ListStackActions(id)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, a := range list.Actions {
				fmt.Fprintln(printer.Writer, a.Name)
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(list)
		case output.FormatYAML:
			return printer.PrintYAML(list)
		}
		if len(list.Actions) == 0 {
			printer.PrintMessage("This stack has no custom actions.")
			if list.ScopeIncomplete {
				printer.PrintMessage(msgActionsIncomplete)
			}
			return nil
		}
		rows := make([][]string, len(list.Actions))
		for i, a := range list.Actions {
			rows[i] = []string{
				client.SanitizeServerText(a.Name),
				client.SanitizeServerText(a.Label),
				yesNo(a.CanInvoke),
				yesNo(a.HasJobLog),
				client.SanitizeServerText(parameterSummary(a.Parameters)),
			}
		}
		if err := printer.PrintTable([]string{"NAME", "LABEL", "CAN RUN", "JOB LOG", "PARAMETERS"}, rows); err != nil {
			return err
		}
		if list.ScopeIncomplete {
			printer.PrintMessage(msgActionsIncomplete)
		}
		return nil
	},
}

// msgActionsIncomplete tells that the server left out actions it could not
// check for the stack (scope_incomplete).
const msgActionsIncomplete = "Some actions are not shown: the server could not check where they apply. Try again."

// parameterSummary returns the parameters of an action for the table, for
// example "env:enum(se|dk)*=se, dry_run:bool".
func parameterSummary(params []types.ActionParameter) string {
	if len(params) == 0 {
		return "-"
	}
	parts := make([]string, len(params))
	for i, p := range params {
		s := p.Name + ":" + p.Type
		if p.Type == "enum" && len(p.Options) > 0 {
			s = p.Name + ":enum(" + strings.Join(p.Options, "|") + ")"
		}
		if p.Required {
			s += "*"
		}
		if p.Default != nil {
			s += "=" + fmt.Sprint(p.Default)
		}
		parts[i] = s
	}
	return strings.Join(parts, ", ")
}

var stackActionRunCmd = &cobra.Command{
	Use:   "run <name|id> <action>",
	Short: "Run a custom action on a stack",
	Long: `Run a custom action on a stack.

--param key=value sets a parameter (repeat the flag for more parameters).
The command checks the declared parameters before the request: a bool takes
true or false, an enum takes one of its options, and a required parameter
must have a value. A declared parameter without --param gets its default.
Parameters that the action does not declare go to the subscriber as strings.

When the action has a confirmation text, the command asks for confirmation.
--yes skips the question.

The command prints the status of the subscriber and its result. A status of
400 or higher gives a non-zero exit code.

An asynchronous action returns a job ID. --wait then follows the job log
until the job ends: it polls every 3 s and prints the new log text. The exit
code is non-zero when the job ends with a failed status, on 401, 403 or 404,
and after --timeout (default 60m). The job continues on the server after
the command stops. With -o json or -o yaml, the log text goes to stderr and
the result has job_status.

Examples:
  stackctl stack action list my-stack
  stackctl stack action run my-stack refresh-db --yes
  stackctl stack action run my-stack refresh-db --param market=dk --wait
  stackctl stack action run my-stack restart --param dry_run=true -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		rawParams, _ := cmd.Flags().GetStringArray("param")
		wait, _ := cmd.Flags().GetBool("wait")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if timeout <= 0 {
			return fmt.Errorf("--timeout must be a positive duration")
		}
		given, err := parseParamFlags(rawParams)
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		list, err := c.ListStackActions(id)
		if err != nil {
			return err
		}
		action, err := findAction(list, args[1])
		if err != nil {
			return err
		}
		if !action.CanInvoke {
			return fmt.Errorf("you cannot run actions on stack %s: only the owner, a devops user and an admin can", args[0])
		}
		params, err := buildActionParams(action.Parameters, given)
		if err != nil {
			return err
		}

		if action.Confirm != "" {
			ok, err := confirmAction(cmd, client.SanitizeServerText(action.Confirm)+" (y/n): ")
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
				return nil
			}
		}

		res, err := c.RunStackAction(id, action.Name, params)
		if err != nil {
			return err
		}
		failed := actionFailed(res)

		if wait && res.JobID != "" && failed == nil {
			// The job ID goes to stderr first: after Ctrl-C the user can
			// still follow the job with "stack action log".
			fmt.Fprintf(cmd.ErrOrStderr(), "Action %s: status %d. Job ID: %s. Following the job log; the job continues on the server if you stop the command.\n",
				res.Action, res.StatusCode, res.JobID)
			status, waitErr := followJobLog(cmd, c, id, action.Name, res.JobID, 0, timeout, printer.Format != output.FormatTable || printer.Quiet)
			res.JobStatus = status
			if waitErr == nil || status != "" {
				if err := printActionResult(cmd, res, true); err != nil {
					return err
				}
			}
			return waitErr
		}

		if err := printActionResult(cmd, res, false); err != nil {
			return err
		}
		if failed != nil {
			return failed
		}
		if wait && res.JobID == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: the action returned no job ID; there is no job log to follow.")
		}
		return nil
	},
}

// parseParamFlags reads the --param key=value flags.
func parseParamFlags(raw []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range raw {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("--param %q: use key=value", kv)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--param %s is set more than once", k)
		}
		out[k] = v
	}
	return out, nil
}

// findAction returns the action with the given name. The list holds the
// actions that apply to the stack: a server can limit an action to some
// templates or clusters (applies_to).
func findAction(list *types.ActionList, name string) (types.ActionInfo, error) {
	names := make([]string, 0, len(list.Actions))
	for _, a := range list.Actions {
		if a.Name == name {
			return a, nil
		}
		names = append(names, a.Name)
	}
	hint := ""
	if list.ScopeIncomplete {
		hint = "; some actions could not be checked for this stack, try again"
	}
	if len(names) == 0 {
		return types.ActionInfo{}, fmt.Errorf("unknown action %q: this stack has no custom actions%s", name, hint)
	}
	return types.ActionInfo{}, fmt.Errorf("unknown action %q (actions of this stack: %s)%s", name, strings.Join(names, ", "), hint)
}

// buildActionParams checks the given parameters against the declared ones
// and converts them to their types. A declared parameter that is not given
// gets its default. Undeclared parameters stay strings.
func buildActionParams(declared []types.ActionParameter, given map[string]string) (map[string]any, error) {
	params := map[string]any{}
	known := map[string]bool{}
	for _, p := range declared {
		known[p.Name] = true
		raw, ok := given[p.Name]
		if !ok {
			if p.Default != nil {
				params[p.Name] = p.Default
			} else if p.Required {
				return nil, fmt.Errorf("parameter %q is required: use --param %s=<value>", p.Name, p.Name)
			}
			continue
		}
		switch p.Type {
		case "bool":
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("parameter %q must be true or false, not %q", p.Name, raw)
			}
			params[p.Name] = b
		case "enum":
			if !containsString(p.Options, raw) {
				return nil, fmt.Errorf("parameter %q must be one of %s, not %q", p.Name, strings.Join(p.Options, ", "), raw)
			}
			params[p.Name] = raw
		default:
			if p.Required && strings.TrimSpace(raw) == "" {
				return nil, fmt.Errorf("parameter %q is required and cannot be empty", p.Name)
			}
			params[p.Name] = raw
		}
	}
	extra := make([]string, 0, len(given))
	for k := range given {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		params[k] = given[k]
	}
	return params, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// actionFailed returns an error when the subscriber answered with a status
// of 400 or higher.
func actionFailed(res *types.ActionResult) error {
	if res.StatusCode < 400 {
		return nil
	}
	msg := client.SanitizeServerText(res.Message)
	if msg == "" {
		msg = http.StatusText(res.StatusCode)
	}
	return fmt.Errorf("action %s failed: status %d: %s", res.Action, res.StatusCode, msg)
}

// printActionResult prints the envelope of "stack action run". With
// waited, the job log is already printed (table mode).
func printActionResult(cmd *cobra.Command, res *types.ActionResult, waited bool) error {
	if printer.Quiet {
		if res.JobID != "" {
			fmt.Fprintln(printer.Writer, res.JobID)
		}
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(res)
	case output.FormatYAML:
		return printer.PrintYAML(res)
	}

	if waited {
		printer.PrintMessage("Job %s ended with status %s", res.JobID, statusText(res.JobStatus))
		return nil
	}
	printer.PrintMessage("Action %s: status %d", res.Action, res.StatusCode)
	if msg := client.SanitizeServerText(res.Message); msg != "" {
		printer.PrintMessage("Message: %s", msg)
	}
	if res.Result != nil {
		var data bytes.Buffer
		enc := json.NewEncoder(&data)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res.Result); err != nil {
			return err
		}
		printer.PrintMessage("Result:\n%s", strings.TrimRight(client.SanitizeLogText(data.String()), "\n"))
	}
	if res.JobID != "" {
		printer.PrintMessage("Job ID: %s", res.JobID)
		fmt.Fprintf(cmd.ErrOrStderr(), "Follow the job log: "+actionLogHintFmt+"\n", res.InstanceID, res.Action, res.JobID)
	}
	return nil
}

// statusText returns the status, or "unknown" for an empty status.
func statusText(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var stackActionLogCmd = &cobra.Command{
	Use:   "log <name|id> <action> <job-id>",
	Short: "Show the job log of an action",
	Long: `Show the job log of an asynchronous action from --offset (bytes, default
0). The log text goes to stdout, the job status to stderr.

--follow polls until the job ends, as "stack action run --wait" does. The
exit code is non-zero when the job ends with a failed status, on 401, 403
or 404, and after --timeout (default 60m).

-o json and -o yaml print one log chunk with status, next_offset, done and
truncated (not with --follow).

Examples:
  stackctl stack action log my-stack refresh-db job-1a2b3c
  stackctl stack action log my-stack refresh-db job-1a2b3c --follow
  stackctl stack action log my-stack refresh-db job-1a2b3c -o json`,
	Args:         cobra.ExactArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		follow, _ := cmd.Flags().GetBool("follow")
		offset, _ := cmd.Flags().GetInt64("offset")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if offset < 0 {
			return fmt.Errorf("--offset must be 0 or more")
		}
		if timeout <= 0 {
			return fmt.Errorf("--timeout must be a positive duration")
		}
		if follow && printer.Format != output.FormatTable {
			return fmt.Errorf("--follow prints log text; it cannot be used with -o %s", printer.Format)
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveStackID(c, args[0])
		if err != nil {
			return err
		}
		action, jobID := args[1], args[2]

		if follow {
			status, err := followJobLog(cmd, c, id, action, jobID, offset, timeout, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Job %s ended with status %s\n", jobID, statusText(status))
			return nil
		}

		chunk, err := c.GetActionJobLog(id, action, jobID, offset)
		if err != nil {
			return err
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(chunk)
		case output.FormatYAML:
			return printer.PrintYAML(chunk)
		}
		fmt.Fprint(printer.Writer, client.SanitizeLogText(chunk.Log))
		state := "running"
		if chunk.Done {
			state = "done"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Status: %s (%s, next offset %d)\n", statusText(client.SanitizeServerText(chunk.Status)), state, chunk.NextOffset)
		return nil
	},
}

// followJobLog polls the job log from offset until the job is done and
// returns the final status. The log text goes to stdout, or to stderr when
// logToStderr is set (JSON, YAML and quiet output). A failed final status,
// 401, 403, 404 and the timeout give an error.
func followJobLog(cmd *cobra.Command, c *client.Client, id, action, jobID string, offset int64, timeout time.Duration, logToStderr bool) (string, error) {
	logOut := printer.Writer
	if logToStderr {
		logOut = cmd.ErrOrStderr()
	}
	// The poll loop has its own backoff: no retry inside the client.
	c.RetryBackoff = []time.Duration{}
	deadline := nowFunc().Add(timeout)
	backoff := time.Duration(0)
	hint := fmt.Sprintf(actionLogHintFmt, id, action, jobID)

	for {
		chunk, err := c.GetActionJobLog(id, action, jobID, offset)
		if err != nil {
			if !transientJobLogError(err) {
				return "", fmt.Errorf("reading the job log: %w", err)
			}
			backoff = nextBackoff(backoff, err)
			if !nowFunc().Add(backoff).Before(deadline) {
				return "", fmt.Errorf("timed out after %s; the job continues on the server. Run: %s", timeout, hint)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "The job log is not available (%s). Next try in %s.\n", shortError(err), backoff)
			jobSleep(backoff)
			continue
		}
		backoff = 0
		if chunk.Log != "" {
			fmt.Fprint(logOut, client.SanitizeLogText(chunk.Log))
		}
		if chunk.NextOffset > offset {
			offset = chunk.NextOffset
		}
		if chunk.Done {
			status := client.SanitizeServerText(chunk.Status)
			if !jobSuccessStatuses[strings.ToLower(status)] {
				return status, fmt.Errorf("job %s ended with status %s", jobID, status)
			}
			return status, nil
		}
		if chunk.Truncated {
			if !nowFunc().Before(deadline) {
				return "", fmt.Errorf("timed out after %s; the job continues on the server. Run: %s", timeout, hint)
			}
			continue
		}
		if !nowFunc().Add(jobPollInterval).Before(deadline) {
			return "", fmt.Errorf("timed out after %s; the job continues on the server. Run: %s", timeout, hint)
		}
		jobSleep(jobPollInterval)
	}
}

// nextBackoff returns the next wait after a transient error: Retry-After
// when the server sends it, else a doubling delay from 3 s, at most 30 s.
func nextBackoff(prev time.Duration, err error) time.Duration {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if ra := apiErr.RetryAfter(); ra > 0 {
			return min(ra, jobMaxRetryAfter)
		}
	}
	if prev == 0 {
		return jobPollInterval
	}
	return min(prev*2, jobMaxBackoff)
}

// transientJobLogError reports whether a job log error is worth a retry:
// 429, 502, 503 and 504, and network errors. Other API errors (401, 403,
// 404) and errors such as a response that is not JSON stop the poll.
func transientJobLogError(err error) bool {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	var netErr net.Error
	var urlErr *url.Error
	return errors.As(err, &netErr) || errors.As(err, &urlErr)
}

// shortError returns the HTTP status of an API error, else the error text.
func shortError(err error) string {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("status %d", apiErr.StatusCode)
	}
	return client.SanitizeServerText(err.Error())
}

func init() {
	stackActionRunCmd.Flags().StringArray("param", nil, "Parameter as key=value (repeat for more parameters)")
	stackActionRunCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation question")
	stackActionRunCmd.Flags().Bool("wait", false, "Follow the job log of an asynchronous action until the job ends")
	stackActionRunCmd.Flags().Duration("timeout", jobDefaultWait, "Maximum time to follow the job log (with --wait)")

	stackActionLogCmd.Flags().BoolP("follow", "f", false, "Poll the job log until the job ends")
	stackActionLogCmd.Flags().Int64("offset", 0, "Byte offset to read from")
	stackActionLogCmd.Flags().Duration("timeout", jobDefaultWait, "Maximum time to follow the job log (with --follow)")

	stackActionCmd.AddCommand(stackActionListCmd)
	stackActionCmd.AddCommand(stackActionRunCmd)
	stackActionCmd.AddCommand(stackActionLogCmd)
	stackCmd.AddCommand(stackActionCmd)
}
