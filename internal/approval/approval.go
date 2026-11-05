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

// OpenVouchedApprover verifies an approver entry signed with a voucher's
// claim key, for the world owner names. Whether the voucher counts is
// the registry's to decide.
func OpenVouchedApprover(s v1.Signed, owner string) (v1.ApproverEntry, error) {
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
	if !nkeys.IsValidPublicUserKey(a.Voucher) {
		return a, errors.New("the approver entry names no voucher")
	}
	if s.Signer != a.Voucher {
		return a, errors.New("the approver entry is not signed by its voucher's claim key")
	}
	if !nkeys.IsValidPublicUserKey(a.Key) || a.Key == a.Voucher {
		return a, errors.New("the approver's key is not a user public nkey of its own")
	}
	if err := checkName(a.Name, "the approver"); err != nil {
		return a, err
	}
	return a, nil
}

// OpenVoucher verifies a voucher signed by an approver key, for the world
// owner names. Whether its signer is an approver is the registry's to
// decide.
func OpenVoucher(s v1.Signed, owner string) (v1.Voucher, error) {
	var v v1.Voucher
	if err := open(s, &v); err != nil {
		return v, err
	}
	if v.Type != "voucher" {
		return v, fmt.Errorf("a voucher has type %q", v.Type)
	}
	if v.World != owner {
		return v, errors.New("the voucher is for another world")
	}
	if !nkeys.IsValidPublicUserKey(s.Signer) {
		return v, errors.New("the voucher is not signed by an approver key")
	}
	if !nkeys.IsValidPublicUserKey(v.Claim) || v.Claim == s.Signer {
		return v, errors.New("the voucher's claim key is not a user public nkey of its own")
	}
	return v, nil
}

// OpenRevocation verifies a revocation signed by the owner key or an
// approver key, for the world owner names.
func OpenRevocation(s v1.Signed, owner string) (v1.Revocation, error) {
	var r v1.Revocation
	if err := open(s, &r); err != nil {
		return r, err
	}
	if r.Type != "revocation" {
		return r, fmt.Errorf("a revocation has type %q", r.Type)
	}
	if r.World != owner {
		return r, errors.New("the revocation is for another world")
	}
	if s.Signer != owner && !nkeys.IsValidPublicUserKey(s.Signer) {
		return r, errors.New("the revocation is not signed by the owner key or an approver key")
	}
	if !nkeys.IsValidPublicUserKey(r.Key) {
		return r, errors.New("the revocation names no approver key")
	}
	return r, nil
}

// OpenSignIn verifies a sign-in signed by an approver key, for the world
// owner names. Whether its signer is an approver, and whether it is current
// and for this citadel, are the citadel's to decide.
func OpenSignIn(s v1.Signed, owner string) (v1.SignIn, error) {
	var in v1.SignIn
	if err := open(s, &in); err != nil {
		return in, err
	}
	if in.Type != "signin" {
		return in, fmt.Errorf("a sign-in has type %q", in.Type)
	}
	if in.World != owner {
		return in, errors.New("the sign-in is for another world")
	}
	if !nkeys.IsValidPublicUserKey(s.Signer) {
		return in, errors.New("the sign-in is not signed by an approver key")
	}
	return in, nil
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
