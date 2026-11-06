package agents

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// slowRecorder handles each tool call report slowly, so the agent's
// updates queue up behind it, and notes where among them a permission
// request arrives.
type slowRecorder struct {
	recorder
	seenMu sync.Mutex
	seen   []string
}

func (s *slowRecorder) note(what string) {
	s.seenMu.Lock()
	s.seen = append(s.seen, what)
	s.seenMu.Unlock()
}

func (s *slowRecorder) ToolCall(tc ToolCall) {
	time.Sleep(30 * time.Millisecond)
	s.note("report " + tc.ID + " " + tc.Status)
	s.recorder.ToolCall(tc)
}

func (s *slowRecorder) Permission(ctx context.Context, req PermissionRequest) Decision {
	s.note("ask " + req.ToolCall.ID)
	return s.recorder.Permission(ctx, req)
}

// A request from the agent follows on the wire the reports it sent before
// it, and must reach the observer after them too, however slowly they are
// handled: otherwise the edit it asks about goes on the record before a
// read the agent had already reported.
func TestARequestComesAfterTheReportsBeforeIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cwd := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(cwd, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd, env := fakeagent.TestCommand()
	obs := &slowRecorder{}
	r, err := Start(ctx, Adapter{Name: "fake", Command: cmd, Env: env}, cwd, Settings{}, obs)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Prompt(ctx, "tidy up"); err != nil {
		t.Fatal(err)
	}
	obs.seenMu.Lock()
	seen := slices.Clone(obs.seen)
	obs.seenMu.Unlock()
	ask := slices.Index(seen, "ask t1-edit-0")
	read := slices.Index(seen, "report t1-read-1 completed")
	if ask < 0 || read < 0 || ask < read {
		t.Errorf("the edit was asked about before the second read was reported: %q", seen)
	}
}
