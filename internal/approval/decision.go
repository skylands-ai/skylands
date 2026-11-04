package approval

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
)

// DecisionVersion is the version of the decision format below. A daemon
// refuses a signature over any other.
const DecisionVersion = 1

// Decision is what an approver signs to allow or deny one tool call. Its
// canonical JSON (RFC 8785), hashed with SHA-256, is what the signature
// covers. The browser builds it from what it shows, and the daemon from
// its own record of the request, so a signature verifies only if the two
// agree on every field.
type Decision struct {
	V int `json:"v"`
	// World is the world's owner key.
	World   string `json:"world"`
	Host    string `json:"host"`
	Session string `json:"session"`
	Request string `json:"request"`
	// Decision is "allow" or "deny".
	Decision string `json:"decision"`
	// Option is the option id an approval chose, and "" for a denial.
	Option string `json:"option"`
	// Input is the SHA-256 of the tool call's exact input, in lowercase
	// hex, as PermissionRequested names it.
	Input string `json:"input"`
}

// Digest is the SHA-256 of the decision's canonical JSON, which is what is
// signed.
func (d Decision) Digest() ([]byte, error) {
	data, err := canonical.Marshal(d)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

// SignDecision signs d with an approver key. The browser signs the same
// way; this is for the Go side and for tests.
func SignDecision(kp nkeys.KeyPair, d Decision) (v1.DecisionSignature, error) {
	digest, err := d.Digest()
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	sig, err := kp.Sign(digest)
	if err != nil {
		return v1.DecisionSignature{}, err
	}
	return v1.DecisionSignature{Approver: pub, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// ErrBadSignature is a decision signature that doesn't verify.
var ErrBadSignature = errors.New("the decision's signature doesn't verify")

// VerifyDecision checks that s is its approver's signature over d. Whether
// the approver approves for the world is the registry's to say.
func VerifyDecision(d Decision, s v1.DecisionSignature) error {
	if d.V != DecisionVersion {
		return fmt.Errorf("decision version %d is not %d", d.V, DecisionVersion)
	}
	if !nkeys.IsValidPublicUserKey(s.Approver) {
		return errors.New("the decision isn't signed by an approver key")
	}
	kp, err := nkeys.FromPublicKey(s.Approver)
	if err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(s.Sig)
	if err != nil {
		return ErrBadSignature
	}
	digest, err := d.Digest()
	if err != nil {
		return err
	}
	if kp.Verify(digest, sig) != nil {
		return ErrBadSignature
	}
	return nil
}
