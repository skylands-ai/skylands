package agents

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spachava753/acp-sdk/acp"
)

// seqKey is the _meta key this transport stamps on each session/update so
// the handler can restore the order the agent sent them in. The SDK hands
// notifications to the handler concurrently, so without it chunks of one
// message, or a tool call's start and end, can arrive swapped.
const seqKey = "skylands_seq"

// processTransport runs an agent as a child process and speaks ACP over its
// stdio, numbering session updates on the way in.
type processTransport struct {
	cmd *exec.Cmd
	// read counts the session updates read from the agent so far.
	read atomic.Uint64
}

// Connect starts the process and returns an ACP connection to it.
func (t *processTransport) Connect(ctx context.Context) (acp.Connection, error) {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := t.cmd.Start(); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go t.stampUpdates(stdout, pw)
	return (&acp.IOTransport{Reader: pr, Writer: &processStdin{stdin: stdin, cmd: t.cmd}}).Connect(ctx)
}

// stampUpdates copies newline-delimited JSON-RPC messages from the agent,
// adding a sequence number to each session/update.
func (t *processTransport) stampUpdates(from io.Reader, to *io.PipeWriter) {
	reader := bufio.NewReaderSize(from, 1<<20)
	var seq uint64
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = stamp(line, &seq)
			t.read.Store(seq)
			if _, werr := to.Write(line); werr != nil {
				to.CloseWithError(werr)
				return
			}
		}
		if err != nil {
			to.CloseWithError(err)
			return
		}
	}
}

// stamp adds the next sequence number to a session/update line. Any other
// line, and anything it can't parse, passes through unchanged.
func stamp(line []byte, seq *uint64) []byte {
	if !bytes.Contains(line, []byte(`"session/update"`)) {
		return line
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(line, &msg) != nil {
		return line
	}
	var method string
	if json.Unmarshal(msg["method"], &method) != nil || method != "session/update" {
		return line
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(msg["params"], &params) != nil {
		return line
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok {
		_ = json.Unmarshal(raw, &meta)
	}
	*seq++
	meta[seqKey] = json.RawMessage(strconv.FormatUint(*seq, 10))
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return line
	}
	params["_meta"] = metaRaw
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return line
	}
	msg["params"] = paramsRaw
	out, err := json.Marshal(msg)
	if err != nil {
		return line
	}
	return append(out, '\n')
}

// stopWait is how long each step of stopping an agent waits for it to
// exit before the next, harder one. Tests shorten it.
var stopWait = 5 * time.Second

// processStdin is the agent's stdin. Closing it ends the conversation and
// then the agent's process group: the agent is asked to exit by the close
// itself, then with SIGTERM and SIGKILL to the whole group if it does not,
// and whatever it leaves in its group, such as a command its shell tool
// started, is ended once it has exited.
type processStdin struct {
	stdin io.WriteCloser
	cmd   *exec.Cmd
	once  sync.Once
	err   error
}

func (p *processStdin) Write(b []byte) (int, error) { return p.stdin.Write(b) }

func (p *processStdin) Close() error {
	p.once.Do(func() {
		if err := p.stdin.Close(); err != nil {
			p.err = fmt.Errorf("closing stdin: %w", err)
		}
		res := make(chan error, 1)
		go func() { res <- p.cmd.Wait() }()
		var waitErr error
		waited := false
		// exited waits up to d for the agent's own process to exit. Wait
		// also waits for the process's output pipes, which a child left in
		// the group holds open, so the process counts as exited as soon as
		// Wait has reaped it.
		exited := func(d time.Duration) bool {
			deadline := time.Now().Add(d)
			for {
				select {
				case err := <-res:
					waitErr, waited = err, true
					return true
				case <-time.After(20 * time.Millisecond):
				}
				if leaderGone(p.cmd) {
					return true
				}
				if time.Now().After(deadline) {
					return false
				}
			}
		}
		signalled := false
		if !exited(stopWait) {
			signalled = true
			_ = signalGroup(p.cmd, syscall.SIGTERM)
			if !exited(stopWait) {
				if err := signalGroup(p.cmd, syscall.SIGKILL); err != nil {
					p.err = errors.Join(p.err, err)
				}
				if !exited(stopWait) {
					p.err = errors.Join(p.err, errors.New("unresponsive agent process"))
					return
				}
			}
		}
		endGroup(p.cmd)
		if !waited {
			// WaitDelay bounds this, in case something that left the group
			// still holds the agent's output open.
			waitErr = <-res
		}
		// An agent that exits on its own after stdin closes reports its exit
		// status. One we had to signal is expected to exit abnormally.
		if !signalled && !errors.Is(waitErr, exec.ErrWaitDelay) {
			p.err = errors.Join(p.err, waitErr)
		}
	})
	return p.err
}

// endGroup ends whatever is left in the group cmd leads once its own
// process has exited: SIGTERM, then SIGKILL for anything still there after
// stopWait.
func endGroup(cmd *exec.Cmd) {
	if !groupLeft(cmd) {
		return
	}
	_ = signalGroup(cmd, syscall.SIGTERM)
	for deadline := time.Now().Add(stopWait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !groupLeft(cmd) {
			return
		}
	}
	_ = signalGroup(cmd, syscall.SIGKILL)
}

// sequencer delivers session updates to fn in the order the agent sent
// them, holding back any that arrive early.
type sequencer struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]*acp.SessionNotification
	fn      func(*acp.SessionNotification)
}

func newSequencer(fn func(*acp.SessionNotification)) *sequencer {
	return &sequencer{next: 1, pending: map[uint64]*acp.SessionNotification{}, fn: fn}
}

// Delivered is how many stamped updates have been handled so far.
func (s *sequencer) Delivered() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next - 1
}

// Deliver handles n now if it is next, or when its turn comes. An update
// without a stamp is handled at once.
func (s *sequencer) Deliver(n *acp.SessionNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := seqOf(n)
	if !ok {
		s.fn(n)
		return
	}
	if seq != s.next {
		if seq > s.next {
			s.pending[seq] = n
		}
		return
	}
	s.fn(n)
	s.next++
	for {
		m, ok := s.pending[s.next]
		if !ok {
			return
		}
		delete(s.pending, s.next)
		s.fn(m)
		s.next++
	}
}

func seqOf(n *acp.SessionNotification) (uint64, bool) {
	switch v := n.Meta[seqKey].(type) {
	case float64:
		return uint64(v), true
	case json.Number:
		u, err := strconv.ParseUint(v.String(), 10, 64)
		return u, err == nil
	}
	return 0, false
}
