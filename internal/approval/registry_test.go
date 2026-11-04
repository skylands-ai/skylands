package approval

import (
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// world builds registry records the way a citadel stores them, numbering
// revisions and stamping times as the server would.
type world struct {
	t        *testing.T
	owner    nkeys.KeyPair
	ownerPub string
	records  []Record
	now      time.Time
}

func newWorld(t *testing.T) *world {
	owner, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := owner.PublicKey()
	return &world{t: t, owner: owner, ownerPub: pub, now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
}

func (w *world) user() (nkeys.KeyPair, string) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		w.t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

// store adds a record signed by kp, a minute after the last one.
func (w *world) store(kp nkeys.KeyPair, payload any) {
	w.t.Helper()
	s, err := Sign(kp, payload)
	if err != nil {
		w.t.Fatal(err)
	}
	w.now = w.now.Add(time.Minute)
	w.records = append(w.records, Record{Signed: s, Revision: uint64(len(w.records) + 1), Stored: w.now})
}

// ownerEnrolls makes a new approver with an entry the owner key signs.
func (w *world) ownerEnrolls(name string) (nkeys.KeyPair, string) {
	kp, pub := w.user()
	w.store(w.owner, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: pub, Name: name, Created: Now()})
	return kp, pub
}

// vouch stores a voucher signed by by, and returns its claim key.
func (w *world) vouch(by nkeys.KeyPair) (nkeys.KeyPair, string) {
	claim, claimPub := w.user()
	w.store(by, v1.Voucher{Type: "voucher", World: w.ownerPub, Claim: claimPub, Created: Now()})
	return claim, claimPub
}

// claim makes a new approver with an entry signed by a voucher's claim key.
func (w *world) claim(claim nkeys.KeyPair, claimPub, name string) (nkeys.KeyPair, string) {
	kp, pub := w.user()
	w.store(claim, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: pub, Name: name, Created: Now(), Voucher: claimPub})
	return kp, pub
}

func (w *world) revoke(by nkeys.KeyPair, key string) {
	w.store(by, v1.Revocation{Type: "revocation", World: w.ownerPub, Key: key, Created: Now()})
}

func (w *world) registry() *Registry { return NewRegistry(w.ownerPub, w.records) }

func TestRegistryEnrollsThroughVouchers(t *testing.T) {
	w := newWorld(t)
	laptop, laptopPub := w.ownerEnrolls("laptop")
	claim, claimPub := w.vouch(laptop)
	_, phonePub := w.claim(claim, claimPub, "phone")

	r := w.registry()
	if !r.Valid(laptopPub) || !r.Valid(phonePub) {
		t.Fatalf("laptop %v, phone %v; want both valid; skipped %v", r.Valid(laptopPub), r.Valid(phonePub), r.Skipped)
	}
	phone, _ := r.Approver(phonePub)
	if phone.By != laptopPub || phone.Voucher != claimPub || phone.Name != "phone" {
		t.Errorf("phone = %+v, want it enrolled by the laptop through its voucher", phone)
	}
	if v, _ := r.Voucher(claimPub); v.UsedBy != phonePub || v.By != laptopPub || !v.Expires.Equal(v.Stored.Add(VoucherLifetime)) {
		t.Errorf("voucher = %+v", v)
	}
	if got := r.Approvers(); len(got) != 2 || got[0].Key != laptopPub || got[1].Key != phonePub {
		t.Errorf("approvers = %+v, want laptop then phone", got)
	}
}

func TestRegistryVoucherWorksOnceAndExpires(t *testing.T) {
	w := newWorld(t)
	laptop, _ := w.ownerEnrolls("laptop")
	claim, claimPub := w.vouch(laptop)
	_, firstPub := w.claim(claim, claimPub, "first")
	_, secondPub := w.claim(claim, claimPub, "second")

	late, latePub := w.vouch(laptop)
	w.now = w.now.Add(VoucherLifetime) // the next record lands a minute past the lifetime
	_, tooLatePub := w.claim(late, latePub, "too late")

	r := w.registry()
	if !r.Valid(firstPub) {
		t.Errorf("the first browser to use the voucher isn't an approver; skipped %v", r.Skipped)
	}
	if r.Valid(secondPub) {
		t.Error("a second browser used the same voucher")
	}
	if r.Valid(tooLatePub) {
		t.Error("a browser used a voucher after it expired")
	}
}

func TestRegistryRefusesWhatNoApproverSigned(t *testing.T) {
	w := newWorld(t)
	stranger, strangerPub := w.user()
	// A voucher from a key that isn't an approver, and an entry using it.
	claim, claimPub := w.vouch(stranger)
	_, viaStrangerPub := w.claim(claim, claimPub, "via stranger")
	// An entry naming a voucher that was never stored.
	ghost, ghostPub := w.user()
	_, viaGhostPub := w.claim(ghost, ghostPub, "via nothing")
	// An entry signed by the approver itself rather than the owner key.
	self, selfPub := w.user()
	w.store(self, v1.ApproverEntry{Type: "approver", World: w.ownerPub, Key: selfPub, Name: "self", Created: Now()})

	r := w.registry()
	for name, key := range map[string]string{"stranger": strangerPub, "via stranger": viaStrangerPub, "via nothing": viaGhostPub, "self-signed": selfPub} {
		if r.Valid(key) {
			t.Errorf("%s is an approver", name)
		}
	}
	if len(r.Skipped) != 4 {
		t.Errorf("skipped %d records, want 4: %v", len(r.Skipped), r.Skipped)
	}
}

func TestRegistryRevocationCascades(t *testing.T) {
	w := newWorld(t)
	laptop, laptopPub := w.ownerEnrolls("laptop")
	desktop, desktopPub := w.ownerEnrolls("desktop")
	claim, claimPub := w.vouch(laptop)
	phone, phonePub := w.claim(claim, claimPub, "phone")
	claim2, claim2Pub := w.vouch(phone)
	_, tabletPub := w.claim(claim2, claim2Pub, "tablet")

	if got := w.registry().Through(laptopPub); !slices.Equal(got, []string{phonePub, tabletPub}) {
		t.Errorf("Through(laptop) = %v, want the phone and the tablet", got)
	}
	w.revoke(desktop, laptopPub)
	r := w.registry()
	if r.Valid(laptopPub) || r.Valid(phonePub) || r.Valid(tabletPub) {
		t.Errorf("after revoking the laptop: laptop %v, phone %v, tablet %v; want none valid", r.Valid(laptopPub), r.Valid(phonePub), r.Valid(tabletPub))
	}
	if !r.Valid(desktopPub) {
		t.Error("the desktop that revoked the laptop was revoked too")
	}
	laptopState, _ := r.Approver(laptopPub)
	if laptopState.RevokedBy != desktopPub || laptopState.Revoked.IsZero() {
		t.Errorf("laptop = %+v, want it revoked by the desktop", laptopState)
	}
	// The phone, no longer an approver, can't vouch or revoke.
	claim3, claim3Pub := w.vouch(phone)
	_, viaPhonePub := w.claim(claim3, claim3Pub, "via revoked phone")
	w.revoke(phone, desktopPub)
	r = w.registry()
	if r.Valid(viaPhonePub) || !r.Valid(desktopPub) {
		t.Errorf("a revoked approver vouched (%v) or revoked (%v)", r.Valid(viaPhonePub), !r.Valid(desktopPub))
	}
}

func TestRegistryRevocationIsPermanent(t *testing.T) {
	w := newWorld(t)
	laptop, laptopPub := w.ownerEnrolls("laptop")
	desktop, desktopPub := w.ownerEnrolls("desktop")
	_, thiefPub := w.ownerEnrolls("thief")
	// The laptop revokes the thief, and later the owner revokes the laptop.
	// The thief stays revoked, because the laptop was an approver when it
	// revoked it.
	w.revoke(laptop, thiefPub)
	w.revoke(w.owner, laptopPub)
	// An approver may revoke itself.
	w.revoke(desktop, desktopPub)

	r := w.registry()
	if r.Valid(thiefPub) || r.Valid(laptopPub) || r.Valid(desktopPub) {
		t.Errorf("thief %v, laptop %v, desktop %v; want all revoked", r.Valid(thiefPub), r.Valid(laptopPub), r.Valid(desktopPub))
	}
	// The owner key enrolls a fresh key for the laptop's browser.
	_, freshPub := w.ownerEnrolls("laptop again")
	if !w.registry().Valid(freshPub) {
		t.Error("the recovery file couldn't enroll a fresh key")
	}
}

func TestRegistryIsTheSameInAnyOrder(t *testing.T) {
	w := newWorld(t)
	laptop, laptopPub := w.ownerEnrolls("laptop")
	claim, claimPub := w.vouch(laptop)
	_, phonePub := w.claim(claim, claimPub, "phone")
	w.revoke(w.owner, laptopPub)

	reversed := slices.Clone(w.records)
	slices.Reverse(reversed)
	r := NewRegistry(w.ownerPub, reversed)
	if r.Valid(laptopPub) || r.Valid(phonePub) || len(r.Approvers()) != 2 || len(r.Skipped) != 0 {
		t.Errorf("from reversed records: laptop %v, phone %v, %d approvers, skipped %v", r.Valid(laptopPub), r.Valid(phonePub), len(r.Approvers()), r.Skipped)
	}
}
