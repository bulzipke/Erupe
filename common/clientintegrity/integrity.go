// Package clientintegrity verifies live client measurements on a bounded side
// connection. All channel servers and the entrance share one Manager instance.
package clientintegrity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"erupe-ce/common/accountprivilege"
)

const HelloMagic = "ERUINQ01"
const HelloSize = 96
const ChallengeBody = 108
const ChallengeSize = 174
const ProofSize = 108

// EnvProofSize is the size of a proof that also carries the client's report
// about its own PC (Env): the 76 proof bytes, four report bytes, the MAC.
const EnvProofSize = 112

const (
	proofMagic    = "ERUINP01"
	envProofMagic = "ERUINP02"
	// envOffer is written over the first eight bytes of every challenge nonce.
	// A client that knows it answers with an env proof. An older client treats
	// the whole nonce as opaque and answers with a plain proof, and a newer
	// client answers an older server (whose nonce is random there) the same
	// way, so either side can be deployed first. The other 24 nonce bytes stay
	// random.
	envOffer = "ERUENV02"
)

// Env is what a client observed about its own PC since its previous proof. It
// is a report, not a verdict: this package neither checks nor acts on it.
type Env struct {
	Debugger bool
	// Tools is a bit set of known memory editors seen as a process, a window
	// or a module inside the game; ToolsString names the bits.
	Tools uint8
	// Looks is how many times the client looked. Zero means it did not look,
	// so the report says nothing either way.
	Looks uint8
	// Span is the time since this connection's previous proof, which bounds
	// when the reported things were seen. Zero on the first proof.
	Span time.Duration
}

// The client's bit order (integrity_watch.h in the client DLL).
var envToolNames = [...]string{"ce:p", "ce:w", "ce:m", "am:p", "am:w"}

// ToolsString lists the tools seen, or "-" when there were none.
func (e Env) ToolsString() string {
	var names []string
	for bit := 0; bit < 8; bit++ {
		if e.Tools&(1<<bit) == 0 {
			continue
		}
		if bit < len(envToolNames) {
			names = append(names, envToolNames[bit])
		} else {
			names = append(names, "bit"+strconv.Itoa(bit))
		}
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

var ErrRejected = errors.New("client integrity rejected")

type Build struct {
	ID         string `json:"id"`
	FileSHA256 string `json:"file_sha256"`
	Image      []byte `json:"image"`
}
type Manifest struct {
	Version int     `json:"version"`
	Builds  []Build `json:"builds"`
}
type Key struct {
	TokenID, CharacterID uint32
	TokenHash            [32]byte
}

func SessionKey(tid, cid uint32, token string) Key {
	return Key{tid, cid, sha256.Sum256([]byte(token))}
}

type Hello struct {
	Raw                  []byte
	TokenID, CharacterID uint32
	BuildID              [32]byte
}

func ParseHello(b []byte) (Hello, error) {
	if len(b) != HelloSize || string(b[:8]) != HelloMagic {
		return Hello{}, ErrRejected
	}
	h := Hello{Raw: append([]byte(nil), b...), TokenID: binary.BigEndian.Uint32(b[8:12]), CharacterID: binary.BigEndian.Uint32(b[12:16])}
	copy(h.BuildID[:], b[32:64])
	if h.TokenID == 0 || h.CharacterID == 0 {
		return Hello{}, ErrRejected
	}
	return h, nil
}
func mac(b []byte, token string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	m.Write(b)
	return m.Sum(nil)
}
func (h Hello) Authenticate(token string) bool {
	return len(token) == 16 && len(h.Raw) == HelloSize && hmac.Equal(h.Raw[64:], mac(h.Raw[:64], token))
}

type Manager struct {
	resourceKey       []byte
	mu                sync.Mutex
	signer            *accountprivilege.Signer
	builds            map[[32]byte][]byte
	sessions          map[Key]*Lease
	interval, timeout time.Duration
	closed            bool
	manifestPath      string
}

func Load(path string, signer *accountprivilege.Signer, interval, timeout time.Duration) (*Manager, error) {
	if signer == nil || interval < 15*time.Second || interval > 600*time.Second || timeout < 5*time.Second || timeout > 60*time.Second {
		return nil, errors.New("integrity: signing key and bounded interval/timeout required")
	}
	raw, err := readManifest(path)
	if err != nil {
		return nil, err
	}
	builds, err := decodeManifest(raw)
	if err != nil {
		return nil, err
	}
	return &Manager{signer: signer, builds: builds, sessions: make(map[Key]*Lease),
		interval: interval, timeout: timeout, manifestPath: path}, nil
}

type Lease struct {
	mu       sync.Mutex
	conn     net.Conn
	done     chan struct{}
	wake     chan struct{}
	changed  chan struct{}
	sequence uint32
	closed   bool
	next     uint64
	watchers map[uint64]watcher
	env      *Env // the latest report, nil until an env proof arrives
}

// watcher is one channel connection bound to a lease.
type watcher struct {
	disconnect func()
	env        func(Env) // may be nil
}

func (l *Lease) fail() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.done)
	close(l.changed)
	callbacks := l.watchers
	l.watchers = nil
	l.mu.Unlock()
	l.conn.Close()
	for _, w := range callbacks {
		w.disconnect()
	}
}

// report keeps the latest environment report and hands it to every channel
// connection bound to the lease.
func (l *Lease) report(e Env) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.env = &e
	sinks := make([]func(Env), 0, len(l.watchers))
	for _, w := range l.watchers {
		if w.env != nil {
			sinks = append(sinks, w.env)
		}
	}
	l.mu.Unlock()
	for _, f := range sinks {
		f(e)
	}
}
func (l *Lease) advance(seq uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.sequence = seq
	close(l.changed)
	l.changed = make(chan struct{})
}

// Attach requires a fresh server challenge before channel login proceeds. Each
// connection gets its own watcher: an old channel's teardown cannot detach a
// newly joined channel or clear a different session's integrity state.
func (m *Manager) Attach(ctx context.Context, key Key, disconnect func()) (func(), error) {
	return m.AttachWithEnv(ctx, key, disconnect, nil)
}

// AttachWithEnv is Attach for a connection that also wants the client's
// environment reports: report is called with the one carried by the fresh
// proof this call waited for, and again with every later one. A client that
// sends plain proofs never triggers it.
func (m *Manager) AttachWithEnv(ctx context.Context, key Key, disconnect func(), report func(Env)) (func(), error) {
	m.mu.Lock()
	l := m.sessions[key]
	m.mu.Unlock()
	if l == nil {
		return nil, ErrRejected
	}
	l.mu.Lock()
	seq := l.sequence
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return nil, ErrRejected
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, ErrRejected
		}
		if l.sequence > seq {
			l.next++
			id := l.next
			l.watchers[id] = watcher{disconnect, report}
			latest := l.env
			l.mu.Unlock()
			if report != nil && latest != nil {
				report(*latest)
			}
			return func() { l.mu.Lock(); delete(l.watchers, id); l.mu.Unlock() }, nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
func (m *Manager) Timeout() time.Duration { return m.timeout }
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	ls := make([]*Lease, 0, len(m.sessions))
	for _, l := range m.sessions {
		ls = append(ls, l)
	}
	m.mu.Unlock()
	for _, l := range ls {
		l.fail()
	}
}
func writeFull(c net.Conn, b []byte) error {
	for len(b) > 0 {
		n, e := c.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func (m *Manager) challenge(h Hello, seq uint32) ([]byte, error) {
	b := make([]byte, ChallengeBody)
	copy(b, "ERUINC01")
	copy(b[8:32], h.Raw[8:32])
	binary.BigEndian.PutUint32(b[32:36], seq)
	binary.BigEndian.PutUint32(b[36:40], uint32(m.interval/time.Second))
	binary.BigEndian.PutUint32(b[40:44], uint32(m.timeout/time.Second))
	if _, err := rand.Read(b[44:76]); err != nil {
		return nil, err
	}
	copy(b[44:52], envOffer)
	if seq == 1 && len(m.resourceKey) != 0 {
		copy(b[52:60], resourceOffer)
	}
	copy(b[76:108], h.BuildID[:])
	return m.signer.SignMessage(b)
}
func verifyProof(b, challenge, image []byte, token string) bool {
	// The MAC covers everything before it: 76 bytes of a plain proof, 80 of an
	// env proof.
	var body int
	switch {
	case len(b) == ProofSize && string(b[:8]) == proofMagic:
		body = 76
	case len(b) == EnvProofSize && string(b[:8]) == envProofMagic:
		body = 80
	case len(b) == resourceProofSize && string(b[:8]) == resourceProof:
		body = resourceProofBody
	default:
		return false
	}
	if len(challenge) != ChallengeSize || !bytes.Equal(b[8:12], challenge[32:36]) || !hmac.Equal(b[12:44], challenge[44:76]) || !hmac.Equal(b[body:], mac(b[:body], token)) {
		return false
	}
	hash := sha256.New()
	hash.Write(challenge[:ChallengeBody])
	hash.Write(image)
	return hmac.Equal(b[44:76], hash.Sum(nil))
}

// proofEnv returns the report carried by a verified env proof. Byte 76 is
// reserved.
func proofEnv(b []byte) (Env, bool) {
	if len(b) != EnvProofSize && len(b) != resourceProofSize {
		return Env{}, false
	}
	return Env{Debugger: b[77] != 0, Tools: b[78], Looks: b[79]}, true
}

// Serve receives an authenticated hello only. A duplicate cannot replace a
// healthy lease. Any later EOF, malformed proof, timeout or write failure
// closes all channel connections bound to this exact token/character session.
func (m *Manager) Serve(conn net.Conn, h Hello, token string) error {
	if !h.Authenticate(token) {
		return ErrRejected
	}
	m.mu.Lock()
	image, ok := m.builds[h.BuildID]
	m.mu.Unlock()
	if !ok {
		return ErrRejected
	}
	key := SessionKey(h.TokenID, h.CharacterID, token)
	l := &Lease{conn: conn, done: make(chan struct{}), wake: make(chan struct{}, 1), changed: make(chan struct{}), watchers: make(map[uint64]watcher)}
	// Reserve even before proof to cap one authenticated stream per session.
	m.mu.Lock()
	if m.closed || m.sessions[key] != nil {
		m.mu.Unlock()
		return ErrRejected
	}
	m.sessions[key] = l
	m.mu.Unlock()

	// One reader detects FIN/RST even between challenges. Leaving reads dormant
	// for the whole interval would retain dead leases and break fast relogins.
	type frame struct {
		data []byte
		err  error
	}
	frames := make(chan frame, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			// The magic decides how long the proof is. Anything unknown is read
			// as a plain proof and fails verification.
			b := make([]byte, resourceProofSize)
			_, err := io.ReadFull(conn, b[:8])
			size := ProofSize
			if string(b[:8]) == envProofMagic {
				size = EnvProofSize
			} else if string(b[:8]) == resourceProof {
				size = resourceProofSize
			}
			b = b[:size]
			if err == nil {
				_, err = io.ReadFull(conn, b[8:])
			}
			select {
			case frames <- frame{b, err}:
			case <-l.done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		l.fail()
		<-readerDone
		m.mu.Lock()
		if m.sessions[key] == l {
			delete(m.sessions, key)
		}
		m.mu.Unlock()
	}()
	var lastProof time.Time
	for seq := uint32(1); seq != 0; seq++ {
		if err := conn.SetDeadline(time.Now().Add(m.timeout)); err != nil {
			return err
		}
		challenge, err := m.challenge(h, seq)
		if err != nil {
			return err
		}
		if err = writeFull(conn, challenge); err != nil {
			return err
		}
		var received frame
		select {
		case received = <-frames:
		case <-l.done:
			return net.ErrClosed
		}
		if received.err != nil {
			return received.err
		}
		proof := received.data
		if !verifyProof(proof, challenge, image, token) {
			return ErrRejected
		}
		var resource []byte
		if len(proof) == resourceProofSize {
			if seq != 1 {
				return ErrRejected
			}
			resource, err = m.resourceReply(challenge, proof)
			if err != nil {
				return err
			}
		}
		ack := append([]byte(nil), challenge[:ChallengeBody]...)
		copy(ack, "ERUINA01")
		ack, err = m.signer.SignMessage(ack)
		if err != nil {
			return err
		}
		if err = writeFull(conn, append(ack, resource...)); err != nil {
			return err
		}
		if err = conn.SetDeadline(time.Time{}); err != nil {
			return err
		}
		// Report before advancing, so a channel login waiting on this proof
		// finds its report already in place.
		now := time.Now()
		if env, ok := proofEnv(proof); ok {
			if !lastProof.IsZero() {
				env.Span = now.Sub(lastProof)
			}
			l.report(env)
		}
		lastProof = now
		l.advance(seq)
		timer := time.NewTimer(m.interval)
		select {
		case <-timer.C:
		case <-l.wake:
		case extra := <-frames:
			timer.Stop()
			if extra.err != nil {
				return extra.err
			}
			return ErrRejected // Unsolicited or repeated proof between challenges.
		case <-l.done:
			timer.Stop()
			return net.ErrClosed
		}
		timer.Stop()
	}
	return ErrRejected
}
