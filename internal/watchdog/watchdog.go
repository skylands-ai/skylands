// Package watchdog ends a daemon's agents if the daemon dies without
// ending them itself, as in a crash, SIGKILL, or the OOM killer.
//
// Each agent runs in a process group of its own, and the daemon ends the
// whole group when the agent stops. A daemon that dies abruptly cannot, and
// while its agents exit when their stdin closes, whatever they started that
// does not read stdin, such as a dev server or a test run, would keep
// running. So a daemon starts one watchdog: a small process, the same
// skylands binary, in a process group of its own. The daemon tells it each
// agent group as it starts and ends, over the watchdog's stdin. Only the
// daemon holds the writing end of that pipe, so however the daemon dies,
// the kernel closes it, and the watchdog reads end-of-file and ends every
// group still registered. A daemon that stops cleanly has ended its groups
// already, so its watchdog finds nothing to do and exits.
//
// The watchdog acts within moments on groups it was told about while they
// existed, which is what makes signalling them by process group ID safe:
// an ID cannot be reused while its group still has members.
package watchdog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EnvFlag, when set to 1, makes MainHook run the watchdog instead of the
// program.
const EnvFlag = "SKYLANDS_WATCHDOG"

// Grace is how long the watchdog gives the groups it ends to exit after
// SIGTERM before it sends SIGKILL.
var Grace = 5 * time.Second

// ready is the line the watchdog writes once it is listening.
const ready = "ready"

// MainHook runs the watchdog and exits when this process was started as
// one. The skylands binary calls it first thing in main, and test binaries
// that start daemons call it from TestMain.
func MainHook() {
	if os.Getenv(EnvFlag) != "1" {
		return
	}
	ignoreSignals()
	fmt.Println(ready)
	serve(os.Stdin)
	os.Exit(0)
}

// serve reads "+pgid" and "-pgid" lines until in ends, then ends the
// groups still registered.
func serve(in io.Reader) {
	groups := map[int]bool{}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if len(line) < 2 {
			continue
		}
		pgid, err := strconv.Atoi(line[1:])
		if err != nil || pgid <= 1 {
			continue
		}
		switch line[0] {
		case '+':
			groups[pgid] = true
		case '-':
			delete(groups, pgid)
		}
	}
	endGroups(groups)
}

// Watchdog is a running watchdog, as the daemon that started it sees it.
type Watchdog struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	in  io.WriteCloser
	// failed is set once a write has failed, so the failure is reported
	// once.
	failed bool
}

// Start runs exe as a watchdog for this process. exe must call MainHook
// before anything else.
func Start(exe string) (*Watchdog, error) {
	if !supported {
		return nil, errors.New("a watchdog needs Unix process groups")
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), EnvFlag+"=1")
	ownGroup(cmd)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// A binary that is not a watchdog, such as a test binary without the
	// hook, never says it is ready.
	said := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		said <- strings.TrimSpace(line)
		_, _ = io.Copy(io.Discard, out)
	}()
	select {
	case line := <-said:
		if line == ready {
			return &Watchdog{cmd: cmd, in: in}, nil
		}
		err = fmt.Errorf("%s did not start as a watchdog: %q", exe, line)
	case <-time.After(5 * time.Second):
		err = fmt.Errorf("%s did not start as a watchdog in time", exe)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, err
}

// Started registers an agent's process group.
func (w *Watchdog) Started(pgid int) { w.send('+', pgid) }

// Ended unregisters a group that has ended.
func (w *Watchdog) Ended(pgid int) { w.send('-', pgid) }

func (w *Watchdog) send(op byte, pgid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return
	}
	if _, err := fmt.Fprintf(w.in, "%c%d\n", op, pgid); err != nil {
		w.failed = true
		fmt.Fprintln(os.Stderr, "watchdog: lost; if the daemon dies, what its agents started may outlive it:", err)
	}
}

// Close tells the watchdog the daemon is done and waits for it to exit.
// The daemon has ended its agents by then, so the watchdog has nothing
// left to end.
func (w *Watchdog) Close() error {
	w.mu.Lock()
	err := w.in.Close()
	w.failed = true
	w.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case werr := <-done:
		return errors.Join(err, werr)
	case <-time.After(2*Grace + time.Second):
		_ = w.cmd.Process.Kill()
		return errors.Join(err, errors.New("watchdog did not exit"))
	}
}
