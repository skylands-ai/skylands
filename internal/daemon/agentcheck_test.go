package daemon

import (
	"reflect"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// TestAgentModelsAreReadAgainForANewLogin keeps the models an agent offered
// for as long as it is logged in the same way, and asks for them again
// when its login changes, when they were never read, or after it could
// not run.
func TestAgentModelsAreReadAgainForANewLogin(t *testing.T) {
	c := &agentChecker{}
	free := []v1.AgentModel{{ID: "luna"}}
	c.remember([]v1.AgentAvailability{
		{Name: "codex", Available: true, Login: "ChatGPT Free", ModelsRead: true, Models: free, DefaultModel: "luna"},
		{Name: "fake", Available: true, ModelsRead: true},
		{Name: "claude", Available: true, Login: "Claude Max"},
	})

	found := []v1.AgentAvailability{
		{Name: "claude", Available: true, Login: "Claude Max"},
		{Name: "codex", Available: true, Login: "ChatGPT Free"},
		{Name: "fake", Available: true},
	}
	if again := c.fromCache(found); !reflect.DeepEqual(again, []string{"claude"}) {
		t.Errorf("read again = %v, want only the agent whose models were never read", again)
	}
	if a := found[1]; !a.ModelsRead || !reflect.DeepEqual(a.Models, free) || a.DefaultModel != "luna" {
		t.Errorf("codex = %+v, want the models read for its login", a)
	}
	if a := found[2]; !a.ModelsRead || len(a.Models) != 0 {
		t.Errorf("fake = %+v, want read, with no model setting", a)
	}

	// A new login, and an agent that could not run, are read again.
	found = []v1.AgentAvailability{{Name: "codex", Available: true, Login: "ChatGPT Plus"}, {Name: "fake"}}
	if again := c.fromCache(found); !reflect.DeepEqual(again, []string{"codex"}) || found[0].ModelsRead {
		t.Errorf("read again = %v, codex = %+v, want codex read for its new login", again, found[0])
	}
	c.remember(found)
	if again := c.fromCache([]v1.AgentAvailability{{Name: "fake", Available: true}}); !reflect.DeepEqual(again, []string{"fake"}) {
		t.Errorf("read again = %v, want fake, which could not run in between", again)
	}
}
