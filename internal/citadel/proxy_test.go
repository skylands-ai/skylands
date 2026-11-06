package citadel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
)

// startProxy puts a reverse proxy in front of the citadel at target, as
// tailscale serve does: it passes the browser's Host through, and adds
// X-Forwarded-Host and X-Forwarded-Proto: https. With rewriteHost it sends
// the citadel's own address as Host instead, as a proxy left at its
// defaults may. It listens over plain HTTP, standing in for the HTTPS a
// proxy ends.
func startProxy(t *testing.T, target string, rewriteHost bool) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(u)
		if !rewriteHost {
			r.Out.Host = r.In.Host
		}
		r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
		r.Out.Header.Set("X-Forwarded-Proto", "https")
	}})
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestBehindTailscaleServe signs in, acts, and opens the socket through a
// proxy that keeps the browser's Host, at the tailnet address a phone
// opens, and says plainly what's wrong behind one that rewrites Host.
func TestBehindTailscaleServe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, token := startCitadel(t)
	owner, _ := nkeys.CreateOperator()
	ownerPub, _ := owner.PublicKey()
	laptop, laptopPub := userKey(t)
	world, _ := approval.Sign(owner, v1.WorldRecord{Type: "world", Name: "w", Owner: ownerPub, Created: approval.Now()})
	first, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: laptopPub, Name: "laptop", Created: approval.Now()})
	if code, out := postJSON(t, base+"/api/world/setup", setupRequest{Token: token, World: world, Approver: first}); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, out)
	}

	const tailnet = "box.example.ts.net"
	send := func(proxy, method, path, body string, cookie *http.Cookie) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, proxy+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = tailnet
		if method != http.MethodGet {
			req.Header.Set("Origin", "https://"+tailnet)
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(data)
	}

	// The sign-in names the tailnet address, which the proxy passed on.
	proxy := startProxy(t, base, false)
	resp, body := send(proxy, http.MethodGet, "/api/signin", "", nil)
	var ch signInChallenge
	if err := json.Unmarshal([]byte(body), &ch); err != nil || resp.StatusCode != http.StatusOK || ch.Citadel != tailnet {
		t.Fatalf("the challenge through the proxy = %d %s", resp.StatusCode, body)
	}
	signed, _ := approval.Sign(laptop, v1.SignIn{Type: "signin", World: ch.World, Citadel: ch.Citadel, Issued: ch.Issued, Expires: ch.Expires})
	data, _ := json.Marshal(map[string]any{"signin": signed})
	resp, body = send(proxy, http.MethodPost, "/api/signin", string(data), nil)
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "skylands-signin" {
			cookie = c
		}
	}
	// The browser came over HTTPS, so the cookie is only sent that way.
	if resp.StatusCode != http.StatusOK || cookie == nil || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("sign in through the proxy = %d %s, cookie %+v", resp.StatusCode, body, cookie)
	}

	// Signed in, a request that changes something gets past the origin
	// check and the sign-in to the handler, which wants a real order.
	if resp, body := send(proxy, http.MethodPost, "/api/sessions", "{}", cookie); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an order through the proxy = %d %s, want the handler's 400", resp.StatusCode, body)
	}
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(proxy, "http")+"/ws", &websocket.DialOptions{
		Host:       tailnet,
		HTTPHeader: http.Header{"Origin": {"https://" + tailnet}, "Cookie": {cookie.Name + "=" + cookie.Value}},
	})
	if err != nil {
		t.Fatalf("the socket through the proxy: %v", err)
	}
	if _, _, err := ws.Read(ctx); err != nil {
		t.Errorf("reading the socket's first message: %v", err)
	}
	_ = ws.CloseNow()

	// A proxy that rewrites Host makes every page look like another origin,
	// and the refusal says so.
	rewriting := startProxy(t, base, true)
	resp, body = send(rewriting, http.MethodPost, "/api/signin", string(data), nil)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "changes the Host header from "+tailnet) {
		t.Errorf("behind a proxy that rewrites Host = %d %s", resp.StatusCode, body)
	}
}

func TestViaHTTPS(t *testing.T) {
	for _, tc := range []struct {
		remote, proto string
		tls, want     bool
	}{
		{"127.0.0.1:5000", "https", false, true},
		{"[::1]:5000", "https", false, true},
		{"192.168.1.20:5000", "https", false, false},
		{"127.0.0.1:5000", "", false, false},
		{"192.168.1.20:5000", "", true, true},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/signin", nil)
		r.RemoteAddr = tc.remote
		if tc.proto != "" {
			r.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := viaHTTPS(r); got != tc.want {
			t.Errorf("viaHTTPS from %s, proto %q, TLS %v = %v, want %v", tc.remote, tc.proto, tc.tls, got, tc.want)
		}
	}
}
