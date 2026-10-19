package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestStackCloneCmd_OutputModes(t *testing.T) {
	tests := []struct {
		name   string
		format output.Format
		quiet  bool
		check  func(t *testing.T, out string)
	}{
		{"json", output.FormatJSON, false, func(t *testing.T, out string) {
			var inst types.StackInstance
			require.NoError(t, json.Unmarshal([]byte(out), &inst), "stdout is JSON")
			assert.Equal(t, "55", inst.ID)
			assert.Equal(t, "my-stack-copy", inst.Name)
		}},
		{"yaml", output.FormatYAML, false, func(t *testing.T, out string) {
			var m map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(out), &m))
			assert.Equal(t, "55", m["id"], "id is a top-level key")
		}},
		{"quiet", output.FormatTable, true, func(t *testing.T, out string) {
			assert.Equal(t, "55\n", out)
		}},
		{"table", output.FormatTable, false, func(t *testing.T, out string) {
			assert.Contains(t, out, "Cloned stack 42")
			assert.Contains(t, out, "new stack 55")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, out := setupChannelCmd(t, stackCloneCmd, map[string]route{
				"POST /api/v1/stack-instances/42/clone": {http.StatusCreated, `{"id":"55","name":"my-stack-copy","status":"draft"}`},
			})
			printer.Format = tt.format
			printer.Quiet = tt.quiet
			require.NoError(t, stackCloneCmd.RunE(stackCloneCmd, []string{"42"}))
			stdout, _ := out()
			tt.check(t, stdout)
		})
	}
}

func TestPrintChartConfig_ShowsServerFields(t *testing.T) {
	var buf bytes.Buffer
	prev := printer
	printer = output.NewPrinter("table", false, true)
	printer.Writer = &buf
	t.Cleanup(func() { printer = prev })

	require.NoError(t, printChartConfig(&types.ChartConfig{
		Base: types.Base{ID: "c1"}, ChartName: "api", ChartPath: "/charts/api", ChartVersion: "1.2.0", DeployOrder: 3, Required: true,
	}))
	out := buf.String()
	assert.Regexp(t, `Chart:\s+api`, out)
	assert.Regexp(t, `Repository:\s+-`, out)
	assert.Regexp(t, `Chart Path:\s+/charts/api`, out)
	assert.Regexp(t, `Deploy Order:\s+3`, out)
	assert.Regexp(t, `Required:\s+yes`, out)
	assert.NotContains(t, out, "Release Name", "the server sends no release name")
	assert.NotRegexp(t, `(?m)^Name:`, out, "the server sends no chart name field other than chart_name")

	buf.Reset()
	require.NoError(t, printChartConfig(&types.ChartConfig{Base: types.Base{ID: "c2"}, ChartName: "db"}))
	assert.Regexp(t, `Deploy Order:\s+0`, buf.String())
	assert.NotContains(t, buf.String(), "Required", "no Required row for an optional chart")
}

// TestPrinterColor_OnlyOnATerminal: a pipe or a file gets no ANSI colors.
// Tests run without a terminal, so os.Stdout and a buffer are both not a
// terminal here.
func TestPrinterColor_OnlyOnATerminal(t *testing.T) {
	assert.False(t, isTerminalWriter(&bytes.Buffer{}))
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	assert.False(t, isTerminalWriter(f), "a regular file is not a terminal")

	t.Setenv("NO_COLOR", "1")
	assert.True(t, noColorEnv())
	t.Setenv("NO_COLOR", "")
	assert.False(t, noColorEnv(), "an empty NO_COLOR does not count (no-color.org)")

	// PersistentPreRunE builds the printer: with a buffer as stdout the
	// status text has no escape codes.
	prev := printer
	t.Cleanup(func() { printer = prev; versionCmd.SetOut(nil) })
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	require.NoError(t, rootCmd.PersistentPreRunE(versionCmd, nil))
	assert.True(t, printer.NoColor)
	assert.Equal(t, "running", printer.StatusColor("running"))
}
