package main

import (
	"os"

	"github.com/omattsson/stackctl/cmd"
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
		os.Exit(1)
	}
}
