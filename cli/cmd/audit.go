package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// auditCmd is the top-level command group. The intermediate `audit log`
// noun mirrors the API path /api/v1/audit-logs and leaves room for future
// audit subgroups (settings, retention) without restructuring later.
var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit log queries and exports (admin/devops)",
	Long: `Inspect the platform audit log.

  log list    — paginated query with filters
  log export  — bulk export as JSON or CSV (admin-only)

All audit endpoints surface backend permission errors as command errors;
non-admin callers on the export endpoint receive 403.`,
}

var auditLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Audit log subcommands",
}

// Filter flags shared between list and export. Declared at package scope so
// the cmd.Flags().Get* calls in RunE can read them; populated by cobra when
// the user passes --user / --action / etc.
var (
	auditFlagUser       string
	auditFlagAction     string
	auditFlagEntityType string
	auditFlagEntityID   string
	auditFlagSince      string
	auditFlagUntil      string
	auditFlagLimit      int
	auditFlagOffset     int
	auditFlagCursor     string

	auditFlagFormat     string
	auditFlagOutputFile string
)

var auditLogListCmd = &cobra.Command{
	Use:   "list",
	Short: "List audit log entries",
	Long: `List audit log entries with optional filters and pagination.

Time filters (--since / --until) accept either an absolute RFC3339 timestamp
(e.g. 2026-05-01T00:00:00Z) or a Go duration relative to now (e.g. 24h, 168h
for 7 days). Day/week units ("7d", "1w") are NOT accepted — Go's standard
time.ParseDuration only understands ns/us/ms/s/m/h. Relative values are
negated and added to time.Now() before being sent to the backend as RFC3339.

Examples:
  stackctl audit log list
  stackctl audit log list --user u-123 --action stack.deploy
  stackctl audit log list --entity-type stack --since 24h
  stackctl audit log list --since 2026-05-01T00:00:00Z --limit 100
  stackctl audit log list --cursor eyJpZCI6...`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		params, err := buildAuditListParams()
		if err != nil {
			return err
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		page, err := c.ListAuditLogs(params)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, e := range page.Data {
				fmt.Fprintln(printer.Writer, e.ID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(page)
		case output.FormatYAML:
			return printer.PrintYAML(page)
		default:
			if len(page.Data) == 0 {
				printer.PrintMessage("No audit log entries found.")
				return nil
			}
			headers := []string{"TIMESTAMP", "USER", "ACTION", "ENTITY", "ENTITY ID", "DETAILS"}
			rows := make([][]string, len(page.Data))
			for i, e := range page.Data {
				rows[i] = []string{
					e.Timestamp.UTC().Format(time.RFC3339),
					auditUserLabel(e),
					e.Action,
					e.EntityType,
					e.EntityID,
					truncate(e.Details, 60),
				}
			}
			if err := printer.PrintTable(headers, rows); err != nil {
				return err
			}
			if page.NextCursor != "" {
				printer.PrintMessage("(more results — re-run with --cursor %s)", page.NextCursor)
			}
			return nil
		}
	},
}

var auditLogExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export audit logs as JSON or CSV (admin)",
	Long: `Export the audit log as a JSON array or CSV file.

The server caps each export at 10,000 entries (most-recent first). Use
--since / --until to narrow the window for larger histories.

By default the export is written to stdout. Use --output-file to write to a
file instead. The export endpoint is admin-only; non-admin callers receive
a 403 surfaced as a command error.

The --format flag is independent of the global --output (-o) flag: --output
controls how the command's own progress messages are formatted (table is
fine), --format controls the server's response encoding.

Examples:
  stackctl audit log export                       # JSON to stdout
  stackctl audit log export --format csv > log.csv
  stackctl audit log export --format csv --output-file log.csv
  stackctl audit log export --since 7d --format csv --output-file weekly.csv`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		format := strings.ToLower(strings.TrimSpace(auditFlagFormat))
		if format != "json" && format != "csv" {
			return fmt.Errorf("invalid --format %q: must be 'json' or 'csv'", auditFlagFormat)
		}

		// Validate --output-file up front so an obvious misconfiguration is
		// rejected before we hit the API and pull down a 10k-row payload.
		// Defense-in-depth against accidental `..` slips — NOT a sandbox;
		// absolute paths and symlinks are still resolved by os.WriteFile.
		if auditFlagOutputFile != "" {
			for _, segment := range strings.Split(filepath.ToSlash(auditFlagOutputFile), "/") {
				if segment == ".." {
					return fmt.Errorf("output file path must not contain '..' segments")
				}
			}
		}

		params, err := buildAuditListParams()
		if err != nil {
			return err
		}
		// Server enforces limit/offset=0/maxExportLimit on export; passing user
		// values would be ignored. Skip sending them to avoid confusion.
		params.Limit = 0
		params.Offset = 0
		params.Cursor = ""

		c, err := newClient()
		if err != nil {
			return err
		}
		data, err := c.ExportAuditLogs(format, params)
		if err != nil {
			return err
		}

		if auditFlagOutputFile != "" {
			outPath := filepath.Clean(auditFlagOutputFile)
			if err := os.WriteFile(outPath, data, 0600); err != nil {
				return fmt.Errorf("writing file %s: %w", outPath, err)
			}
			if !printer.Quiet {
				printer.PrintMessage("Exported %d bytes (%s) to %s", len(data), format, outPath)
			}
			return nil
		}

		_, err = printer.Writer.Write(data)
		return err
	},
}

// buildAuditListParams converts the package-level flag vars into the typed
// query-param struct. Time flags are parsed here so an invalid input is
// rejected before an API call is made.
func buildAuditListParams() (types.AuditLogListParams, error) {
	p := types.AuditLogListParams{
		UserID:     auditFlagUser,
		Action:     auditFlagAction,
		EntityType: auditFlagEntityType,
		EntityID:   auditFlagEntityID,
		Cursor:     auditFlagCursor,
		Limit:      auditFlagLimit,
		Offset:     auditFlagOffset,
	}
	now := time.Now().UTC()
	if auditFlagSince != "" {
		t, err := parseTimeFlag(auditFlagSince, now)
		if err != nil {
			return p, fmt.Errorf("--since: %w", err)
		}
		p.StartDate = &t
	}
	if auditFlagUntil != "" {
		t, err := parseTimeFlag(auditFlagUntil, now)
		if err != nil {
			return p, fmt.Errorf("--until: %w", err)
		}
		p.EndDate = &t
	}
	return p, nil
}

// parseTimeFlag accepts either an absolute RFC3339 timestamp or a Go
// duration ("24h", "30m", "2h45m"). Durations are subtracted from `now` to
// produce an absolute timestamp, which is what the backend requires.
//
// Note: time.ParseDuration does NOT accept "d" / "w" units (Go's stdlib
// limitation). For "7 days" the user must pass "168h". We document this in
// the error message rather than rolling a custom parser, to keep behaviour
// predictable and avoid silently misinterpreting "1d".
func parseTimeFlag(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %q", v)
		}
		return now.Add(-d).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: expected RFC3339 (2026-05-01T00:00:00Z) or Go duration (24h, 30m)", v)
}

// auditUserLabel returns a printable user label for a log entry. Backend
// guarantees UserID is set; Username can be empty when the user has been
// deleted. Prefer the username for human readability, fall back to the ID.
func auditUserLabel(e types.AuditLogEntry) string {
	if e.Username != "" {
		return e.Username
	}
	if e.UserID != "" {
		return e.UserID
	}
	return "-"
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
// Used to keep audit-log "details" column readable in table mode without
// hard-wrapping; JSON/YAML output is never truncated.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func init() {
	// Filter flags are shared between list and export. Register them ONCE on
	// the parent `audit log` group as persistent flags so both subcommands
	// see the same values without double-binding the same package-level var
	// to two distinct Flags() sets (which would silently override defaults
	// and share state across commands within one Execute()).
	auditLogCmd.PersistentFlags().StringVar(&auditFlagUser, "user", "", "Filter by user ID")
	auditLogCmd.PersistentFlags().StringVar(&auditFlagAction, "action", "", "Filter by action name (e.g. stack.deploy)")
	auditLogCmd.PersistentFlags().StringVar(&auditFlagEntityType, "entity-type", "", "Filter by entity type (e.g. stack, template)")
	auditLogCmd.PersistentFlags().StringVar(&auditFlagEntityID, "entity-id", "", "Filter by entity ID")
	auditLogCmd.PersistentFlags().StringVar(&auditFlagSince, "since", "", "Lower bound (RFC3339 or duration like 24h)")
	auditLogCmd.PersistentFlags().StringVar(&auditFlagUntil, "until", "", "Upper bound (RFC3339 or duration like 24h)")

	// Pagination flags are only meaningful for list — export server-side
	// caps the row count and ignores limit/offset/cursor.
	auditLogListCmd.Flags().IntVar(&auditFlagLimit, "limit", 0, "Page size (default 25, max 100)")
	auditLogListCmd.Flags().IntVar(&auditFlagOffset, "offset", 0, "Pagination offset")
	auditLogListCmd.Flags().StringVar(&auditFlagCursor, "cursor", "", "Pagination cursor (from previous page)")

	// Export-only flags.
	auditLogExportCmd.Flags().StringVar(&auditFlagFormat, "format", "json", "Export format: json or csv")
	auditLogExportCmd.Flags().StringVar(&auditFlagOutputFile, "output-file", "", "Write export to file instead of stdout")

	auditLogCmd.AddCommand(auditLogListCmd)
	auditLogCmd.AddCommand(auditLogExportCmd)
	auditCmd.AddCommand(auditLogCmd)
	rootCmd.AddCommand(auditCmd)
}

// ResetAuditFlagsForTest clears the package-level audit flag vars between
// in-process Cobra invocations in tests. The persistent filter flags on
// `audit log` are NOT reset by ResetFlagsForTest (which only handles
// persistent flags on rootCmd), so integration tests that exercise multiple
// audit subcommands within one process MUST call this between Execute()s to
// avoid filter leakage from a previous call.
//
// Exported so the integration_test package can call it; the lowercase alias
// remains available within the cmd package itself.
func ResetAuditFlagsForTest() {
	auditFlagUser = ""
	auditFlagAction = ""
	auditFlagEntityType = ""
	auditFlagEntityID = ""
	auditFlagSince = ""
	auditFlagUntil = ""
	auditFlagLimit = 0
	auditFlagOffset = 0
	auditFlagCursor = ""
	auditFlagFormat = "json"
	auditFlagOutputFile = ""
}

// resetAuditFlagsForTest is the in-package alias retained so existing cmd-
// package test files don't need updating.
func resetAuditFlagsForTest() { ResetAuditFlagsForTest() }

