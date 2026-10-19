package cmd

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

// The subcommands are "follow" and "unfollow". The --follow flag of
// "stack deploy" and "stack logs" streams a log; it is not related.

var stackFollowCmd = &cobra.Command{
	Use:   "follow <name|id>",
	Short: "Follow a stack instance and get its notifications",
	Long: `Follow a stack instance. You then get its in-app notifications (deploy,
stop, clean, expiry), as the owner does, filtered by your notification
preferences. Every user who can view a stack can follow it. Following a
stack that you already follow is not an error.

This is not the --follow flag of "stack deploy" and "stack logs", which
streams the deployment log.

Examples:
  stackctl stack follow my-stack
  stackctl stack follow my-stack -o json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetFollow(args[0], true)
	},
}

var stackUnfollowCmd = &cobra.Command{
	Use:   "unfollow <name|id>",
	Short: "Stop following a stack instance",
	Long: `Stop following a stack instance. Unfollowing a stack that you do not
follow is not an error.

Examples:
  stackctl stack unfollow my-stack`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetFollow(args[0], false)
	},
}

// runSetFollow follows or unfollows a stack and prints the new state.
func runSetFollow(nameOrID string, follow bool) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	id, err := resolveStackID(c, nameOrID)
	if err != nil {
		return err
	}

	var state *types.FollowState
	if follow {
		state, err = c.FollowStack(id)
	} else {
		state, err = c.UnfollowStack(id)
	}
	if err != nil {
		return followError(err)
	}

	if printer.Quiet {
		fmt.Fprintln(printer.Writer, id)
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(state)
	case output.FormatYAML:
		return printer.PrintYAML(state)
	default:
		if state.Following {
			printer.PrintMessage("You follow stack %s (%s)", id, followerText(state.FollowerCount))
		} else {
			printer.PrintMessage("You do not follow stack %s (%s)", id, followerText(state.FollowerCount))
		}
		return nil
	}
}

// followError adds a hint to the answer of a server without follow
// support: 404 without a JSON message (no route, before v0.8.0) or 501
// (following is not configured on the server).
func followError(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode == http.StatusNotFound && apiErr.Message == http.StatusText(http.StatusNotFound):
		return fmt.Errorf("%w (the server does not support following; upgrade k8s-stack-manager to v0.8.0 or later)", err)
	case apiErr.StatusCode == http.StatusNotImplemented:
		return fmt.Errorf("%w (following is not configured on the server)", err)
	}
	return err
}

// followerText returns "1 follower" or "N followers".
func followerText(n int64) string {
	if n == 1 {
		return "1 follower"
	}
	return fmt.Sprintf("%d followers", n)
}

// yesNo returns "yes" or "no".
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func init() {
	stackCmd.AddCommand(stackFollowCmd)
	stackCmd.AddCommand(stackUnfollowCmd)
}
