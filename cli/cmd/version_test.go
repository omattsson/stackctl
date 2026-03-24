package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/omattsson/stackctl/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Version tests mutate package-level globals, so they cannot run in parallel.

func TestVersionCmd_Table(t *testing.T) {
	buildVersion = "1.2.3"
	buildCommit = "abc123"
	buildDate = "2025-01-01"

	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	flagOutput = "table"
	printer = output.NewPrinter("table", false, true)
	printer.Writer = &buf

	err := versionCmd.RunE(versionCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "1.2.3")
	assert.Contains(t, out, "abc123")
	assert.Contains(t, out, "2025-01-01")
}

func TestVersionCmd_JSON(t *testing.T) {
	buildVersion = "2.0.0"
	buildCommit = "def456"
	buildDate = "2025-06-15"

	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	flagOutput = "json"
	printer = output.NewPrinter("json", false, true)
	printer.Writer = &buf

	err := versionCmd.RunE(versionCmd, []string{})
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "2.0.0", result["version"])
	assert.Equal(t, "def456", result["commit"])
	assert.Equal(t, "2025-06-15", result["date"])
}

func TestSetVersionInfo(t *testing.T) {
	SetVersionInfo("3.0.0", "xyz789", "2025-12-25")
	assert.Equal(t, "3.0.0", buildVersion)
	assert.Equal(t, "xyz789", buildCommit)
	assert.Equal(t, "2025-12-25", buildDate)
}
