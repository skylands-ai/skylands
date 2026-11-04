package approval

import (
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func keys(t *testing.T) (owner, approver nkeys.KeyPair, ownerPub, approverPub string) {
	t.Helper()
	owner, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	approver, err = nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	ownerPub, _ = owner.PublicKey()
	approverPub, _ = approver.PublicKey()
	return owner, approver, ownerPub, approverPub
}

func TestOpenWorld(t *testing.T) {
	owner, approver, ownerPub, _ := keys(t)
	good, err := Sign(owner, v1.WorldRecord{Type: "world", Name: "skylands", Owner: ownerPub, Created: Now()})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := OpenWorld(good); err != nil || w.Owner != ownerPub || w.Name != "skylands" {
		t.Fatalf("OpenWorld = %+v, %v", w, err)
	}

	tampered := good
	tampered.Payload = strings.Replace(good.Payload, "skylands", "elsewhere", 1)
	notOwner, _ := Sign(approver, v1.WorldRecord{Type: "world", Name: "skylands", Owner: ownerPub, Created: Now()})
	noName, _ := Sign(owner, v1.WorldRecord{Type: "world", Name: " ", Owner: ownerPub, Created: Now()})
	wrongType, _ := Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: ownerPub, Name: "x"})
	for name, s := range map[string]v1.Signed{"tampered": tampered, "signed by another key": notOwner, "no name": noName, "wrong type": wrongType} {
		if _, err := OpenWorld(s); err == nil {
			t.Errorf("%s: OpenWorld accepted it", name)
		}
	}
}

func TestOpenOwnerApprover(t *testing.T) {
	owner, approver, ownerPub, approverPub := keys(t)
	entry := v1.ApproverEntry{Type: "approver", World: ownerPub, Key: approverPub, Name: "Chrome on wopr", Created: Now()}
	good, _ := Sign(owner, entry)
	if a, err := OpenOwnerApprover(good, ownerPub); err != nil || a.Key != approverPub {
		t.Fatalf("OpenOwnerApprover = %+v, %v", a, err)
	}

	other, _, otherPub, _ := keys(t)
	byApprover, _ := Sign(approver, entry)
	forOther := entry
	forOther.World = otherPub
	otherWorld, _ := Sign(other, forOther)
	ownerAsKey := entry
	ownerAsKey.Key = ownerPub
	notUser, _ := Sign(owner, ownerAsKey)
	badSig := good
	badSig.Sig = good.Sig[:len(good.Sig)-4] + "AAAA"
	for name, s := range map[string]v1.Signed{"signed by the approver": byApprover, "another world": otherWorld, "owner key as approver": notUser, "bad signature": badSig} {
		if _, err := OpenOwnerApprover(s, ownerPub); err == nil {
			t.Errorf("%s: OpenOwnerApprover accepted it", name)
		}
	}
}
