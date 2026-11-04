package approval

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// VoucherLifetime is how long a voucher works after it is stored, by the
// NATS server's clock.
const VoucherLifetime = 15 * time.Minute

// Record is one message stored in a world's approver registry, with the
// revision and time NATS gave it when it was stored. The revision orders
// the registry, and the time is the server's, which no browser or citadel
// sets.
type Record struct {
	Signed   v1.Signed
	Revision uint64
	Stored   time.Time
}

// Approver is one approver key as the registry sees it.
type Approver struct {
	Key  string
	Name string
	// Enrolled is when its entry was stored. By is the key that enrolled
	// it: the owner key, or the approver whose voucher it used, named in
	// Voucher.
	Enrolled time.Time
	By       string
	Voucher  string
	// Revoked is when it was revoked, and RevokedBy the key that revoked
	// it. Both are zero while it isn't revoked.
	Revoked   time.Time
	RevokedBy string
}

// VoucherState is one voucher as the registry sees it.
type VoucherState struct {
	Claim string
	// By is the approver that signed it.
	By      string
	Stored  time.Time
	Expires time.Time
	// UsedBy is the approver key that used it, or "" while it is unused.
	UsedBy string
}

// Registry decides who approves for a world, from everything stored in its
// approver registry:
//
//   - An approver is enrolled by an entry the owner key signs, or by an
//     entry signed with the claim key of a voucher an approver signed. The
//     first entry for a key counts.
//   - A voucher counts if its signer was an approver when it was stored.
//     The first entry that uses it counts, if it was stored within
//     VoucherLifetime of the voucher.
//   - A revocation counts if the owner key signed it, or an approver did
//     while it was one. It is never undone.
//   - An approver approves while it isn't revoked and the approver that
//     enrolled it approves, back to the owner key. Revoking an approver
//     takes everything enrolled through it along.
//
// Records are taken in revision order, so the registry is the same
// wherever it is built from the same records.
type Registry struct {
	owner     string
	approvers map[string]*Approver
	// order is the approver keys in the order they were enrolled.
	order    []string
	vouchers map[string]*VoucherState
	// Skipped says why each record that didn't count was skipped.
	Skipped []string
}

// NewRegistry builds the registry of the world owner names from records.
func NewRegistry(owner string, records []Record) *Registry {
	r := &Registry{owner: owner, approvers: map[string]*Approver{}, vouchers: map[string]*VoucherState{}}
	sorted := append([]Record(nil), records...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Revision < sorted[j].Revision })
	for _, rec := range sorted {
		if err := r.apply(rec); err != nil {
			r.Skipped = append(r.Skipped, fmt.Sprintf("revision %d: %v", rec.Revision, err))
		}
	}
	return r
}

func (r *Registry) apply(rec Record) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(rec.Signed.Payload), &head); err != nil {
		return fmt.Errorf("not a registry record: %w", err)
	}
	switch head.Type {
	case "approver":
		if rec.Signed.Signer == r.owner {
			a, err := OpenOwnerApprover(rec.Signed, r.owner)
			if err != nil {
				return err
			}
			return r.enroll(a, r.owner, "", rec.Stored)
		}
		a, err := OpenVouchedApprover(rec.Signed, r.owner)
		if err != nil {
			return err
		}
		v := r.vouchers[a.Voucher]
		switch {
		case v == nil:
			return fmt.Errorf("approver %s used voucher %s, which doesn't count", a.Key, a.Voucher)
		case v.UsedBy != "":
			return fmt.Errorf("approver %s used voucher %s, which %s already used", a.Key, a.Voucher, v.UsedBy)
		case rec.Stored.After(v.Expires):
			return fmt.Errorf("approver %s used voucher %s after it expired", a.Key, a.Voucher)
		}
		if err := r.enroll(a, v.By, a.Voucher, rec.Stored); err != nil {
			return err
		}
		v.UsedBy = a.Key
		return nil
	case "voucher":
		v, err := OpenVoucher(rec.Signed, r.owner)
		if err != nil {
			return err
		}
		if !r.Valid(rec.Signed.Signer) {
			return fmt.Errorf("voucher %s is signed by %s, which isn't an approver", v.Claim, rec.Signed.Signer)
		}
		if _, ok := r.vouchers[v.Claim]; ok {
			return fmt.Errorf("voucher %s is already stored", v.Claim)
		}
		r.vouchers[v.Claim] = &VoucherState{Claim: v.Claim, By: rec.Signed.Signer, Stored: rec.Stored, Expires: rec.Stored.Add(VoucherLifetime)}
		return nil
	case "revocation":
		rv, err := OpenRevocation(rec.Signed, r.owner)
		if err != nil {
			return err
		}
		if rec.Signed.Signer != r.owner && !r.Valid(rec.Signed.Signer) {
			return fmt.Errorf("the revocation of %s is signed by %s, which isn't an approver", rv.Key, rec.Signed.Signer)
		}
		a := r.approvers[rv.Key]
		if a == nil {
			return fmt.Errorf("the revocation names %s, which was never an approver", rv.Key)
		}
		if a.Revoked.IsZero() {
			a.Revoked, a.RevokedBy = rec.Stored, rec.Signed.Signer
		}
		return nil
	default:
		return fmt.Errorf("unknown record type %q", head.Type)
	}
}

func (r *Registry) enroll(a v1.ApproverEntry, by, voucher string, at time.Time) error {
	if _, ok := r.approvers[a.Key]; ok {
		return fmt.Errorf("approver %s is already enrolled", a.Key)
	}
	r.approvers[a.Key] = &Approver{Key: a.Key, Name: a.Name, Enrolled: at, By: by, Voucher: voucher}
	r.order = append(r.order, a.Key)
	return nil
}

// Owner is the owner key of the world the registry is for.
func (r *Registry) Owner() string { return r.owner }

// Valid reports whether key approves for the world: it was enrolled, isn't
// revoked, and the approver that enrolled it is valid, back to the owner.
func (r *Registry) Valid(key string) bool {
	// An approver is enrolled after the one that enrolled it, so the chain
	// ends; the bound only guards against a registry built wrong.
	for range len(r.order) + 1 {
		a := r.approvers[key]
		if a == nil || !a.Revoked.IsZero() {
			return false
		}
		if a.By == r.owner {
			return true
		}
		key = a.By
	}
	return false
}

// Approvers is every approver key ever enrolled, valid or not, in the
// order they were enrolled.
func (r *Registry) Approvers() []Approver {
	out := make([]Approver, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, *r.approvers[k])
	}
	return out
}

// Approver is one approver key, if it was ever enrolled.
func (r *Registry) Approver(key string) (Approver, bool) {
	a, ok := r.approvers[key]
	if !ok {
		return Approver{}, false
	}
	return *a, true
}

// Voucher is one voucher that counts, used or not.
func (r *Registry) Voucher(claim string) (VoucherState, bool) {
	v, ok := r.vouchers[claim]
	if !ok {
		return VoucherState{}, false
	}
	return *v, true
}

// Through is every valid approver enrolled through key, directly or
// further down: what revoking key would take along with it.
func (r *Registry) Through(key string) []string {
	var out []string
	for _, k := range r.order {
		if k == key || !r.Valid(k) {
			continue
		}
		for up, n := r.approvers[k].By, 0; up != r.owner && n <= len(r.order); n++ {
			if up == key {
				out = append(out, k)
				break
			}
			a := r.approvers[up]
			if a == nil {
				break
			}
			up = a.By
		}
	}
	return out
}
