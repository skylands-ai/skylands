package citadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/web"
)

// readUntilClosed reads c until it closes, and returns the close status.
func readUntilClosed(t *testing.T, ctx context.Context, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("the socket never closed")
			}
			return websocket.CloseStatus(err)
		}
	}
}

func getStatus(t *testing.T, client *http.Client, url string) int {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestSignIn covers signing in to the citadel with an approver key: open
// before the world exists, required after, and ended by revocation.
func TestSignIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, token := startCitadel(t)
	host := strings.TrimPrefix(base, "http://")
	plain := http.DefaultClient

	// Before the world exists there's no approver to sign in as, and
	// nothing asks for one.
	if code := getStatus(t, plain, base+"/api/signin"); code != http.StatusConflict {
		t.Errorf("GET /api/signin before setup = %d, want 409", code)
	}
	if code, out := postJSON(t, base+"/api/sessions", map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("starting a session before setup = %d %s, want 400 from the handler itself", code, out)
	}
	early, _, err := websocket.Dial(ctx, "ws://"+host+"/ws", nil)
	if err != nil {
		t.Fatalf("the socket before setup: %v", err)
	}
	defer func() { _ = early.CloseNow() }()

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

	// The socket opened before the world existed signed in as nobody, so
	// it closes, asking the browser to sign in.
	if status := readUntilClosed(t, ctx, early); status != web.StatusSignIn {
		t.Errorf("the early socket closed with %v, want %v", status, web.StatusSignIn)
	}

	// Without signing in, nothing but what setup needs answers.
	if code, out := postJSON(t, base+"/api/sessions", map[string]any{}); code != http.StatusUnauthorized {
		t.Errorf("starting a session without signing in = %d %s, want 401", code, out)
	}
	if code := getStatus(t, plain, base+"/api/sessions/h/x/transcript"); code != http.StatusUnauthorized {
		t.Errorf("reading a transcript without signing in = %d, want 401", code)
	}
	if _, resp, err := websocket.Dial(ctx, "ws://"+host+"/ws", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the socket without signing in = %v, %v; want 401", resp, err)
	}
	for _, path := range []string{"/api/world", "/api/world/approvers"} {
		if code := getStatus(t, plain, base+path); code != http.StatusOK {
			t.Errorf("GET %s without signing in = %d, want 200", path, code)
		}
	}

	// The citadel names what to sign, from its own clock.
	resp, err := plain.Get(base + "/api/signin")
	if err != nil {
		t.Fatal(err)
	}
	var ch signInChallenge
	_ = json.NewDecoder(resp.Body).Decode(&ch)
	_ = resp.Body.Close()
	issued, _ := time.Parse(time.RFC3339, ch.Issued)
	expires, _ := time.Parse(time.RFC3339, ch.Expires)
	if ch.World != ownerPub || ch.Citadel != host || expires.Sub(issued) != signInLife || time.Since(issued).Abs() > time.Minute {
		t.Errorf("challenge = %+v", ch)
	}

	// Signing it sets an HTTP-only, same-site cookie named for the port.
	signed, _ := approval.Sign(laptop, v1.SignIn{Type: "signin", World: ch.World, Citadel: ch.Citadel, Issued: ch.Issued, Expires: ch.Expires})
	data, _ := json.Marshal(map[string]any{"signin": signed})
	resp, err = plain.Post(base+"/api/signin", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == signInCookie(host) {
			cookie = c
		}
	}
	if resp.StatusCode != http.StatusOK || cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("sign in = %d, cookie %+v", resp.StatusCode, cookie)
	}

	asLaptop := signIn(t, base, laptop)
	if code, out := postWith(t, asLaptop, base+"/api/sessions", map[string]any{}, nil); code != http.StatusBadRequest {
		t.Errorf("starting a session signed in = %d %s, want 400 from the handler itself", code, out)
	}
	c, _, err := dialWith(ctx, base, asLaptop)
	if err != nil {
		t.Fatalf("the socket signed in: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if _, data, err := c.Read(ctx); err != nil || !strings.Contains(string(data), `"type":"snapshot"`) {
		t.Fatalf("first message signed in = %s, %v", data, err)
	}

	// Sign-ins the citadel refuses.
	stranger, _ := userKey(t)
	if _, code, out := trySignIn(t, base, stranger, nil); code != http.StatusForbidden {
		t.Errorf("a key that isn't an approver signing in = %d %s, want 403", code, out)
	}
	refused := map[string]func(*v1.SignIn){
		"for another citadel": func(in *v1.SignIn) { in.Citadel = "elsewhere:8080" },
		"for another world":   func(in *v1.SignIn) { in.World = laptopPub },
		"lasting a century":   func(in *v1.SignIn) { in.Expires = time.Now().AddDate(100, 0, 0).UTC().Format(time.RFC3339) },
		"offered long ago": func(in *v1.SignIn) {
			at := time.Now().Add(-10 * time.Minute).UTC()
			in.Issued, in.Expires = at.Format(time.RFC3339), at.Add(signInLife).Format(time.RFC3339)
		},
		"not a sign-in": func(in *v1.SignIn) { in.Type = "voucher" },
	}
	for what, change := range refused {
		if _, code, out := trySignIn(t, base, laptop, change); code != http.StatusBadRequest {
			t.Errorf("a sign-in %s = %d %s, want 400", what, code, out)
		}
	}

	// A cookie that expired, or that a browser made up, doesn't count.
	cookieFor := func(in v1.SignIn) map[string]string {
		s, _ := approval.Sign(laptop, in)
		data, _ := json.Marshal(s)
		return map[string]string{"Cookie": signInCookie(host) + "=" + base64.RawURLEncoding.EncodeToString(data)}
	}
	past := time.Now().Add(-13 * time.Hour).UTC()
	stale := cookieFor(v1.SignIn{Type: "signin", World: ownerPub, Citadel: host, Issued: past.Format(time.RFC3339), Expires: past.Add(signInLife).Format(time.RFC3339)})
	if code, out := postWith(t, plain, base+"/api/sessions", map[string]any{}, stale); code != http.StatusUnauthorized || !strings.Contains(out, "expired") {
		t.Errorf("an expired sign-in = %d %s, want 401", code, out)
	}
	now := time.Now().UTC()
	long := cookieFor(v1.SignIn{Type: "signin", World: ownerPub, Citadel: host, Issued: now.Format(time.RFC3339), Expires: now.AddDate(1, 0, 0).Format(time.RFC3339)})
	if code, out := postWith(t, plain, base+"/api/sessions", map[string]any{}, long); code != http.StatusUnauthorized {
		t.Errorf("a sign-in lasting a year = %d %s, want 401", code, out)
	}

	// A page from another origin can't use the cookie to change anything.
	if code, out := postWith(t, asLaptop, base+"/api/sessions", map[string]any{}, map[string]string{"Origin": "http://127.0.0.1:1"}); code != http.StatusForbidden {
		t.Errorf("a request from another origin = %d %s, want 403", code, out)
	}
	if code, out := postWith(t, asLaptop, base+"/api/sessions", map[string]any{}, map[string]string{"Origin": base}); code != http.StatusBadRequest {
		t.Errorf("a request from the citadel's own page = %d %s, want 400 from the handler itself", code, out)
	}

	// Revoking the laptop closes its socket and ends its sign-in.
	revocation, _ := approval.Sign(owner, v1.Revocation{Type: "revocation", World: ownerPub, Key: laptopPub, Created: approval.Now()})
	if code, out := postWith(t, asLaptop, base+"/api/world/revocations", map[string]any{"revocation": revocation}, nil); code != http.StatusOK {
		t.Fatalf("revoke = %d %s", code, out)
	}
	if status := readUntilClosed(t, ctx, c); status != web.StatusSignIn {
		t.Errorf("the revoked laptop's socket closed with %v, want %v", status, web.StatusSignIn)
	}
	if code, out := postWith(t, asLaptop, base+"/api/sessions", map[string]any{}, nil); code != http.StatusUnauthorized {
		t.Errorf("the revoked laptop's sign-in = %d %s, want 401", code, out)
	}
	if _, code, out := trySignIn(t, base, laptop, nil); code != http.StatusForbidden {
		t.Errorf("the revoked laptop signing in again = %d %s, want 403", code, out)
	}
}
