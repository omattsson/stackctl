package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// pluginPrefix is the filename prefix that marks an executable as a stackctl
// plugin. A binary at PATH/stackctl-foo becomes the subcommand `stackctl foo`.
const pluginPrefix = "stackctl-"

// registerPlugins scans $PATH for executables named stackctl-<name> and adds
// each as a top-level subcommand that proxies to the external binary.
//
// Behaviour:
//   - Built-in subcommands always win on name collision; the plugin is skipped.
//   - Earlier entries in $PATH win over later duplicates (standard PATH semantics).
//   - Non-regular files, directories, and non-executables are ignored.
//   - Plugin stdout/stderr/stdin pass through the parent tty; exit codes propagate.
//
// Pattern modelled on git, kubectl, and gh. See also:
//
//	https://git-scm.com/docs/git#_git_commands
//	https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/
func registerPlugins(root *cobra.Command, pathEnv string) {
	builtins := existingCommandNames(root)
	for name, path := range discoverPlugins(pathEnv) {
		if _, collides := builtins[name]; collides {
			continue
		}
		root.AddCommand(newPluginCommand(name, path))
		builtins[name] = struct{}{}
	}
}

// discoverPlugins walks pathEnv (a colon-separated PATH) and returns a map
// of plugin name to absolute path. First-win semantics: earlier PATH entries
// take precedence over later ones when multiple binaries share a name.
func discoverPlugins(pathEnv string) map[string]string {
	found := make(map[string]string)
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, pluginPrefix) || name == pluginPrefix {
				continue
			}
			pluginName := strings.TrimPrefix(name, pluginPrefix)
			if _, seen := found[pluginName]; seen {
				continue
			}
			full := filepath.Join(dir, name)
			info, err := os.Stat(full)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if info.Mode().Perm()&0o111 == 0 {
				continue
			}
			found[pluginName] = full
		}
	}
	return found
}

// pluginEnv returns the environment to pass to a plugin subprocess. It
// preserves the full parent environment — plugins might legitimately need
// unrelated variables (AWS_PROFILE, KUBECONFIG, etc.) — and injects
// STACKCTL_* values resolved from flags so a plugin sees the same effective
// config stackctl itself would use for a built-in command.
//
// Flag-to-env wiring documented in EXTENDING.md as a plugin-author contract.
func pluginEnv(cmd *cobra.Command) []string {
	env := os.Environ()
	// Resolve --insecure (flag > env > config). The config layer may not be
	// loaded yet for plugin commands because persistent pre-run depends on
	// the command name, so favor the flag value here.
	if cmd != nil {
		if insecure, err := cmd.Root().PersistentFlags().GetBool("insecure"); err == nil && insecure {
			env = append(env, "STACKCTL_INSECURE=1")
		}
		if quiet, err := cmd.Root().PersistentFlags().GetBool("quiet"); err == nil && quiet {
			env = append(env, "STACKCTL_QUIET=1")
		}
		if output, err := cmd.Root().PersistentFlags().GetString("output"); err == nil && output != "" {
			env = append(env, "STACKCTL_OUTPUT="+output)
		}
	}
	return env
}

// existingCommandNames returns the set of top-level subcommand names already
// registered on root, so discovery can avoid clobbering built-ins.
func existingCommandNames(root *cobra.Command) map[string]struct{} {
	names := make(map[string]struct{})
	for _, c := range root.Commands() {
		names[c.Name()] = struct{}{}
		for _, alias := range c.Aliases {
			names[alias] = struct{}{}
		}
	}
	return names
}

// newPluginCommand wraps an external binary as a Cobra subcommand. The binary
// receives all arguments after the plugin name; stdin/stdout/stderr pass
// through directly, and the exit code propagates to the caller.
//
// binaryPath is captured at discovery time so later PATH modifications can't
// rebind which binary we exec — the absolute path we resolved in
// discoverPlugins is the exact path we run.
func newPluginCommand(name, binaryPath string) *cobra.Command {
	return &cobra.Command{
		// Hide the absolute path from --help listings (leaks home dir in screenshots).
		// The full path is in Long so `stackctl help <plugin>` still reveals it for debugging.
		Use:                name,
		Short:              "Plugin: " + name,
		Long:               "External plugin resolved to " + binaryPath,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			proc := exec.Command(binaryPath, args...) //nolint:gosec // absolute path captured at discovery time; rebinding via PATH is impossible after that point
			proc.Stdin = cmd.InOrStdin()
			proc.Stdout = cmd.OutOrStdout()
			proc.Stderr = cmd.ErrOrStderr()
			proc.Env = pluginEnv(cmd)
			if err := proc.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					// Propagate the plugin's exit code. Cobra will surface
					// the error; explicit os.Exit keeps the code intact.
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("plugin %q: %w", name, err)
			}
			return nil
		},
	}
}
