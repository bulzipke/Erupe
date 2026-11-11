// Package accountprivilege carries eligibility over the existing entrance port.
// Session HMACs authenticate both directions without transmitting the token.
package accountprivilege

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"os"
	"time"
)

const RequestMagic = "ERUPRV01"
const ResponseMagic = "ERUPRS01"
const RequestSize = 64
const ResponseSize = 72

type Request struct {
	TokenID, CharacterID uint32
	Nonce                [16]byte
}

func Timestamp(t time.Time) uint32 {
	// Native comparisons are signed 32-bit. NULL/pre-epoch/overflow must not wrap.
	if t.IsZero() || t.Unix() <= 0 || t.Unix() > 0x7fffffff {
		return 0
	}
	return uint32(t.Unix())
}

func sign(data []byte, token string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	_, _ = m.Write(data)
	return m.Sum(nil)
}

func ParseRequest(b []byte) (Request, error) {
	var r Request
	if len(b) != RequestSize || string(b[:8]) != RequestMagic {
		return r, errors.New("invalid privilege frame")
	}
	r.TokenID = binary.BigEndian.Uint32(b[8:12])
	r.CharacterID = binary.BigEndian.Uint32(b[12:16])
	copy(r.Nonce[:], b[16:32])
	return r, nil
}

func Authenticate(b []byte, token string) bool {
	return len(b) == RequestSize && len(token) == 16 && hmac.Equal(b[32:], sign(b[:32], token))
}

func Response(r Request, now, newcomer, returning time.Time, token string) []byte {
	b := make([]byte, ResponseSize)
	copy(b, ResponseMagic)
	binary.BigEndian.PutUint32(b[8:12], Timestamp(now))
	binary.BigEndian.PutUint32(b[12:16], Timestamp(newcomer))
	binary.BigEndian.PutUint32(b[16:20], Timestamp(returning))
	// Bytes 20..23 are reserved zeroes in version 1.
	copy(b[24:40], r.Nonce[:])
	copy(b[40:], sign(b[:40], token))
	return b
}

// --- Server-identity signature (ECDSA P-256 / SHA-256) ----------------------
// Only this server holds the private key, so only this server can append a
// signature that a client's baked-in public key accepts. A different server
// cannot, which is how the client tells the operator's server apart. The
// signature binds the per-request nonce (inside the frame) and the account, so
// a captured reply cannot be replayed.

const (
	SignatureScheme    = 1  // v1: ECDSA P-256 over SHA-256, raw r||s.
	SignatureSize      = 66 // scheme(1) + keyID(1) + r(32) + s(32).
	SignedResponseSize = ResponseSize + SignatureSize
)

// Signer holds the server's identity key. A nil *Signer signs nothing.
type Signer struct {
	key   *ecdsa.PrivateKey
	keyID byte
}

// NewSigner parses a PEM EC P-256 private key (PKCS#8 or SEC1). An empty pemKey
// returns (nil, nil): signing is simply disabled.
func NewSigner(pemKey string, keyID uint8) (*Signer, error) {
	if len(pemKey) == 0 {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("accountprivilege: no PEM block in signing key")
	}
	var key *ecdsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("accountprivilege: signing key is not ECDSA")
		}
		key = ec
	} else if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		return nil, errors.New("accountprivilege: unrecognised EC private key")
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("accountprivilege: signing key is not P-256")
	}
	return &Signer{key: key, keyID: byte(keyID)}, nil
}

// Appendix returns the 66-byte signature block for a response, or nil when the
// signer is nil. It signs SHA-256(response[:72] || tid || cid).
func (s *Signer) Appendix(response []byte, r Request) ([]byte, error) {
	if s == nil || s.key == nil {
		return nil, nil
	}
	if len(response) < ResponseSize {
		return nil, errors.New("accountprivilege: short response")
	}
	msg := make([]byte, 0, ResponseSize+8)
	msg = append(msg, response[:ResponseSize]...)
	var tc [8]byte
	binary.BigEndian.PutUint32(tc[0:4], r.TokenID)
	binary.BigEndian.PutUint32(tc[4:8], r.CharacterID)
	msg = append(msg, tc[:]...)
	digest := sha256.Sum256(msg)
	rr, ss, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, SignatureSize)
	out[0] = SignatureScheme
	out[1] = s.keyID
	rr.FillBytes(out[2:34])
	ss.FillBytes(out[34:66])
	return out, nil
}

// LoadSigner fails on an invalid configured key instead of silently replying unsigned.
func LoadSigner(inline, path string, id uint8) (*Signer, error) {
	if inline != "" && path != "" {
		return nil, errors.New("accountprivilege: specify only one signing key source")
	}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		inline = string(b)
	}
	if inline != "" && id == 0 {
		return nil, errors.New("accountprivilege: key id must be nonzero")
	}
	return NewSigner(inline, id)
}

// SignMessage signs a domain-separated protocol body INCLUDING scheme and key id.
func (s *Signer) SignMessage(body []byte) ([]byte, error) {
	if s == nil || s.key == nil {
		return nil, errors.New("accountprivilege: signing required")
	}
	b := append(append([]byte(nil), body...), SignatureScheme, s.keyID)
	digest := sha256.Sum256(b)
	r, ss, e := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if e != nil {
		return nil, e
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	ss.FillBytes(signature[32:])
	return append(b, signature...), nil
}
