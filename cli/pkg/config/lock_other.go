//go:build !unix && !windows

package config

import (
	"errors"
	"io/fs"
	"os"
)

// tryLockFile has no file lock on this platform. Renewal still works, but two
// processes that renew at the same time can end the session.
func tryLockFile(*os.File) (bool, error) { return true, nil }

func unlockFile(*os.File) error { return nil }

// isTransientFileError reports whether a file error can go away on retry.
func isTransientFileError(err error) bool { return errors.Is(err, fs.ErrPermission) }
