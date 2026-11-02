package v1

import (
	"encoding/json"
	"testing"
)

func TestTokenUsageAddsUp(t *testing.T) {
	var sum TokenUsage
	if !sum.IsZero() {
		t.Fatal("a fresh TokenUsage is not zero")
	}
	sum.Add(TokenUsage{Input: 100, Output: 20, CachedRead: 30, Total: 150})
	sum.Add(TokenUsage{Input: 10, Output: 2, CachedWrite: 5, Thought: 1, Total: 18})
	want := TokenUsage{Input: 110, Output: 22, CachedRead: 30, CachedWrite: 5, Thought: 1, Total: 168}
	if sum != want {
		t.Errorf("sum = %+v, want %+v", sum, want)
	}
	// The event names the session and what the tokens paid for, and the
	// counts that were not reported stay out of the JSON.
	data, err := json.Marshal(TokensUsed{SessionID: "s1", For: TokensForTurn, Tokens: TokenUsage{Input: 1, Output: 2, Total: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"sessionId":"s1","for":"turn","tokens":{"input":1,"output":2,"total":3}}` {
		t.Errorf("TokensUsed JSON = %s", data)
	}
	if !IsSessionEvent(EventTokensUsed) {
		t.Error("TokensUsed is not a session event")
	}
}
