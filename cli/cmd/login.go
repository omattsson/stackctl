package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the stack manager API",
	Long: `Authenticate with the stack manager API using username and password.

Credentials can be provided via flags or entered interactively.
The returned JWT token is stored locally for the current context.

Examples:
  stackctl login
  stackctl login --username admin

Environment variables:
  STACKCTL_USERNAME   Username for authentication
  STACKCTL_PASSWORD   Password for authentication (avoids interactive prompt)`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		username, _ := cmd.Flags().GetString("username")
		password, _ := cmd.Flags().GetString("password")

		// Check environment variables if flags not provided
		if username == "" {
			username = os.Getenv("STACKCTL_USERNAME")
		}
		if password == "" {
			password = os.Getenv("STACKCTL_PASSWORD")
		}

		// Prompt interactively if not provided via flags or env
		if username == "" {
			fmt.Fprint(cmd.OutOrStdout(), "Username: ")
			reader := bufio.NewReader(cmd.InOrStdin())
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return fmt.Errorf("reading username: %w", err)
			}
			username = strings.TrimSpace(line)
		}
		if username == "" {
			return fmt.Errorf("username is required")
		}

		if password == "" {
			fmt.Fprint(cmd.OutOrStdout(), "Password: ")
			if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				raw, err := term.ReadPassword(int(f.Fd()))
				fmt.Fprintln(cmd.OutOrStdout()) // newline after hidden input
				if err != nil {
					return fmt.Errorf("reading password: %w", err)
				}
				password = string(raw)
			} else {
				reader := bufio.NewReader(cmd.InOrStdin())
				line, err := reader.ReadString('\n')
				if err != nil && err != io.EOF {
					return fmt.Errorf("reading password: %w", err)
				}
				password = strings.TrimSpace(line)
			}
		}
		if password == "" {
			return fmt.Errorf("password is required")
		}

		if cfg == nil || cfg.CurrentContext == "" {
			return fmt.Errorf("no context configured. Run 'stackctl config use-context <name>' first")
		}

		c, err := newUnauthenticatedClient()
		if err != nil {
			return err
		}

		resp, err := c.Login(username, password)
		if err != nil {
			return err
		}

		// Parse expiry from response
		var expiresAt time.Time
		if resp.ExpiresAt != "" {
			expiresAt, err = time.Parse(time.RFC3339, resp.ExpiresAt)
			if err != nil {
				// Try alternative formats
				expiresAt, err = time.Parse(time.RFC3339Nano, resp.ExpiresAt)
				if err != nil {
					return fmt.Errorf("parsing token expiry: %w", err)
				}
			}
		}

		loginUser := resp.User.Username
		if loginUser == "" {
			loginUser = username
		}

		if err := saveToken(resp.Token, loginUser, expiresAt); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}

		printer.PrintMessage("Logged in as %s", loginUser)
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored authentication token",
	Long: `Clear the stored JWT token for the current context.

Example:
  stackctl logout`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := deleteToken(); err != nil {
			return err
		}
		ctx := cfg.CurrentContext
		if ctx == "" {
			ctx = "default"
		}
		printer.PrintMessage("Logged out from context %q", ctx)
		return nil
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Display current authenticated user",
	Long: `Display information about the currently authenticated user.

Examples:
  stackctl whoami
  stackctl whoami -o json
  stackctl whoami -q`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		user, err := c.Whoami()
		if err != nil {
			return err
		}

		if printer.Quiet {
			// Quiet mode outputs IDs per global flag contract
			fmt.Fprintln(printer.Writer, user.ID)
			return nil
		}

		switch printer.Format {
		case output.FormatJSON:
			return printer.PrintJSON(user)
		case output.FormatYAML:
			return printer.PrintYAML(user)
		default:
			fields := []output.KeyValue{
				{Key: "Username", Value: user.Username},
				{Key: "Role", Value: user.Role},
				{Key: "Created", Value: user.CreatedAt.Format(time.RFC3339)},
			}
			return printer.PrintSingle(user, fields)
		}
	},
}

func init() {
	loginCmd.Flags().String("username", "", "Username for authentication")
	loginCmd.Flags().String("password", "", "Password for authentication")

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}
