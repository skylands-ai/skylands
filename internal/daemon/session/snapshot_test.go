package session

import (
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// TestCopiesKeepWhatTheyHad checks that a session from Get or List keeps
// what it held when later events change the manager's own session: apply
// changes a tool call in place, and filters pending requests and questions
// in place, so a copy that shared their backing arrays would change under a
// reader who holds no lock.
func TestCopiesKeepWhatTheyHad(t *testing.T) {
	m := &Manager{sessions: map[string]*Session{}, handled: map[string]string{}}
	apply := func(eventType string, data any) {
		t.Helper()
		ev, err := v1.NewEvent(eventType, "", data)
		if err != nil {
			t.Fatal(err)
		}
		m.apply(ev)
	}
	apply(v1.EventSessionStarted, v1.SessionStarted{SessionID: "s1"})
	apply(v1.EventToolCallStarted, v1.ToolCallStarted{SessionID: "s1", ToolCallID: "t1", Title: "Edit a.go", Kind: "edit"})
	apply(v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "r1", ToolCallID: "t1"})
	apply(v1.EventPermissionRequested, v1.PermissionRequested{SessionID: "s1", RequestID: "r2", ToolCallID: "t1"})
	apply(v1.EventQuestionAsked, v1.QuestionAsked{SessionID: "s1", QuestionID: "q1"})
	apply(v1.EventQuestionAsked, v1.QuestionAsked{SessionID: "s1", QuestionID: "q2"})

	got, _ := m.Get("s1")
	listed := m.List()[0]

	apply(v1.EventToolCallCompleted, v1.ToolCallCompleted{SessionID: "s1", ToolCallID: "t1", Status: "completed"})
	apply(v1.EventPermissionGranted, v1.PermissionGranted{SessionID: "s1", RequestID: "r1", By: "local"})
	apply(v1.EventQuestionAnswered, v1.QuestionAnswered{SessionID: "s1", QuestionID: "q1", By: "local"})

	for name, s := range map[string]Session{"Get": got, "List": listed} {
		if s.ToolCalls[0].Status != "in_progress" {
			t.Errorf("%s's copy has its tool call %q, want in_progress", name, s.ToolCalls[0].Status)
		}
		if len(s.Pending) != 2 || s.Pending[0].RequestID != "r1" || s.Pending[1].RequestID != "r2" {
			t.Errorf("%s's copy has pending %+v, want r1 and r2", name, s.Pending)
		}
		if len(s.Questions) != 2 || s.Questions[0].QuestionID != "q1" || s.Questions[1].QuestionID != "q2" {
			t.Errorf("%s's copy has questions %+v, want q1 and q2", name, s.Questions)
		}
	}

	now, _ := m.Get("s1")
	if now.ToolCalls[0].Status != "completed" || len(now.Pending) != 1 || now.Pending[0].RequestID != "r2" || len(now.Questions) != 1 || now.Questions[0].QuestionID != "q2" {
		t.Errorf("the manager's session didn't take the later events: %+v", now)
	}
}
