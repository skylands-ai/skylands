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

func TestOpenPushSubscription(t *testing.T) {
	owner, approver, ownerPub, approverPub := keys(t)
	sub := v1.PushSubscription{Type: "push", World: ownerPub, Origin: "https://skylands.example", Endpoint: "https://push.example/abc",
		P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", Auth: "BTBZMqHH6r4Tts7J_aSIgg", Created: Now()}
	good, _ := Sign(approver, sub)
	if p, err := OpenPushSubscription(good, ownerPub); err != nil || p.Endpoint != sub.Endpoint || good.Signer != approverPub {
		t.Fatalf("OpenPushSubscription = %+v, %v", p, err)
	}

	byOwner, _ := Sign(owner, sub)
	_, _, otherPub, _ := keys(t)
	forOther := sub
	forOther.World = otherPub
	otherWorld, _ := Sign(approver, forOther)
	plain := sub
	plain.Endpoint = "http://push.example/abc"
	notHTTPS, _ := Sign(approver, plain)
	noKeys := sub
	noKeys.P256dh = ""
	keyless, _ := Sign(approver, noKeys)
	wrongType := sub
	wrongType.Type = "signin"
	typed, _ := Sign(approver, wrongType)
	for name, s := range map[string]v1.Signed{"signed by the owner key": byOwner, "another world": otherWorld, "http endpoint": notHTTPS, "no p256dh key": keyless, "wrong type": typed} {
		if _, err := OpenPushSubscription(s, ownerPub); err == nil {
			t.Errorf("%s: OpenPushSubscription accepted it", name)
		}
	}
}
