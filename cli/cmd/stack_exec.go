package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/spf13/cobra"
)

var stackExecCmd = &cobra.Command{
	Use:   "exec <name|id> <chart> [-c CONTAINER] [flags] -- COMMAND [ARGS...]",
	Short: "Run a command in a container of a stack",
	Long: `Run one command in a container of a chart of a stack, through the
k8s-stack-manager server (GET /api/v1/stack-instances/:id/exec). You need
no kubeconfig and no cluster access: the server checks your access to the
stack and chooses the pod (the first running and ready pod of the chart).

CHART is the chart (Helm release) name, as in the CHART column of
'stackctl stack pods'. -c selects the container; without it the server
uses the first container that allows exec. The server decides which
containers allow exec; the EXEC column of 'stackctl stack pods' shows
them when the server sends exec hints. When the server refuses the
container (container_not_allowed, container_not_found,
service_account_token), stackctl reads these hints once and the error
names the containers that accept exec (for example: Use -c web or -c
worker), or says that the server lists no container of the chart that
accepts exec.

The '--' is required. Everything after it is the command and its
arguments, passed as a list without a shell: stackctl does not read flags
after '--'. For pipes, variables or globs, run a shell: -- sh -c '...'.

The command gets no stdin and no TTY. Its stdout goes to stdout and its
stderr to stderr, unchanged and as it arrives. -o and -q are ignored.
stackctl never connects again: a lost connection ends stackctl (exit 255).

Exit codes:
  N    the exit code of the remote command (0 to 255)
  124  the server time limit ended the command
  130  interrupted (Ctrl-C or SIGTERM)
  255  an error of stackctl or the server: stack, chart or container not
       found, no access, no ready pod, a lost connection, the refusal of
       --insecure or of an http:// API URL, an exit code outside 0 to 255,
       an exit frame without a valid exit_code
  1    invalid flags or arguments
A remote command can return any code, including 1, 124, 130 and 255;
stackctl's own codes come with a stackctl message on stderr (remote
stderr goes to the same stream, so read the message, not just its
presence). stackctl itself uses 1 only for invalid flags or arguments.

Ctrl-C closes the connection and exits 130 within a few seconds (a second
Ctrl-C exits at once); when the command had already ended, stderr names its
exit code. The remote process can keep running.

Security:
  - The output can show secrets, for example from the environment of the
    container, and can end up in the terminal history or in CI logs.
  - The output of the remote command is passed through unchanged: it can
    hold terminal escape sequences (colors, cursor moves, changes of the
    window title or the clipboard (OSC 52), hyperlinks). When you do not
    trust the container, pipe it through '| cat -v' or write it to a file.
  - The server writes each command, with its arguments, to the audit log.
  - --insecure is refused: without TLS verification, a man in the middle
    can read the commands and their output. An http:// API URL is refused
    for the same reason, except for localhost, 127.0.0.1 and [::1]. Add
    --allow-insecure-stream to accept that risk.
  - API keys are not allowed by default: run 'stackctl login'. With an
    API key, stackctl first reads the public /api/v1/ui-config (without
    credentials) and stops at once (exit 255) when the server does not
    allow API keys for exec (k8s-stack-manager v0.15.0 or later); when
    that read fails, the server decides.

Examples:
  # Clear a cache
  stackctl stack exec my-stack backend -- bin/console cache:clear

  # A shell command line, in a given container
  stackctl stack exec my-stack backend -c php -- sh -c 'ls -la /var/www | head'

  # Flags after '--' belong to the remote command
  stackctl stack exec my-stack backend -- ls -la --color=never /tmp`,
	Args: stackExecArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		if parent == nil {
			parent = context.Background()
		}
		signals, stop := execSignals()
		defer stop()
		return runStackExec(parent, cmd, args, signals)
	},
}

func init() {
	stackExecCmd.Flags().StringP("container", "c", "", "Container name (default: the first container of the chart that allows exec)")
	stackExecCmd.Flags().Bool("allow-insecure-stream", false, "Allow exec with --insecure or an http:// API URL (the commands and their output are not protected against a man in the middle)")
	stackCmd.AddCommand(stackExecCmd)
}

// execSignals returns the channel of the signals that interrupt stack exec
// (Ctrl-C, SIGTERM) and the function that stops their delivery. Tests
// replace it to inject signals.
var execSignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, portForwardSignals...)
	return ch, func() { signal.Stop(ch) }
}

// execForceExit ends the process at the second interrupt. Tests replace it.
var execForceExit = os.Exit

// execKeepalive and execCancelWait are the timings of the exec stream
// (zero: the client defaults). Tests shorten them.
var (
	execKeepalive  client.KeepaliveTimings
	execCancelWait time.Duration
)

// stackExecArgs checks <name|id> <chart> -- COMMAND [ARGS...] and the fixed
// command rules of the server. Its errors exit 1.
func stackExecArgs(cmd *cobra.Command, args []string) error {
	const usage = "usage: stackctl stack exec <name|id> <chart> [-c CONTAINER] -- COMMAND [ARGS...]"
	dash := cmd.ArgsLenAtDash()
	switch {
	case dash < 0:
		return errors.New("missing '--' before the command; " + usage)
	case dash != 2:
		return fmt.Errorf("expected <name|id> and <chart> before '--', got %d argument(s); %s", dash, usage)
	case len(args) == 2:
		return errors.New("missing command after '--'; " + usage)
	case strings.TrimSpace(args[0]) == "":
		return errors.New("stack name or ID must not be empty")
	case strings.TrimSpace(args[1]) == "":
		return errors.New("chart name must not be empty")
	}
	if err := client.ValidateExecCommand(args[2:]); err != nil {
		return fmt.Errorf("invalid command: %w", err)
	}
	return nil
}

// runStackExec runs the command and maps the result to an *ExitError (see
// the exit codes in the help). The first signal on signals cancels the
// exec; a second one calls execForceExit(130).
func runStackExec(parent context.Context, cmd *cobra.Command, args []string, signals <-chan os.Signal) error {
	chart := strings.TrimSpace(args[1])
	command := args[2:]
	container, _ := cmd.Flags().GetString("container")
	container = strings.TrimSpace(container)
	stderr := &syncWriter{w: cmd.ErrOrStderr()}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var interrupted atomic.Bool
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-signals:
		case <-done:
			return
		}
		interrupted.Store(true)
		cancel()
		select {
		case <-signals:
			// Never through stderr: the reader can hold its lock in a
			// blocked write of the remote stderr (for example 2>&1 | less,
			// paused). The raw writer can block too, so wait at most
			// execNoteWait.
			writeNoteNoWait(cmd.ErrOrStderr(), "interrupted", execNoteWait)
			execForceExit(exitInterrupted)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
	}()

	res, err := execOnce(ctx, cmd, args[0], chart, container, command, stderr)
	if interrupted.Load() {
		return interruptedExit(res, err)
	}
	if err != nil {
		if errors.Is(err, client.ErrExecTimeLimit) {
			return &ExitError{Code: exitTimeLimit, Err: err}
		}
		return &ExitError{Code: exitStackctlError, Err: err}
	}
	return remoteExit(res.ExitCode)
}

// interruptedExit maps the result of an interrupted exec to exit 130 and
// says whether the command can have run.
func interruptedExit(res *client.ExecResult, err error) *ExitError {
	var canceled *client.ExecCanceledError
	var closed *client.ExecClosedError
	var refused *client.WSHandshakeError
	switch {
	case res != nil:
		return &ExitError{Code: exitInterrupted, Err: fmt.Errorf("interrupted; the command ended with exit code %d", res.ExitCode)}
	case errors.Is(err, errExecNotStarted), errors.As(err, &canceled) && !canceled.Started:
		return &ExitError{Code: exitInterrupted, Err: errors.New("interrupted before the command started")}
	case errors.As(err, &closed) && closed.Code != 0 && !closed.Started:
		// A close frame of the server without started before it (it can
		// come before the cancel): the command did not run. Keep the text
		// of the server.
		return &ExitError{Code: exitInterrupted, Err: fmt.Errorf("interrupted before the command started: %w", err)}
	case errors.As(err, &refused):
		// The server refused the upgrade (an interrupt during the exec
		// hints call after the refusal): the command did not run. Keep the
		// text of the server.
		return &ExitError{Code: exitInterrupted, Err: fmt.Errorf("interrupted before the command started: %w", err)}
	}
	return &ExitError{Code: exitInterrupted, Err: errors.New("interrupted; the command can still run on the server")}
}

// errExecNotStarted marks an interrupt before the dial.
var errExecNotStarted = errors.New("interrupted before the command started")

// execNoteWait bounds the write of the note of the second interrupt.
const execNoteWait = 100 * time.Millisecond

// writeNoteNoWait writes note and a newline to w from a goroutine and waits
// at most wait for it, so a blocked w cannot delay the caller. The
// goroutine stays blocked with w; the caller exits the process next.
func writeNoteNoWait(w io.Writer, note string, wait time.Duration) {
	written := make(chan struct{})
	go func() {
		defer close(written)
		fmt.Fprintln(w, note)
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-written:
	case <-timer.C:
	}
}

// execOnce resolves the stack and runs the command once. An error before
// the dial after an interrupt is errExecNotStarted.
func execOnce(ctx context.Context, cmd *cobra.Command, stack, chart, container string, command []string, stderr *syncWriter) (*client.ExecResult, error) {
	beforeDial := func(err error) error {
		if ctx.Err() != nil {
			return errExecNotStarted
		}
		return err
	}
	c, err := newClient()
	if err != nil {
		return nil, beforeDial(err)
	}
	if err := checkStreamTransport(cmd, c, stderr, streamRisk{
		op:      "exec",
		data:    "the commands and their output",
		warning: "the commands and their output are not protected against a man in the middle",
	}); err != nil {
		return nil, beforeDial(err)
	}
	c.WarnWriter = stderr
	if c.Debug {
		// The same writer as the remote stderr, so the lines do not mix.
		c.DebugWriter = stderr
	}
	if err := checkStreamAPIKey(ctx, c, "exec"); err != nil {
		return nil, beforeDial(err)
	}
	if ctx.Err() != nil {
		// Ctrl-C during the API-key check: no stack lookup, no dial.
		return nil, errExecNotStarted
	}
	id, err := resolveStackID(c, stack)
	if err != nil {
		return nil, beforeDial(err)
	}
	if ctx.Err() != nil {
		return nil, errExecNotStarted
	}
	res, err := c.Exec(ctx, id, client.ExecRequest{
		Chart: chart, Container: container, Command: command,
		Keepalive: execKeepalive, CancelWait: execCancelWait,
	}, cmd.OutOrStdout(), stderr)
	if err != nil {
		addExecHints(ctx, c, id, chart, err)
	}
	return res, err
}

// addExecHints adds the exec hints of the chart to a refusal that they
// answer (container_not_allowed, container_not_found,
// service_account_token): one best-effort GET /pods?exec_hints=true. Without
// hints, or after an interrupt, the error stays as it is.
func addExecHints(ctx context.Context, c *client.Client, id, chart string, err error) {
	var hs *client.WSHandshakeError
	if !errors.As(err, &hs) || ctx.Err() != nil {
		return
	}
	switch hs.Code {
	case client.StreamCodeContainerNotAllowed, client.StreamCodeContainerNotFound, client.StreamCodeServiceAccountToken:
	default:
		return
	}
	// Build the client before the goroutine: after an interrupt
	// bestEffortCtx returns while the call still runs, and that call must
	// not read c then.
	q := bestEffortClient(c)
	status, ok := bestEffortCtx(ctx, func() (*types.InstanceStatus, error) {
		return q.GetStackPods(id, true)
	})
	if ok {
		hs.ExecHints = client.ExecHintsForChart(status, chart)
	}
}

// remoteExit maps the exit code of the remote command to the exit code of
// stackctl: 0 is success, 1 to 255 pass through without a message. A code
// outside 0 to 255 (the operating system would cut it) exits 255 with a
// message.
func remoteExit(code int) error {
	switch {
	case code == 0:
		return nil
	case code > 0 && code <= 255:
		return &ExitError{Code: code}
	}
	return &ExitError{Code: exitStackctlError, Err: fmt.Errorf("the command exited with code %d, which is outside 0 to 255", code)}
}
