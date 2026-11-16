package clientintegrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// envProof is the answer of a client that carries its environment report.
func envProof(ch, image []byte, debugger bool, tools, looks byte) []byte {
	b := make([]byte, EnvProofSize)
	copy(b, "ERUINP02")
	copy(b[8:12], ch[32:36])
	copy(b[12:44], ch[44:76])
	hash := sha256.New()
	hash.Write(ch[:ChallengeBody])
	hash.Write(image)
	copy(b[44:76], hash.Sum(nil))
	if debugger {
		b[77] = 1
	}
	b[78], b[79] = tools, looks
	copy(b[80:], mac(b[:80], testToken))
	return b
}

// answer completes one challenge with the proof build returns.
func answer(t *testing.T, c net.Conn, build func(ch []byte) []byte) {
	t.Helper()
	c.SetDeadline(time.Now().Add(time.Second))
	ch := make([]byte, ChallengeSize)
	if _, e := io.ReadFull(c, ch); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Write(build(ch)); e != nil {
		t.Fatal(e)
	}
	ack := make([]byte, ChallengeSize)
	if _, e := io.ReadFull(c, ack); e != nil {
		t.Fatal(e)
	}
	if string(ack[:8]) != "ERUINA01" {
		t.Fatal("ack")
	}
}

func TestChallengeOffersEnvAndBothProofFormsVerify(t *testing.T) {
	m, h, image, _ := fixture(t)
	ch, e := m.challenge(h, 1)
	if e != nil {
		t.Fatal(e)
	}
	if string(ch[44:52]) != "ERUENV02" {
		t.Fatalf("challenge nonce does not offer env proofs: %x", ch[44:52])
	}
	// The rest of the nonce is still fresh for every challenge.
	other, _ := m.challenge(h, 1)
	if string(ch[52:76]) == string(other[52:76]) {
		t.Fatal("nonce repeated")
	}

	plain := proof(ch, image)
	if !verifyProof(plain, ch, image, testToken) {
		t.Fatal("an older client's plain proof was refused")
	}
	if _, ok := proofEnv(plain); ok {
		t.Fatal("a plain proof produced a report")
	}

	p := envProof(ch, image, true, 0b10011, 7)
	if !verifyProof(p, ch, image, testToken) {
		t.Fatal("valid env proof refused")
	}
	env, ok := proofEnv(p)
	if !ok || !env.Debugger || env.Tools != 0b10011 || env.Looks != 7 || env.ToolsString() != "ce:p,ce:w,am:w" {
		t.Fatalf("report = %+v", env)
	}
	// The report is inside the MAC: no byte of the proof can be changed.
	for i := range p {
		p[i] ^= 1
		if verifyProof(p, ch, image, testToken) {
			t.Fatalf("accepted byte %d", i)
		}
		p[i] ^= 1
	}
	// A form is its magic and its length together.
	long := append(append([]byte(nil), plain...), 0, 0, 0, 0)
	short := append([]byte(nil), p[:ProofSize]...)
	if verifyProof(long, ch, image, testToken) || verifyProof(short, ch, image, testToken) {
		t.Fatal("magic and length mismatch accepted")
	}
	next, _ := m.challenge(h, 2)
	if verifyProof(p, next, image, testToken) {
		t.Fatal("replay")
	}
}

func TestEnvToolsString(t *testing.T) {
	for tools, want := range map[uint8]string{0: "-", 1: "ce:p", 4: "ce:m", 8 | 16: "am:p,am:w", 31: "ce:p,ce:w,ce:m,am:p,am:w", 0x80 | 2: "ce:w,bit7"} {
		if got := (Env{Tools: tools}).ToolsString(); got != want {
			t.Fatalf("tools %#x = %q, want %q", tools, got, want)
		}
	}
}

func TestEnvReportsReachAttachedChannels(t *testing.T) {
	m, h, image, _ := fixture(t)
	m.interval = 30 * time.Millisecond
	m.timeout = 500 * time.Millisecond
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- m.Serve(server, h, testToken) }()

	// Startup proof: one look, nothing seen.
	answer(t, client, func(ch []byte) []byte { return envProof(ch, image, false, 0, 1) })

	var mu sync.Mutex
	var reports []Env
	attached := make(chan error, 1)
	var release func()
	go func() {
		var e error
		release, e = m.AttachWithEnv(context.Background(), SessionKey(123, 456, testToken), func() {},
			func(env Env) { mu.Lock(); reports = append(reports, env); mu.Unlock() })
		attached <- e
	}()
	// The proof the channel login waits for carries a tool.
	answer(t, client, func(ch []byte) []byte { return envProof(ch, image, false, 3, 2) })
	if e := wait(t, attached); e != nil {
		t.Fatal(e)
	}
	// A channel login that raced the startup proof is satisfied by it and gets
	// the startup report first; the tool report then follows as a later one.
	waitFor := func(n int) []Env {
		deadline := time.Now().Add(time.Second)
		for {
			mu.Lock()
			got := append([]Env(nil), reports...)
			mu.Unlock()
			if len(got) > 0 && got[0].Looks == 1 {
				got = got[1:] // the startup report; every later proof here has two looks
			}
			if len(got) >= n || time.Now().After(deadline) {
				return got
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got := waitFor(1); len(got) != 1 || got[0].Tools != 3 || got[0].Looks != 2 || got[0].Debugger || got[0].Span < 0 {
		t.Fatalf("report at attach = %+v", got)
	}

	// A later proof reaches the attached channel as well.
	answer(t, client, func(ch []byte) []byte { return envProof(ch, image, true, 0, 2) })
	// An older client's plain proof is still accepted and reports nothing.
	answer(t, client, func(ch []byte) []byte { return proof(ch, image) })
	// One more round proves the plain one was acknowledged and the loop went on.
	answer(t, client, func(ch []byte) []byte { return envProof(ch, image, false, 16, 2) })
	got := waitFor(3)
	if len(got) != 3 || got[0].Tools != 3 || !got[1].Debugger || got[1].Tools != 0 || got[2].Tools != 16 || got[2].Span < 0 {
		t.Fatalf("reports = %+v", got)
	}
	release()
}

func TestAttachWithoutEnvProofReportsNothing(t *testing.T) {
	m, h, image, key := fixture(t)
	m.interval = 30 * time.Millisecond
	m.timeout = 500 * time.Millisecond
	server, client := net.Pipe()
	defer client.Close()
	go func() { _ = m.Serve(server, h, testToken) }()
	round(t, client, image, key)
	called := make(chan Env, 1)
	attached := make(chan error, 1)
	go func() {
		_, e := m.AttachWithEnv(context.Background(), SessionKey(123, 456, testToken), func() {},
			func(env Env) { called <- env })
		attached <- e
	}()
	round(t, client, image, key)
	if e := wait(t, attached); e != nil {
		t.Fatal(e)
	}
	select {
	case env := <-called:
		t.Fatalf("a plain-proof client produced a report: %+v", env)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestCrossLanguageEnvProofVector(t *testing.T) {
	raw, e := os.ReadFile("testdata/vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var data map[string]string
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	value := func(name string) []byte {
		b, e := hex.DecodeString(data[name])
		if e != nil || len(b) == 0 {
			t.Fatalf("vector %s: %v", name, e)
		}
		return b
	}
	ch, image, p := value("challenge"), value("image"), value("proof_env")
	if !verifyProof(p, ch, image, testToken) {
		t.Fatal("C++/Python env proof")
	}
	env, ok := proofEnv(p)
	if !ok || !env.Debugger || env.Tools != 3 || env.Looks != 7 {
		t.Fatalf("vector report = %+v", env)
	}
	// The Go side builds the same bytes as the other two.
	if string(envProof(ch, image, true, 3, 7)) != string(p) {
		t.Fatal("Go env proof differs from the shared vector")
	}
}
