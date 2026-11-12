//go:build unix

package terminal

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// spawn runs cmd as the leader of a session of its own, with a new
// terminal of cols by rows as its controlling terminal, and returns the
// terminal's other side.
func spawn(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tty.Close() }()
	if err := setSize(ptmx, cols, rows); err != nil {
		_ = ptmx.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = ptmx.Close()
		return nil, err
	}
	return ptmx, nil
}

// setSize sets the terminal's size. It goes through the file's raw
// connection, which a concurrent Close waits for, rather than Fd, which
// races with one.
func setSize(f *os.File, cols, rows int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		ioErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	}); err != nil {
		return err
	}
	return ioErr
}

// hangUpGroup sends SIGHUP to the process group pgid leads, as a terminal
// hanging up does. An interactive shell passes it on to its jobs and exits.
func hangUpGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGHUP) }

// killGroup kills the process group pgid leads.
func killGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }
