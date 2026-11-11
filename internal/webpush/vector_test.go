package webpush

import (
	"crypto/ecdh"
	"encoding/base64"
	"testing"
)

// RFC 8291's worked example, in section 5 and appendix A: its keys, salt,
// and auth secret give exactly its body.
func TestEncryptMatchesRFC8291(t *testing.T) {
	b64 := func(s string) []byte {
		t.Helper()
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	sender, err := ecdh.P256().NewPrivateKey(b64("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	ua, err := ecdh.P256().NewPrivateKey(b64("q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if err != nil {
		t.Fatal(err)
	}
	if got := base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()); got != "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4" {
		t.Fatalf("the user agent's public key = %s", got)
	}
	auth := b64("BTBZMqHH6r4Tts7J_aSIgg")
	salt := b64("DGv6ra1nlYgDCS1FRnbzlw")
	plain := "When I grow up, I want to be a watermelon"

	shared, _ := sender.ECDH(ua.PublicKey())
	cek, nonce, err := derive(shared, ua.PublicKey().Bytes(), sender.PublicKey().Bytes(), auth, salt)
	if err != nil {
		t.Fatal(err)
	}
	if got := base64.RawURLEncoding.EncodeToString(cek); got != "oIhVW04MRdy2XN9CiKLxTg" {
		t.Errorf("CEK = %s, want oIhVW04MRdy2XN9CiKLxTg", got)
	}
	if got := base64.RawURLEncoding.EncodeToString(nonce); got != "4h_95klXJ5E_qnoN" {
		t.Errorf("NONCE = %s, want 4h_95klXJ5E_qnoN", got)
	}

	body, err := encrypt(ua.PublicKey(), auth, sender, salt, []byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	const want = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := base64.RawURLEncoding.EncodeToString(body); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
	back, err := Decrypt(ua, auth, body)
	if err != nil || string(back) != plain {
		t.Errorf("Decrypt = %q, %v, want the plaintext", back, err)
	}
}
