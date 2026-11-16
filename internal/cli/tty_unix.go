//go:build linux || darwin

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readSecret asks for a secret on the terminal this process runs in, with
// what's typed kept off the screen. It reads the terminal itself, not
// standard input, so nothing piped to the command can stand in for a
// person typing.
func readSecret(ctx context.Context, prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", errNoTerminal
	}
	defer func() { _ = tty.Close() }()
	fd := int(tty.Fd())
	was, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return "", errNoTerminal
	}
	quiet := *was
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON
	if err := unix.IoctlSetTermios(fd, setTermios, &quiet); err != nil {
		return "", err
	}
	defer func() {
		_ = unix.IoctlSetTermios(fd, setTermios, was)
		fmt.Fprintln(tty)
	}()
	fmt.Fprint(tty, prompt)
	type line struct {
		text string
		err  error
	}
	read := make(chan line, 1)
	go func() {
		s, err := bufio.NewReader(tty).ReadString('\n')
		read <- line{s, err}
	}()
	select {
	case l := <-read:
		if l.err != nil && l.text == "" {
			return "", l.err
		}
		return strings.TrimRight(l.text, "\r\n"), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
