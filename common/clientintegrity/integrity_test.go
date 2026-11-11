package clientintegrity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"erupe-ce/common/accountprivilege"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "0123456789abcdef"

func fixture(t *testing.T) (*Manager, Hello, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	signer, e := accountprivilege.NewSigner(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), 7)
	if e != nil {
		t.Fatal(e)
	}
	image := []byte("executable and read-only data in the approved build")
	id := sha256.Sum256(image)
	manifest := Manifest{1, []Build{{hex.EncodeToString(id[:]), hex.EncodeToString(id[:]), image}}}
	raw, _ := json.Marshal(manifest)
	path := filepath.Join(t.TempDir(), "manifest.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	m, e := Load(path, signer, 60*time.Second, 15*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	b := make([]byte, HelloSize)
	copy(b, HelloMagic)
	binary.BigEndian.PutUint32(b[8:], 123)
	binary.BigEndian.PutUint32(b[12:], 456)
	rand.Read(b[16:32])
	copy(b[32:], id[:])
	copy(b[64:], mac(b[:64], testToken))
	h, e := ParseHello(b)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return m, h, image, key
}
func proof(ch, image []byte) []byte {
	b := make([]byte, ProofSize)
	copy(b, "ERUINP01")
	copy(b[8:12], ch[32:36])
	copy(b[12:44], ch[44:76])
	hash := sha256.New()
	hash.Write(ch[:ChallengeBody])
	hash.Write(image)
	copy(b[44:76], hash.Sum(nil))
	copy(b[76:], mac(b[:76], testToken))
	return b
}
func round(t *testing.T, c net.Conn, image []byte, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	c.SetDeadline(time.Now().Add(time.Second))
	b := make([]byte, ChallengeSize)
	if _, e := io.ReadFull(c, b); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(b[:110])
	if !ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(b[110:142]), new(big.Int).SetBytes(b[142:])) {
		t.Fatal("challenge signature")
	}
	p := proof(b, image)
	if _, e := c.Write(p); e != nil {
		t.Fatal(e)
	}
	ack := make([]byte, ChallengeSize)
	if _, e := io.ReadFull(c, ack); e != nil {
		t.Fatal(e)
	}
	if string(ack[:8]) != "ERUINA01" {
		t.Fatal("ack")
	}
	hash = sha256.Sum256(ack[:110])
	if !ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(ack[110:142]), new(big.Int).SetBytes(ack[142:])) {
		t.Fatal("ack signature")
	}
	return p
}
func wait(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("worker leaked")
		return nil
	}
}
func TestProofTamperingAndReplay(t *testing.T) {
	m, h, image, _ := fixture(t)
	ch, e := m.challenge(h, 1)
	if e != nil {
		t.Fatal(e)
	}
	p := proof(ch, image)
	if !verifyProof(p, ch, image, testToken) {
		t.Fatal("valid")
	}
	for i := range p {
		p[i] ^= 1
		if verifyProof(p, ch, image, testToken) {
			t.Fatalf("accepted byte %d", i)
		}
		p[i] ^= 1
	}
	next, _ := m.challenge(h, 2)
	if verifyProof(p, next, image, testToken) {
		t.Fatal("replay")
	}
	if verifyProof(p, ch, append(image, '!'), testToken) || verifyProof(p, ch, image, "fedcba9876543210") {
		t.Fatal("image/token mismatch")
	}
	for n := 0; n < HelloSize; n++ {
		if _, e := ParseHello(h.Raw[:n]); e == nil {
			t.Fatal("short hello")
		}
	}
	if h.Authenticate("wrong") {
		t.Fatal("wrong token")
	}
}
func TestChannelBindingFailureAndReplacement(t *testing.T) {
	m, h, image, key := fixture(t)
	m.interval = 30 * time.Millisecond
	m.timeout = 200 * time.Millisecond
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- m.Serve(server, h, testToken) }()
	round(t, client, image, key)
	var kicks atomic.Int32
	attached := make(chan error, 1)
	var release func()
	go func() {
		var e error
		release, e = m.Attach(context.Background(), SessionKey(123, 456, testToken), func() { kicks.Add(1) })
		attached <- e
	}()
	round(t, client, image, key)
	if e := wait(t, attached); e != nil {
		t.Fatal(e)
	}
	// A different token/character cannot inherit the authenticated lease.
	if _, e := m.Attach(context.Background(), SessionKey(123, 457, testToken), func() {}); e == nil {
		t.Fatal("cross-character")
	}
	a, b := net.Pipe()
	if e := m.Serve(a, h, testToken); e == nil {
		t.Fatal("duplicate replaced healthy lease")
	}
	a.Close()
	b.Close()
	// Replay a proof from another challenge: server closes the actual watcher.
	challenge := make([]byte, ChallengeSize)
	if _, e := io.ReadFull(client, challenge); e != nil {
		t.Fatal(e)
	}
	bad := proof(challenge, image)
	bad[44] ^= 1
	copy(bad[76:], mac(bad[:76], testToken))
	client.Write(bad)
	if e := wait(t, done); e == nil {
		t.Fatal("bad proof accepted")
	}
	if kicks.Load() != 1 {
		t.Fatalf("kicks %d", kicks.Load())
	}
	release()
	// Clean reconnection obtains a fresh lease. Old release must not erase it.
	s2, c2 := net.Pipe()
	defer c2.Close()
	d2 := make(chan error, 1)
	go func() { d2 <- m.Serve(s2, h, testToken) }()
	round(t, c2, image, key)
	release()
	m.Close()
	wait(t, d2)
}
func TestTimeoutMissingAndCancelled(t *testing.T) {
	m, h, image, key := fixture(t)
	m.interval = 10 * time.Millisecond
	m.timeout = 40 * time.Millisecond
	if _, e := m.Attach(context.Background(), SessionKey(123, 456, testToken), func() {}); e == nil {
		t.Fatal("no side connection")
	}
	s, c := net.Pipe()
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- m.Serve(s, h, testToken) }()
	round(t, c, image, key)
	var kicks atomic.Int32
	attached := make(chan error, 1)
	go func() {
		_, e := m.Attach(context.Background(), SessionKey(123, 456, testToken), func() { kicks.Add(1) })
		attached <- e
	}()
	round(t, c, image, key)
	if e := wait(t, attached); e != nil {
		t.Fatal(e)
	}
	if e := wait(t, done); e == nil {
		t.Fatal("silent peer accepted")
	}
	if kicks.Load() != 1 {
		t.Fatal("timeout did not disconnect game")
	}
}
func TestManifestRejectsBadConfiguration(t *testing.T) {
	m, _, _, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "bad.json")
	for _, data := range []string{`{}`, `{"version":1,"builds":[]}`, `{"version":1,"builds":[],"unknown":true}`} {
		os.WriteFile(path, []byte(data), 0600)
		if _, e := Load(path, m.signer, 60*time.Second, 15*time.Second); e == nil {
			t.Fatal("bad manifest")
		}
	}
	if _, e := Load(path, nil, 60*time.Second, 15*time.Second); e == nil {
		t.Fatal("no signer")
	}
	if _, e := Load(path, m.signer, time.Second, 15*time.Second); e == nil {
		t.Fatal("unbounded schedule")
	}
}

func TestConfiguredTenMinuteIntervalIsSignedInChallenge(t *testing.T) {
	fixtureManager, hello, _, key := fixture(t)
	m, err := Load(fixtureManager.manifestPath, fixtureManager.signer, 600*time.Second, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if bad, err := Load(fixtureManager.manifestPath, fixtureManager.signer, 601*time.Second, 15*time.Second); err == nil {
		bad.Close()
		t.Fatal("accepted an interval greater than 600 seconds")
	}
	challenge, err := m.challenge(hello, 1)
	if err != nil {
		t.Fatal(err)
	}
	if interval, timeout := binary.BigEndian.Uint32(challenge[36:40]), binary.BigEndian.Uint32(challenge[40:44]); interval != 600 || timeout != 15 {
		t.Fatalf("signed interval=%d timeout=%d", interval, timeout)
	}
	hash := sha256.Sum256(challenge[:110])
	if !ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(challenge[110:142]), new(big.Int).SetBytes(challenge[142:])) {
		t.Fatal("invalid 600-second challenge signature")
	}
}

func TestCrossLanguageVectors(t *testing.T) {
	raw, e := os.ReadFile("testdata/vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var data map[string]string
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	values := make(map[string][]byte)
	for k, v := range data {
		values[k], e = hex.DecodeString(v)
		if e != nil {
			t.Fatal(e)
		}
	}
	h, e := ParseHello(values["hello"])
	if e != nil || !h.Authenticate(testToken) {
		t.Fatal("Python hello")
	}
	ch := values["challenge"]
	p := values["public"]
	pub := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(p[:32]), Y: new(big.Int).SetBytes(p[32:])}
	hash := sha256.Sum256(ch[:110])
	if !ecdsa.Verify(&pub, hash[:], new(big.Int).SetBytes(ch[110:142]), new(big.Int).SetBytes(ch[142:])) {
		t.Fatal("Python signature")
	}
	if !verifyProof(values["proof"], ch, values["image"], testToken) {
		t.Fatal("C++/Python proof")
	}
}

func TestEOFReleasesLeaseWithoutWaitingForInterval(t *testing.T) {
	m, h, image, key := fixture(t)
	for i := 0; i < 5; i++ {
		s, c := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- m.Serve(s, h, testToken) }()
		round(t, c, image, key)
		c.Close()
		if e := wait(t, done); e == nil {
			t.Fatal("EOF accepted")
		}
		m.mu.Lock()
		remaining := len(m.sessions)
		m.mu.Unlock()
		if remaining != 0 {
			t.Fatal("dead lease retained")
		}
	}
}
