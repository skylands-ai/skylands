package citadel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
)

// A sign-in lasts signInLife from the time the citadel names, and the
// browser has signInWindow to sign that time and send it back. A browser
// signs in again whenever it opens the world's page or reconnects, and
// whenever a request says its sign-in lapsed, so nobody sees one end.
const (
	signInLife   = 12 * time.Hour
	signInWindow = 2 * time.Minute
)

// signInChallenge is the body of GET /api/signin: what a browser signs,
// unchanged, to sign in here.
type signInChallenge struct {
	World   string `json:"world"`
	Citadel string `json:"citadel"`
	Issued  string `json:"issued"`
	Expires string `json:"expires"`
}

// getSignIn answers GET /api/signin with a sign-in for the browser to
// sign: this world, this citadel's address as the browser reached it, and
// the times, from this citadel's clock rather than the browser's. Until
// the world exists it answers 409, and the browser needs no sign-in.
func (c *citadel) getSignIn(w http.ResponseWriter, r *http.Request) {
	view, ok := c.signInWorld(w)
	if !ok {
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	writeJSON(w, http.StatusOK, signInChallenge{
		World:   view.record.Owner,
		Citadel: r.Host,
		Issued:  now.Format(time.RFC3339),
		Expires: now.Add(signInLife).Format(time.RFC3339),
	})
}

// postSignIn answers POST /api/signin: a sign-in an approver signed. It
// becomes the browser's cookie.
func (c *citadel) postSignIn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SignIn v1.Signed `json:"signin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a signed sign-in is required")
		return
	}
	view, ok := c.signInWorld(w)
	if !ok {
		return
	}
	in, err := approval.OpenSignIn(req.SignIn, view.record.Owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the sign-in: "+err.Error())
		return
	}
	issued, expires, err := signInTimes(in)
	now := time.Now()
	switch {
	case err != nil:
		writeError(w, http.StatusBadRequest, "the sign-in: "+err.Error())
		return
	case !strings.EqualFold(in.Citadel, r.Host):
		writeError(w, http.StatusBadRequest, "that sign-in is for another citadel")
		return
	case expires.Sub(issued) != signInLife || now.Sub(issued).Abs() > signInWindow:
		writeError(w, http.StatusBadRequest, "that sign-in isn't the one this citadel offered just now; sign in again")
		return
	case !view.registry.Valid(req.SignIn.Signer):
		writeError(w, http.StatusForbidden, "this browser doesn't approve for this world")
		return
	}
	data, _ := json.Marshal(req.SignIn)
	http.SetCookie(w, &http.Cookie{
		Name:     signInCookie(r.Host),
		Value:    base64.RawURLEncoding.EncodeToString(data),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   viaHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	a, _ := view.registry.Approver(req.SignIn.Signer)
	writeJSON(w, http.StatusOK, map[string]string{"key": a.Key, "name": a.Name, "expires": in.Expires})
}

// signInWorld is the world to sign in to, or false after writing why
// there's none: 503 while the citadel can't tell, and 409 while there's no
// world, so no approver to sign in as.
func (c *citadel) signInWorld(w http.ResponseWriter) (worldView, bool) {
	c.mu.Lock()
	view := c.world
	c.mu.Unlock()
	if view.signed == nil && !view.checked {
		writeError(w, http.StatusServiceUnavailable, "the citadel hasn't reached NATS yet, so it can't tell whether this world is set up")
		return view, false
	}
	return c.readyWorld(w)
}

// signedInAs is the approver key r signed in with. Until the world exists
// there's no approver to sign in as, so every request goes through, as
// decisions do, and the key is "". Once it exists, a request goes through
// only with a current sign-in from an approver. Otherwise status and why
// say what to answer.
func (c *citadel) signedInAs(r *http.Request) (key string, status int, why string) {
	c.mu.Lock()
	view := c.world
	c.mu.Unlock()
	switch {
	case view.signed == nil && !view.checked:
		return "", http.StatusServiceUnavailable, "the citadel hasn't reached NATS yet, so it can't tell whether this world is set up"
	case view.signed == nil:
		return "", 0, ""
	case view.registry == nil:
		return "", http.StatusServiceUnavailable, "the citadel hasn't read the approvers yet"
	}
	key, err := cookieSignIn(r, view.record.Owner, view.registry, time.Now())
	if err != nil {
		return "", http.StatusUnauthorized, "sign in with this browser's approver key: " + err.Error()
	}
	return key, 0, ""
}

// cookieSignIn checks the sign-in in r's cookie: signed by an approver
// that still approves, for this world and this citadel, and not expired.
func cookieSignIn(r *http.Request, owner string, reg *approval.Registry, now time.Time) (string, error) {
	ck, err := r.Cookie(signInCookie(r.Host))
	if err != nil {
		return "", errors.New("this browser hasn't signed in")
	}
	data, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil {
		return "", errors.New("its sign-in is damaged")
	}
	var s v1.Signed
	if json.Unmarshal(data, &s) != nil {
		return "", errors.New("its sign-in is damaged")
	}
	in, err := approval.OpenSignIn(s, owner)
	if err != nil {
		return "", err
	}
	issued, expires, err := signInTimes(in)
	switch {
	case err != nil:
		return "", err
	case !strings.EqualFold(in.Citadel, r.Host):
		return "", errors.New("its sign-in is for another citadel")
	case expires.Sub(issued) > signInLife || issued.After(now.Add(signInWindow)):
		return "", errors.New("its sign-in isn't one a citadel offered")
	case !now.Before(expires):
		return "", errors.New("its sign-in expired")
	case !reg.Valid(s.Signer):
		return "", errors.New("it no longer approves for this world")
	}
	return s.Signer, nil
}

func signInTimes(in v1.SignIn) (issued, expires time.Time, err error) {
	if issued, err = time.Parse(time.RFC3339, in.Issued); err != nil {
		return issued, expires, errors.New("the sign-in's issue time isn't RFC 3339")
	}
	if expires, err = time.Parse(time.RFC3339, in.Expires); err != nil {
		return issued, expires, errors.New("the sign-in's expiry isn't RFC 3339")
	}
	return issued, expires, nil
}

// signInCookie names the sign-in cookie for the citadel at host. A
// browser sends a host's cookies to every port on it, so the name carries
// the port, and two citadels on one machine keep their sign-ins apart.
func signInCookie(host string) string {
	if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
		return "skylands-signin-" + port
	}
	return "skylands-signin"
}

// requireSignIn lets a request through to h only when signedInAs does.
func (c *citadel) requireSignIn(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, status, why := c.signedInAs(r); status != 0 {
			writeError(w, status, why)
			return
		}
		h(w, r)
	})
}

// sameOrigin refuses a request that changes something when a page from
// another origin sent it. A browser sends its cookies with requests from
// pages on the same site, such as another port on 127.0.0.1, so the
// sign-in cookie alone doesn't show that this citadel's own page asked.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || !strings.EqualFold(u.Host, r.Host) {
					why := "a page from another origin can't do that here"
					// A proxy that sends its own Host, rather than the
					// browser's, makes every page look like another origin.
					if err == nil && strings.EqualFold(u.Host, r.Header.Get("X-Forwarded-Host")) {
						why = "the proxy in front of the citadel changes the Host header from " + u.Host + " to " + r.Host + "; have it pass the browser's Host through, as tailscale serve does"
					}
					writeError(w, http.StatusForbidden, why)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// viaHTTPS says whether the browser reached the citadel over HTTPS: the
// citadel's own TLS, or a proxy on this machine, such as tailscale serve,
// that ends HTTPS and says so with X-Forwarded-Proto. The header is
// trusted only from this machine.
func viaHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
