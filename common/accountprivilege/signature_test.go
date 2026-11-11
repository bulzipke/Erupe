package accountprivilege

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// signedMessage mirrors the client: SHA-256(response[:72] || tid || cid).
func signedMessage(resp []byte, r Request) []byte {
	msg := make([]byte, ResponseSize+8)
	copy(msg, resp[:ResponseSize])
	binary.BigEndian.PutUint32(msg[ResponseSize:], r.TokenID)
	binary.BigEndian.PutUint32(msg[ResponseSize+4:], r.CharacterID)
	return msg
}

func TestSignerAppendix(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	signer, err := NewSigner(pemKey, 3)
	if err != nil || signer == nil {
		t.Fatal("NewSigner", err)
	}

	r := Request{TokenID: 123, CharacterID: 456}
	for i := range r.Nonce {
		r.Nonce[i] = byte(i + 16)
	}
	resp := Response(r, time.Unix(1700000000, 0), time.Unix(1702592000, 0), time.Time{}, "0123456789abcdef")

	app, err := signer.Appendix(resp, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(app) != SignatureSize || app[0] != SignatureScheme || app[1] != 3 {
		t.Fatalf("appendix layout: len=%d scheme=%d id=%d", len(app), app[0], app[1])
	}
	if len(resp)+len(app) != SignedResponseSize {
		t.Fatalf("signed size %d", len(resp)+len(app))
	}

	digest := sha256.Sum256(signedMessage(resp, r))
	rr := new(big.Int).SetBytes(app[2:34])
	ss := new(big.Int).SetBytes(app[34:66])
	if !ecdsa.Verify(&key.PublicKey, digest[:], rr, ss) {
		t.Fatal("signature does not verify against the public key")
	}

	// The signature binds the account: a different tid must not verify.
	other := signedMessage(resp, Request{TokenID: r.TokenID ^ 1, CharacterID: r.CharacterID})
	d2 := sha256.Sum256(other)
	if ecdsa.Verify(&key.PublicKey, d2[:], rr, ss) {
		t.Fatal("verified a tampered account")
	}
}

func TestSignerDisabled(t *testing.T) {
	s, err := NewSigner("", 1)
	if err != nil || s != nil {
		t.Fatalf("empty key should disable signing: signer=%v err=%v", s, err)
	}
	app, err := s.Appendix(make([]byte, ResponseSize), Request{})
	if err != nil || app != nil {
		t.Fatalf("nil signer should produce no appendix: app=%v err=%v", app, err)
	}
}

func TestNewSignerRejectsNonP256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if s, err := NewSigner(pemKey, 1); err == nil || s != nil {
		t.Fatal("expected P-384 key to be rejected")
	}
}
