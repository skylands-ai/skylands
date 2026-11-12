package approval

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
)

// vector is testdata/decision-vector.json, which the browser's tests read
// too, so both sides must produce its bytes.
type vector struct {
	Comment   string           `json:"comment"`
	Seed      string           `json:"seed"`
	Decisions []vectorDecision `json:"decisions"`
	Form      vectorForm       `json:"form"`
	Answers   vectorAnswers    `json:"answers"`
	Rules     vectorRules      `json:"rules"`
}

type vectorDecision struct {
	Decision  json.RawMessage      `json:"decision"`
	Canonical string               `json:"canonical"`
	Digest    string               `json:"digest"`
	Signature v1.DecisionSignature `json:"signature"`
}

type vectorForm struct {
	Message string             `json:"message"`
	Fields  []v1.QuestionField `json:"fields"`
	Hash    string             `json:"hash"`
}

type vectorAnswers struct {
	Answers json.RawMessage `json:"answers"`
	Hash    string          `json:"hash"`
}

type vectorRules struct {
	Rules []v1.Rule `json:"rules"`
	Hash  string    `json:"hash"`
}

func readVector(t *testing.T) vector {
	t.Helper()
	data, err := os.ReadFile("testdata/decision-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// decode reads one of the vector's decisions as the type its kind names.
func decode(t *testing.T, raw json.RawMessage) Decision {
	t.Helper()
	var head struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(raw, &head)
	var d Decision
	var err error
	switch head.Kind {
	case KindToolCall:
		var x ToolCall
		err, d = json.Unmarshal(raw, &x), x
	case KindAnswer:
		var x Answer
		err, d = json.Unmarshal(raw, &x), x
	case KindStart:
		var x Start
		err, d = json.Unmarshal(raw, &x), x
	case KindPrompt:
		var x Prompt
		err, d = json.Unmarshal(raw, &x), x
	case KindChange:
		var x Change
		err, d = json.Unmarshal(raw, &x), x
	case KindTrust:
		var x Trust
		err, d = json.Unmarshal(raw, &x), x
	case KindSessionRules:
		var x SessionRules
		err, d = json.Unmarshal(raw, &x), x
	case KindRepoRules:
		var x RepoRules
		err, d = json.Unmarshal(raw, &x), x
	case KindTerminal:
		var x Terminal
		err, d = json.Unmarshal(raw, &x), x
	case KindTerminalIn:
		var x TerminalInput
		err, d = json.Unmarshal(raw, &x), x
	default:
		t.Fatalf("the vector has a decision of kind %q", head.Kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecisionVector(t *testing.T) {
	v := readVector(t)
	kp, err := nkeys.FromSeed([]byte(v.Seed))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, vd := range v.Decisions {
		d := decode(t, vd.Decision)
		_, kind, _ := d.header()
		kinds[kind] = true
		if c, err := canonical.Marshal(d); err != nil || string(c) != vd.Canonical {
			t.Errorf("%s: canonical = %s, %v; want %s", kind, c, err, vd.Canonical)
		}
		if dg, err := Digest(d); err != nil || hex.EncodeToString(dg) != vd.Digest {
			t.Errorf("%s: digest = %x, %v; want %s", kind, dg, err, vd.Digest)
		}
		// Ed25519 signs deterministically, so the signature is the vector's.
		if s, err := SignDecision(kp, d); err != nil || s != vd.Signature {
			t.Errorf("%s: signature = %+v, %v; want %+v", kind, s, err, vd.Signature)
		}
		if err := VerifyDecision(d, vd.Signature); err != nil {
			t.Errorf("%s: the vector's signature doesn't verify: %v", kind, err)
		}
	}
	for _, k := range []string{KindToolCall, KindAnswer, KindTrust, KindSessionRules, KindRepoRules, KindStart, KindPrompt, KindChange, KindTerminal, KindTerminalIn} {
		if !kinds[k] {
			t.Errorf("the vector has no %s decision", k)
		}
	}
	if h, err := FormHash(v.Form.Message, v.Form.Fields); err != nil || h != v.Form.Hash {
		t.Errorf("FormHash = %s, %v; want %s", h, err, v.Form.Hash)
	}
	if h, err := AnswersHash(v.Answers.Answers); err != nil || h != v.Answers.Hash {
		t.Errorf("AnswersHash = %s, %v; want %s", h, err, v.Answers.Hash)
	}
	if h, err := RulesHash(v.Rules.Rules); err != nil || h != v.Rules.Hash {
		t.Errorf("RulesHash = %s, %v; want %s", h, err, v.Rules.Hash)
	}
}

func TestVerifyDecisionRefuses(t *testing.T) {
	v := readVector(t)
	owner, _ := nkeys.CreateOperator()
	otherWorld, _ := owner.PublicKey()
	toolCall := decode(t, v.Decisions[0].Decision).(ToolCall)
	sig := v.Decisions[0].Signature
	change := map[string]func(d *ToolCall){
		"another request": func(d *ToolCall) { d.Request = "Req2" },
		"a denial":        func(d *ToolCall) { d.Decision, d.Option = "deny", "" },
		"another option":  func(d *ToolCall) { d.Option = "allow_always" },
		"another input":   func(d *ToolCall) { d.Input = d.Input[:63] + "9" },
		"another host":    func(d *ToolCall) { d.Host = "laptop" },
		"another world":   func(d *ToolCall) { d.World = otherWorld },
	}
	for name, f := range change {
		d := toolCall
		f(&d)
		if err := VerifyDecision(d, sig); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v, want ErrBadSignature", name, err)
		}
	}
	d := toolCall
	d.V = 2
	if err := VerifyDecision(d, sig); err == nil {
		t.Error("a decision of another version verified")
	}
	d = toolCall
	d.Kind = KindTrust
	if err := VerifyDecision(d, sig); err == nil || errors.Is(err, ErrBadSignature) {
		t.Errorf("a tool call naming another kind = %v, want a refusal of the kind", err)
	}
	other, _ := nkeys.CreateUser()
	otherPub, _ := other.PublicKey()
	if err := VerifyDecision(toolCall, v1.DecisionSignature{Approver: otherPub, Sig: sig.Sig}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("another approver's key: %v, want ErrBadSignature", err)
	}

	// The other kinds refuse a changed field the same way, and a
	// signature over one kind never verifies another.
	for i, vd := range v.Decisions[1:] {
		d := decode(t, vd.Decision)
		var changed Decision
		switch x := d.(type) {
		case Answer:
			if x.Declined {
				x.Declined, x.Answers = false, v.Answers.Hash
			} else {
				x.Answers = x.Answers[:63] + "0"
			}
			changed = x
		case Trust:
			x.Revision++
			changed = x
		case SessionRules:
			x.Revision = 0
			changed = x
		case RepoRules:
			x.Rules = x.Rules[:63] + "0"
			changed = x
		case Start:
			x.Mode = ""
			changed = x
		case Prompt:
			x.Revision++
			changed = x
		case Change:
			x.Action = ChangeCommit + "s"
			changed = x
		case Terminal:
			x.Terminal += "2"
			changed = x
		case TerminalInput:
			x.Seq++
			changed = x
		}
		if err := VerifyDecision(changed, vd.Signature); !errors.Is(err, ErrBadSignature) {
			t.Errorf("decision %d changed: %v, want ErrBadSignature", i+1, err)
		}
		if err := VerifyDecision(toolCall, vd.Signature); !errors.Is(err, ErrBadSignature) {
			t.Errorf("decision %d's signature over the tool call: %v, want ErrBadSignature", i+1, err)
		}
	}
}

func TestFormHashIgnoresWhatIsLeftOut(t *testing.T) {
	// A field with its empty properties left out hashes as one with them
	// spelled out empty, as the browser may get it.
	a, _ := FormHash("m", []v1.QuestionField{{Key: "k", Type: "string"}})
	var fields []v1.QuestionField
	_ = json.Unmarshal([]byte(`[{"key":"k","title":"","type":"string","options":[],"required":false}]`), &fields)
	b, _ := FormHash("m", fields)
	if a != b {
		t.Errorf("FormHash differs for a field spelled out: %s and %s", a, b)
	}
	c, _ := FormHash("m", []v1.QuestionField{{Key: "k", Type: "string", Required: true}})
	if a == c {
		t.Error("FormHash is the same for a required field")
	}
}
