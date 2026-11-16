package approval

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// Sealed is a secret encrypted with a passphrase, in the form the web
// client's recovery file uses: PBKDF2-SHA256 over the passphrase and Salt
// gives an AES-256-GCM key, which encrypts the secret under IV. Salt, IV,
// and Data are unpadded base64url.
type Sealed struct {
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	IV         string `json:"iv"`
	Cipher     string `json:"cipher"`
	Data       string `json:"data"`
}

// sealIterations is how many PBKDF2 rounds Seal uses, the same as the web
// client's recovery file. Tests lower it.
var sealIterations = 600_000

// The rounds Open accepts: few enough rounds would make a passphrase easy
// to guess, and a file naming very many would hang whoever opens it.
const (
	minIterations = 100_000
	maxIterations = 10_000_000
)

// ErrPassphrase means a sealed secret didn't open with the passphrase
// given: it's the wrong one, or the file was changed.
var ErrPassphrase = errors.New("that passphrase doesn't open it")

// Seal encrypts secret with passphrase.
func Seal(secret []byte, passphrase string) (Sealed, error) {
	if passphrase == "" {
		return Sealed{}, errors.New("a passphrase is required")
	}
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return Sealed{}, err
	}
	if _, err := rand.Read(iv); err != nil {
		return Sealed{}, err
	}
	gcm, err := sealCipher(passphrase, salt, sealIterations)
	if err != nil {
		return Sealed{}, err
	}
	enc := base64.RawURLEncoding
	return Sealed{
		KDF: "PBKDF2-SHA256", Iterations: sealIterations, Salt: enc.EncodeToString(salt), IV: enc.EncodeToString(iv),
		Cipher: "AES-GCM", Data: enc.EncodeToString(gcm.Seal(nil, iv, secret, nil)),
	}, nil
}

// Open decrypts the secret with passphrase. A wrong passphrase gives
// ErrPassphrase.
func (s Sealed) Open(passphrase string) ([]byte, error) {
	if s.KDF != "PBKDF2-SHA256" || s.Cipher != "AES-GCM" {
		return nil, fmt.Errorf("it's sealed with %s and %s, not PBKDF2-SHA256 and AES-GCM", s.KDF, s.Cipher)
	}
	if s.Iterations < minIterations || s.Iterations > maxIterations {
		return nil, fmt.Errorf("it names %d PBKDF2 rounds, outside %d to %d", s.Iterations, minIterations, maxIterations)
	}
	enc := base64.RawURLEncoding
	salt, err := enc.DecodeString(s.Salt)
	if err != nil {
		return nil, fmt.Errorf("its salt: %w", err)
	}
	iv, err := enc.DecodeString(s.IV)
	if err != nil {
		return nil, fmt.Errorf("its IV: %w", err)
	}
	data, err := enc.DecodeString(s.Data)
	if err != nil {
		return nil, fmt.Errorf("its data: %w", err)
	}
	gcm, err := sealCipher(passphrase, salt, s.Iterations)
	if err != nil {
		return nil, err
	}
	if len(iv) != gcm.NonceSize() {
		return nil, fmt.Errorf("its IV is %d bytes, not %d", len(iv), gcm.NonceSize())
	}
	secret, err := gcm.Open(nil, iv, data, nil)
	if err != nil {
		return nil, ErrPassphrase
	}
	return secret, nil
}

func sealCipher(passphrase string, salt []byte, iterations int) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
