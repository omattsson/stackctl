package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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

// FormatterFunc renders data in a custom format to w. The headers/rows
// pair is populated for list-style output; data is the same payload for
// a structured format like JSON. Implementations may use either or both.
type FormatterFunc func(w io.Writer, data interface{}, headers []string, rows [][]string) error

// SingleFormatterFunc renders a single item's key/value fields. Optional;
// when a custom format does not register a single formatter, PrintSingle
// falls back to the list formatter with headers=["field", "value"].
type SingleFormatterFunc func(w io.Writer, data interface{}, fields []KeyValue) error

var (
	formatRegistryMu sync.RWMutex
	formatRegistry   = map[string]FormatterFunc{}
	singleRegistry   = map[string]SingleFormatterFunc{}
)

// RegisterFormat attaches a named custom format. Call at init time, before
// any Printer is used. The registry is synchronised so registration during
// rendering is not a Go data race, but doing so is unsupported and may lead
// to inconsistent behaviour across concurrent renders.
//
// Name is normalised with strings.ToLower + TrimSpace before storage, so
// lookups match NewPrinter's case-insensitive resolution. Passing a built-in
// name (table, json, yaml) panics to surface the mistake early. A nil fn
// also panics at registration time, so the "which handler?" error surfaces
// at startup rather than inside a render call that could be minutes later.
func RegisterFormat(name string, fn FormatterFunc) {
	norm := normalizeFormatName(name)
	if norm == "" {
		panic("output: RegisterFormat called with empty/whitespace-only name")
	}
	if norm == string(FormatTable) || norm == string(FormatJSON) || norm == string(FormatYAML) {
		panic(fmt.Sprintf("output: cannot override built-in format %q", norm))
	}
	if fn == nil {
		panic(fmt.Sprintf("output: nil FormatterFunc for format %q", norm))
	}
	formatRegistryMu.Lock()
	defer formatRegistryMu.Unlock()
	formatRegistry[norm] = fn
}

// RegisterSingleFormat attaches a custom single-item formatter for an
// already-registered format. Optional — only needed when the list and
// single shapes genuinely differ. A nil fn panics at registration time,
// as does registering a single formatter for a format that has no list
// formatter — that combination would silently fall back to a tabwriter
// and is almost always a wiring mistake.
// Names are normalised identically to RegisterFormat.
func RegisterSingleFormat(name string, fn SingleFormatterFunc) {
	norm := normalizeFormatName(name)
	if fn == nil {
		panic(fmt.Sprintf("output: nil SingleFormatterFunc for format %q", norm))
	}
	formatRegistryMu.Lock()
	defer formatRegistryMu.Unlock()
	if _, ok := formatRegistry[norm]; !ok {
		panic(fmt.Sprintf("output: RegisterSingleFormat %q called before RegisterFormat — register the list formatter first", norm))
	}
	singleRegistry[norm] = fn
}

// normalizeFormatName is the single source of truth for turning a user-
// supplied format name into its registry key. Case-insensitive + whitespace
// tolerant.
func normalizeFormatName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// lookupFormat returns the registered list formatter for name.
func lookupFormat(name string) (FormatterFunc, bool) {
	formatRegistryMu.RLock()
	defer formatRegistryMu.RUnlock()
	fn, ok := formatRegistry[normalizeFormatName(name)]
	return fn, ok
}

func lookupSingleFormat(name string) (SingleFormatterFunc, bool) {
	formatRegistryMu.RLock()
	defer formatRegistryMu.RUnlock()
	fn, ok := singleRegistry[normalizeFormatName(name)]
	return fn, ok
}

// Printer handles formatted output.
type Printer struct {
	Format  Format
	Quiet   bool
	NoColor bool
	Writer  io.Writer
}

// NewPrinter creates a new Printer with the given format.
// Falls back to FormatTable if the name is empty, unknown, or a misspelt
// built-in. Custom formats registered via RegisterFormat are recognised.
func NewPrinter(format string, quiet, noColor bool) *Printer {
	name := normalizeFormatName(format)
	var f Format
	switch name {
	case "", "table":
		f = FormatTable
	case "json":
		f = FormatJSON
	case "yaml":
		f = FormatYAML
	default:
		if _, ok := lookupFormat(name); ok {
			f = Format(name)
		} else {
			f = FormatTable
		}
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
func (p *Printer) PrintIDs(ids []string) {
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
	case "running", "deployed", "healthy", "online", "success":
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
func (p *Printer) Print(data interface{}, headers []string, rows [][]string, ids []string) error {
	if p.Quiet {
		p.PrintIDs(ids)
		return nil
	}
	switch p.Format {
	case FormatJSON:
		return p.PrintJSON(data)
	case FormatYAML:
		return p.PrintYAML(data)
	case FormatTable:
		return p.PrintTable(headers, rows)
	default:
		if fn, ok := lookupFormat(string(p.Format)); ok {
			return fn(p.Writer, data, headers, rows)
		}
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
	case FormatTable:
		w := p.TableWriter()
		for _, f := range fields {
			fmt.Fprintf(w, "%s:\t%s\n", f.Key, f.Value)
		}
		return w.Flush()
	default:
		// Custom single formatter wins; otherwise adapt the list formatter by
		// synthesising a two-column (field, value) table.
		if fn, ok := lookupSingleFormat(string(p.Format)); ok {
			return fn(p.Writer, data, fields)
		}
		if fn, ok := lookupFormat(string(p.Format)); ok {
			rows := make([][]string, len(fields))
			for i, f := range fields {
				rows[i] = []string{f.Key, f.Value}
			}
			return fn(p.Writer, data, []string{"field", "value"}, rows)
		}
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
