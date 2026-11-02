package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
)

// A tool call's record lives beside the transcript, one file per call under
// <session>.tools/, and holds what the transcript's one line cannot: the
// exact input, what came back, and when. Like the transcript it stays on
// the host until a conversation's reader opens the call.

// MaxToolText is the most of any one text, such as a command's output or a
// diff's new text, a tool call's record keeps. A longer one keeps its
// start and says it was cut.
const MaxToolText = 128 * 1024

// MaxToolInput is the most of a tool call's input the record keeps. A
// longer one, such as a whole file being written, is replaced by a note.
const MaxToolInput = 256 * 1024

// ErrUnknownToolCall is returned when a session has no record of a call.
var ErrUnknownToolCall = errors.New("no such tool call in this session")

func (m *Manager) toolDir(sessionID string) string {
	return filepath.Join(m.cfg.TranscriptsDir, sessionID+".tools")
}

// toolPath is the record's file for one call. An id that is not a safe
// file name is cleaned and tagged with a hash so two ids cannot collide.
func (m *Manager) toolPath(sessionID, toolCallID string) string {
	return filepath.Join(m.toolDir(sessionID), toolFileName(toolCallID)+".json")
}

func toolFileName(id string) string {
	var b strings.Builder
	clean := true
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			clean = false
		}
	}
	name := b.String()
	if clean && name != "" && name != "." && name != ".." {
		return name
	}
	sum := sha256.Sum256([]byte(id))
	if len(name) > 40 {
		name = name[:40]
	}
	return name + "-" + hex.EncodeToString(sum[:4])
}

// recordTool folds one report of a tool call into its record: identity and
// locations as last reported, the input once known, the content and raw
// output when the report carries them. The first report starts the record
// and a finished status ends it.
func (m *Manager) recordTool(sessionID string, tc agents.ToolCall, now time.Time) {
	if tc.ID == "" {
		return
	}
	m.toolMu.Lock()
	defer m.toolMu.Unlock()
	d, err := m.readTool(sessionID, tc.ID)
	if err != nil {
		d = v1.ToolCallDetail{ToolCallID: tc.ID, StartedAt: now}
	}
	if tc.Title != "" {
		d.Title = tc.Title
	}
	if tc.Kind != "" {
		d.Kind = tc.Kind
	}
	if tc.Status != "" {
		d.Status = tc.Status
	}
	if len(tc.Locations) > 0 {
		d.Locations = tc.Locations
	}
	if tc.Input != nil {
		if len(tc.Input) > MaxToolInput {
			note, _ := json.Marshal(fmt.Sprintf("(an input of %d bytes, left out for length)", len(tc.Input)))
			d.Input, d.Truncated = note, true
		} else {
			d.Input = tc.Input
		}
	}
	if tc.Content != nil {
		d.Content = make([]v1.ToolCallContent, 0, len(tc.Content))
		for _, c := range tc.Content {
			if cut(&c.Text) || cut(&c.NewText) {
				d.Truncated = true
			}
			if c.OldText != nil {
				old := *c.OldText
				if cut(&old) {
					d.Truncated = true
				}
				c.OldText = &old
			}
			d.Content = append(d.Content, c)
		}
	}
	if tc.Output != "" {
		out := tc.Output
		if cut(&out) {
			d.Truncated = true
		}
		d.Output = out
	}
	d.UpdatedAt = now
	if tc.Finished() && d.EndedAt == nil {
		ended := now
		d.EndedAt = &ended
	}
	m.writeTool(sessionID, d)
}

// recordToolInput notes the input a permission request carried for a call,
// which for some agents is the first time the whole input is seen.
func (m *Manager) recordToolInput(sessionID string, tc agents.ToolCall, input json.RawMessage, now time.Time) {
	if len(input) == 0 {
		return
	}
	m.recordTool(sessionID, agents.ToolCall{ID: tc.ID, Title: tc.Title, Kind: tc.Kind, Locations: tc.Locations, Input: input}, now)
}

// cut shortens a text to MaxToolText on a rune boundary, noting the cut at
// its end, and reports whether it did.
func cut(s *string) bool {
	if len(*s) <= MaxToolText {
		return false
	}
	n := MaxToolText
	for n > 0 && !utf8.RuneStart((*s)[n]) {
		n--
	}
	*s = (*s)[:n] + "\n[cut at the host's size limit]"
	return true
}

func (m *Manager) readTool(sessionID, toolCallID string) (v1.ToolCallDetail, error) {
	data, err := os.ReadFile(m.toolPath(sessionID, toolCallID))
	if err != nil {
		return v1.ToolCallDetail{}, err
	}
	var d v1.ToolCallDetail
	if err := json.Unmarshal(data, &d); err != nil {
		return v1.ToolCallDetail{}, err
	}
	return d, nil
}

// writeTool replaces the record whole, through a temporary file, so a
// reader never sees half of one.
func (m *Manager) writeTool(sessionID string, d v1.ToolCallDetail) {
	path := m.toolPath(sessionID, d.ToolCallID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(d)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// ToolCall returns one tool call's record. A call the session's events
// know but no record was kept for, such as one from before records were
// kept, comes back with what the events say and nothing more.
func (m *Manager) ToolCall(sessionID, toolCallID string) (v1.ToolCallDetail, error) {
	s, ok := m.Get(sessionID)
	if !ok {
		return v1.ToolCallDetail{}, fmt.Errorf("%w: %s", ErrUnknownSession, sessionID)
	}
	if toolCallID == "" {
		return v1.ToolCallDetail{}, errors.New("a toolCallId is required")
	}
	m.toolMu.Lock()
	d, err := m.readTool(sessionID, toolCallID)
	m.toolMu.Unlock()
	if err == nil {
		return d, nil
	}
	for _, tc := range s.ToolCalls {
		if tc.ID == toolCallID {
			return v1.ToolCallDetail{ToolCallID: tc.ID, Title: tc.Title, Kind: tc.Kind, Status: tc.Status, Locations: tc.Locations}, nil
		}
	}
	return v1.ToolCallDetail{}, fmt.Errorf("%w: %s", ErrUnknownToolCall, toolCallID)
}
