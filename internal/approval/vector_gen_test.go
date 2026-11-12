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
	// The key and world are kept from the vector already written, so adding
	// a decision leaves the others' bytes as they were.
	kp, _ := nkeys.CreateUser()
	owner, _ := nkeys.CreateOperator()
	ownerPub, _ := owner.PublicKey()
	if data, err := os.ReadFile("testdata/decision-vector.json"); err == nil {
		var old vector
		if json.Unmarshal(data, &old) == nil && old.Seed != "" && len(old.Decisions) > 0 {
			kp, _ = nkeys.FromSeed([]byte(old.Seed))
			var first struct {
				World string `json:"world"`
			}
			_ = json.Unmarshal(old.Decisions[0].Decision, &first)
			ownerPub = first.World
		}
	}
	seed, _ := kp.Seed()

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
	prHash, _ := PullRequestHash("Make it faster", "It was slow.\n\nNow it isn't.")
	mentions := []string{"internal/main.go", "docs/é \U0001F600.md"}
	images := []v1.PromptImage{{Name: "screen shot.png", MimeType: "image/png", Hash: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", Size: 1234567}}
	attachmentsHash, err := AttachmentsHash(mentions, images)
	if err != nil {
		t.Fatal(err)
	}

	decisions := []Decision{
		ToolCall{V: DecisionVersion, Kind: KindToolCall, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Request: "Req1", Decision: "allow", Option: "allow_once", Input: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		Answer{V: DecisionVersion, Kind: KindAnswer, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Question: "Q1", Form: form, Answers: answersHash},
		Answer{V: DecisionVersion, Kind: KindAnswer, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Question: "Q1", Form: form, Declined: true},
		Trust{V: DecisionVersion, Kind: KindTrust, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Trusted: true, Revision: 2},
		SessionRules{V: DecisionVersion, Kind: KindSessionRules, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Rules: rulesHash, Revision: 1},
		RepoRules{V: DecisionVersion, Kind: KindRepoRules, World: ownerPub, Host: "workstation", Repo: "R3po", Rules: rulesHash, Revision: 0},
		Start{V: DecisionVersion, Kind: KindStart, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Repo: "R3po", Agent: "claude", Mode: "plan", Model: "", InCheckout: true, Prompt: TextHash("Fix the flaky test é\U0001F600\nThen run it.")},
		Prompt{V: DecisionVersion, Kind: KindPrompt, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 3, Prompt: TextHash("/review the diff")},
		Change{V: DecisionVersion, Kind: KindChange, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 4, Action: ChangeCommit, Text: TextHash("Make it faster")},
		Change{V: DecisionVersion, Kind: KindChange, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 5, Action: ChangeMerge},
		Change{V: DecisionVersion, Kind: KindChange, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 5, Action: ChangePullRequest, Text: prHash},
		Change{V: DecisionVersion, Kind: KindChange, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 6, Action: ChangeRemove},
		Start{V: DecisionVersion, Kind: KindStart, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Repo: "R3po", Agent: "claude", Prompt: TextHash("Look at @internal/main.go"), Attachments: attachmentsHash},
		Prompt{V: DecisionVersion, Kind: KindPrompt, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Revision: 7, Prompt: TextHash("And this one"), Attachments: attachmentsHash},
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
		Comment:     "A test key and decisions it signed, and the hashes they name, which Go and the browser must reproduce byte for byte. The seed is for tests only.",
		Seed:        string(seed),
		Decisions:   out,
		Form:        vectorForm{Message: message, Fields: fields, Hash: form},
		Answers:     vectorAnswers{Answers: answers, Hash: answersHash},
		Rules:       vectorRules{Rules: list, Hash: rulesHash},
		Attachments: vectorAttachments{Mentions: mentions, Images: images, Hash: attachmentsHash},
	}, "", "  ")
	if err := os.WriteFile("testdata/decision-vector.json", append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
