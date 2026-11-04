package approval

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/nats-io/nkeys"

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
	d := Decision{V: DecisionVersion, World: ownerPub, Host: "workstation", Session: "Ses5ion-éh", Request: "Req1", Decision: "allow", Option: "allow_once", Input: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}
	c, _ := canonical.Marshal(d)
	digest, _ := d.Digest()
	sig, err := SignDecision(kp, d)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(map[string]any{"comment": "A test key and a decision it signed, which Go and the browser must reproduce byte for byte. The seed is for tests only.", "seed": string(seed), "decision": d, "canonical": string(c), "digest": hex.EncodeToString(digest), "signature": sig}, "", "  ")
	if err := os.WriteFile("testdata/decision-vector.json", append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
