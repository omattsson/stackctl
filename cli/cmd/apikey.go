package cmd

import (
	"fmt"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var apikeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Manage user API keys",
	Long: `Manage API keys used for non-interactive (CI / service account)
authentication against the stack manager API.

By default every subcommand operates on the caller's own API keys. Admins can
manage any user's keys by passing --user <id>. The backend gates non-admin
callers to their own user-id; --user pointing at another user returns 403.

Created keys are returned ONCE in plaintext and cannot be retrieved again —
treat the create-step output as a secret. The CLI never writes the raw key
to config or debug logs.`,
}

var apikeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys for the current user (or --user)",
	Long: `List API keys configured for the caller (default) or for another
user when --user is supplied.

The raw key value is NEVER returned by this endpoint — only the prefix
(first 16 chars) is shown for visual identification, plus name, creation
time, last-used time, and expiry.

Examples:
  stackctl apikey list
  stackctl apikey list --user <uuid>
  stackctl apikey list -o json`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		userID, err := resolveAPIKeyUserID(cmd, c)
		if err != nil {
			return err
		}
		keys, err := c.ListAPIKeys(userID)
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, k := range keys {
				fmt.Fprintln(printer.Writer, k.ID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(keys)
		case output.FormatYAML:
			return printer.PrintYAML(keys)
		default:
			if len(keys) == 0 {
				printer.PrintMessage("No API keys configured for user %s.", userID)
				return nil
			}
			headers := []string{"ID", "NAME", "PREFIX", "CREATED", "LAST USED", "EXPIRES"}
			rows := make([][]string, len(keys))
			for i, k := range keys {
				rows[i] = []string{
					k.ID,
					k.Name,
					k.Prefix,
					k.CreatedAt.Format("2006-01-02"),
					formatTime(k.LastUsedAt),
					formatTime(k.ExpiresAt),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var apikeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API key (returned once, in plaintext)",
	Long: `Create a new API key for the caller (default) or for --user.

The backend REQUIRES an expiry — pass exactly one of --expires-at
(YYYY-MM-DD or RFC3339) or --expires-in-days (positive integer). Passing
both returns 400. The expiry must be strictly in the future and may be
capped by the server's API_KEY_MAX_LIFETIME_DAYS setting.

The raw key is returned ONCE in the response and CANNOT be retrieved again.
In quiet mode the command prints only the raw key on stdout (one line),
making it pipeable into a token file for CI/service-account bootstrap:

  stackctl apikey create --name ci --expires-in-days 365 --quiet > /run/secrets/stackctl-token

Default and JSON/YAML modes print the full response including ID, name,
prefix, created/expires timestamps, and the raw key — store the raw key
immediately.

Examples:
  stackctl apikey create --name ci --expires-in-days 365
  stackctl apikey create --name release --expires-at 2027-01-01
  stackctl apikey create --name svc-ci --user <uuid> --expires-in-days 90 --quiet`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("--name is required")
		}
		expiresAt, _ := cmd.Flags().GetString("expires-at")
		expiresInDays, _ := cmd.Flags().GetInt("expires-in-days")
		expiresAtChanged := cmd.Flags().Changed("expires-at")
		expiresInDaysChanged := cmd.Flags().Changed("expires-in-days")

		if !expiresAtChanged && !expiresInDaysChanged {
			return fmt.Errorf("an expiry is required: pass --expires-at <YYYY-MM-DD|RFC3339> or --expires-in-days <int>")
		}
		if expiresAtChanged && expiresInDaysChanged {
			return fmt.Errorf("--expires-at and --expires-in-days are mutually exclusive; pass exactly one")
		}

		req := types.CreateAPIKeyRequest{Name: name}
		if expiresAtChanged {
			s := strings.TrimSpace(expiresAt)
			if s == "" {
				return fmt.Errorf("--expires-at must not be empty")
			}
			req.ExpiresAt = &s
		}
		if expiresInDaysChanged {
			if expiresInDays <= 0 {
				return fmt.Errorf("--expires-in-days must be a positive integer")
			}
			d := expiresInDays
			req.ExpiresInDays = &d
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		userID, err := resolveAPIKeyUserID(cmd, c)
		if err != nil {
			return err
		}

		resp, err := c.CreateAPIKey(userID, &req)
		if err != nil {
			return err
		}

		// Quiet mode is the script-bootstrap path: ONLY the raw key on stdout,
		// nothing else, no trailing whitespace beyond the single newline that
		// Fprintln adds. This is the documented contract for piping into a
		// token file.
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.RawKey)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			return printer.PrintSingle(resp, []output.KeyValue{
				{Key: "ID", Value: resp.ID},
				{Key: "Name", Value: resp.Name},
				{Key: "Prefix", Value: resp.Prefix},
				{Key: "Created", Value: resp.CreatedAt.Format("2006-01-02T15:04:05Z07:00")},
				{Key: "Expires", Value: formatTime(resp.ExpiresAt)},
				{Key: "Raw Key (store now — non-retrievable)", Value: resp.RawKey},
			})
		}
	},
}

var apikeyRevokeCmd = &cobra.Command{
	Use:   "revoke <key-id>",
	Short: "Revoke an API key by ID",
	Long: `Revoke (delete) an API key. The key stops authenticating new
requests immediately.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified.

Examples:
  stackctl apikey revoke <key-uuid>
  stackctl apikey revoke <key-uuid> --yes
  stackctl apikey revoke <key-uuid> --user <user-uuid> --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		keyID, err := parseID(args[0])
		if err != nil {
			return err
		}

		if isDryRun(cmd, "Would revoke API key %s", keyID) {
			return nil
		}

		confirmed, err := confirmAction(cmd, fmt.Sprintf("This will revoke API key %s. Continue? (y/n): ", keyID))
		if err != nil {
			return err
		}
		if !confirmed {
			printer.PrintMessage("Aborted.")
			return nil
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		userID, err := resolveAPIKeyUserID(cmd, c)
		if err != nil {
			return err
		}

		if err := c.DeleteAPIKey(userID, keyID); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, keyID)
			return nil
		}
		printer.PrintMessage("Revoked API key %s", keyID)
		return nil
	},
}

// resolveAPIKeyUserID returns the user ID to use for the api-keys/* request:
// the --user flag value when set, otherwise the caller's own user ID
// resolved via GET /api/v1/auth/me. The latter is cached for the duration
// of a single command invocation but not across invocations.
//
// When --user is set, it's run through parseID() to trim whitespace and
// reject empty values before going into a /users/:id/... path.
func resolveAPIKeyUserID(cmd *cobra.Command, c *client.Client) (string, error) {
	if u, _ := cmd.Flags().GetString("user"); strings.TrimSpace(u) != "" {
		return parseID(u)
	}
	me, err := c.Whoami()
	if err != nil {
		return "", fmt.Errorf("resolving caller user ID via /auth/me: %w", err)
	}
	if me.ID == "" {
		return "", fmt.Errorf("server returned empty user ID from /auth/me")
	}
	return me.ID, nil
}

func init() {
	apikeyListCmd.Flags().String("user", "", "Target user ID (defaults to the caller's own user via /auth/me)")

	apikeyCreateCmd.Flags().String("name", "", "Human-readable name for the new key (required)")
	apikeyCreateCmd.Flags().String("user", "", "Target user ID (defaults to the caller's own user via /auth/me)")
	apikeyCreateCmd.Flags().String("expires-at", "", "Expiry as YYYY-MM-DD or RFC3339 (mutually exclusive with --expires-in-days)")
	apikeyCreateCmd.Flags().Int("expires-in-days", 0, "Expiry in days from now (mutually exclusive with --expires-at)")
	_ = apikeyCreateCmd.MarkFlagRequired("name")

	apikeyRevokeCmd.Flags().String("user", "", "Target user ID (defaults to the caller's own user via /auth/me)")
	apikeyRevokeCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	apikeyRevokeCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")

	apikeyCmd.AddCommand(apikeyListCmd)
	apikeyCmd.AddCommand(apikeyCreateCmd)
	apikeyCmd.AddCommand(apikeyRevokeCmd)
	rootCmd.AddCommand(apikeyCmd)
}
