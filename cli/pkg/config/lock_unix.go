//go:build unix

package config

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes an exclusive flock on f without blocking. It returns
// false when another open file description holds the lock.
func tryLockFile(f *os.File) (bool, error) {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// isTransientFileError reports whether a file error can go away on retry.
func isTransientFileError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, unix.EBUSY) || errors.Is(err, unix.EINTR)
}
