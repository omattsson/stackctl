package output

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registry state bleeds across tests since it's package-global. tests that
// register formats clean up via t.Cleanup.
func unregisterForTest(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		formatRegistryMu.Lock()
		defer formatRegistryMu.Unlock()
		for _, n := range names {
			delete(formatRegistry, n)
			delete(singleRegistry, n)
		}
	})
}

func TestRegisterFormat_PanicOnBuiltinOverride(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"table", "json", "yaml"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.PanicsWithValue(t,
				fmt.Sprintf("output: cannot override built-in format %q", name),
				func() { RegisterFormat(name, nil) })
		})
	}
}

func TestNewPrinter_UsesCustomFormat(t *testing.T) {
	t.Parallel()
	RegisterFormat("csv", func(w io.Writer, _ interface{}, headers []string, rows [][]string) error {
		_, err := fmt.Fprintln(w, strings.Join(headers, ","))
		if err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := fmt.Fprintln(w, strings.Join(r, ",")); err != nil {
				return err
			}
		}
		return nil
	})
	unregisterForTest(t, "csv")

	var buf bytes.Buffer
	p := NewPrinter("csv", false, true)
	p.Writer = &buf
	require.Equal(t, Format("csv"), p.Format)

	err := p.Print(nil, []string{"id", "name"}, [][]string{{"1", "alpha"}, {"2", "beta"}}, []string{"1", "2"})
	require.NoError(t, err)
	assert.Equal(t, "id,name\n1,alpha\n2,beta\n", buf.String())
}

func TestNewPrinter_UnknownFormatFallsBackToTable(t *testing.T) {
	t.Parallel()
	p := NewPrinter("neverregistered", false, true)
	assert.Equal(t, FormatTable, p.Format)
}

func TestPrint_CustomFormatReceivesTableArgs(t *testing.T) {
	t.Parallel()

	var (
		gotHeaders []string
		gotRows    [][]string
		gotData    interface{}
	)
	RegisterFormat("capture", func(_ io.Writer, data interface{}, headers []string, rows [][]string) error {
		gotData = data
		gotHeaders = headers
		gotRows = rows
		return nil
	})
	unregisterForTest(t, "capture")

	p := NewPrinter("capture", false, true)
	p.Writer = io.Discard
	err := p.Print("payload", []string{"h1"}, [][]string{{"r1"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "payload", gotData)
	assert.Equal(t, []string{"h1"}, gotHeaders)
	assert.Equal(t, [][]string{{"r1"}}, gotRows)
}

func TestPrintSingle_FallsBackToListFormatterWhenNoSingleRegistered(t *testing.T) {
	t.Parallel()

	var (
		gotRows [][]string
	)
	RegisterFormat("csv2", func(_ io.Writer, _ interface{}, _ []string, rows [][]string) error {
		gotRows = rows
		return nil
	})
	unregisterForTest(t, "csv2")

	p := NewPrinter("csv2", false, true)
	p.Writer = io.Discard
	require.NoError(t, p.PrintSingle(nil, []KeyValue{{Key: "id", Value: "42"}, {Key: "name", Value: "x"}}))

	assert.Equal(t, [][]string{{"id", "42"}, {"name", "x"}}, gotRows)
}

func TestRegisterSingleFormat_UsedWhenPresent(t *testing.T) {
	t.Parallel()

	var called bool
	RegisterFormat("custom1", func(io.Writer, interface{}, []string, [][]string) error { return nil })
	RegisterSingleFormat("custom1", func(_ io.Writer, _ interface{}, fields []KeyValue) error {
		called = true
		assert.Len(t, fields, 2)
		return nil
	})
	unregisterForTest(t, "custom1")

	p := NewPrinter("custom1", false, true)
	p.Writer = io.Discard
	require.NoError(t, p.PrintSingle(nil, []KeyValue{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}))
	assert.True(t, called, "single formatter takes precedence over list formatter when registered")
}

// TestRegisterFormat_ConcurrentReadsWithoutRegistration ensures that the
// read path is safe when no writers are active. Combined reads/writes are
// not supported — documented as "register at init time".
func TestRegisterFormat_ConcurrentReadsWithoutRegistration(t *testing.T) {
	t.Parallel()
	RegisterFormat("concurrent", func(io.Writer, interface{}, []string, [][]string) error { return nil })
	unregisterForTest(t, "concurrent")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = lookupFormat("concurrent")
			}
		}()
	}
	wg.Wait()
}
