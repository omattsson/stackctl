package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// notificationCmd is the top-level group. The `prefs` subgroup mirrors the
// backend grouping of /api/v1/notifications/preferences and leaves the
// single-noun verbs (list/count/read) at the same level as the API's
// path structure.
var notificationCmd = &cobra.Command{
	Use:     "notification",
	Aliases: []string{"notifications"},
	Short:   "List, mark, and configure in-app notifications",
	Long: `Read and manage the authenticated user's in-app notifications.

  list      — paginated list (table/json/yaml/quiet)
  count     — unread count for the current user (badge value)
  read      — mark a single notification as read
  read-all  — mark every notification as read
  prefs get — list notification preferences
  prefs set — update notification preferences from a JSON file

All endpoints require an authenticated session and operate on the calling
user's own data (ownership is enforced server-side).`,
}

var (
	notifFlagUnreadOnly bool
	notifFlagLimit      int
	notifFlagOffset     int

	notifPrefsFlagFile string
)

var notificationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List notifications",
	Long: `List the authenticated user's notifications.

The server caps the page size at 100. Use --unread-only to filter to unread
notifications. The table footer reports the unread count (badge value)
returned by the server alongside the page; --quiet prints just the IDs.

Examples:
  stackctl notification list
  stackctl notification list --unread-only --limit 50
  stackctl notification list -o json
  stackctl notification list --quiet`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		page, err := c.ListNotifications(client.NotificationListParams{
			UnreadOnly: notifFlagUnreadOnly,
			Limit:      notifFlagLimit,
			Offset:     notifFlagOffset,
		})
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, n := range page.Notifications {
				fmt.Fprintln(printer.Writer, n.ID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(page)
		case output.FormatYAML:
			return printer.PrintYAML(page)
		default:
			if len(page.Notifications) == 0 {
				printer.PrintMessage("No notifications.")
				return nil
			}
			headers := []string{"ID", "TYPE", "READ", "CREATED", "TITLE"}
			rows := make([][]string, len(page.Notifications))
			for i, n := range page.Notifications {
				rows[i] = []string{
					n.ID,
					n.Type,
					strconv.FormatBool(n.IsRead),
					n.CreatedAt.UTC().Format("2006-01-02 15:04"),
					truncate(n.Title, 60),
				}
			}
			if err := printer.PrintTable(headers, rows); err != nil {
				return err
			}
			printer.PrintMessage("(%d unread of %d total)", page.UnreadCount, page.Total)
			return nil
		}
	},
}

var notificationCountCmd = &cobra.Command{
	Use:   "count",
	Short: "Print the unread notification count",
	Long: `Print the unread notification count for the authenticated user.

In table mode (default) the output is a single integer suitable for shell
scripting; -o json wraps it in {"unread_count": N} matching the server
response shape.

Examples:
  stackctl notification count          # 7
  stackctl notification count -o json  # {"unread_count":7}`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		count, err := c.CountUnreadNotifications()
		if err != nil {
			return err
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(types.UnreadCountResponse{UnreadCount: count})
		case output.FormatYAML:
			return printer.PrintYAML(types.UnreadCountResponse{UnreadCount: count})
		default:
			fmt.Fprintln(printer.Writer, count)
			return nil
		}
	},
}

var notificationReadCmd = &cobra.Command{
	Use:   "read <id>",
	Short: "Mark a notification as read",
	Long: `Mark a single notification as read. The backend verifies ownership
and returns 404 (surfaced as "Resource not found") if the notification
belongs to a different user or does not exist.

Examples:
  stackctl notification read abc-123`,
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
		if err := c.MarkNotificationAsRead(id); err != nil {
			return err
		}
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
		} else {
			printer.PrintMessage("Marked notification %s as read.", id)
		}
		return nil
	},
}

var notificationReadAllCmd = &cobra.Command{
	Use:   "read-all",
	Short: "Mark every notification as read",
	Long: `Mark every notification for the authenticated user as read in
a single request.

Examples:
  stackctl notification read-all`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		if err := c.MarkAllNotificationsAsRead(); err != nil {
			return err
		}
		if !printer.Quiet {
			printer.PrintMessage("Marked all notifications as read.")
		}
		return nil
	},
}

var notificationPrefsCmd = &cobra.Command{
	Use:   "prefs",
	Short: "Get/set notification preferences",
}

var notificationPrefsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "List notification preferences",
	Long: `List the authenticated user's notification preferences (event
type, enabled flag, delivery channel).

Examples:
  stackctl notification prefs get
  stackctl notification prefs get -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		prefs, err := c.GetNotificationPreferences()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, p := range prefs {
				fmt.Fprintln(printer.Writer, p.EventType)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(prefs)
		case output.FormatYAML:
			return printer.PrintYAML(prefs)
		default:
			if len(prefs) == 0 {
				printer.PrintMessage("No notification preferences configured.")
				return nil
			}
			headers := []string{"EVENT TYPE", "ENABLED", "CHANNEL"}
			rows := make([][]string, len(prefs))
			for i, p := range prefs {
				rows[i] = []string{p.EventType, strconv.FormatBool(p.Enabled), p.Channel}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var notificationPrefsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update notification preferences from a JSON file",
	Long: `Update notification preferences. The file must contain a JSON
array of preference objects:

  [
    {"event_type": "stack.deploy.failed", "enabled": true, "channel": "in_app"},
    {"event_type": "stack.deploy.succeeded", "enabled": false}
  ]

The "channel" field is optional and defaults to "in_app" server-side.
An empty array, or any element with an empty event_type, is rejected
with HTTP 400.

The roundtrip "prefs get -o json | edit | prefs set --from-file -" works
because GET returns the same shape PUT accepts (with id and user_id
ignored on the wire). Pass "-" as the filename to read from stdin.

Examples:
  stackctl notification prefs set --from-file prefs.json
  stackctl notification prefs get -o json > p.json && stackctl notification prefs set --from-file p.json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if notifPrefsFlagFile == "" {
			return fmt.Errorf("--from-file is required")
		}

		var data []byte
		if notifPrefsFlagFile == "-" {
			b, err := readAllStdin(cmd)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
			data = b
		} else {
			for _, segment := range strings.Split(filepath.ToSlash(notifPrefsFlagFile), "/") {
				if segment == ".." {
					return fmt.Errorf("input file path must not contain '..' segments")
				}
			}
			b, err := os.ReadFile(filepath.Clean(notifPrefsFlagFile))
			if err != nil {
				return fmt.Errorf("reading %s: %w", notifPrefsFlagFile, err)
			}
			data = b
		}

		var prefs []types.NotificationPreference
		if err := json.Unmarshal(data, &prefs); err != nil {
			return fmt.Errorf("invalid JSON in %s: %w", notifPrefsFlagFile, err)
		}
		if len(prefs) == 0 {
			return fmt.Errorf("at least one preference is required")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		updated, err := c.UpdateNotificationPreferences(prefs)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, p := range updated {
				fmt.Fprintln(printer.Writer, p.EventType)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(updated)
		case output.FormatYAML:
			return printer.PrintYAML(updated)
		default:
			printer.PrintMessage("Updated %d notification preferences.", len(updated))
			return nil
		}
	},
}

// readAllStdin reads cmd.InOrStdin() into a byte slice. Extracted so the
// notification prefs set command can be unit-tested with a custom reader.
func readAllStdin(cmd *cobra.Command) ([]byte, error) {
	return io.ReadAll(cmd.InOrStdin())
}

func init() {
	notificationListCmd.Flags().BoolVar(&notifFlagUnreadOnly, "unread-only", false, "Only return unread notifications")
	notificationListCmd.Flags().IntVar(&notifFlagLimit, "limit", 0, "Page size (default 20, max 100)")
	notificationListCmd.Flags().IntVar(&notifFlagOffset, "offset", 0, "Pagination offset")

	notificationPrefsSetCmd.Flags().StringVar(&notifPrefsFlagFile, "from-file", "", "Path to a JSON array of preference objects (use '-' for stdin)")

	notificationPrefsCmd.AddCommand(notificationPrefsGetCmd)
	notificationPrefsCmd.AddCommand(notificationPrefsSetCmd)

	notificationCmd.AddCommand(notificationListCmd)
	notificationCmd.AddCommand(notificationCountCmd)
	notificationCmd.AddCommand(notificationReadCmd)
	notificationCmd.AddCommand(notificationReadAllCmd)
	notificationCmd.AddCommand(notificationPrefsCmd)

	rootCmd.AddCommand(notificationCmd)
}

// ResetNotificationFlagsForTest clears the notification command flag vars
// between in-process Cobra invocations. Subcommand flags are NOT covered
// by ResetFlagsForTest.
func ResetNotificationFlagsForTest() {
	notifFlagUnreadOnly = false
	notifFlagLimit = 0
	notifFlagOffset = 0
	notifPrefsFlagFile = ""
}

func resetNotificationFlagsForTest() { ResetNotificationFlagsForTest() }
