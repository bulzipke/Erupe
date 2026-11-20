package clientintegrity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

const (
	resourceOffer     = "KRKEY001"
	resourceProof     = "ERUINP03"
	resourceProofBody = 144
	resourceProofSize = resourceProofBody + sha256.Size
)

// LoadResourceKey is called before the listeners are started. The private
// key file is independent of the public patch data and integrity manifest.
func (m *Manager) LoadResourceKey(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("resource key must contain exactly 32 raw bytes")
	}
	m.resourceKey = key
	return nil
}

func (m *Manager) resourceReply(challenge, proof []byte) ([]byte, error) {
	if len(m.resourceKey) != 32 || string(challenge[52:60]) != resourceOffer {
		return nil, ErrRejected
	}
	curve := ecdh.P256()
	var encoded [65]byte
	encoded[0] = 4
	copy(encoded[1:], proof[80:resourceProofBody])
	peer, err := curve.NewPublicKey(encoded[:])
	if err != nil {
		return nil, ErrRejected
	}
	private, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := private.ECDH(peer)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	// SP 800-56C one-step SHA-256 KDF. The signed challenge binds the
	// derivation to this connection, build and proof sequence.
	kdf := sha256.New()
	kdf.Write([]byte{0, 0, 0, 1})
	kdf.Write(shared)
	kdf.Write([]byte(resourceOffer))
	kdf.Write(challenge[:ChallengeBody])
	wrappingKey := kdf.Sum(nil)
	defer clear(wrappingKey)
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	body := make([]byte, 84, 132)
	copy(body, "ERUINK01")
	copy(body[8:72], private.PublicKey().Bytes()[1:])
	if _, err = rand.Read(body[72:84]); err != nil {
		return nil, err
	}
	body = gcm.Seal(body, body[72:84], m.resourceKey, challenge[:ChallengeBody])
	return m.signer.SignMessage(body)
}
