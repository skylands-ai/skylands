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
	go stampUpdates(stdout, pw)
	return (&acp.IOTransport{Reader: pr, Writer: &processStdin{stdin: stdin, cmd: t.cmd}}).Connect(ctx)
}

// stampUpdates copies newline-delimited JSON-RPC messages from the agent,
// adding a sequence number to each session/update.
func stampUpdates(from io.Reader, to *io.PipeWriter) {
	reader := bufio.NewReaderSize(from, 1<<20)
	var seq uint64
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = stamp(line, &seq)
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

// processStdin is the agent's stdin. Closing it ends the conversation and
// then the process: politely first, then with SIGTERM, then SIGKILL.
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
		wait := func() (error, bool) {
			select {
			case err := <-res:
				return err, true
			case <-time.After(5 * time.Second):
				return nil, false
			}
		}
		// An agent that exits on its own after stdin closes reports its exit
		// status. One we had to signal is expected to exit abnormally.
		if exitErr, ok := wait(); ok {
			p.err = errors.Join(p.err, exitErr)
			return
		}
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err == nil {
			if _, ok := wait(); ok {
				return
			}
		}
		if err := p.cmd.Process.Kill(); err != nil {
			p.err = errors.Join(p.err, err)
			return
		}
		if _, ok := wait(); !ok {
			p.err = errors.Join(p.err, errors.New("unresponsive agent process"))
		}
	})
	return p.err
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
