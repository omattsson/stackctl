package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewPrinter_Formats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  Format
	}{
		{"table", FormatTable},
		{"json", FormatJSON},
		{"yaml", FormatYAML},
		{"JSON", FormatJSON},
		{"YAML", FormatYAML},
		{"", FormatTable},
		{"unknown", FormatTable},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			p := NewPrinter(tt.input, false, false)
			assert.Equal(t, tt.want, p.Format)
		})
	}
}

func TestNewPrinter_Flags(t *testing.T) {
	t.Parallel()
	p := NewPrinter("json", true, true)
	assert.True(t, p.Quiet)
	assert.True(t, p.NoColor)
}

func TestPrintJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	data := map[string]string{"name": "test", "status": "running"}
	err := p.PrintJSON(data)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, "running", result["status"])

	// Verify it's indented
	assert.Contains(t, buf.String(), "\n")
	assert.Contains(t, buf.String(), "  ")
}

func TestPrintYAML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	data := map[string]string{"name": "test", "status": "running"}
	err := p.PrintYAML(data)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, "running", result["status"])
}

func TestPrintIDs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintIDs([]uint{1, 42, 100})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, []string{"1", "42", "100"}, lines)
}

func TestPrintIDs_Empty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintIDs([]uint{})
	assert.Empty(t, buf.String())
}

func TestStatusColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  string
		noColor bool
		want    string
	}{
		{name: "running green", status: "running", noColor: false, want: colorGreen + "running" + colorReset},
		{name: "deployed green", status: "deployed", noColor: false, want: colorGreen + "deployed" + colorReset},
		{name: "healthy green", status: "healthy", noColor: false, want: colorGreen + "healthy" + colorReset},
		{name: "online green", status: "online", noColor: false, want: colorGreen + "online" + colorReset},
		{name: "error red", status: "error", noColor: false, want: colorRed + "error" + colorReset},
		{name: "failed red", status: "failed", noColor: false, want: colorRed + "failed" + colorReset},
		{name: "unhealthy red", status: "unhealthy", noColor: false, want: colorRed + "unhealthy" + colorReset},
		{name: "offline red", status: "offline", noColor: false, want: colorRed + "offline" + colorReset},
		{name: "deploying yellow", status: "deploying", noColor: false, want: colorYellow + "deploying" + colorReset},
		{name: "stopping yellow", status: "stopping", noColor: false, want: colorYellow + "stopping" + colorReset},
		{name: "pending yellow", status: "pending", noColor: false, want: colorYellow + "pending" + colorReset},
		{name: "draft gray", status: "draft", noColor: false, want: colorGray + "draft" + colorReset},
		{name: "stopped gray", status: "stopped", noColor: false, want: colorGray + "stopped" + colorReset},
		{name: "unknown gray", status: "unknown", noColor: false, want: colorGray + "unknown" + colorReset},
		{name: "no color", status: "running", noColor: true, want: "running"},
		{name: "unrecognized status", status: "custom", noColor: false, want: "custom"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Printer{NoColor: tt.noColor}
			assert.Equal(t, tt.want, p.StatusColor(tt.status))
		})
	}
}

func TestPrintTable(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	headers := []string{"ID", "NAME", "STATUS"}
	rows := [][]string{
		{"1", "my-stack", "running"},
		{"2", "other-stack", "stopped"},
	}
	p.PrintTable(headers, rows)

	output := buf.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "my-stack")
	assert.Contains(t, output, "other-stack")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "stopped")
}

func TestPrintTable_Empty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintTable([]string{"ID", "NAME"}, nil)
	output := buf.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "NAME")
	// Only header line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 1)
}

func TestPrint_QuietMode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Quiet: true, Format: FormatTable}

	err := p.Print(nil, nil, nil, []uint{5, 10, 15})
	require.NoError(t, err)
	assert.Equal(t, "5\n10\n15\n", buf.String())
}

func TestPrint_JSONFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	data := []map[string]string{{"id": "1"}}
	err := p.Print(data, nil, nil, nil)
	require.NoError(t, err)

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestPrint_YAMLFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	data := []map[string]string{{"id": "1"}}
	err := p.Print(data, nil, nil, nil)
	require.NoError(t, err)

	var result []map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Len(t, result, 1)
}

func TestPrint_TableFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable}

	headers := []string{"ID", "NAME"}
	rows := [][]string{{"1", "test"}}
	err := p.Print(nil, headers, rows, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test")
}

func TestPrintSingle_Table(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatTable}

	fields := []KeyValue{
		{Key: "ID", Value: "42"},
		{Key: "Name", Value: "my-stack"},
		{Key: "Status", Value: "running"},
	}
	err := p.PrintSingle(nil, fields)
	require.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "ID:")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "my-stack")
}

func TestPrintSingle_JSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatJSON}

	data := map[string]string{"id": "42", "name": "test"}
	err := p.PrintSingle(data, nil)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result["id"])
}

func TestPrintSingle_YAML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf, Format: FormatYAML}

	data := map[string]string{"id": "42", "name": "test"}
	err := p.PrintSingle(data, nil)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &result))
	assert.Equal(t, "42", result["id"])
}

func TestPrintMessage(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &Printer{Writer: &buf}

	p.PrintMessage("Hello %s, count: %d", "world", 42)
	assert.Equal(t, "Hello world, count: 42\n", buf.String())
}
