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
	Seed      string               `json:"seed"`
	Decision  Decision             `json:"decision"`
	Canonical string               `json:"canonical"`
	Digest    string               `json:"digest"`
	Signature v1.DecisionSignature `json:"signature"`
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

func TestDecisionVector(t *testing.T) {
	v := readVector(t)
	kp, err := nkeys.FromSeed([]byte(v.Seed))
	if err != nil {
		t.Fatal(err)
	}
	if c, err := canonical.Marshal(v.Decision); err != nil || string(c) != v.Canonical {
		t.Errorf("canonical = %s, %v; want %s", c, err, v.Canonical)
	}
	if d, err := v.Decision.Digest(); err != nil || hex.EncodeToString(d) != v.Digest {
		t.Errorf("digest = %x, %v; want %s", d, err, v.Digest)
	}
	// Ed25519 signs deterministically, so the signature is the vector's.
	if s, err := SignDecision(kp, v.Decision); err != nil || s != v.Signature {
		t.Errorf("signature = %+v, %v; want %+v", s, err, v.Signature)
	}
	if err := VerifyDecision(v.Decision, v.Signature); err != nil {
		t.Errorf("the vector's signature doesn't verify: %v", err)
	}
}

func TestVerifyDecisionRefuses(t *testing.T) {
	v := readVector(t)
	owner, _ := nkeys.CreateOperator()
	otherWorld, _ := owner.PublicKey()
	change := map[string]func(d *Decision){
		"another request": func(d *Decision) { d.Request = "Req2" },
		"a denial":        func(d *Decision) { d.Decision, d.Option = "deny", "" },
		"another option":  func(d *Decision) { d.Option = "allow_always" },
		"another input":   func(d *Decision) { d.Input = d.Input[:63] + "9" },
		"another host":    func(d *Decision) { d.Host = "laptop" },
		"another world":   func(d *Decision) { d.World = otherWorld },
	}
	for name, f := range change {
		d := v.Decision
		f(&d)
		if err := VerifyDecision(d, v.Signature); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v, want ErrBadSignature", name, err)
		}
	}
	d := v.Decision
	d.V = 2
	if err := VerifyDecision(d, v.Signature); err == nil {
		t.Error("a decision of another version verified")
	}
	other, _ := nkeys.CreateUser()
	otherPub, _ := other.PublicKey()
	if err := VerifyDecision(v.Decision, v1.DecisionSignature{Approver: otherPub, Sig: v.Signature.Sig}); !errors.Is(err, ErrBadSignature) {
		t.Errorf("another approver's key: %v, want ErrBadSignature", err)
	}
}
