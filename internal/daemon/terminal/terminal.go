// Package terminal runs a person's shell in a pseudo-terminal on the host,
// in a session's worktree, so they can build and test where the agent works
// without spending the agent's tokens on it. The daemon relays what goes in
// and out over plain NATS; this package owns the shell, its terminal, and
// the newest of its output, which a browser opening the terminal reads
// before it follows what comes next.
package terminal

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Scrollback is how much of the newest output a terminal keeps, which a
// browser opening it is given to draw from.
const Scrollback = 256 * 1024

// MaxRate is the most output a terminal publishes in a second. A command
// that writes faster waits on its terminal, as it would on a slow screen,
// so a browser stays responsive and a Ctrl-C shows promptly.
const MaxRate = 1 << 20

// closeWait is how long a closed terminal's shell has to exit after its
// hangup before its process group is killed.
const closeWait = 3 * time.Second

// inputQueue is how many inputs wait for a shell that isn't reading before
// more are dropped.
const inputQueue = 256

// ErrInputFull is an input dropped because the shell isn't reading.
var ErrInputFull = errors.New("the terminal isn't reading its input")

// ErrClosed is an input or resize for a terminal whose shell is gone.
var ErrClosed = errors.New("the terminal is closed")

// Watch hears the shell's process group start and end, so the daemon's
// watchdog ends it if the daemon dies.
type Watch interface {
	Started(pgid int)
	Ended(pgid int)
}

// Config describes the shell to start.
type Config struct {
	// Shell is the program to run, as a login shell. Empty means
	// DefaultShell.
	Shell string
	// Dir is the directory it starts in.
	Dir string
	// Cols and Rows are its first size. Zero means 80 by 24.
	Cols, Rows int
	// Output, if set, gets each piece the shell writes, in order, with its
	// offset among everything it has written.
	Output func(v1.TerminalOutput)
	// Watch, if set, hears the shell's process group start and end.
	Watch Watch
}

// Terminal is one running shell.
type Terminal struct {
	cmd    *exec.Cmd
	pty    *os.File
	shell  string
	output func(v1.TerminalOutput)
	watch  Watch
	in     chan []byte

	mu sync.Mutex
	// buf is the newest output, which begins at off among everything the
	// shell has written.
	buf []byte
	off int64

	// read is closed once the reader has stopped, and done once the shell
	// has exited and everything it wrote has been published.
	read     chan struct{}
	done     chan struct{}
	exitCode int
	closing  sync.Once
}

// Start runs the shell in a terminal of its own.
func Start(cfg Config) (*Terminal, error) {
	shell := cfg.Shell
	if shell == "" {
		shell = DefaultShell()
	}
	cols, rows := clamp(cfg.Cols, 80), clamp(cfg.Rows, 24)
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cfg.Dir
	cmd.Env = Env(os.Environ())
	// The shell leads a session of its own, with the terminal as its
	// controlling terminal, so it leads its own process group too.
	f, err := spawn(cmd, cols, rows)
	if err != nil {
		return nil, err
	}
	t := &Terminal{cmd: cmd, pty: f, shell: shell, output: cfg.Output, watch: cfg.Watch, in: make(chan []byte, inputQueue), read: make(chan struct{}), done: make(chan struct{})}
	if t.watch != nil {
		t.watch.Started(cmd.Process.Pid)
	}
	go t.readLoop()
	go t.writeLoop()
	go t.wait()
	return t, nil
}

// Shell is the program the terminal runs.
func (t *Terminal) Shell() string { return t.shell }

// Done is closed once the shell has exited.
func (t *Terminal) Done() <-chan struct{} { return t.done }

// ExitCode is the shell's exit status once Done is closed: -1 if a signal
// ended it.
func (t *Terminal) ExitCode() int {
	<-t.done
	return t.exitCode
}

// Write queues input for the shell. It never blocks: a shell that isn't
// reading gets ErrInputFull once its queue is full.
func (t *Terminal) Write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	select {
	case <-t.done:
		return ErrClosed
	default:
	}
	select {
	case t.in <- append([]byte(nil), p...):
		return nil
	default:
		return ErrInputFull
	}
}

// Resize tells the shell its terminal is now cols by rows.
func (t *Terminal) Resize(cols, rows int) error {
	select {
	case <-t.done:
		return ErrClosed
	default:
	}
	return setSize(t.pty, clamp(cols, 80), clamp(rows, 24))
}

// Screen is the newest output, as much as the terminal keeps, and where it
// begins.
func (t *Terminal) Screen() v1.TerminalOutput {
	t.mu.Lock()
	defer t.mu.Unlock()
	data := t.buf
	off := t.off
	if len(data) > Scrollback {
		off += int64(len(data) - Scrollback)
		data = data[len(data)-Scrollback:]
	}
	// Never nil, which JSON would carry as null rather than empty.
	out := make([]byte, len(data))
	copy(out, data)
	return v1.TerminalOutput{Off: off, Data: out}
}

// Close hangs the terminal up, as closing a terminal window does: the
// shell and what runs in the foreground get SIGHUP. A shell that hasn't
// exited after a few seconds has its process group killed. Close returns
// once the shell has exited.
func (t *Terminal) Close() {
	t.closing.Do(func() {
		// Closing the terminal hangs it up where the platform's terminal
		// can be closed under a pending read, as Linux's can; the signal
		// does the same everywhere else.
		hangUpGroup(t.cmd.Process.Pid)
		_ = t.pty.Close()
		select {
		case <-t.done:
		case <-time.After(closeWait):
			killGroup(t.cmd.Process.Pid)
		}
	})
	<-t.done
}

func (t *Terminal) readLoop() {
	defer close(t.read)
	buf := make([]byte, 32*1024)
	window, sent := time.Now(), 0
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			piece := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			off := t.off + int64(len(t.buf))
			t.buf = append(t.buf, piece...)
			// Trimmed only once it is twice what is kept, so a busy
			// terminal copies its scrollback rarely.
			if len(t.buf) > 2*Scrollback {
				drop := len(t.buf) - Scrollback
				t.buf = append([]byte(nil), t.buf[drop:]...)
				t.off += int64(drop)
			}
			t.mu.Unlock()
			if t.output != nil {
				t.output(v1.TerminalOutput{Off: off, Data: piece})
			}
			if sent += n; sent >= MaxRate {
				if wait := time.Second - time.Since(window); wait > 0 {
					time.Sleep(wait)
				}
			}
			if time.Since(window) >= time.Second {
				window, sent = time.Now(), 0
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) writeLoop() {
	for {
		select {
		case p := <-t.in:
			if _, err := t.pty.Write(p); err != nil {
				return
			}
		case <-t.done:
			return
		}
	}
}

// wait waits for the shell to exit, then for its last output, and then
// ends what is left of its process group.
func (t *Terminal) wait() {
	err := t.cmd.Wait()
	code := 0
	if t.cmd.ProcessState != nil {
		code = t.cmd.ProcessState.ExitCode()
	} else if err != nil {
		code = -1
	}
	// The reader stops once nothing holds the terminal open any more. A
	// job left running in the background may, so it gets a moment, and
	// then the terminal is hung up under it.
	select {
	case <-t.read:
	case <-time.After(200 * time.Millisecond):
	}
	_ = t.pty.Close()
	// Where closing can't interrupt a pending read, the reader stops when
	// the last job holding the terminal does, which needn't hold this up.
	select {
	case <-t.read:
	case <-time.After(time.Second):
	}
	hangUpGroup(t.cmd.Process.Pid)
	if t.watch != nil {
		t.watch.Ended(t.cmd.Process.Pid)
	}
	t.exitCode = code
	close(t.done)
}

// Env is the environment a terminal's shell gets: the daemon's own, with a
// terminal type the browser's terminal emulates.
func Env(base []string) []string {
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TERM" || k == "COLORTERM" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// DefaultShell is the shell a terminal runs: $SHELL, else the user's login
// shell, else the platform's usual one.
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" && executable(s) {
		return s
	}
	if s := passwdShell("/etc/passwd", os.Getuid()); s != "" && executable(s) {
		return s
	}
	candidates := []string{"/bin/bash", "/bin/sh"}
	if runtime.GOOS == "darwin" {
		candidates = append([]string{"/bin/zsh"}, candidates...)
	}
	for _, s := range candidates {
		if executable(s) {
			return s
		}
	}
	return "/bin/sh"
}

// passwdShell is uid's login shell in a passwd file, or "".
func passwdShell(path string, uid int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	want := strconv.Itoa(uid)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) == 7 && fields[2] == want {
			return fields[6]
		}
	}
	// A file that can't be read to the end names no shell for uid.
	_ = sc.Err()
	return ""
}

func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// clamp keeps a dimension a terminal can have, with def for none.
func clamp(n, def int) int {
	switch {
	case n <= 0:
		return def
	case n > 1000:
		return 1000
	}
	return n
}
