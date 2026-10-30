package cmd

import "fmt"

// ExitError ends stackctl with exit code Code. When Err is not nil, main
// prints the returned error (the ExitError or a wrapper around it; Error()
// is the text of Err) to stderr; a nil Err prints nothing (for example the exit code of
// a remote command of stack exec). Errors of another type exit 1.
type ExitError struct {
	Code int
	Err  error
}

// Error returns the text of Err, or "exit status N" when Err is nil.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap returns Err.
func (e *ExitError) Unwrap() error {
	return e.Err
}

// Exit codes of stack exec besides the exit code of the remote command.
const (
	// exitTimeLimit: the server time limit ended the command (the GNU
	// timeout convention).
	exitTimeLimit = 124
	// exitInterrupted: Ctrl-C or SIGTERM (128 + SIGINT).
	exitInterrupted = 130
	// exitStackctlError: an error of stackctl or the server, not of the
	// remote command (the ssh convention).
	exitStackctlError = 255
)
