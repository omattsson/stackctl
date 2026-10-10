package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage user accounts (admin only)",
	Long: `Manage user accounts on the stack manager API.

All user-management commands require admin role. The CLI surface intentionally
omits a generic 'update' verb because the backend only exposes the
disable/enable/reset-password/set-role operations rather than a full PUT.`,
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all user accounts (admin only)",
	Long: `List every user account on the server.

Examples:
  stackctl user list
  stackctl user list -o json
  stackctl user list --quiet`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		users, err := c.ListUsers()
		if err != nil {
			return err
		}

		if printer.Quiet {
			for _, u := range users {
				fmt.Fprintln(printer.Writer, u.ID)
			}
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(users)
		case output.FormatYAML:
			return printer.PrintYAML(users)
		default:
			if len(users) == 0 {
				printer.PrintMessage("No users found.")
				return nil
			}
			headers := []string{"ID", "USERNAME", "DISPLAY NAME", "ROLE", "PROVIDER", "DISABLED", "SERVICE ACCOUNT"}
			rows := make([][]string, len(users))
			for i, u := range users {
				rows[i] = []string{
					u.ID,
					u.Username,
					u.DisplayName,
					u.Role,
					u.AuthProvider,
					strconv.FormatBool(u.Disabled),
					strconv.FormatBool(u.ServiceAccount),
				}
			}
			return printer.PrintTable(headers, rows)
		}
	},
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a user account (admin only)",
	Long: `Permanently delete a user account.

This is a destructive operation. You will be prompted for confirmation
unless --yes is specified. The backend rejects an attempt to delete the
caller's own account.

Examples:
  stackctl user delete <uuid>
  stackctl user delete <uuid> --yes`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deleteByID(cmd, args,
			"This will permanently delete user %s. Continue? (y/n): ",
			passthroughID,
			func(c *client.Client, id string) error { return c.DeleteUser(id) },
			"Deleted user %s",
		)
	},
}

var userDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a user account (admin only)",
	Long: `Disable a user account. All active sessions and API keys are revoked
server-side; the user can no longer authenticate until re-enabled.

The backend rejects an attempt to disable the caller's own account.

Examples:
  stackctl user disable <uuid>`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return userToggle(cmd, args, "Disabled", (*client.Client).DisableUser)
	},
}

var userEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Re-enable a previously disabled user account (admin only)",
	Long: `Re-enable a previously disabled user account.

Examples:
  stackctl user enable <uuid>`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return userToggle(cmd, args, "Enabled", (*client.Client).EnableUser)
	},
}

var userResetPasswordCmd = &cobra.Command{
	Use:   "reset-password <id>",
	Short: "Reset a user's password (admin only)",
	Long: `Reset the password for a local user account.

The new password must be at least 8 characters. The backend rejects users
whose AuthProvider is not "local" (e.g. OIDC-federated users).

The password is read interactively by default. Use --password-stdin to read
the password from stdin (recommended for scripting); one line is read (up to
the first newline, or EOF) and the trailing CR/LF is stripped. Embedded
newlines in the password are not supported.

Examples:
  stackctl user reset-password <uuid>
  echo 'hunter2!!' | stackctl user reset-password <uuid> --password-stdin`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}

		fromStdin, _ := cmd.Flags().GetBool("password-stdin")
		password, err := readPassword(cmd, "New password: ", fromStdin)
		if err != nil {
			return err
		}
		if len(password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		if err := c.ResetUserPassword(id, password); err != nil {
			return err
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, id)
			return nil
		}
		printer.PrintMessage("Reset password for user %s", id)
		return nil
	},
}

// validRoles lists the roles that `user set-role` accepts.
var validRoles = []string{"user", "devops", "admin"}

var userSetRoleCmd = &cobra.Command{
	Use:   "set-role <user> <role>",
	Short: "Change the role of a local user (admin only)",
	Long: `Change the role of a local user account. Use the user ID or the username.
The role is one of: user, devops, admin.

Rules:
  - Only an admin can change a role.
  - Only local users. The identity provider sets the role of an SSO user
    at each login. The server refuses the change (409).
  - You cannot change your own role (403).
  - The last enabled admin cannot lose the admin role (409).
  - A change ends the sessions of the user: the user must log in again.
    The API keys of the user stay valid and use the new role at once.
  - The same role again changes nothing. The sessions stay valid.

Needs k8s-stack-manager v0.8.0 or later.

Examples:
  stackctl user set-role alice devops
  stackctl user set-role 6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d admin
  stackctl user set-role alice user -o json`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		role := strings.ToLower(strings.TrimSpace(args[1]))
		if !isValidRole(role) {
			return fmt.Errorf("invalid role %q: use one of %s", args[1], strings.Join(validRoles, ", "))
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveUserID(c, args[0])
		if err != nil {
			return err
		}

		resp, err := c.ChangeUserRole(id, role)
		if err != nil {
			return setRoleError(err, id)
		}

		if printer.Quiet {
			fmt.Fprintln(printer.Writer, resp.ID)
			return nil
		}
		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(resp)
		case output.FormatYAML:
			return printer.PrintYAML(resp)
		default:
			if !resp.Changed {
				printer.PrintMessage("Role unchanged.")
				return nil
			}
			printer.PrintMessage("Role changed from %s to %s. The user must log in again.", resp.OldRole, resp.NewRole)
			return nil
		}
	},
}

func isValidRole(role string) bool {
	for _, r := range validRoles {
		if r == role {
			return true
		}
	}
	return false
}

// setRoleError adds the next step to the server errors of a role change.
// The server message stays in the text.
func setRoleError(err error, id string) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := strings.ToLower(apiErr.Message)
	switch {
	case apiErr.StatusCode == http.StatusConflict && strings.Contains(msg, "identity provider"):
		return fmt.Errorf("%s. The identity provider sets the role of an SSO user at each login; change the role there", apiErr.Error())
	case apiErr.StatusCode == http.StatusConflict && strings.Contains(msg, "last enabled admin"):
		return fmt.Errorf("%s. Give the admin role to another user first", apiErr.Error())
	case apiErr.StatusCode == http.StatusForbidden && strings.Contains(msg, "own role"):
		return fmt.Errorf("%s. Ask another admin to change your role", apiErr.Error())
	case apiErr.StatusCode == http.StatusNotFound && !strings.Contains(msg, "user"):
		// Gin answers an unknown route with a plain 404 ("Not Found"); a
		// missing user gives "User not found".
		return fmt.Errorf("the server does not support role changes (404 for user %s): upgrade k8s-stack-manager to v0.8.0 or later", id)
	}
	return err
}

// userToggle backs the user disable/enable commands. label is "Disabled"
// or "Enabled" (used verbatim in the success message); apply is the client
// method to invoke.
func userToggle(cmd *cobra.Command, args []string, label string, apply func(*client.Client, string) error) error {
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	if err := apply(c, id); err != nil {
		return err
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, id)
		return nil
	}
	printer.PrintMessage("%s user %s", label, id)
	return nil
}

// printUser renders a single User in the configured output format. Shared
// by `auth register` to display the just-created account.
func printUser(u *types.User) error {
	if printer.Quiet {
		fmt.Fprintln(printer.Writer, u.ID)
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(u)
	case output.FormatYAML:
		return printer.PrintYAML(u)
	default:
		return printer.PrintSingle(u, []output.KeyValue{
			{Key: "ID", Value: u.ID},
			{Key: "Username", Value: u.Username},
			{Key: "Display Name", Value: u.DisplayName},
			{Key: "Email", Value: u.Email},
			{Key: "Role", Value: u.Role},
			{Key: "Provider", Value: u.AuthProvider},
			{Key: "Disabled", Value: strconv.FormatBool(u.Disabled)},
			{Key: "Service Account", Value: strconv.FormatBool(u.ServiceAccount)},
		})
	}
}

func init() {
	userDeleteCmd.Flags().BoolP("yes", "y", false, flagDescSkipConfirm)
	userDeleteCmd.Flags().Bool("dry-run", false, "Show what would happen without executing")
	userResetPasswordCmd.Flags().Bool("password-stdin", false, "Read the new password from stdin (one line, trailing newline stripped)")

	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userDeleteCmd)
	userCmd.AddCommand(userDisableCmd)
	userCmd.AddCommand(userEnableCmd)
	userCmd.AddCommand(userResetPasswordCmd)
	userCmd.AddCommand(userSetRoleCmd)

	rootCmd.AddCommand(userCmd)
}
