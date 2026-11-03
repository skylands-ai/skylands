//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package filelock

import (
	"errors"
	"os"
)

var errHeld = errors.New("held")

// Without flock, there is no lock.
func lock(*os.File) error { return nil }
