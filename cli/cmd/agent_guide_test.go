package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
			t.Cleanup(func() { flagOutput, flagQuiet = "table", false })

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
// commands, and each of its flags must exist on that command. It reads
// rootCmd, so it does not run in parallel.
func TestAgentGuide_CommandsExist(t *testing.T) {
	invocations := guideInvocations(agentGuide)
	require.NotEmpty(t, invocations)

	for _, inv := range invocations {
		t.Run(inv, func(t *testing.T) {
			cmd, rest, err := walkCommand(rootCmd, strings.Fields(inv)[1:])
			require.NoError(t, err)
			for _, tok := range rest {
				name, ok := flagName(tok)
				if !ok {
					continue
				}
				assert.NotNil(t, lookupFlag(cmd, name), "%s has no flag %s", cmd.CommandPath(), tok)
			}
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

// flagName returns the flag name of a token: "image" for "--image=x", "q"
// for "-q". It returns false for a token that is not a flag.
func flagName(tok string) (string, bool) {
	tok = strings.Trim(tok, `"'`)
	if !strings.HasPrefix(tok, "-") || tok == "-" {
		return "", false
	}
	name := strings.TrimLeft(tok, "-")
	name, _, _ = strings.Cut(name, "=")
	return name, name != ""
}

// lookupFlag finds a local or inherited flag by name or shorthand. Cobra
// adds the help flag only when a command runs, so help and h always exist.
func lookupFlag(cmd *cobra.Command, name string) any {
	if name == "help" || name == "h" {
		return name
	}
	if len(name) == 1 {
		if f := cmd.Flags().ShorthandLookup(name); f != nil {
			return f
		}
		if f := cmd.InheritedFlags().ShorthandLookup(name); f != nil {
			return f
		}
		return nil
	}
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	if f := cmd.InheritedFlags().Lookup(name); f != nil {
		return f
	}
	return nil
}
