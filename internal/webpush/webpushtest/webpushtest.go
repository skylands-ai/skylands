// Package webpushtest is a fake push service for tests, which can't reach
// the browser vendors' real ones. It makes subscriptions the way a browser
// does, checks each push's VAPID token against the key the subscription
// was made with, as a real push service does, and decrypts what it was
// sent.
package webpushtest

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nuid"

	"github.com/skylands-ai/skylands/internal/webpush"
)

// Push is one push the service accepted, decrypted.
type Push struct {
	// Endpoint is the subscription it was sent to.
	Endpoint string
	Payload  []byte
	Topic    string
	TTL      time.Duration
	Urgency  string
	// Subject is the sub claim of its VAPID token.
	Subject string
}

// Service is a fake push service on an HTTPS test server.
type Service struct {
	srv *httptest.Server

	mu       sync.Mutex
	subs     map[string]*browser
	received []Push
	refused  []error
	arrived  chan struct{}
}

// browser is what the service keeps of one subscription: the browser's
// keys, which only it and the browser hold, the VAPID key it was made
// with, and the status to answer while it's gone.
type browser struct {
	priv   *ecdh.PrivateKey
	auth   []byte
	vapid  string
	status int
}

// New starts a push service, stopped when the test ends.
func New(t interface{ Cleanup(func()) }) *Service {
	s := &Service{subs: map[string]*browser{}, arrived: make(chan struct{}, 64)}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// Client reaches the service: it trusts the test server's certificate.
func (s *Service) Client() *http.Client { return s.srv.Client() }

// Subscribe makes a subscription for the VAPID public key vapid, as a
// browser's PushManager.subscribe does.
func (s *Service) Subscribe(vapid string) webpush.Subscription {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	endpoint := s.srv.URL + "/push/" + nuid.Next()
	s.mu.Lock()
	s.subs[endpoint] = &browser{priv: priv, auth: auth, vapid: vapid}
	s.mu.Unlock()
	var sub webpush.Subscription
	sub.Endpoint = endpoint
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	return sub
}

// Gone makes the service answer pushes to sub with 410, as it does once
// the browser unsubscribes.
func (s *Service) Gone(sub webpush.Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.subs[sub.Endpoint]; b != nil {
		b.status = http.StatusGone
	}
}

// Received is every push accepted so far, in order.
func (s *Service) Received() []Push {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Push(nil), s.received...)
}

// Refused is why each push the service refused was refused.
func (s *Service) Refused() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.refused...)
}

// Wait waits up to d for the service to have accepted n pushes, and
// returns what it has.
func (s *Service) Wait(n int, d time.Duration) []Push {
	deadline := time.After(d)
	for {
		if got := s.Received(); len(got) >= n {
			return got
		}
		select {
		case <-s.arrived:
		case <-deadline:
			return s.Received()
		}
	}
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	endpoint := s.srv.URL + r.URL.Path
	s.mu.Lock()
	b := s.subs[endpoint]
	s.mu.Unlock()
	push, status, err := s.accept(r, endpoint, b)
	s.mu.Lock()
	if err != nil {
		s.refused = append(s.refused, err)
	} else {
		s.received = append(s.received, push)
	}
	s.mu.Unlock()
	select {
	case s.arrived <- struct{}{}:
	default:
	}
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// accept checks one push as a push service would, and decrypts it.
func (s *Service) accept(r *http.Request, endpoint string, b *browser) (Push, int, error) {
	switch {
	case r.Method != http.MethodPost:
		return Push{}, http.StatusMethodNotAllowed, fmt.Errorf("%s, not POST", r.Method)
	case b == nil:
		return Push{}, http.StatusNotFound, errors.New("no such subscription")
	case b.status != 0:
		return Push{}, b.status, errors.New("the subscription is gone")
	case r.Header.Get("Content-Encoding") != "aes128gcm":
		return Push{}, http.StatusBadRequest, errors.New("Content-Encoding isn't aes128gcm")
	}
	ttl, err := strconv.Atoi(r.Header.Get("TTL"))
	if err != nil || ttl < 0 {
		return Push{}, http.StatusBadRequest, errors.New("no TTL")
	}
	sub, err := s.checkVAPID(r.Header.Get("Authorization"), b.vapid)
	if err != nil {
		return Push{}, http.StatusForbidden, err
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return Push{}, http.StatusRequestEntityTooLarge, errors.New("the body is over 4096 bytes")
	}
	payload, err := webpush.Decrypt(b.priv, b.auth, body)
	if err != nil {
		return Push{}, http.StatusBadRequest, err
	}
	return Push{Endpoint: endpoint, Payload: payload, Topic: r.Header.Get("Topic"), TTL: time.Duration(ttl) * time.Second, Urgency: r.Header.Get("Urgency"), Subject: sub}, 0, nil
}

// checkVAPID checks an Authorization header: a JWT for this service's
// origin, not expired and not too long-lived, signed with key, the
// VAPID key the subscription was made with. It returns the token's sub.
func (s *Service) checkVAPID(header, key string) (string, error) {
	t, k, ok := parseVAPID(header)
	if !ok {
		return "", errors.New("the Authorization header isn't a vapid token and key")
	}
	if k != key {
		return "", errors.New("the VAPID key isn't the one the subscription was made with")
	}
	raw, err := base64.RawURLEncoding.DecodeString(k)
	if err != nil {
		return "", errors.New("the VAPID key isn't base64url")
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		return "", errors.New("the VAPID key isn't a P-256 point")
	}
	parts := strings.Split(t, ".")
	if len(parts) != 3 {
		return "", errors.New("the VAPID token isn't a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return "", errors.New("the VAPID token's signature isn't 64 bytes of base64url")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return "", errors.New("the VAPID token's signature doesn't verify")
	}
	var head struct {
		Alg string `json:"alg"`
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if !decodePart(parts[0], &head) || head.Alg != "ES256" {
		return "", errors.New("the VAPID token isn't ES256")
	}
	if !decodePart(parts[1], &claims) {
		return "", errors.New("the VAPID token's claims aren't JSON")
	}
	exp := time.Unix(claims.Exp, 0)
	switch {
	case claims.Aud != s.srv.URL:
		return "", fmt.Errorf("the VAPID token is for %q, not %q", claims.Aud, s.srv.URL)
	case !exp.After(time.Now()):
		return "", errors.New("the VAPID token expired")
	case exp.After(time.Now().Add(24 * time.Hour)):
		return "", errors.New("the VAPID token lasts over 24 hours")
	case !strings.HasPrefix(claims.Sub, "mailto:") && !strings.HasPrefix(claims.Sub, "https://"):
		return "", fmt.Errorf("the VAPID token's sub %q isn't a mailto: or https: URI", claims.Sub)
	}
	return claims.Sub, nil
}

func parseVAPID(header string) (t, k string, ok bool) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return "", "", false
	}
	for part := range strings.SplitSeq(rest, ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch name {
		case "t":
			t = value
		case "k":
			k = value
		}
	}
	return t, k, t != "" && k != ""
}

func decodePart(part string, v any) bool {
	data, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(data, v) == nil
}
