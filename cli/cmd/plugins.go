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
func newPluginCommand(name, binaryPath string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              "Plugin: " + name + " (" + binaryPath + ")",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			proc := exec.Command(binaryPath, args...) //nolint:gosec // binary path discovered from PATH, not user input
			proc.Stdin = cmd.InOrStdin()
			proc.Stdout = cmd.OutOrStdout()
			proc.Stderr = cmd.ErrOrStderr()
			proc.Env = os.Environ()
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
