package approval

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
)

// TestWriteDecisionVector writes testdata/decision-vector.json when
// SKYLANDS_WRITE_VECTOR is set. It is how the vector was made.
func TestWriteDecisionVector(t *testing.T) {
	if os.Getenv("SKYLANDS_WRITE_VECTOR") == "" {
		t.Skip("set SKYLANDS_WRITE_VECTOR to write the vector")
	}
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	owner, _ := nkeys.CreateOperator()
	ownerPub, _ := owner.PublicKey()

	message := "Which file should I edit? é\U0001F600"
	fields := []v1.QuestionField{
		{Key: "file", Title: "File", Type: "string", Options: []v1.QuestionOption{{Value: "main.go", Title: "main.go"}, {Value: "util.go", Description: "the helpers"}}, Required: true},
		{Key: "note", Type: "string"},
		{Key: "count", Title: "How many", Type: "integer"},
	}
	form, err := FormHash(message, fields)
	if err != nil {
		t.Fatal(err)
	}
	answers := json.RawMessage(`{"note":"line one\nline two","file":"util.go","count":3}`)
	answersHash, _ := AnswersHash(answers)
	list := []v1.Rule{{Decision: "allow", Kind: "execute", Pattern: "go test *"}, {Decision: "deny", Kind: "*", Pattern: "worktree", Source: "repo"}}
	rulesHash, _ := RulesHash(list)

	decisions := []Decision{
		ToolCall{V: DecisionVersion, Kind: KindToolCall, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Request: "Req1", Decision: "allow", Option: "allow_once", Input: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		Answer{V: DecisionVersion, Kind: KindAnswer, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Question: "Q1", Form: form, Answers: answersHash},
		Answer{V: DecisionVersion, Kind: KindAnswer, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Question: "Q1", Form: form, Declined: true},
		Trust{V: DecisionVersion, Kind: KindTrust, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Trusted: true, Revision: 2},
		SessionRules{V: DecisionVersion, Kind: KindSessionRules, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Rules: rulesHash, Revision: 1},
		RepoRules{V: DecisionVersion, Kind: KindRepoRules, World: ownerPub, Host: "workstation", Repo: "R3po", Rules: rulesHash, Revision: 0},
	}
	var out []vectorDecision
	for _, d := range decisions {
		c, _ := canonical.Marshal(d)
		digest, _ := Digest(d)
		sig, err := SignDecision(kp, d)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(d)
		out = append(out, vectorDecision{Decision: raw, Canonical: string(c), Digest: hex.EncodeToString(digest), Signature: sig})
	}
	data, _ := json.MarshalIndent(vector{
		Comment:   "A test key and decisions it signed, and the hashes they name, which Go and the browser must reproduce byte for byte. The seed is for tests only.",
		Seed:      string(seed),
		Decisions: out,
		Form:      vectorForm{Message: message, Fields: fields, Hash: form},
		Answers:   vectorAnswers{Answers: answers, Hash: answersHash},
		Rules:     vectorRules{Rules: list, Hash: rulesHash},
	}, "", "  ")
	if err := os.WriteFile("testdata/decision-vector.json", append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
