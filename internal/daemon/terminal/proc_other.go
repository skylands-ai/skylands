//go:build !unix

package terminal

import (
	"errors"
	"os"
	"os/exec"
)

var errUnsupported = errors.New("terminals need a Unix host")

func spawn(*exec.Cmd, int, int) (*os.File, error) { return nil, errUnsupported }

func setSize(*os.File, int, int) error { return errUnsupported }

func hangUpGroup(int) {}

func killGroup(int) {}
