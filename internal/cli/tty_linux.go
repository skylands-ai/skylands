package cli

import "golang.org/x/sys/unix"

// The ioctls that read and set a terminal's modes.
const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)
