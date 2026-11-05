package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
	"github.com/skylands-ai/skylands/internal/daemon/blobs"
	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// TestRequestsKeepTheirInputAndCallsTheirWholeOutput keeps a request's
// exact input whatever its size, after it is decided, and a finished
// call's whole output, however much of it the call's record cut.
func TestRequestsKeepTheirInputAndCallsTheirWholeOutput(t *testing.T) {
	f := setup(t)
	f.cfg.Blobs = blobs.New(filepath.Join(t.TempDir(), "blobs"))
	m, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(f.ctx, v1.StartSession{ID: "c1", SessionID: "s1", RepoID: f.repo.ID, Agent: "fake", Prompt: "make a " + fakeagent.BigEdit + " with " + fakeagent.BigOutput}); err != nil {
		t.Fatal(err)
	}
	asked := waitPending(t, m, "s1", 1)
	p := asked.Pending[0]
	if p.InputSize <= v1.InputPreviewMax {
		t.Fatalf("the edit's input is %d bytes, no longer than an event carries", p.InputSize)
	}
	if _, err := m.Approve(f.ctx, v1.ApproveToolCall{ID: "c2", SessionID: "s1", RequestID: p.RequestID}, "local"); err != nil {
		t.Fatal(err)
	}
	idle := waitStatus(t, m, "s1", StatusIdle)

	// The input is kept after the request was decided, byte for byte what
	// the request's hash names.
	input, err := m.Blob(p.InputHash)
	if err != nil {
		t.Fatalf("the approved input: %v", err)
	}
	if sum := sha256.Sum256(input); hex.EncodeToString(sum[:]) != p.InputHash || len(input) != p.InputSize {
		t.Errorf("the kept input is %d bytes that don't match the request", len(input))
	}

	// Every finished call names its output, and the edit's is whole.
	var edit ToolCall
	for _, tc := range idle.ToolCalls {
		if tc.OutputHash == "" || tc.OutputSize == 0 {
			t.Errorf("finished call %s names no output: %+v", tc.ID, tc)
		}
		if tc.ID == p.ToolCallID {
			edit = tc
		}
	}
	if edit.Status != "completed" {
		t.Fatalf("the edit = %+v", edit)
	}
	data, err := m.Blob(edit.OutputHash)
	if err != nil {
		t.Fatalf("the edit's output: %v", err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != edit.OutputHash || len(data) != edit.OutputSize {
		t.Errorf("the kept output is %d bytes that don't match the call", len(data))
	}
	if c, err := canonical.Transform(data); err != nil || string(c) != string(data) {
		t.Errorf("the kept output isn't canonical JSON: %v", err)
	}
	var out v1.ToolCallOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "diff" || len(out.Content[0].NewText) <= MaxToolText || strings.Contains(out.Content[0].NewText, "[cut at") {
		t.Errorf("the edit's output isn't the whole diff: %d pieces", len(out.Content))
	}
	record, err := m.ToolCall("s1", edit.ID)
	if err != nil || !record.Truncated {
		t.Errorf("the edit's record = truncated %v, %v; want it cut", record.Truncated, err)
	}

	// The hashes survive a reload, from the events alone.
	m2, err := Load(f.ctx, f.store, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _ := m2.Get("s1")
	for _, tc := range reloaded.ToolCalls {
		if tc.ID == edit.ID && (tc.OutputHash != edit.OutputHash || tc.OutputSize != edit.OutputSize) {
			t.Errorf("after a reload, the edit = %+v", tc)
		}
	}
	if _, err := m.Stop(f.ctx, v1.StopAgent{ID: "c3", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
}
