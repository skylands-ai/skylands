//go:build !unix

package clone

import "os/exec"

func detach(*exec.Cmd) {}
