package main

import (
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
