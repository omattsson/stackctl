package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format represents the output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// Printer handles formatted output.
type Printer struct {
	Format  Format
	Quiet   bool
	NoColor bool
	Writer  io.Writer
}

// NewPrinter creates a new Printer with the given format.
func NewPrinter(format string, quiet, noColor bool) *Printer {
	f := FormatTable
	switch strings.ToLower(format) {
	case "json":
		f = FormatJSON
	case "yaml":
		f = FormatYAML
	}
	return &Printer{
		Format:  f,
		Quiet:   quiet,
		NoColor: noColor,
		Writer:  os.Stdout,
	}
}

// PrintJSON outputs data as formatted JSON.
func (p *Printer) PrintJSON(v interface{}) error {
	enc := json.NewEncoder(p.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintYAML outputs data as YAML.
func (p *Printer) PrintYAML(v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = p.Writer.Write(data)
	return err
}

// PrintIDs outputs a list of IDs, one per line (for --quiet mode).
func (p *Printer) PrintIDs(ids []uint) {
	for _, id := range ids {
		fmt.Fprintln(p.Writer, id)
	}
}

// Color constants for terminal output.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
	colorCyan   = "\033[36m"
)

// StatusColor returns the colored status string.
func (p *Printer) StatusColor(status string) string {
	if p.NoColor {
		return status
	}
	switch strings.ToLower(status) {
	case "running", "deployed", "healthy", "online":
		return colorGreen + status + colorReset
	case "error", "failed", "unhealthy", "offline":
		return colorRed + status + colorReset
	case "deploying", "stopping", "cleaning", "pending":
		return colorYellow + status + colorReset
	case "draft", "stopped", "unknown":
		return colorGray + status + colorReset
	default:
		return status
	}
}

// TableWriter creates a tabwriter for aligned table output.
func (p *Printer) TableWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(p.Writer, 0, 0, 2, ' ', 0)
}

// PrintTable writes a table with headers and rows.
func (p *Printer) PrintTable(headers []string, rows [][]string) error {
	w := p.TableWriter()
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

// Print outputs data in the configured format. For table format, it uses the provided
// headers and rows. For JSON/YAML, it outputs the raw data.
func (p *Printer) Print(data interface{}, headers []string, rows [][]string, ids []uint) error {
	if p.Quiet {
		p.PrintIDs(ids)
		return nil
	}
	switch p.Format {
	case FormatJSON:
		return p.PrintJSON(data)
	case FormatYAML:
		return p.PrintYAML(data)
	default:
		return p.PrintTable(headers, rows)
	}
}

// PrintSingle outputs a single item in the configured format.
// For table format, it outputs key-value pairs.
func (p *Printer) PrintSingle(data interface{}, fields []KeyValue) error {
	switch p.Format {
	case FormatJSON:
		return p.PrintJSON(data)
	case FormatYAML:
		return p.PrintYAML(data)
	default:
		w := p.TableWriter()
		for _, f := range fields {
			fmt.Fprintf(w, "%s:\t%s\n", f.Key, f.Value)
		}
		return w.Flush()
	}
}

// KeyValue is a simple key-value pair for single-item table output.
type KeyValue struct {
	Key   string
	Value string
}

// PrintMessage prints a plain text message (used for confirmations, status updates).
func (p *Printer) PrintMessage(format string, args ...interface{}) {
	fmt.Fprintf(p.Writer, format+"\n", args...)
}

// PrintError prints an error message to stderr.
func PrintError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}
