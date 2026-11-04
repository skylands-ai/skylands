package citadel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

// newKeys makes an owner key and an approver key, as the setup page does.
func newKeys(t *testing.T) (owner nkeys.KeyPair, ownerPub, approverPub string) {
	t.Helper()
	owner, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	approver, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	ownerPub, _ = owner.PublicKey()
	approverPub, _ = approver.PublicKey()
	return owner, ownerPub, approverPub
}

// setupBody is what the setup page sends: the token, and the world record
// and first approver entry, both signed by the owner key.
func setupBody(t *testing.T, token string, owner nkeys.KeyPair, ownerPub, approverPub string) []byte {
	t.Helper()
	world, err := approval.Sign(owner, v1.WorldRecord{Type: "world", Name: "test world", Owner: ownerPub, Created: approval.Now()})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: approverPub, Name: "Chrome", Created: approval.Now()})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(setupRequest{Token: token, World: world, Approver: entry})
	return data
}

func TestSettingUpAWorld(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	addr := freeAddr(t)
	var mu sync.Mutex
	var link string
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{NATSURL: srv.ClientURL(), HTTPAddr: addr, OnSetupLink: func(l string) { mu.Lock(); link = l; mu.Unlock() }})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	}()
	base := "http://" + addr
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// The citadel offers setup through a link with a one-time token.
	var token string
	deadline := time.Now().Add(15 * time.Second)
	for token == "" {
		mu.Lock()
		if l := link; strings.HasPrefix(l, base+"/setup#") {
			token = strings.TrimPrefix(l, base+"/setup#")
		}
		mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the citadel never offered a setup link")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp, err := noRedirect.Get(base + "/"); err != nil || resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/setup" {
		t.Fatalf("GET / before setup = %v, %v; want a redirect to /setup", resp, err)
	}
	// A page asked for through another loopback name goes to the canonical one.
	localhost := strings.Replace(base, "127.0.0.1", "localhost", 1)
	if resp, err := noRedirect.Get(localhost + "/setup"); err != nil || resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != base+"/setup" {
		t.Fatalf("GET localhost /setup = %v, %v; want a redirect to %s/setup", resp, err, base)
	}

	post := func(path string, body []byte) (int, string) {
		t.Helper()
		resp, err := http.Post(base+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out bytes.Buffer
		_, _ = out.ReadFrom(resp.Body)
		return resp.StatusCode, out.String()
	}
	owner, ownerPub, approverPub := newKeys(t)
	if code, body := post("/api/world/setup", setupBody(t, "not the token", owner, ownerPub, approverPub)); code != http.StatusForbidden {
		t.Errorf("setup with a wrong token = %d %s, want 403", code, body)
	}
	// A record signed by one key that names another as its owner is refused.
	other, _, _ := newKeys(t)
	if code, body := post("/api/world/setup", setupBody(t, token, other, ownerPub, approverPub)); code != http.StatusBadRequest {
		t.Errorf("setup with a record not signed by its owner = %d %s, want 400", code, body)
	}
	if code, body := post("/api/world/setup", setupBody(t, token, owner, ownerPub, approverPub)); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, body)
	}
	if code, body := post("/api/world/setup", setupBody(t, token, owner, ownerPub, approverPub)); code != http.StatusConflict {
		t.Errorf("a second setup = %d %s, want 409", code, body)
	}
	mu.Lock()
	if link != "" {
		t.Errorf("the setup link is still offered after setup: %q", link)
	}
	mu.Unlock()
	if resp, err := noRedirect.Get(base + "/"); err != nil || resp.StatusCode == http.StatusFound {
		t.Errorf("GET / after setup = %v, %v; want the world's page", resp, err)
	}

	// A daemon asking for the world's record gets the signed record.
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	msg, err := nc.Request(v1.WorldRecordSubject, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var signed v1.Signed
	_ = json.Unmarshal(msg.Data, &signed)
	if rec, err := approval.OpenWorld(signed); err != nil || rec.Owner != ownerPub || rec.Name != "test world" {
		t.Errorf("world record = %+v, %v", rec, err)
	}

	// A recovery file enrolls another browser with an entry the owner key
	// signs; an entry signed by any other key is refused.
	_, _, second := newKeys(t)
	entry, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: second, Name: "Firefox", Created: approval.Now()})
	body, _ := json.Marshal(map[string]any{"approver": entry})
	if code, out := post("/api/world/approvers", body); code != http.StatusOK {
		t.Errorf("recovery = %d %s", code, out)
	}
	forged, _ := approval.Sign(other, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: second, Name: "Firefox", Created: approval.Now()})
	body, _ = json.Marshal(map[string]any{"approver": forged})
	if code, out := post("/api/world/approvers", body); code != http.StatusBadRequest {
		t.Errorf("an entry not signed by the owner = %d %s, want 400", code, out)
	}
}
