//go:build !unix

package watchdog

import "os/exec"

// Without Unix process groups there is no watchdog.
const supported = false

func ownGroup(*exec.Cmd)     {}
func ignoreSignals()         {}
func endGroups(map[int]bool) {}
