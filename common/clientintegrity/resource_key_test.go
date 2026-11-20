package clientintegrity

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestResourceKeyAfterVerifiedProof(t *testing.T) {
	m, hello, image, signer := fixture(t)
	key := bytes.Repeat([]byte{0x5c}, 32)
	path := filepath.Join(t.TempDir(), "test.key")
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadResourceKey(path); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	done := make(chan error, 1)
	go func() { done <- m.Serve(server, hello, testToken) }()
	challenge := make([]byte, ChallengeSize)
	if _, err := io.ReadFull(client, challenge); err != nil {
		t.Fatal(err)
	}
	if string(challenge[52:60]) != resourceOffer {
		t.Fatal("missing key offer")
	}
	if next, _ := m.challenge(hello, 2); string(next[52:60]) == resourceOffer {
		t.Fatal("offered key again")
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, resourceProofSize)
	copy(b, proof(challenge, image)[:76])
	copy(b, resourceProof)
	copy(b[80:], private.PublicKey().Bytes()[1:])
	var peer *exec.Cmd
	var peerOut io.ReadCloser
	var peerIn io.WriteCloser
	if executable := os.Getenv("MHF_RESOURCE_TEST_PEER"); executable != "" {
		peer = exec.Command(executable, "--peer")
		if peerOut, err = peer.StdoutPipe(); err != nil {
			t.Fatal(err)
		}
		if peerIn, err = peer.StdinPipe(); err != nil {
			t.Fatal(err)
		}
		if err = peer.Start(); err != nil {
			t.Fatal(err)
		}
		defer peer.Process.Kill()
		if _, err := io.ReadFull(peerOut, b[80:resourceProofBody]); err != nil {
			t.Fatal(err)
		}
	}
	copy(b[resourceProofBody:], mac(b[:resourceProofBody], testToken))
	b[91] ^= 1
	if verifyProof(b, challenge, image, testToken) {
		t.Fatal("accepted a substituted ECDH public key")
	}
	b[91] ^= 1
	if _, err := client.Write(b); err != nil {
		t.Fatal(err)
	}
	ack := make([]byte, ChallengeSize+198)
	if _, err := io.ReadFull(client, ack); err != nil {
		t.Fatal(err)
	}
	reply := ack[ChallengeSize:]
	if string(ack[:8]) != "ERUINA01" || string(reply[:8]) != "ERUINK01" || bytes.Contains(reply, key) {
		t.Fatal("unexpected acknowledgement or key exposure")
	}
	if peer != nil {
		public := signer.PublicKey
		data := append(append([]byte(nil), challenge[:ChallengeBody]...), reply...)
		var xy [64]byte
		public.X.FillBytes(xy[:32])
		public.Y.FillBytes(xy[32:])
		if _, err := peerIn.Write(append(data, xy[:]...)); err != nil {
			t.Fatal(err)
		}
		peerIn.Close()
		got, err := io.ReadAll(peerOut)
		if err != nil || peer.Wait() != nil || !bytes.Equal(got, key) {
			t.Fatal("native CNG peer failed to decrypt the verified key")
		}
	} else {
		pub, err := ecdh.P256().NewPublicKey(append([]byte{4}, reply[8:72]...))
		if err != nil {
			t.Fatal(err)
		}
		shared, err := private.ECDH(pub)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(bytes.Join([][]byte{{0, 0, 0, 1}, shared, []byte(resourceOffer), challenge[:ChallengeBody]}, nil))
		block, _ := aes.NewCipher(digest[:])
		gcm, _ := cipher.NewGCM(block)
		clear, err := gcm.Open(nil, reply[72:84], reply[84:132], challenge[:ChallengeBody])
		if err != nil || !bytes.Equal(clear, key) {
			t.Fatal("returned resource key is incorrect")
		}
	}
	client.Close()
	wait(t, done)
}
