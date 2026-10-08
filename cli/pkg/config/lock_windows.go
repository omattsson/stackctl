//go:build windows

package config

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes an exclusive LockFileEx lock on f without blocking. It
// returns false when another handle holds the lock.
func tryLockFile(f *os.File) (bool, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}

// isTransientFileError reports whether a file error can go away on retry.
// Windows refuses a rename or read while another process has the file open
// (sharing violation, or access denied during a pending delete).
func isTransientFileError(err error) bool {
	return errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
