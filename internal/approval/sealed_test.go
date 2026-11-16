package approval

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSealedOpensWithItsPassphraseOnly(t *testing.T) {
	secret := []byte("SUAEXAMPLESEED")
	s, err := Seal(secret, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if s.KDF != "PBKDF2-SHA256" || s.Cipher != "AES-GCM" || s.Iterations != sealIterations || strings.Contains(s.Data, "SUAEXAMPLE") {
		t.Fatalf("Seal = %+v", s)
	}
	got, err := s.Open("correct horse")
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open("wrong horse"); !errors.Is(err, ErrPassphrase) {
		t.Errorf("Open with the wrong passphrase: err = %v", err)
	}

	tampered := s
	tampered.Data = "A" + s.Data[1:]
	if tampered.Data == s.Data {
		tampered.Data = "B" + s.Data[1:]
	}
	if _, err := tampered.Open("correct horse"); err == nil {
		t.Error("Open accepted changed data")
	}
	few := s
	few.Iterations = 1000
	if _, err := few.Open("correct horse"); err == nil {
		t.Error("Open accepted a file naming too few rounds")
	}
	if _, err := Seal(secret, ""); err == nil {
		t.Error("Seal took an empty passphrase")
	}
}
