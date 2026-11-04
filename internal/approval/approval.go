package approval

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Verify checks that a signed message was signed by its signer.
func Verify(s v1.Signed) error {
	kp, err := nkeys.FromPublicKey(s.Signer)
	if err != nil {
		return fmt.Errorf("signer %q is not a public nkey", s.Signer)
	}
	sig, err := base64.RawURLEncoding.DecodeString(s.Sig)
	if err != nil {
		return errors.New("the signature is not unpadded base64url")
	}
	if err := kp.Verify([]byte(s.Payload), sig); err != nil {
		return errors.New("the signature does not verify")
	}
	return nil
}

// Sign signs payload, as JSON, with kp. The browser signs the same way;
// this is for the Go side and for tests.
func Sign(kp nkeys.KeyPair, payload any) (v1.Signed, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return v1.Signed{}, err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return v1.Signed{}, err
	}
	sig, err := kp.Sign(data)
	if err != nil {
		return v1.Signed{}, err
	}
	return v1.Signed{Payload: string(data), Signer: pub, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// MaxNameLength is the longest name a world or an approver may have.
const MaxNameLength = 80

// OpenWorld verifies a world record: signed by the owner it names, with a
// name.
func OpenWorld(s v1.Signed) (v1.WorldRecord, error) {
	var w v1.WorldRecord
	if err := open(s, &w); err != nil {
		return w, err
	}
	if w.Type != "world" {
		return w, fmt.Errorf("a world record has type %q", w.Type)
	}
	if !nkeys.IsValidPublicOperatorKey(w.Owner) {
		return w, errors.New("the owner is not an operator public nkey")
	}
	if s.Signer != w.Owner {
		return w, errors.New("the world record is not signed by its owner")
	}
	if err := checkName(w.Name, "the world"); err != nil {
		return w, err
	}
	return w, nil
}

// OpenOwnerApprover verifies an approver entry the owner key signed for
// the world owner names.
func OpenOwnerApprover(s v1.Signed, owner string) (v1.ApproverEntry, error) {
	var a v1.ApproverEntry
	if err := open(s, &a); err != nil {
		return a, err
	}
	if a.Type != "approver" {
		return a, fmt.Errorf("an approver entry has type %q", a.Type)
	}
	if a.World != owner {
		return a, errors.New("the approver entry is for another world")
	}
	if s.Signer != owner {
		return a, errors.New("the approver entry is not signed by the world's owner")
	}
	if !nkeys.IsValidPublicUserKey(a.Key) {
		return a, errors.New("the approver's key is not a user public nkey")
	}
	if err := checkName(a.Name, "the approver"); err != nil {
		return a, err
	}
	return a, nil
}

// open verifies s and decodes its payload into v.
func open(s v1.Signed, v any) error {
	if err := Verify(s); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(s.Payload), v); err != nil {
		return fmt.Errorf("the signed payload is not the JSON expected: %w", err)
	}
	return nil
}

func checkName(name, what string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s has no name", what)
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("%s's name is longer than %d bytes", what, MaxNameLength)
	}
	return nil
}

// Now is a timestamp in the form records carry.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
