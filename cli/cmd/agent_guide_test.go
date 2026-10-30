package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command tests use rootCmd and the build globals, so they cannot run
// in parallel.

func TestAgentGuideCmd_PrintsGuideWithVersion(t *testing.T) {
	restoreVersionInfo(t)
	buildVersion = "1.2.3"

	var buf bytes.Buffer
	agentGuideCmd.SetOut(&buf)
	t.Cleanup(func() { agentGuideCmd.SetOut(nil) })

	require.NoError(t, agentGuideCmd.RunE(agentGuideCmd, nil))

	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "# stackctl guide for agents and scripts\n"))
	assert.Contains(t, out, "This guide matches stackctl 1.2.3.")
	assert.NotContains(t, out, "{{VERSION}}")
}

func TestAgentGuideCmd_IgnoresOutputFormat(t *testing.T) {
	// The guide is Markdown in every output mode.
	for _, args := range [][]string{
		{"agent-guide"},
		{"agent-guide", "-o", "json"},
		{"agent-guide", "-o", "yaml"},
		{"agent-guide", "-q"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("STACKCTL_CONFIG_DIR", t.TempDir())
			t.Cleanup(func() {
				flagOutput, flagQuiet = "table", false
				// pluginEnv reads Changed: do not leak it into later tests.
				rootCmd.PersistentFlags().Lookup("output").Changed = false
				rootCmd.PersistentFlags().Lookup("quiet").Changed = false
			})

			var stdout bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetArgs(args)
			t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetArgs(nil) })

			require.NoError(t, rootCmd.Execute())
			assert.Equal(t, renderAgentGuide(), stdout.String())
		})
	}
}

func TestAgentGuideCmd_RejectsArgs(t *testing.T) {
	assert.Error(t, agentGuideCmd.Args(agentGuideCmd, []string{"extra"}))
}

func TestAgentGuideWithInvalidConfig(t *testing.T) {
	// agent-guide must work with a corrupted config, like version.
	tmpDir := t.TempDir()
	t.Setenv("STACKCTL_CONFIG_DIR", tmpDir)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(":\n- invalid\n"), 0600))

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"agent-guide"})
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetArgs(nil) })

	require.NoError(t, rootCmd.Execute())
	assert.Contains(t, stdout.String(), "# stackctl guide for agents and scripts")
}

func TestRootHelp_PointsToAgentGuide(t *testing.T) {
	assert.Contains(t, rootCmd.Long, "stackctl agent-guide")
}

// TestAgentGuide_CommandsExist keeps the guide in step with the CLI: every
// stackctl invocation in a code span or code block must name existing
// commands, each of its flags must exist on that command, and the command
// must accept the number of positional arguments. Commands in prose without
// the stackctl prefix are not checked. It reads rootCmd, so it does not run
// in parallel.
func TestAgentGuide_CommandsExist(t *testing.T) {
	invocations := guideInvocations(agentGuide)
	require.NotEmpty(t, invocations)

	for _, inv := range invocations {
		t.Run(inv, func(t *testing.T) {
			cmd, rest, err := walkCommand(rootCmd, shellWords(inv)[1:])
			require.NoError(t, err)
			args, err := positionalArgs(cmd, rest)
			require.NoError(t, err)
			if slices.Contains(rest, "--") {
				// A validator that reads ArgsLenAtDash (stack exec) needs
				// parsed flags.
				require.NoError(t, cmd.ParseFlags(rest))
				t.Cleanup(func() { resetParsedFlags(cmd) })
				args = cmd.Flags().Args()
			}
			assert.NoError(t, cmd.ValidateArgs(args), "arguments of %s", cmd.CommandPath())
		})
	}
}

func TestShellWords(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		[]string{"stackctl", "stack", "update", "x", "--stop-at", "<YYYY-MM-DD HH:MM>", "$ID"},
		shellWords(`stackctl stack update x --stop-at "<YYYY-MM-DD HH:MM>" "$ID"`))
}

func TestPositionalArgs(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *cobra.Command
		args    []string
		want    []string
		wantErr string
	}{
		{"value flag takes the next word", stackCreateCmd, []string{"x", "--definition", "d", "--branch=b"}, []string{"x"}, ""},
		{"bool and global flags", stackDeleteCmd, []string{"--dry-run", "x", "-o", "json", "-q", "--help"}, []string{"x"}, ""},
		{"unknown flag", stackCreateCmd, []string{"--nope"}, nil, "stackctl stack create has no flag --nope"},
		{"words after -- are arguments", stackExecCmd, []string{"x", "web", "-c", "php", "--", "ls", "-la", "--nope"}, []string{"x", "web", "ls", "-la", "--nope"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := positionalArgs(tt.cmd, tt.args)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGuideInvocations(t *testing.T) {
	t.Parallel()

	doc := "Run `stackctl stack get <id>` or `stackctl <command> --help`.\n" +
		"stackctl in prose is skipped, and so is `stackctl-<name>`.\n" +
		"```sh\n" +
		"ID=$(stackctl stack create --name a -q)\n" +
		"stackctl stack list -q | xargs -n 50 stackctl bulk delete --yes # note\n" +
		"```\n"
	// Code blocks come first, then the code spans of the prose.
	assert.Equal(t, []string{
		"stackctl stack create --name a -q",
		"stackctl stack list -q",
		"stackctl bulk delete --yes",
		"stackctl stack get <id>",
	}, guideInvocations(doc))
}

var (
	codeBlockRe  = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	codeSpanRe   = regexp.MustCompile("`([^`\n]+)`")
	invocationRe = regexp.MustCompile(`(?:^|[\s(|;&])(stackctl(?: [^|;&#)\n]*)?)(?:$|[\s|;&#)])`)
)

// guideInvocations returns the stackctl invocations in the code of a
// Markdown document, each cut at a shell operator, a comment or the end of
// a command substitution. Invocations with a placeholder command
// (stackctl <command> --help) are left out.
func guideInvocations(doc string) []string {
	var code []string
	for _, m := range codeBlockRe.FindAllStringSubmatch(doc, -1) {
		code = append(code, strings.Split(m[1], "\n")...)
	}
	prose := codeBlockRe.ReplaceAllString(doc, "")
	for _, m := range codeSpanRe.FindAllStringSubmatch(prose, -1) {
		code = append(code, m[1])
	}

	var out []string
	for _, line := range code {
		for _, m := range invocationRe.FindAllStringSubmatch(line, -1) {
			inv := strings.TrimSpace(m[1])
			fields := strings.Fields(inv)
			if len(fields) > 1 && strings.HasPrefix(fields[1], "<") {
				continue
			}
			out = append(out, inv)
		}
	}
	return out
}

// walkCommand follows args through the subcommands of root. It stops at the
// first flag or at a command without subcommands (the rest are its
// arguments) and fails on an unknown subcommand.
func walkCommand(root *cobra.Command, args []string) (*cobra.Command, []string, error) {
	cmd := root
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") || !cmd.HasSubCommands() {
			return cmd, args[i:], nil
		}
		next, _, err := cmd.Find([]string{arg})
		if err != nil || next == cmd {
			return nil, nil, &unknownCommandError{path: cmd.CommandPath(), name: arg}
		}
		cmd = next
	}
	return cmd, nil, nil
}

type unknownCommandError struct{ path, name string }

func (e *unknownCommandError) Error() string {
	return e.path + " has no subcommand " + e.name
}

// shellWords splits a command line at spaces; double quotes keep words
// together and are removed.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			inWord = true
		case r == ' ' && !quoted:
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// positionalArgs returns the positional arguments of cmd in args. A flag
// that is not boolean takes the next word as its value unless it has
// "=value". It fails on a flag that cmd does not have.
func positionalArgs(cmd *cobra.Command, args []string) ([]string, error) {
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// The words after "--" are arguments, not flags.
			return append(positional, args[i+1:]...), nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "help" || name == "h" {
			// Cobra adds the help flag only when a command runs.
			continue
		}
		f := lookupFlag(cmd, name)
		if f == nil {
			return nil, fmt.Errorf("%s has no flag %s", cmd.CommandPath(), arg)
		}
		if f.Value.Type() != "bool" && !hasValue {
			i++
		}
	}
	return positional, nil
}

// resetParsedFlags undoes cmd.ParseFlags: the flag values and the "--"
// position (pflag keeps ArgsLenAtDash from an earlier parse).
//
// Only scalar flags are reset correctly: Value.Set(DefValue) appends to a
// slice or array flag (StringSlice, StringArray, ...) and parses "[]" as a
// value, instead of clearing it. The commands reset here (stack exec and the
// root command) have no changed slice flags today; reset a slice flag
// through pflag.SliceValue.Replace if one is added.
func resetParsedFlags(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	fs.Init(fs.Name(), pflag.ContinueOnError)
}

// lookupFlag finds a local or inherited flag by name, or by shorthand for a
// one-letter name.
func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags()} {
		if len(name) == 1 {
			if f := fs.ShorthandLookup(name); f != nil {
				return f
			}
		} else if f := fs.Lookup(name); f != nil {
			return f
		}
	}
	return nil
}
