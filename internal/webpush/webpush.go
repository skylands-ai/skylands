// Package webpush sends Web Push messages: VAPID to identify the sender to
// the push service (RFC 8292), and the message encrypted for the browser
// that subscribed, so the push service can't read it (RFC 8291). It uses
// only the standard library.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Keys is a VAPID key pair: the ECDSA P-256 key an application server signs
// its pushes with. Browsers subscribe with its public key, and a push
// service accepts pushes to a subscription only when they are signed with
// the key it was made with.
type Keys struct {
	priv *ecdsa.PrivateKey
}

// NewKeys makes a VAPID key pair.
func NewKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv}, nil
}

// ParseKeys reads a key pair from its private key, as Private writes it.
func ParseKeys(private string) (*Keys, error) {
	d, err := base64.RawURLEncoding.DecodeString(private)
	if err != nil {
		return nil, errors.New("a VAPID private key is unpadded base64url")
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, fmt.Errorf("a VAPID private key: %w", err)
	}
	return &Keys{priv: priv}, nil
}

// Private is the private key, in unpadded base64url.
func (k *Keys) Private() string {
	d, _ := k.priv.Bytes()
	return base64.RawURLEncoding.EncodeToString(d)
}

// Public is the public key as an uncompressed point in unpadded base64url,
// which a browser subscribes with as its applicationServerKey.
func (k *Keys) Public() string {
	p, _ := k.priv.PublicKey.Bytes()
	return base64.RawURLEncoding.EncodeToString(p)
}

// Subscription is a browser's push subscription, as PushSubscription's
// toJSON gives it.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		// P256dh is the browser's ECDH public key, an uncompressed P-256
		// point, and Auth its 16-byte authentication secret, both in
		// unpadded base64url.
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Check says what's wrong with a subscription, or nil for nothing: an
// HTTPS endpoint, and keys of the right sizes.
func (s Subscription) Check() error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("a push subscription's endpoint is an https URL")
	}
	if _, err := browserKey(s.Keys.P256dh); err != nil {
		return err
	}
	if auth, err := decode(s.Keys.Auth); err != nil || len(auth) != 16 {
		return errors.New("a push subscription's auth secret is 16 bytes")
	}
	return nil
}

// Message is what to push.
type Message struct {
	// Payload is what the browser's service worker gets, after decryption.
	// With the encryption, it has to stay under MaxPayload.
	Payload []byte
	// Topic, if set, replaces an undelivered push with the same topic: up
	// to 32 characters of the base64url alphabet.
	Topic string
	// TTL is how long the push service keeps the push for a browser that
	// isn't connected. Zero means it delivers only to one that is.
	TTL time.Duration
	// Urgency is "very-low", "low", "normal", or "high". Empty means
	// normal.
	Urgency string
}

// MaxPayload is the longest payload a push carries: push services take up
// to 4096 bytes of encrypted body, and the encryption adds 103.
const MaxPayload = 3993

// recordSize is the record size named in the encrypted body's header. One
// record holds the whole payload.
const recordSize = 4096

// Sender sends pushes signed with one VAPID key.
type Sender struct {
	Keys *Keys
	// Subject is how a push service can reach whoever runs the sender: a
	// mailto: or https: URI. Some push services refuse pushes without one.
	Subject string
	// Client sends the pushes. Nil means http.DefaultClient.
	Client *http.Client
}

// ErrGone is the push service saying the subscription no longer exists,
// with 404 or 410. Delete it.
var ErrGone = errors.New("the push subscription is gone")

// Send encrypts m for sub and gives it to sub's push service.
func (s *Sender) Send(ctx context.Context, sub Subscription, m Message) error {
	if err := sub.Check(); err != nil {
		return err
	}
	if len(m.Payload) > MaxPayload {
		return fmt.Errorf("a push payload is at most %d bytes, not %d", MaxPayload, len(m.Payload))
	}
	body, err := Encrypt(sub, m.Payload)
	if err != nil {
		return err
	}
	auth, err := s.authorization(sub.Endpoint, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(m.TTL/time.Second)))
	if m.Topic != "" {
		req.Header.Set("Topic", m.Topic)
	}
	if m.Urgency != "" {
		req.Header.Set("Urgency", m.Urgency)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	why, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("the push service answered %s: %s", resp.Status, strings.TrimSpace(string(why)))
	}
	return nil
}

// authorization is the VAPID Authorization header for a push to endpoint:
// a JWT for the push service's origin, signed with the sender's key, and
// the key it verifies with.
func (s *Sender) authorization(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	claims := map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix()}
	if s.Subject != "" {
		claims["sub"] = s.Subject
	}
	head, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	body, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(signing))
	r, sv, err := ecdsa.Sign(rand.Reader, s.Keys.priv, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + s.Keys.Public(), nil
}

// Encrypt encrypts payload for sub as RFC 8291 says, in one aes128gcm
// record (RFC 8188) whose header carries the salt and the sender's
// one-time public key.
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	browser, err := browserKey(sub.Keys.P256dh)
	if err != nil {
		return nil, err
	}
	auth, err := decode(sub.Keys.Auth)
	if err != nil {
		return nil, errors.New("a push subscription's auth secret is unpadded base64url")
	}
	mine, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(browser, auth, mine, salt, payload)
}

// encrypt is Encrypt with the sender's one-time key and the salt given.
func encrypt(browser *ecdh.PublicKey, auth []byte, mine *ecdh.PrivateKey, salt, payload []byte) ([]byte, error) {
	shared, err := mine.ECDH(browser)
	if err != nil {
		return nil, err
	}
	public := mine.PublicKey().Bytes()
	cek, nonce, err := derive(shared, browser.Bytes(), public, auth, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, 86)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(public)))
	header = append(header, public...)
	// The last record ends with a 2 and no padding.
	plain := append(append(make([]byte, 0, len(payload)+1), payload...), 2)
	return gcm.Seal(header, nonce, plain, nil), nil
}

// derive gives a push's content encryption key and nonce, as RFC 8291
// says, from the ECDH secret the two sides share, the browser's and the
// sender's public keys, the browser's auth secret, and the salt.
func derive(shared, browser, sender, auth, salt []byte) (cek, nonce []byte, err error) {
	prk, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prk, "WebPush: info\x00"+string(browser)+string(sender), 32)
	if err != nil {
		return nil, nil, err
	}
	if prk, err = hkdf.Extract(sha256.New, ikm, salt); err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, err
	}
	if nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}

// Decrypt is the browser's side of Encrypt: it reads a push's body with
// the browser's private key and auth secret. Tests use it in place of a
// browser.
func Decrypt(priv *ecdh.PrivateKey, auth, body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("a push body is shorter than its header")
	}
	salt, n := body[:16], int(body[20])
	if len(body) < 21+n {
		return nil, errors.New("a push body is shorter than its header")
	}
	sender, err := ecdh.P256().NewPublicKey(body[21 : 21+n])
	if err != nil {
		return nil, errors.New("a push body's key id isn't a P-256 public key")
	}
	shared, err := priv.ECDH(sender)
	if err != nil {
		return nil, err
	}
	cek, nonce, err := derive(shared, priv.PublicKey().Bytes(), sender.Bytes(), auth, salt)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[21+n:], nil)
	if err != nil {
		return nil, errors.New("a push body doesn't decrypt")
	}
	// One record: the payload, a 2, and any padding of zeros.
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, errors.New("a push body's record has no last-record delimiter")
	}
	return plain[:len(plain)-1], nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func browserKey(p256dh string) (*ecdh.PublicKey, error) {
	raw, err := decode(p256dh)
	if err != nil {
		return nil, errors.New("a push subscription's p256dh key is unpadded base64url")
	}
	key, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, errors.New("a push subscription's p256dh key is an uncompressed P-256 point")
	}
	return key, nil
}

// decode reads base64url with or without padding, since browsers differ.
func decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
