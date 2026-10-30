package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/omattsson/stackctl/cli/cmd"
)

// Build-time variables set via ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	if err := cmd.Execute(); err != nil {
		var exitErr *cmd.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.Err != nil {
				// The whole chain: a wrapper around the ExitError adds its
				// context. ExitError.Error() is the text of Err.
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
