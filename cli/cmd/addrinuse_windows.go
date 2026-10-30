//go:build windows

package cmd

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isAddrInUseOS reports WSAEADDRINUSE, which syscall.EADDRINUSE does not
// match on Windows.
func isAddrInUseOS(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
