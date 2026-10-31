package projections

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestEmptySessionEncodesLists(t *testing.T) {
	p := NewSessions(nil)
	ev, _ := v1.NewEvent(v1.EventSessionStarted, "c", v1.SessionStarted{SessionID: "s1", RepoID: "r1", Agent: "fake"})
	p.Apply("ws", ev)
	data, err := json.Marshal(p.List()[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"recent":[]`, `"files":{}`, `"plan":[]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session JSON lacks %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("session JSON has a null: %s", data)
	}
}

func TestSessionsFollowSessionEvents(t *testing.T) {
	var changes int
	p := NewSessions(func(Session) { changes++ })
	apply := func(typ string, data any) {
		ev, _ := v1.NewEvent(typ, "c", data)
		p.Apply("ws", ev)
	}
	apply(v1.EventSessionStarted, v1.SessionStarted{SessionID: "s1", RepoID: "r1", Agent: "fake", Branch: "skylands/s1"})
	apply(v1.EventPromptSent, v1.PromptSent{SessionID: "s1", Text: "fix the tests"})
	apply(v1.EventAgentSpawned, v1.AgentSpawned{SessionID: "s1", AgentID: "s1", Name: "fakeagent"})
	apply(v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: "s1", ToolCallID: "t1", Title: "Read a.go", Kind: "read", Locations: []string{"a.go"}})
	apply(v1.EventToolCallCompleted, v1.ToolCallCompleted{SessionID: "s1", ToolCallID: "t1", Status: "completed"})
	apply(v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: "s1", ToolCallID: "t2", Title: "Edit a.go", Kind: "edit", Locations: []string{"a.go"}})
	apply(v1.EventPlanUpdated, v1.PlanUpdated{SessionID: "s1", Entries: []v1.PlanEntry{{Content: "x", Status: "pending"}}})
	apply(v1.EventUsageReported, v1.UsageReported{SessionID: "s1", Used: 10, Size: 100})

	list := p.List()
	if len(list) != 1 {
		t.Fatalf("List = %+v", list)
	}
	s := list[0]
	if s.Host != "ws" || s.Prompt != "fix the tests" || s.AgentName != "fakeagent" || s.Status != "running" {
		t.Errorf("session = %+v", s)
	}
	if s.CurrentFile != "a.go" || s.CurrentAction != "Edit a.go" || s.Files["a.go"] != "edit" || len(s.Recent) != 2 || s.Recent[0].Status != "completed" {
		t.Errorf("activity = file %q action %q files %v recent %+v", s.CurrentFile, s.CurrentAction, s.Files, s.Recent)
	}
	if len(s.Plan) != 1 || s.Used != 10 || s.Size != 100 {
		t.Errorf("plan/usage = %+v %d/%d", s.Plan, s.Used, s.Size)
	}

	apply(v1.EventSessionTitled, v1.SessionTitled{SessionID: "s1", Title: "Fix the tests"})
	if s := p.List()[0]; s.Title != "Fix the tests" {
		t.Errorf("titled session = %+v", s)
	}
	apply(v1.EventTurnEnded, v1.TurnEnded{SessionID: "s1", StopReason: "end_turn"})
	if s := p.List()[0]; s.Status != "idle" || s.Turns != 1 || s.UpdatedAt.IsZero() {
		t.Errorf("idle session = %+v", s)
	}
	apply(v1.EventPromptSent, v1.PromptSent{SessionID: "s1", Text: "more"})
	if s := p.List()[0]; s.Status != "running" || s.Turns != 2 || s.Prompt != "fix the tests" {
		t.Errorf("second turn = %+v", s)
	}
	apply(v1.EventSessionEnded, v1.SessionEnded{SessionID: "s1", StopReason: "stopped"})
	if s := p.List()[0]; s.Status != "ended" || s.StopReason != "stopped" || s.EndedAt.IsZero() {
		t.Errorf("ended session = %+v", s)
	}
	// An event for an unknown session changes nothing.
	before := changes
	apply(v1.EventSessionEnded, v1.SessionEnded{SessionID: "nope", StopReason: "end_turn"})
	if changes != before {
		t.Error("unknown session produced a change")
	}

	// Only the last RecentToolCalls stay.
	apply(v1.EventSessionStarted, v1.SessionStarted{SessionID: "s2", RepoID: "r1", Agent: "fake"})
	for i := 0; i < RecentToolCalls+5; i++ {
		apply(v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: "s2", ToolCallID: string(rune('a' + i)), Title: "t"})
	}
	for _, s := range p.List() {
		if s.ID == "s2" && len(s.Recent) != RecentToolCalls {
			t.Errorf("recent = %d, want %d", len(s.Recent), RecentToolCalls)
		}
	}
}
