package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/omattsson/stackctl/cli/pkg/types"
)

// execOp names the exec route in handshake errors.
const execOp = "exec"

// Message types of the exec WebSocket (types.ExecStartMessage,
// types.ExecServerMessage).
const (
	ExecMessageStart   = "start"
	ExecMessageStarted = "started"
	ExecMessageExit    = "exit"
	ExecMessageError   = "error"
)

// Stream bytes: the first byte of a binary frame of the exec WebSocket.
const (
	ExecStreamStdout byte = 1
	ExecStreamStderr byte = 2
)

const (
	// ExecMaxArgs is the largest number of arguments of a command (a fixed
	// rule of the server). The total length limit is a server setting
	// (STACK_EXEC_MAX_COMMAND_LENGTH) that clients cannot read: a command
	// that is too long gets close 4400.
	ExecMaxArgs = 256
	// execReadLimit bounds a server frame (the server sends at most 32 KiB).
	execReadLimit = 1 << 20
	// DefaultExecCancelWait is how long Exec waits for the exit frame after
	// a cancel.
	DefaultExecCancelWait = 2 * time.Second
	// execExitCloseTimeout bounds the close frame after the exit frame.
	execExitCloseTimeout = time.Second
	// stillRuns ends the message of a stream that ended without an exit
	// code: an exec without a TTY does not always stop the process.
	stillRuns = "; the command can still run on the server"
)

// ErrExecTimeLimit reports that the server closed the exec session at its
// maximum duration (close 4001). The server sends no exit code then.
var ErrExecTimeLimit = errors.New("exec closed: server time limit reached" + stillRuns)

// ExecRequest is the command of Exec.
type ExecRequest struct {
	// Chart is the chart (Helm release name) of the stack.
	Chart string
	// Container is the container name; empty lets the server choose the
	// first allowed container.
	Container string
	// Command is the program and its arguments. It goes only in the first
	// message, never in the URL. The server never uses a shell.
	Command []string
	// Keepalive changes the ping and read timings (zero fields: defaults).
	Keepalive KeepaliveTimings
	// CancelWait is how long Exec waits for the exit frame after ctx ends
	// (default DefaultExecCancelWait).
	CancelWait time.Duration
	// OnStarted, when set, runs once when the server sends the started
	// frame, with the sanitized pod and container names. It runs on the
	// reader goroutine, before any output.
	OnStarted func(pod, container string)
}

// ExecResult is the end of a command that reported an exit code.
type ExecResult struct {
	ExitCode int
	// Pod and Container are from the started frame (sanitized; empty when
	// the server sent none).
	Pod       string
	Container string
}

// ExecClosedError reports that the exec stream ended without an exit code
// and without a cancel: a close frame of the server (other than 4001, see
// ErrExecTimeLimit), or a lost connection.
type ExecClosedError struct {
	// Code is the close code of the server; 0 when the connection was lost
	// without a close frame (also a gorilla 1006).
	Code int
	// Reason is the sanitized close reason.
	Reason string
	// Message is the sanitized message of the error frame of the server
	// (4502, 1011), or "".
	Message string
	// Started is true when the server sent the started frame or output (a
	// binary frame).
	Started bool
	// APIKey is true when the stream used an API key.
	APIKey bool
	err    error
}

// notStarted ends the message of a close frame that came before the started
// frame: the server sends started before it runs the command, and its frames
// arrive in order.
const notStarted = "; the server did not start the command"

// Error returns a message for the user, for example "exec failed: the server
// refused the command (too long or not valid): command is longer than 4096
// bytes".
func (e *ExecClosedError) Error() string {
	// A close frame without the started frame before it: the command did not
	// run. Without a close frame (code 0) a started frame can be lost.
	end := stillRuns
	if !e.Started {
		end = notStarted
	}
	switch e.Code {
	case 0:
		const lost = "exec: connection lost" + stillRuns
		var ne net.Error
		var ce *websocket.CloseError
		switch {
		case errors.Is(e.err, websocket.ErrReadLimit):
			return "exec failed: the server sent a frame larger than 1 MiB; stackctl closed the connection" + stillRuns
		case errors.As(e.err, &ce) && ce.Code == websocket.CloseAbnormalClosure:
			// gorilla's text ("close 1006 (abnormal closure): unexpected
			// EOF") says nothing more.
			return lost
		case errors.As(e.err, &ne) && ne.Timeout():
			return lost + " (no response from the server)"
		case e.err != nil:
			return lost + " (" + e.err.Error() + ")"
		}
		return lost
	case CloseNormal, closeNoStatus:
		if !e.Started {
			return "exec: the server closed the connection without an exit code" + notStarted
		}
		return "exec: connection lost" + stillRuns + " (the server closed the connection without an exit code)"
	case ClosePolicy:
		return "exec closed: " + e.policyText() + end
	case CloseGoingAway:
		return "exec closed: the server is shutting down" + end
	case CloseBadStart:
		return "exec failed: " + CloseCodeText(e.Code, e.Reason, e.APIKey)
	case CloseStartTimeout:
		return "exec failed: the server received no command in time (close 4408); try again"
	case CloseMessageTooBig:
		// stackctl sends only the start message.
		return "exec failed: the command is too long for the server (close 1009)"
	case CloseInternalError:
		msg := "exec failed: internal server error"
		if e.Message != "" && !strings.EqualFold(e.Message, "internal server error") {
			msg += ": " + e.Message
		}
		return msg + end
	case ClosePodStreamError:
		msg := "exec failed: pod stream error"
		if detail := e.Message; detail != "" {
			msg += ": " + detail
		} else if e.Reason != "" {
			msg += ": " + e.Reason
		}
		return msg + end
	}
	return "exec closed: " + CloseCodeText(e.Code, e.Reason, e.APIKey) + end
}

// policyText is the text of close 1008. It never says "run the command
// again" while the command can still run: exec is not idempotent.
func (e *ExecClosedError) policyText() string {
	switch e.Reason {
	case CloseReasonAccessLost, CloseReasonAPIKeyExpired:
		return CloseCodeText(ClosePolicy, e.Reason, e.APIKey)
	case CloseReasonTokenExpired:
		if e.Started {
			return "token expired (close 1008); check the effect of the command before you run it again, and run 'stackctl login' if a new command fails"
		}
		return "token expired (close 1008); run 'stackctl login' if a new command fails"
	}
	reason := e.Reason
	if reason == "" {
		reason = "policy violation"
	}
	if e.APIKey {
		return reason + " (close 1008); the API key was deleted or its user changed; check it with 'stackctl apikey list'"
	}
	return reason + " (close 1008); log in again with 'stackctl login'"
}

// Unwrap returns a *WSClosedError for close code 1008 (so callers that
// handle a revoked /ws connection handle a revoked exec too), the read error
// for a lost connection, else nil.
func (e *ExecClosedError) Unwrap() error {
	if e.Code == ClosePolicy {
		return &WSClosedError{Code: e.Code, Reason: e.Reason, APIKey: e.APIKey}
	}
	return e.err
}

// ExecCanceledError reports that the context of Exec ended (Ctrl-C) before
// an exit frame. errors.Is matches the error of the context
// (context.Canceled or context.DeadlineExceeded).
type ExecCanceledError struct {
	// Started is false only when the command surely did not start: the
	// context ended before, during or right after the dial (before the
	// command was sent), or the server closed the connection (a close
	// frame) without a started frame or output before it. It is true when
	// the started frame or output came, and also when the stream ended
	// without a close frame of the server (a started frame can be unread).
	Started bool
	// Err is ctx.Err().
	Err error
}

// Error returns "exec canceled; the command can still run on the server",
// or "exec canceled before the command started".
func (e *ExecCanceledError) Error() string {
	if !e.Started {
		return "exec canceled before the command started"
	}
	return "exec canceled" + stillRuns
}

// Unwrap returns the error of the context.
func (e *ExecCanceledError) Unwrap() error {
	return e.Err
}

// ValidateExecCommand checks the fixed rules of the server for a command: 1
// to ExecMaxArgs arguments, the first one not empty, no NUL byte. The length
// limit is a server setting and is not checked.
func ValidateExecCommand(command []string) error {
	switch {
	case len(command) == 0:
		return errors.New("the command is empty")
	case len(command) > ExecMaxArgs:
		return fmt.Errorf("the command has %d arguments; the server allows at most %d", len(command), ExecMaxArgs)
	case command[0] == "":
		return errors.New("the first argument of the command (the program) is empty")
	}
	for i, a := range command {
		if strings.IndexByte(a, 0) >= 0 {
			return fmt.Errorf("argument %d of the command contains a NUL byte", i+1)
		}
	}
	return nil
}

// execOutcome is the end of the reader goroutine.
type execOutcome struct {
	exit           *int
	pod, container string
	// started is true after the started frame or any binary frame (output
	// proves that the command ran).
	started bool
	// startedFrame is true after the started frame (pod and container set).
	startedFrame bool
	// errMsg is the sanitized message of an error frame.
	errMsg string
	// err is the read error that ended the stream (nil after an exit frame).
	err error
	// localErr is a failed write to stdout or stderr.
	localErr error
}

// Exec runs req.Command in a container of the chart req.Chart of a stack
// instance (GET /api/v1/stack-instances/:id/exec?chart=&container=) and
// copies its output: stdout frames to stdout, stderr frames to stderr, each
// write unbuffered and in the order of the server. It never reads stdin and
// never reconnects.
//
// The route accepts only header auth (no ?token= retry). A refused upgrade
// gives a *WSHandshakeError with Op "exec" and the error code of the server.
//
// Results:
//   - an exit frame gives the ExecResult (also for a non-zero exit code);
//   - close 4001 gives ErrExecTimeLimit;
//   - another close or a lost connection gives a *ExecClosedError;
//   - a failed write to stdout or stderr gives an error;
//   - when ctx ends, Exec sends close 1000 and waits up to req.CancelWait
//     for an exit frame: it returns that result with a nil error, else a
//     *ExecCanceledError (errors.Is matches ctx.Err()); its Started field
//     tells whether the command can have started.
//
// Exec leaves no goroutine behind; it waits for a write to stdout or stderr
// that blocks. It never logs the command or the output; with Debug it logs
// only the pod and the container of the started frame.
func (c *Client) Exec(ctx context.Context, instanceID string, req ExecRequest, stdout, stderr io.Writer) (*ExecResult, error) {
	if strings.TrimSpace(req.Chart) == "" {
		return nil, errors.New("the chart name must not be empty")
	}
	if err := ValidateExecCommand(req.Command); err != nil {
		return nil, err
	}
	start, err := json.Marshal(types.ExecStartMessage{Type: ExecMessageStart, Command: req.Command})
	if err != nil {
		return nil, fmt.Errorf("encoding the command: %w", err)
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	k := req.Keepalive.withDefaults()
	cancelWait := req.CancelWait
	if cancelWait <= 0 {
		cancelWait = DefaultExecCancelWait
	}

	q := url.Values{}
	q.Set("chart", req.Chart)
	if req.Container != "" {
		q.Set("container", req.Container)
	}
	path := fmt.Sprintf("/api/v1/stack-instances/%s/exec", url.PathEscape(instanceID)) + "?" + q.Encode()
	// stream: true: header auth only. dialWSOpts returns before the ?token=
	// fallback for a stream route, so the token never goes in the URL.
	ws, err := c.dialWSOpts(ctx, path, c.WarnWriter, wsDialOptions{stream: true, op: execOp})
	if err != nil {
		if ctx.Err() != nil {
			// The command goes in the first message, after the dial.
			return nil, &ExecCanceledError{Err: ctx.Err()}
		}
		var hs *WSHandshakeError
		if errors.As(err, &hs) {
			hs.Container = req.Container
		}
		return nil, err
	}
	if ctx.Err() != nil {
		// A cancel right after the dial: never send the command.
		_ = ws.Close()
		return nil, &ExecCanceledError{Err: ctx.Err()}
	}
	ws.SetReadLimit(execReadLimit)
	extend := setKeepalive(ws, k)

	readDone := make(chan execOutcome, 1)
	stopPing := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readDone <- c.execRead(ws, extend, stdout, stderr, req.OnStarted)
	}()
	go func() {
		defer wg.Done()
		pingLoop(ws, k, stopPing)
	}()
	teardown := func() {
		close(stopPing)
		_ = ws.Close()
		wg.Wait()
	}

	// The only data write of the connection. Pings and closes use
	// WriteControl, which is safe next to it.
	_ = ws.SetWriteDeadline(time.Now().Add(k.WriteTimeout))
	if werr := ws.WriteMessage(websocket.TextMessage, start); werr != nil {
		// The reader usually holds the close frame that explains it.
		select {
		case o := <-readDone:
			teardown()
			return c.execEnd(o)
		case <-ctx.Done():
			teardown()
			return nil, &ExecCanceledError{Started: (<-readDone).mayHaveStarted(), Err: ctx.Err()}
		case <-time.After(interruptCloseTimeout):
			teardown()
			return nil, fmt.Errorf("exec: sending the command: %w", werr)
		}
	}

	select {
	case o := <-readDone:
		if o.exit != nil || o.localErr != nil {
			_ = sendClose(ws, CloseNormal, execExitCloseTimeout)
		}
		teardown()
		return c.execEnd(o)
	case <-ctx.Done():
		_ = sendClose(ws, CloseNormal, interruptCloseTimeout)
		timer := time.NewTimer(cancelWait)
		defer timer.Stop()
		var o execOutcome
		select {
		case o = <-readDone:
			teardown()
		case <-timer.C:
			teardown()
			// teardown waited for the reader: its outcome is buffered.
			o = <-readDone
		}
		if o.exit != nil {
			return &ExecResult{ExitCode: *o.exit, Pod: o.pod, Container: o.container}, nil
		}
		return nil, &ExecCanceledError{Started: o.mayHaveStarted(), Err: ctx.Err()}
	}
}

// mayHaveStarted reports whether the command can have started: the reader
// saw the started frame, or it ended without a close frame of the server
// (a lost connection, a read error, or the teardown of stackctl), so a
// started frame can have been lost or unread. Frames of the server arrive in
// order, and the server sends started before it runs the command.
func (o execOutcome) mayHaveStarted() bool {
	if o.started {
		return true
	}
	var ce *websocket.CloseError
	return !errors.As(o.err, &ce) || ce.Code == websocket.CloseAbnormalClosure
}

// execEnd classifies the end of the reader goroutine.
func (c *Client) execEnd(o execOutcome) (*ExecResult, error) {
	if o.localErr != nil {
		return nil, fmt.Errorf("exec: writing the output: %w"+stillRuns, o.localErr)
	}
	if o.exit != nil {
		return &ExecResult{ExitCode: *o.exit, Pod: o.pod, Container: o.container}, nil
	}
	apiKey := c.APIKey != ""
	var ce *websocket.CloseError
	// gorilla reports a lost connection as close code 1006; no frame came.
	if errors.As(o.err, &ce) && ce.Code != websocket.CloseAbnormalClosure {
		if ce.Code == CloseMaxDuration {
			return nil, ErrExecTimeLimit
		}
		return nil, &ExecClosedError{
			Code: ce.Code, Reason: sanitizeServerMessage(ce.Text), Message: o.errMsg,
			Started: o.started, APIKey: apiKey,
		}
	}
	return nil, &ExecClosedError{Message: o.errMsg, Started: o.started, APIKey: apiKey, err: o.err}
}

// execRead is the only reader of ws. It writes the binary frames to stdout
// and stderr and handles the text frames, until the exit frame or a read
// error. Unknown stream bytes, empty binary frames, malformed text frames
// and unknown message types are ignored (a binary frame still counts as
// proof that the command started); no frame is ever logged.
func (c *Client) execRead(ws *websocket.Conn, extend func(), stdout, stderr io.Writer, onStarted func(pod, container string)) (o execOutcome) {
	buf := make([]byte, maxTunnelFrame)
	for {
		extend()
		mt, r, err := ws.NextReader()
		if err != nil {
			o.err = err
			return o
		}
		switch mt {
		case websocket.BinaryMessage:
			// The server sends output only after it started the command,
			// also when the started frame was malformed.
			o.started = true
			rerr, werr := copyExecFrame(r, buf, extend, stdout, stderr)
			if werr != nil {
				o.localErr = werr
				return o
			}
			if rerr != nil {
				o.err = rerr
				return o
			}
		case websocket.TextMessage:
			data, err := io.ReadAll(r)
			if err != nil {
				o.err = err
				return o
			}
			var msg types.ExecServerMessage
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case ExecMessageStarted:
				if o.startedFrame {
					continue
				}
				o.started, o.startedFrame = true, true
				o.pod, o.container = sanitizeServerMessage(msg.Pod), sanitizeServerMessage(msg.Container)
				c.debugf("exec: started in pod %s, container %s\n", o.pod, o.container)
				if onStarted != nil {
					onStarted(o.pod, o.container)
				}
			case ExecMessageExit:
				if msg.ExitCode == nil {
					continue
				}
				code := *msg.ExitCode
				o.exit = &code
				return o
			case ExecMessageError:
				o.errMsg = sanitizeServerMessage(msg.Message)
			}
		}
	}
}

// copyExecFrame writes one binary frame without its stream byte to stdout
// (1) or stderr (2), in chunks as they arrive. A frame without data or with
// another stream byte is read and dropped. rerr is a read error of ws, werr a
// failed local write.
func copyExecFrame(r io.Reader, buf []byte, extend func(), stdout, stderr io.Writer) (rerr, werr error) {
	var stream [1]byte
	if _, err := io.ReadFull(r, stream[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil // an empty frame
		}
		return err, nil
	}
	w := io.Discard
	switch stream[0] {
	case ExecStreamStdout:
		w = stdout
	case ExecStreamStderr:
		w = stderr
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			extend()
			if _, werr := w.Write(buf[:n]); werr != nil {
				return nil, werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return err, nil
		}
	}
}
