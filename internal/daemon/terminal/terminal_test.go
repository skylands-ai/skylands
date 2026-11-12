package terminal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// output collects what a terminal publishes, checking each piece starts
// where the last ended.
type output struct {
	t    *testing.T
	mu   sync.Mutex
	next int64
	all  bytes.Buffer
}

func (o *output) add(p v1.TerminalOutput) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p.Off != o.next {
		o.t.Errorf("a piece at %d, after %d bytes", p.Off, o.next)
	}
	o.next = p.Off + int64(len(p.Data))
	o.all.Write(p.Data)
}

func (o *output) waitFor(text string) {
	o.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o.mu.Lock()
		got := o.all.String()
		o.mu.Unlock()
		if strings.Contains(got, text) {
			return
		}
		if time.Now().After(deadline) {
			o.t.Fatalf("the terminal never showed %q; it showed %q", text, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type watch struct {
	mu             sync.Mutex
	started, ended []int
}

func (w *watch) Started(pgid int) { w.mu.Lock(); w.started = append(w.started, pgid); w.mu.Unlock() }
func (w *watch) Ended(pgid int)   { w.mu.Lock(); w.ended = append(w.ended, pgid); w.mu.Unlock() }

func start(t *testing.T, dir string) (*Terminal, *output, *watch) {
	t.Helper()
	out := &output{t: t}
	w := &watch{}
	term, err := Start(Config{Shell: "/bin/sh", Dir: dir, Cols: 100, Rows: 30, Output: out.add, Watch: w})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(term.Close)
	return term, out, w
}

func TestTerminalRunsAShellWhereAsked(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	term, out, w := start(t, dir)
	if term.Shell() != "/bin/sh" {
		t.Errorf("shell = %q", term.Shell())
	}
	// It starts in the directory it was given, with the size it was given
	// and a terminal type the browser emulates.
	if err := term.Write([]byte("ls; stty size; echo \"term=$TERM\"\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor("marker.txt")
	out.waitFor("30 100")
	out.waitFor("term=xterm-256color")
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := term.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor("40 120")

	// What it showed can be read back from where it begins.
	screen := term.Screen()
	if screen.Off != 0 || !bytes.Contains(screen.Data, []byte("marker.txt")) {
		t.Errorf("screen = at %d, %q", screen.Off, screen.Data)
	}

	// The shell exiting on its own is the terminal's end, with its status.
	if err := term.Write([]byte("exit 3\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal didn't end when its shell exited")
	}
	if code := term.ExitCode(); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if err := term.Write([]byte("echo late\n")); err != ErrClosed {
		t.Errorf("input after the end: err = %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.started) != 1 || len(w.ended) != 1 || w.started[0] != w.ended[0] {
		t.Errorf("watch heard started %v, ended %v", w.started, w.ended)
	}
}

func TestCloseHangsUpWhatRuns(t *testing.T) {
	term, out, _ := start(t, t.TempDir())
	// A command that would outlive the terminal if nothing hung it up.
	if err := term.Write([]byte("echo ready; sleep 600\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor("ready")
	began := time.Now()
	term.Close()
	if took := time.Since(began); took > closeWait+2*time.Second {
		t.Errorf("Close took %s", took)
	}
	select {
	case <-term.Done():
	default:
		t.Fatal("Close returned before the shell ended")
	}
}

func TestScreenKeepsTheNewest(t *testing.T) {
	term, out, _ := start(t, t.TempDir())
	// More than the scrollback, then a line to find at the end, spelled so
	// the terminal's echo of the command doesn't match it.
	if err := term.Write([]byte("i=0; while [ $i -lt 6000 ]; do echo 0123456789012345678901234567890123456789012345678901234567890123456789; i=$((i+1)); done; echo done-\"\"writing\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor("done-writing\r\n")
	screen := term.Screen()
	if len(screen.Data) != Scrollback {
		t.Errorf("screen holds %d bytes, want %d", len(screen.Data), Scrollback)
	}
	out.mu.Lock()
	total := out.next
	out.mu.Unlock()
	if end := screen.Off + int64(len(screen.Data)); end != total {
		t.Errorf("screen ends at %d, output at %d", end, total)
	}
	if !bytes.Contains(screen.Data, []byte("done-writing")) {
		t.Error("the screen lost the newest output")
	}
}

func TestScreenOfANewTerminalIsEmptyNotNull(t *testing.T) {
	// A shell that hasn't written yet, as when a browser reads a terminal
	// the moment it opens.
	data, err := json.Marshal((&Terminal{}).Screen())
	if err != nil || string(data) != `{"off":0,"data":""}` {
		t.Errorf("an empty screen = %s, %v", data, err)
	}
}

func TestEnvSetsTheTerminalType(t *testing.T) {
	env := Env([]string{"TERM=dumb", "HOME=/h", "COLORTERM=no", "PATH=/bin"})
	want := []string{"HOME=/h", "PATH=/bin", "TERM=xterm-256color", "COLORTERM=truecolor"}
	if strings.Join(env, " ") != strings.Join(want, " ") {
		t.Errorf("Env = %v, want %v", env, want)
	}
}

func TestPasswdShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(path, []byte("root:x:0:0:root:/root:/bin/bash\nkim:x:1000:1000:Kim:/home/kim:/usr/bin/fish\nbad line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := passwdShell(path, 1000); got != "/usr/bin/fish" {
		t.Errorf("uid 1000's shell = %q", got)
	}
	if got := passwdShell(path, 7); got != "" {
		t.Errorf("an unknown uid's shell = %q", got)
	}
}

// TestResizeWhileClosing resizes from one goroutine while another closes
// the terminal, as a browser resizing while a person closes it does.
func TestResizeWhileClosing(t *testing.T) {
	term, out, _ := start(t, t.TempDir())
	if err := term.Write([]byte("echo up-\"\"now\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor("up-now")
	stop := make(chan struct{})
	resized := make(chan struct{})
	go func() {
		defer close(resized)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = term.Resize(80+i%20, 24)
		}
	}()
	term.Close()
	close(stop)
	<-resized
	if err := term.Resize(90, 30); err != ErrClosed {
		t.Errorf("resizing a closed terminal: err = %v", err)
	}
}
