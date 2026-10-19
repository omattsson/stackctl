package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// defaultMaxTTLDays is the TTL limit of a server that does not send
// max_instance_ttl_days (k8s-stack-manager before v0.12.0).
const defaultMaxTTLDays = 30

// stopAtLayouts are the local time formats of --stop-at and --until, after
// RFC 3339. A time without a zone is in the local time zone.
var stopAtLayouts = []string{
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

// stopAtHelp is the format part of the help text of --stop-at and --until.
const stopAtHelp = `RFC 3339 (2026-10-17T18:00:00+02:00) or a local time (2026-10-17 18:00)`

// parseStopAt reads a --stop-at or --until value. It accepts RFC 3339 and
// the local formats of stopAtLayouts, and returns the time in UTC.
func parseStopAt(flag, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range stopAtLayouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("--%s %q is not a valid time: use %s", flag, value, stopAtHelp)
}

// expiryFlags holds the --ttl and --stop-at values of a command.
type expiryFlags struct {
	ttl    *int       // nil: --ttl not given
	stopAt *time.Time // nil: --stop-at not given
}

// readExpiryFlags reads --ttl and --stop-at. Both together, a negative TTL
// or a bad time is a usage error. The check runs before any request.
func readExpiryFlags(cmd *cobra.Command) (expiryFlags, error) {
	var f expiryFlags
	ttlSet := cmd.Flags().Changed("ttl")
	stopSet := cmd.Flags().Changed("stop-at")
	if ttlSet && stopSet {
		return f, fmt.Errorf("--ttl and --stop-at cannot be used together")
	}
	if ttlSet {
		ttl, _ := cmd.Flags().GetInt("ttl")
		if ttl < 0 {
			return f, fmt.Errorf("--ttl must be a non-negative integer (0 means no TTL)")
		}
		f.ttl = &ttl
	}
	if stopSet {
		raw, _ := cmd.Flags().GetString("stop-at")
		t, err := parseStopAt("stop-at", raw)
		if err != nil {
			return f, err
		}
		if !t.After(nowFunc()) {
			return f, fmt.Errorf("--stop-at %s is in the past", t.Format(time.RFC3339))
		}
		f.stopAt = &t
	}
	return f, nil
}

// serverMaxTTLMinutes returns the TTL limit of the server in minutes from
// GET /api/v1/ui-config. A server without the value (404 or 0) gets
// defaultMaxTTLDays. ok is false when the call fails for another reason:
// the caller then skips the local check and the server checks the value.
func serverMaxTTLMinutes(c *client.Client) (minutes int, ok bool) {
	days := defaultMaxTTLDays
	cfg, err := bestEffortClient(c).GetUIConfig()
	switch {
	case err == nil:
		if cfg.MaxInstanceTTLDays > 0 {
			days = cfg.MaxInstanceTTLDays
		}
	case isStatus(err, http.StatusNotFound):
	default:
		return 0, false
	}
	return days * 24 * 60, true
}

// checkExpiryLimit refuses a TTL or a stop time above the TTL limit of the
// server, before the create or update request. It makes the ui-config call
// only when one of the flags is set to a value that can exceed the limit.
func checkExpiryLimit(c *client.Client, f expiryFlags) error {
	if (f.ttl == nil || *f.ttl == 0) && f.stopAt == nil {
		return nil
	}
	maxMinutes, ok := serverMaxTTLMinutes(c)
	if !ok {
		return nil
	}
	if f.ttl != nil && *f.ttl > maxMinutes {
		return fmt.Errorf("--ttl %d is above the limit of the server: %s (%d minutes)", *f.ttl, daysText(maxMinutes), maxMinutes)
	}
	if f.stopAt != nil {
		return checkStopLimit("stop-at", *f.stopAt, maxMinutes)
	}
	return nil
}

// checkStopLimit refuses a stop time later than now plus maxMinutes.
func checkStopLimit(flag string, stop time.Time, maxMinutes int) error {
	limit := nowFunc().Add(time.Duration(maxMinutes) * time.Minute)
	if stop.After(limit) {
		return fmt.Errorf("--%s %s is too far away: the limit of the server is %s from now (%s)",
			flag, stop.Format(time.RFC3339), daysText(maxMinutes), limit.UTC().Format(time.RFC3339))
	}
	return nil
}

// daysText returns "N days" for a limit in minutes.
func daysText(minutes int) string {
	days := minutes / (24 * 60)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// isStatus reports whether err is an API error with the given status.
func isStatus(err error, status int) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// hasFixedStopTime reports whether an instance has a fixed stop time: no
// TTL and an expiry.
func hasFixedStopTime(ttlMinutes int, expiresAt *time.Time) bool {
	return ttlMinutes == 0 && expiresAt != nil
}

// expiryText is the expiry of an instance for a table: the time for a TTL
// expiry, "stops at <time>" for a fixed stop time, "-" without an expiry.
func expiryText(ttlMinutes int, expiresAt *time.Time) string {
	if hasFixedStopTime(ttlMinutes, expiresAt) {
		return "stops at " + formatTime(expiresAt)
	}
	return formatTime(expiresAt)
}

// checkStopApplied returns an error when the server did not set the
// requested stop time: a server before k8s-stack-manager v0.12.0 ignores
// expires_at and "until" and answers 2xx. The command shows the result
// first, then exits non-zero. A nil want means no stop time was requested.
func checkStopApplied(flag string, want *time.Time, got *types.StackInstance) error {
	if want == nil || got == nil {
		return nil
	}
	if got.ExpiresAt != nil {
		diff := got.ExpiresAt.Sub(*want)
		if diff < 0 {
			diff = -diff
		}
		if diff <= time.Minute {
			return nil
		}
	}
	return fmt.Errorf("the server did not apply --%s %s (expiry: %s). "+
		"The server does not support a fixed stop time; upgrade k8s-stack-manager to v0.12.0 or later",
		flag, want.UTC().Format(time.RFC3339), formatTime(got.ExpiresAt))
}

// ttlText is the TTL of an instance for the table of "stack get".
func ttlText(ttlMinutes int, expiresAt *time.Time) string {
	switch {
	case hasFixedStopTime(ttlMinutes, expiresAt):
		return "- (fixed stop time)"
	case ttlMinutes == 0:
		return "- (no expiry)"
	default:
		return fmt.Sprintf("%d minutes", ttlMinutes)
	}
}

// stopAtFlagUsage is the usage text of --stop-at.
const stopAtFlagUsage = "Fixed stop time: " + stopAtHelp + "; sets the TTL to 0"

// expiryFlagsHelp is the help text of --ttl and --stop-at.
const expiryFlagsHelp = `--ttl and --stop-at cannot be used together. --stop-at accepts ` + stopAtHelp + `.
The command sends it in UTC. The stop time must be in the future and at
most the TTL limit of the server from now (max_instance_ttl_days of
GET /api/v1/ui-config, 30 days on an older server). The command refuses a
higher --ttl or a later --stop-at before the request. A server before
k8s-stack-manager v0.12.0 ignores --stop-at: the command then shows the
result and exits with a non-zero code.`
