package citadel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/web"
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

// startCitadel runs a citadel on a fresh NATS server and returns its base
// URL and the token from the setup link it offers.
func startCitadel(t *testing.T) (base, token string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)
	addr := freeAddr(t)
	links := make(chan string, 4)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{NATSURL: srv.ClientURL(), HTTPAddr: addr, OnSetupLink: func(l string) { links <- l }})
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	})
	base = "http://" + addr
	select {
	case l := <-links:
		return base, strings.TrimPrefix(l, base+"/setup#")
	case <-time.After(15 * time.Second):
		t.Fatal("the citadel never offered a setup link")
	}
	return "", ""
}

func postJSON(t *testing.T, url string, body any) (int, string) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

func userKey(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

// signIn signs in to the citadel at base with kp, as the world's page does,
// and returns a client that sends the sign-in cookie, or fails the test.
func signIn(t *testing.T, base string, kp nkeys.KeyPair) *http.Client {
	t.Helper()
	client, code, out := trySignIn(t, base, kp, nil)
	if code != http.StatusOK {
		t.Fatalf("sign in = %d %s", code, out)
	}
	return client
}

// trySignIn asks for a sign-in, lets change alter it, signs it with kp, and
// sends it back. It returns a client with whatever cookie came back.
func trySignIn(t *testing.T, base string, kp nkeys.KeyPair, change func(*v1.SignIn)) (*http.Client, int, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(base + "/api/signin")
	if err != nil {
		t.Fatal(err)
	}
	var ch signInChallenge
	err = json.NewDecoder(resp.Body).Decode(&ch)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil {
		t.Fatalf("GET /api/signin = %d, %v", resp.StatusCode, err)
	}
	in := v1.SignIn{Type: "signin", World: ch.World, Citadel: ch.Citadel, Issued: ch.Issued, Expires: ch.Expires}
	if change != nil {
		change(&in)
	}
	signed, err := approval.Sign(kp, in)
	if err != nil {
		t.Fatal(err)
	}
	code, out := postWith(t, client, base+"/api/signin", map[string]any{"signin": signed}, nil)
	return client, code, out
}

// postWith posts body as JSON with client and any extra headers.
func postWith(t *testing.T, client *http.Client, url string, body any, header map[string]string) (int, string) {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

// dialWith opens the world's WebSocket with client's cookies.
func dialWith(ctx context.Context, base string, client *http.Client) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, strings.Replace(base, "http", "ws", 1)+"/ws", &websocket.DialOptions{HTTPClient: client})
}

// TestApproversThroughTheCitadel adds an approver with a voucher, refuses
// a second use of it, and revokes, taking along what was enrolled through
// the revoked approver.
func TestApproversThroughTheCitadel(t *testing.T) {
	base, token := startCitadel(t)
	owner, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	ownerPub, _ := owner.PublicKey()
	laptop, laptopPub := userKey(t)
	world, _ := approval.Sign(owner, v1.WorldRecord{Type: "world", Name: "w", Owner: ownerPub, Created: approval.Now()})
	first, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: laptopPub, Name: "laptop", Created: approval.Now()})
	if code, out := postJSON(t, base+"/api/world/setup", setupRequest{Token: token, World: world, Approver: first}); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, out)
	}
	// A desk computer is enrolled with the recovery file, apart from the
	// laptop, so it stays an approver when the laptop is revoked.
	desk, deskPub := userKey(t)
	deskEntry, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: deskPub, Name: "desk", Created: approval.Now()})
	if code, out := postJSON(t, base+"/api/world/approvers", map[string]any{"approver": deskEntry}); code != http.StatusOK {
		t.Fatalf("recovery = %d %s", code, out)
	}
	asLaptop, asDesk := signIn(t, base, laptop), signIn(t, base, desk)

	// The laptop makes a link; the phone uses it.
	claim, claimPub := userKey(t)
	voucher, _ := approval.Sign(laptop, v1.Voucher{Type: "voucher", World: ownerPub, Claim: claimPub, Created: approval.Now()})
	code, out := postWith(t, asLaptop, base+"/api/world/vouchers", map[string]any{"voucher": voucher}, nil)
	var vi struct {
		By, ByName, Expires string
		Valid               bool
	}
	_ = json.Unmarshal([]byte(out), &vi)
	if code != http.StatusOK || vi.By != laptopPub || vi.ByName != "laptop" || !vi.Valid {
		t.Fatalf("voucher = %d %s", code, out)
	}
	if exp, err := time.Parse(time.RFC3339, vi.Expires); err != nil || time.Until(exp) < 14*time.Minute || time.Until(exp) > 16*time.Minute {
		t.Errorf("the voucher expires at %q, want about 15 minutes from now", vi.Expires)
	}
	if resp, err := http.Get(base + "/api/world/vouchers/" + claimPub); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("GET the voucher = %v, %v", resp, err)
	}
	_, phonePub := userKey(t)
	phoneEntry, _ := approval.Sign(claim, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: phonePub, Name: "phone", Created: approval.Now(), Voucher: claimPub})
	if code, out := postJSON(t, base+"/api/world/approvers", map[string]any{"approver": phoneEntry}); code != http.StatusOK {
		t.Fatalf("using the link = %d %s", code, out)
	}
	_, againPub := userKey(t)
	again, _ := approval.Sign(claim, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: againPub, Name: "again", Created: approval.Now(), Voucher: claimPub})
	if code, out := postJSON(t, base+"/api/world/approvers", map[string]any{"approver": again}); code != http.StatusConflict {
		t.Errorf("using the link twice = %d %s, want 409", code, out)
	}
	// A key that isn't an approver can't make a link.
	stranger, _ := userKey(t)
	_, otherClaim := userKey(t)
	forged, _ := approval.Sign(stranger, v1.Voucher{Type: "voucher", World: ownerPub, Claim: otherClaim, Created: approval.Now()})
	if code, out := postWith(t, asLaptop, base+"/api/world/vouchers", map[string]any{"voucher": forged}, nil); code != http.StatusForbidden {
		t.Errorf("a voucher from a stranger = %d %s, want 403", code, out)
	}

	approvers := func() map[string]web.ApproverInfo {
		t.Helper()
		resp, err := http.Get(base + "/api/world/approvers")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct{ Approvers []web.ApproverInfo }
		_ = json.NewDecoder(resp.Body).Decode(&body)
		out := map[string]web.ApproverInfo{}
		for _, a := range body.Approvers {
			out[a.Key] = a
		}
		return out
	}
	got := approvers()
	if l, p := got[laptopPub], got[phonePub]; !l.Valid || l.By != "owner" || len(l.Through) != 1 || l.Through[0] != phonePub || !p.Valid || p.By != laptopPub || p.Voucher != claimPub {
		t.Fatalf("approvers = %+v", got)
	}

	// The owner key revokes the laptop, and the phone it enrolled goes too.
	revocation, _ := approval.Sign(owner, v1.Revocation{Type: "revocation", World: ownerPub, Key: laptopPub, Created: approval.Now()})
	if code, out := postWith(t, asDesk, base+"/api/world/revocations", map[string]any{"revocation": revocation}, nil); code != http.StatusOK {
		t.Fatalf("revoke = %d %s", code, out)
	}
	got = approvers()
	if l, p := got[laptopPub], got[phonePub]; l.Valid || l.RevokedBy != "owner" || l.Revoked == "" || p.Valid || p.Revoked != "" {
		t.Errorf("after revoking the laptop: %+v", got)
	}
	// The revoked laptop's sign-in no longer counts.
	if code, out := postWith(t, asLaptop, base+"/api/world/vouchers", map[string]any{"voucher": voucher}, nil); code != http.StatusUnauthorized {
		t.Errorf("the revoked laptop's sign-in = %d %s, want 401", code, out)
	}
	// Nor does what the revoked laptop signs, sent by an approver.
	byLaptop, _ := approval.Sign(laptop, v1.Revocation{Type: "revocation", World: ownerPub, Key: phonePub, Created: approval.Now()})
	if code, out := postWith(t, asDesk, base+"/api/world/revocations", map[string]any{"revocation": byLaptop}, nil); code != http.StatusForbidden {
		t.Errorf("a revoked approver revoking = %d %s, want 403", code, out)
	}
	_, lateClaim := userKey(t)
	late, _ := approval.Sign(laptop, v1.Voucher{Type: "voucher", World: ownerPub, Claim: lateClaim, Created: approval.Now()})
	if code, out := postWith(t, asDesk, base+"/api/world/vouchers", map[string]any{"voucher": late}, nil); code != http.StatusForbidden {
		t.Errorf("a revoked approver making a link = %d %s, want 403", code, out)
	}

	// A browser connecting now gets the registry in its snapshot.
	c, _, err := dialWith(context.Background(), base, asDesk)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	_, data, err := c.Read(context.Background())
	var snap web.Message
	if err != nil || json.Unmarshal(data, &snap) != nil || snap.Type != "snapshot" || len(snap.Approvers) != 3 {
		t.Errorf("snapshot = %s, %v; want three approvers", data, err)
	}
}

func TestCheckPublicURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"":                                true,
		"https://box.example.ts.net":      true,
		"https://box.example.ts.net/":     true,
		"http://192.168.1.20:8080":        true,
		"http://[fd7a:115c::1]:8080":      true,
		"box.example.ts.net":              false,
		"box:8080":                        false,
		"ftp://box":                       false,
		"https://":                        false,
		"https://box/skylands":            false,
		"https://box/?x=1":                false,
		"https://box/#setup":              false,
		"https://someone@box.example.net": false,
	} {
		if err := CheckPublicURL(raw); (err == nil) != ok {
			t.Errorf("CheckPublicURL(%q) = %v, want ok %v", raw, err, ok)
		}
	}
	c := &citadel{cfg: Config{PublicURL: "https://box.example.ts.net/", HTTPAddr: "127.0.0.1:8080"}}
	if got := c.publicURL(); got != "https://box.example.ts.net" {
		t.Errorf("publicURL = %q", got)
	}
	// The world's description names the address only when one was given,
	// for an invite link to open at.
	if got := c.worldInfo().URL; got != "https://box.example.ts.net" {
		t.Errorf("worldInfo().URL = %q", got)
	}
	if got := (&citadel{cfg: Config{HTTPAddr: "127.0.0.1:8080"}}).worldInfo().URL; got != "" {
		t.Errorf("worldInfo().URL without --url = %q", got)
	}
}
