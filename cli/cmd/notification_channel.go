package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Notification channels: outbound webhooks for stack events (devops or
// admin). The webhook URL can hold a token, so the command masks it in
// every output mode unless --show-url is set. The secret goes to the
// server only; it is read from an environment variable or stdin, never from
// a flag value.

var notificationChannelCmd = &cobra.Command{
	Use:     "channel",
	Aliases: []string{"channels"},
	Short:   "Manage the outbound webhook channels (devops or admin)",
	Long: `Manage the outbound webhook channels of the server. A channel sends the
events that it subscribes to (for example deployment.success) to a webhook
URL, as generic JSON or as a Microsoft Teams Adaptive Card ("teams").

Filters limit a channel to some stack instances: instance name globs
(path.Match syntax; the match ignores case), owner IDs, definitions and
cluster IDs.
Each set filter must match; one value of a filter is enough.

The webhook URL can hold a token (for example the signature of a Teams
workflow URL). The commands show only the scheme and the host of the URL,
also with -o json and -o yaml. --show-url shows the full URL. To keep the
URL out of the shell history and the process list, give it with --url-env
NAME (read from that environment variable) instead of --url.`,
}

// channelOutputFlags adds --show-url to a command that prints a channel.
func channelOutputFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("show-url", false, "Show the full webhook URL (it can hold a token)")
}

// maskWebhookURL returns the scheme and the host (with the port) of a
// webhook URL, and "/..." when the URL has a path, a query or a fragment.
// User info is dropped. A URL that does not parse becomes "<hidden>".
func maskWebhookURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		if raw == "" {
			return ""
		}
		return "<hidden>"
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		out += "/..."
	}
	return out
}

// shownChannel returns a copy of ch with the webhook URL masked unless
// --show-url is set.
func shownChannel(cmd *cobra.Command, ch types.NotificationChannel) types.NotificationChannel {
	if show, _ := cmd.Flags().GetBool("show-url"); !show {
		ch.WebhookURL = maskWebhookURL(ch.WebhookURL)
	}
	return ch
}

// resolveChannelID returns the ID of the channel with the name or ID.
func resolveChannelID(c *client.Client, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return "", fmt.Errorf("channel name or ID must not be empty")
	}
	if looksLikeID(nameOrID) {
		return nameOrID, nil
	}
	channels, err := c.ListNotificationChannels()
	if err != nil {
		return "", fmt.Errorf("resolving channel %q: %w", nameOrID, err)
	}
	for _, ch := range channels {
		if ch.Name == nameOrID {
			return ch.ID, nil
		}
	}
	return "", fmt.Errorf("notification channel %q not found", nameOrID)
}

// filterSummary returns the filters of a channel for a table.
func filterSummary(f types.NotificationChannelFilters) string {
	var parts []string
	add := func(label string, values []string) {
		if len(values) > 0 {
			parts = append(parts, label+"="+strings.Join(values, "|"))
		}
	}
	add("name", f.InstanceNamePatterns)
	add("owner", f.OwnerIDs)
	add("definition", f.DefinitionIDs)
	add("cluster", f.ClusterIDs)
	if len(parts) == 0 {
		return "all instances"
	}
	return client.SanitizeServerText(strings.Join(parts, ", "))
}

// printChannel prints one channel. events is added to the table when it
// is not nil.
func printChannel(cmd *cobra.Command, ch types.NotificationChannel, events []string) error {
	ch = shownChannel(cmd, ch)
	for _, w := range ch.Warnings {
		printServerWarning(cmd, w)
	}
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, ch.ID)
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(ch)
	case output.FormatYAML:
		return printer.PrintYAML(ch)
	}
	fields := []output.KeyValue{
		{Key: "ID", Value: ch.ID},
		{Key: "Name", Value: client.SanitizeServerText(ch.Name)},
		{Key: "Webhook URL", Value: client.SanitizeServerText(ch.WebhookURL)},
		{Key: "Format", Value: client.SanitizeServerText(ch.Format)},
		{Key: "Enabled", Value: yesNo(ch.Enabled)},
		{Key: "Filters", Value: filterSummary(ch.Filters)},
	}
	if events != nil {
		ev := strings.Join(events, ", ")
		if ev == "" {
			ev = "none"
		}
		fields = append(fields, output.KeyValue{Key: "Events", Value: client.SanitizeServerText(ev)})
	}
	return printer.PrintSingle(ch, fields)
}

var notificationChannelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the notification channels",
	Long: `List the notification channels with their format, state, number of
subscribed events and filters. The webhook URL shows only the scheme and the
host unless --show-url is set.

Examples:
  stackctl notification channel list
  stackctl notification channel list -o json`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		channels, err := c.ListNotificationChannels()
		if err != nil {
			return err
		}
		for i := range channels {
			channels[i] = shownChannel(cmd, channels[i])
		}
		if printer.Quiet {
			for _, ch := range channels {
				fmt.Fprintln(printer.Writer, ch.ID)
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(channels)
		case output.FormatYAML:
			return printer.PrintYAML(channels)
		}
		if len(channels) == 0 {
			printer.PrintMessage("No notification channels.")
			return nil
		}
		rows := make([][]string, len(channels))
		for i, ch := range channels {
			count := "-"
			if ch.SubscriptionCount != nil {
				count = strconv.Itoa(*ch.SubscriptionCount)
			}
			rows[i] = []string{
				ch.ID, client.SanitizeServerText(ch.Name), client.SanitizeServerText(ch.Format), yesNo(ch.Enabled), count,
				filterSummary(ch.Filters), client.SanitizeServerText(ch.WebhookURL),
			}
		}
		return printer.PrintTable([]string{"ID", "NAME", "FORMAT", "ENABLED", "EVENTS", "FILTERS", "WEBHOOK URL"}, rows)
	},
}

var notificationChannelGetCmd = &cobra.Command{
	Use:   "get <channel>",
	Short: "Show a notification channel",
	Long: `Show a notification channel (name or ID). The table also lists the
subscribed events.

Examples:
  stackctl notification channel get team-alerts
  stackctl notification channel get team-alerts --show-url`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		ch, err := c.GetNotificationChannel(id)
		if err != nil {
			return err
		}
		var events []string
		if printer.Format == output.FormatTable && !printer.Quiet {
			if events, err = c.GetChannelSubscriptions(id); err != nil {
				return err
			}
			if events == nil {
				events = []string{}
			}
		}
		return printChannel(cmd, *ch, events)
	},
}

// channelFlags registers the flags of channel create and update.
func channelFlags(cmd *cobra.Command, update bool) {
	cmd.Flags().String("name", "", "Channel name")
	cmd.Flags().String("url", "", "Webhook URL (https:// only)")
	cmd.Flags().String("url-env", "", "Read the webhook URL from this environment variable (keeps it out of the shell history)")
	cmd.Flags().String("format", "", "Body format: generic (the event JSON) or teams (Teams Adaptive Card)")
	cmd.Flags().String("secret-env", "", "Read the HMAC secret from this environment variable (signs the requests)")
	cmd.Flags().Bool("secret-stdin", false, "Read the HMAC secret from stdin")
	cmd.Flags().StringArray("filter-name", nil, "Instance name glob, for example 'pr-*' (repeat the flag for more)")
	cmd.Flags().StringArray("filter-owner", nil, "Owner user ID (repeat the flag for more)")
	cmd.Flags().StringArray("filter-definition", nil, "Stack definition name or ID (repeat the flag for more)")
	cmd.Flags().StringArray("filter-cluster", nil, "Cluster ID (repeat the flag for more)")
	if update {
		cmd.Flags().Bool("clear-secret", false, "Remove the secret (the requests are not signed)")
		cmd.Flags().Bool("clear-filters", false, "Remove all filters (the channel gets the events of all instances)")
		cmd.Flags().Bool("enable", false, "Enable the channel")
		cmd.Flags().Bool("disable", false, "Disable the channel")
	} else {
		cmd.Flags().Bool("disabled", false, "Create the channel disabled")
		cmd.Flags().StringArray("event", nil, "Subscribe to this event type (repeat the flag for more; see 'notification channel events')")
	}
	channelOutputFlags(cmd)
}

// readSecret returns the secret of --secret-env or --secret-stdin. set is
// false when neither flag is given.
func readSecret(cmd *cobra.Command) (secret string, set bool, err error) {
	env, _ := cmd.Flags().GetString("secret-env")
	stdin, _ := cmd.Flags().GetBool("secret-stdin")
	switch {
	case env != "" && stdin:
		return "", false, fmt.Errorf("--secret-env and --secret-stdin cannot be used together")
	case env != "":
		v, ok := os.LookupEnv(env)
		if !ok || v == "" {
			return "", false, fmt.Errorf("--secret-env: the environment variable %s is not set or empty", env)
		}
		return v, true, nil
	case stdin:
		// On a terminal the input is hidden; from a pipe the first line counts.
		f, isFile := cmd.InOrStdin().(*os.File)
		line, err := readPassword(cmd, "Channel secret: ", !(isFile && term.IsTerminal(int(f.Fd()))))
		if err != nil {
			return "", false, fmt.Errorf("reading the secret from stdin: %w", err)
		}
		if line == "" {
			return "", false, fmt.Errorf("--secret-stdin: stdin has no secret")
		}
		return line, true, nil
	}
	return "", false, nil
}

// readFilterFlags returns the filters of the --filter-* flags, or nil
// when none is given. It checks the globs and resolves definition names.
func readFilterFlags(cmd *cobra.Command, c *client.Client) (*types.NotificationChannelFilters, error) {
	names, _ := cmd.Flags().GetStringArray("filter-name")
	owners, _ := cmd.Flags().GetStringArray("filter-owner")
	defs, _ := cmd.Flags().GetStringArray("filter-definition")
	clusters, _ := cmd.Flags().GetStringArray("filter-cluster")
	if len(names)+len(owners)+len(defs)+len(clusters) == 0 {
		return nil, nil
	}
	for _, p := range names {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("--filter-name %q is not a valid glob: %w", p, err)
		}
	}
	f := &types.NotificationChannelFilters{InstanceNamePatterns: names, OwnerIDs: owners, ClusterIDs: clusters}
	for _, d := range defs {
		id, err := resolveDefinitionID(c, d)
		if err != nil {
			return nil, err
		}
		f.DefinitionIDs = append(f.DefinitionIDs, id)
	}
	return f, nil
}

// readWebhookURL returns the webhook URL of --url or --url-env, checked
// before any request ("" when neither flag is given).
func readWebhookURL(cmd *cobra.Command) (string, error) {
	raw, _ := cmd.Flags().GetString("url")
	env, _ := cmd.Flags().GetString("url-env")
	switch {
	case raw != "" && env != "":
		return "", fmt.Errorf("--url and --url-env cannot be used together")
	case env != "":
		v, ok := os.LookupEnv(env)
		if !ok || strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("--url-env: the environment variable %s is not set or empty", env)
		}
		raw = v
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	// The error texts never contain the URL: it can hold a token.
	if !strings.HasPrefix(raw, "https://") {
		return "", fmt.Errorf("the webhook URL must start with https://")
	}
	if u, err := url.Parse(raw); err != nil || u.Host == "" {
		return "", fmt.Errorf("the webhook URL is not a valid URL")
	}
	return raw, nil
}

// checkChannelFormat checks --format before any request.
func checkChannelFormat(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("format") {
		return nil
	}
	if f, _ := cmd.Flags().GetString("format"); f != "generic" && f != "teams" {
		return fmt.Errorf("--format must be generic or teams, not %q", f)
	}
	return nil
}

// checkEventTypes refuses event types that the server does not know,
// before a channel is created. When the list cannot be read, the server
// checks the types at the subscription.
func checkEventTypes(c *client.Client, events []string) error {
	if len(events) == 0 {
		return nil
	}
	known, err := c.ListChannelEventTypes()
	if err != nil || len(known) == 0 {
		return nil
	}
	for _, e := range events {
		if !containsString(known, e) {
			return fmt.Errorf("unknown event type %q (see 'stackctl notification channel events')", e)
		}
	}
	return nil
}

var notificationChannelCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a notification channel",
	Long: `Create a notification channel. --event subscribes the channel to event
types in the same step (see "notification channel events").

The HMAC secret signs each request (X-StackManager-Signature). Give it with
--secret-env NAME (read from that environment variable) or --secret-stdin,
never as a flag value. The server does not return the secret.

The server saves a filter ID that does not exist, and the command writes
its warning to stderr.

Examples:
  stackctl notification channel create --name team-alerts --url https://hooks.example.com/abc --event deployment.failed
  STACKCTL_CHANNEL_SECRET=... stackctl notification channel create --name ci --url https://ci.example.com/hook --secret-env STACKCTL_CHANNEL_SECRET
  stackctl notification channel create --name pr-stacks --url https://example.webhook.office.com/... --format teams --filter-name 'pr-*'`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		format, _ := cmd.Flags().GetString("format")
		disabled, _ := cmd.Flags().GetBool("disabled")
		events, _ := cmd.Flags().GetStringArray("event")
		webhook, err := readWebhookURL(cmd)
		if err != nil {
			return err
		}
		if strings.TrimSpace(name) == "" || webhook == "" {
			return fmt.Errorf("--name and --url (or --url-env) are required")
		}
		if err := checkChannelFormat(cmd); err != nil {
			return err
		}
		secret, _, err := readSecret(cmd)
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		filters, err := readFilterFlags(cmd, c)
		if err != nil {
			return err
		}
		if err := checkEventTypes(c, events); err != nil {
			return err
		}
		enabled := !disabled
		ch, err := c.CreateNotificationChannel(&types.CreateNotificationChannelRequest{
			Name: name, WebhookURL: webhook, Secret: secret,
			Enabled: &enabled, Format: format, Filters: filters,
		})
		if err != nil {
			return err
		}
		var subscribed []string
		if len(events) > 0 {
			if subscribed, err = c.SetChannelSubscriptions(ch.ID, events); err != nil {
				// The channel exists: show it, then say how to subscribe.
				if perr := printChannel(cmd, *ch, nil); perr != nil {
					return perr
				}
				return fmt.Errorf("the channel %s is created, but the subscription failed: %w. Run: stackctl notification channel subscribe %s --event <type>",
					ch.ID, err, ch.ID)
			}
		}
		return printChannel(cmd, *ch, subscribed)
	},
}

var notificationChannelUpdateCmd = &cobra.Command{
	Use:   "update <channel>",
	Short: "Change a notification channel",
	Long: `Change a notification channel (name or ID). Only the given flags change.

A --filter-* flag replaces all filters of the channel with the given
filters. --clear-filters removes all filters. --secret-env or --secret-stdin
sets a new secret; --clear-secret removes it.

Examples:
  stackctl notification channel update team-alerts --disable
  stackctl notification channel update team-alerts --format teams --filter-cluster 5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e
  stackctl notification channel update team-alerts --clear-filters`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		webhook, err := readWebhookURL(cmd)
		if err != nil {
			return err
		}
		if err := checkChannelFormat(cmd); err != nil {
			return err
		}
		enable, _ := cmd.Flags().GetBool("enable")
		disable, _ := cmd.Flags().GetBool("disable")
		clearSecret, _ := cmd.Flags().GetBool("clear-secret")
		clearFilters, _ := cmd.Flags().GetBool("clear-filters")
		if enable && disable {
			return fmt.Errorf("--enable and --disable cannot be used together")
		}
		secret, secretSet, err := readSecret(cmd)
		if err != nil {
			return err
		}
		if clearSecret && secretSet {
			return fmt.Errorf("--clear-secret cannot be used with --secret-env or --secret-stdin")
		}

		req := &types.UpdateNotificationChannelRequest{}
		req.Name, _ = cmd.Flags().GetString("name")
		req.WebhookURL = webhook
		if cmd.Flags().Changed("format") {
			f, _ := cmd.Flags().GetString("format")
			req.Format = &f
		}
		if secretSet {
			req.Secret = &secret
		} else if clearSecret {
			empty := ""
			req.Secret = &empty
		}
		if enable || disable {
			req.Enabled = &enable
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		filters, err := readFilterFlags(cmd, c)
		if err != nil {
			return err
		}
		switch {
		case filters != nil && clearFilters:
			return fmt.Errorf("--clear-filters cannot be used with a --filter-* flag")
		case filters != nil:
			req.Filters = filters
		case clearFilters:
			req.Filters = &types.NotificationChannelFilters{}
		}
		if *req == (types.UpdateNotificationChannelRequest{}) {
			return fmt.Errorf("nothing to update: set at least one flag")
		}

		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		ch, err := c.UpdateNotificationChannel(id, req)
		if err != nil {
			return err
		}
		return printChannel(cmd, *ch, nil)
	},
}

var notificationChannelDeleteCmd = &cobra.Command{
	Use:   "delete <channel>",
	Short: "Delete a notification channel",
	Long: `Delete a notification channel (name or ID) and its subscriptions. The
command asks for confirmation unless --yes is set.

Examples:
  stackctl notification channel delete team-alerts
  stackctl notification channel delete team-alerts --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		ok, err := confirmAction(cmd, fmt.Sprintf("Delete notification channel %s? (y/n): ", args[0]))
		if err != nil {
			return err
		}
		if !ok {
			return errAborted
		}
		if err := c.DeleteNotificationChannel(id); err != nil {
			return err
		}
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}
		printer.PrintMessage("Deleted notification channel %s", id)
		return nil
	},
}

// printEventTypes prints a list of event types, one per line in table mode.
func printEventTypes(events []string) error {
	if events == nil {
		events = []string{}
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(map[string][]string{"event_types": events})
	case output.FormatYAML:
		return printer.PrintYAML(map[string][]string{"event_types": events})
	}
	for _, e := range events {
		fmt.Fprintln(printer.Writer, client.SanitizeServerText(e))
	}
	return nil
}

var notificationChannelEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List the event types that a channel can subscribe to",
	Args:  cobra.NoArgs,
	Long: `List the event types that a channel can subscribe to, one per line.

Examples:
  stackctl notification channel events`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		events, err := c.ListChannelEventTypes()
		if err != nil {
			return err
		}
		return printEventTypes(events)
	},
}

var notificationChannelSubscriptionsCmd = &cobra.Command{
	Use:   "subscriptions <channel>",
	Short: "List the event types of a channel",
	Long: `List the event types that a channel (name or ID) subscribes to, one per
line.

Examples:
  stackctl notification channel subscriptions team-alerts`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		events, err := c.GetChannelSubscriptions(id)
		if err != nil {
			return err
		}
		return printEventTypes(events)
	},
}

var notificationChannelSubscribeCmd = &cobra.Command{
	Use:   "subscribe <channel>",
	Short: "Set the event types of a channel",
	Long: `Set the event types of a channel (name or ID). The given --event list
replaces all subscriptions of the channel. --none removes all
subscriptions. An unknown event type gives an error of the server.

Examples:
  stackctl notification channel subscribe team-alerts --event deployment.success --event deployment.failed
  stackctl notification channel subscribe team-alerts --none`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		events, _ := cmd.Flags().GetStringArray("event")
		none, _ := cmd.Flags().GetBool("none")
		switch {
		case none && len(events) > 0:
			return fmt.Errorf("--none cannot be used with --event")
		case !none && len(events) == 0:
			return fmt.Errorf("--event or --none is required")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		stored, err := c.SetChannelSubscriptions(id, events)
		if err != nil {
			return err
		}
		return printEventTypes(stored)
	},
}

var notificationChannelTestCmd = &cobra.Command{
	Use:   "test <channel>",
	Short: "Send a test event to a channel",
	Long: `Send a test event (event type "test") to the webhook of a channel (name
or ID), with one attempt, in the format of the channel. The server writes
the send to the delivery log. A disabled channel can be tested; the command
then writes a note to stderr. The exit code is non-zero when the webhook
did not accept the event.

Examples:
  stackctl notification channel test team-alerts`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		res, err := c.TestNotificationChannel(id)
		if err != nil {
			return err
		}
		if !res.ChannelEnabled {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: the channel is disabled; it gets no events until you enable it.")
		}
		msg := client.SanitizeServerText(res.Message)
		switch {
		case printer.Quiet:
		case printer.Format == output.FormatJSON:
			if err := printer.PrintJSON(res); err != nil {
				return err
			}
		case printer.Format == output.FormatYAML:
			if err := printer.PrintYAML(res); err != nil {
				return err
			}
		case res.Success:
			printer.PrintMessage("Test succeeded: status %d. %s", res.StatusCode, msg)
		}
		if !res.Success {
			return fmt.Errorf("the test event to channel %s failed (status %d): %s", args[0], res.StatusCode, msg)
		}
		return nil
	},
}

var notificationChannelLogsCmd = &cobra.Command{
	Use:   "logs <channel>",
	Short: "Show the delivery log of a channel",
	Long: `Show the webhook deliveries of a channel (name or ID), newest first.

Examples:
  stackctl notification channel logs team-alerts
  stackctl notification channel logs team-alerts --limit 50 --offset 50`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		if limit < 1 || limit > 100 {
			return fmt.Errorf("--limit must be 1 to 100")
		}
		if offset < 0 {
			return fmt.Errorf("--offset must be 0 or more")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveChannelID(c, args[0])
		if err != nil {
			return err
		}
		page, err := c.ListChannelDeliveryLogs(id, limit, offset)
		if err != nil {
			return err
		}
		if printer.Quiet {
			for _, l := range page.Logs {
				fmt.Fprintln(printer.Writer, l.ID)
			}
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(page)
		case output.FormatYAML:
			return printer.PrintYAML(page)
		}
		if len(page.Logs) == 0 {
			printer.PrintMessage("No deliveries.")
			return nil
		}
		rows := make([][]string, len(page.Logs))
		for i, l := range page.Logs {
			code := "-"
			if l.StatusCode != 0 {
				code = strconv.Itoa(l.StatusCode)
			}
			rows[i] = []string{
				l.CreatedAt.Format(time.RFC3339), client.SanitizeServerText(l.EventType),
				printer.StatusColor(client.SanitizeServerText(l.Status)), code, valueOrDash(client.SanitizeServerText(l.ErrorMessage)),
			}
		}
		if err := printer.PrintTable([]string{"TIME", "EVENT", "STATUS", "CODE", "ERROR"}, rows); err != nil {
			return err
		}
		if shown := offset + len(page.Logs); int64(shown) < page.Total {
			fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d to %d of %d. Use --offset %d to see more.\n", offset+1, shown, page.Total, shown)
		}
		return nil
	},
}

func init() {
	channelOutputFlags(notificationChannelListCmd)
	channelOutputFlags(notificationChannelGetCmd)
	channelFlags(notificationChannelCreateCmd, false)
	channelFlags(notificationChannelUpdateCmd, true)
	notificationChannelDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	notificationChannelSubscribeCmd.Flags().StringArray("event", nil, "Event type (repeat the flag for more)")
	notificationChannelSubscribeCmd.Flags().Bool("none", false, "Remove all subscriptions")
	notificationChannelLogsCmd.Flags().Int("limit", 20, "Number of entries (1 to 100)")
	notificationChannelLogsCmd.Flags().Int("offset", 0, "Number of entries to skip")

	notificationChannelCmd.AddCommand(notificationChannelListCmd, notificationChannelGetCmd,
		notificationChannelCreateCmd, notificationChannelUpdateCmd, notificationChannelDeleteCmd,
		notificationChannelEventsCmd, notificationChannelSubscriptionsCmd, notificationChannelSubscribeCmd,
		notificationChannelTestCmd, notificationChannelLogsCmd)
	notificationCmd.AddCommand(notificationChannelCmd)
}
