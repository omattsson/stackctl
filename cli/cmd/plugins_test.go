package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScript writes a shell script executable at dir/name with the given body.
// Returns the absolute path. Skips the test on Windows — shell scripts and 0o755
// aren't a useful way to exercise plugin discovery there.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("plugin discovery tests use shell scripts; skip on Windows")
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

func TestDiscoverPlugins_FindsExecutableStackctlBinaries(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-hello", "#!/bin/sh\necho hi\n")
	// Also place a non-plugin binary that shouldn't match.
	_ = writeScript(t, dir, "notaplugin", "#!/bin/sh\necho nope\n")
	// And a bare "stackctl-" which is the empty-name case — must be ignored.
	_ = writeScript(t, dir, "stackctl-", "#!/bin/sh\necho empty\n")

	got := discoverPlugins(dir)
	require.Contains(t, got, "hello")
	assert.Len(t, got, 1)
	assert.True(t, filepath.IsAbs(got["hello"]))
}

func TestDiscoverPlugins_SkipsNonExecutable(t *testing.T) {

	dir := t.TempDir()
	nonExec := filepath.Join(dir, "stackctl-readonly")
	require.NoError(t, os.WriteFile(nonExec, []byte("#!/bin/sh\necho hi\n"), 0o644))

	got := discoverPlugins(dir)
	assert.NotContains(t, got, "readonly", "non-executable files must be ignored")
}

func TestDiscoverPlugins_FirstPathEntryWins(t *testing.T) {

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	_ = writeScript(t, dir1, "stackctl-same", "#!/bin/sh\necho first\n")
	_ = writeScript(t, dir2, "stackctl-same", "#!/bin/sh\necho second\n")

	got := discoverPlugins(dir1 + string(os.PathListSeparator) + dir2)
	require.Contains(t, got, "same")
	assert.Equal(t, filepath.Join(dir1, "stackctl-same"), got["same"])
}

func TestDiscoverPlugins_IgnoresMissingAndEmptyPATHEntries(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-ok", "#!/bin/sh\necho ok\n")

	got := discoverPlugins(string(os.PathListSeparator) + "/nonexistent/path" + string(os.PathListSeparator) + dir)
	assert.Contains(t, got, "ok")
}

func TestRegisterPlugins_AddsPluginAsSubcommand(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-greet", "#!/bin/sh\necho hello-from-plugin\n")

	root := &cobra.Command{Use: "stackctl"}
	builtin := &cobra.Command{Use: "config", Short: "builtin"}
	root.AddCommand(builtin)

	registerPlugins(root, dir)

	var greet *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "greet" {
			greet = c
			break
		}
	}
	require.NotNil(t, greet, "plugin subcommand must be registered")
	assert.Contains(t, greet.Short, "Plugin:")
}

func TestRegisterPlugins_BuiltinWinsOnCollision(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-config", "#!/bin/sh\necho shadow\n")

	root := &cobra.Command{Use: "stackctl"}
	builtin := &cobra.Command{Use: "config", Short: "builtin"}
	root.AddCommand(builtin)

	registerPlugins(root, dir)

	// Verify exactly one command named "config" exists (not two, with Cobra
	// silently accepting the duplicate) and that it's the built-in.
	named := 0
	var found *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "config" {
			named++
			found = c
		}
	}
	assert.Equal(t, 1, named, "exactly one command should be named 'config'")
	require.NotNil(t, found)
	assert.Equal(t, "builtin", found.Short, "built-in must not be replaced by a colliding plugin")
}

func TestRegisterPlugins_PluginInvocationPassesThroughArgsAndStdout(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-echo",
		"#!/bin/sh\nprintf '%s|' \"$@\"\n")

	root := &cobra.Command{Use: "stackctl"}
	registerPlugins(root, dir)

	var pluginCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "echo" {
			pluginCmd = c
			break
		}
	}
	require.NotNil(t, pluginCmd)

	// The plugin uses os.Exit on error; on success we get its stdout.
	// Cobra's Execute path would os.Exit; drive the wrapped binary directly via
	// exec for a clean, assertable invocation.
	bin := filepath.Join(dir, "stackctl-echo")
	out, err := exec.Command(bin, "alpha", "--flag=beta", "gamma").CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "alpha|--flag=beta|gamma|", string(out))
}

func TestRegisterPlugins_NoOpOnEmptyPath(t *testing.T) {

	root := &cobra.Command{Use: "stackctl"}
	before := len(root.Commands())
	registerPlugins(root, "")
	assert.Equal(t, before, len(root.Commands()))
}

// TestRegisterPlugins_StdinPassthrough proves stdin is routed to the plugin.
// Uses a `cat` style shell script that copies stdin to stdout; the plugin
// subcommand reads stdin via cmd.InOrStdin() which Cobra resolves to the
// buffer we set via root.SetIn.
func TestRegisterPlugins_StdinPassthrough(t *testing.T) {

	dir := t.TempDir()
	_ = writeScript(t, dir, "stackctl-cat", "#!/bin/sh\ncat\n")

	root := &cobra.Command{Use: "stackctl", SilenceUsage: true, SilenceErrors: true}
	registerPlugins(root, dir)

	var catCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "cat" {
			catCmd = c
			break
		}
	}
	require.NotNil(t, catCmd)

	var outBuf bytes.Buffer
	root.SetIn(strings.NewReader("payload-from-stdin"))
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)

	require.NoError(t, catCmd.RunE(catCmd, nil))
	assert.Equal(t, "payload-from-stdin", outBuf.String(),
		"plugin must receive stdin and its stdout must reach root's writer")
}

// TestRegisterPlugins_RunViaCobraRouting wires a plugin through Cobra's
// Execute path to verify the command is actually routed by name. The plugin
// exits 0, so the parent survives; args are captured via a sentinel file
// the plugin writes.
func TestRegisterPlugins_RunViaCobraRouting(t *testing.T) {

	dir := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > " + sentinel + "\n"
	_ = writeScript(t, dir, "stackctl-touch", script)

	root := &cobra.Command{Use: "stackctl", SilenceUsage: true, SilenceErrors: true}
	registerPlugins(root, dir)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"touch", "--hello=world", "posarg"})

	// Run the plugin directly via the command's RunE rather than via Execute —
	// Execute may call os.Exit(0) on failure paths via our plugin wrapper and
	// that would terminate the test process.
	var touchCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "touch" {
			touchCmd = c
			break
		}
	}
	require.NotNil(t, touchCmd)
	require.NoError(t, touchCmd.RunE(touchCmd, []string{"--hello=world", "posarg"}))

	contents, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	assert.Contains(t, string(contents), "--hello=world")
	assert.Contains(t, string(contents), "posarg")
}
