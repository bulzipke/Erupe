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
	"sync"
	"time"

	"erupe-ce/common/accountprivilege"
)

const HelloMagic = "ERUINQ01"
const HelloSize = 96
const ChallengeBody = 108
const ChallengeSize = 174
const ProofSize = 108

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
	mu                sync.Mutex
	signer            *accountprivilege.Signer
	builds            map[[32]byte][]byte
	sessions          map[Key]*Lease
	interval, timeout time.Duration
	closed            bool
	manifestPath      string
	manifestHash      [32]byte // Startup contents; watcher state stays local to its goroutine.
	stopped           chan struct{}
	watchDone         chan struct{}
}

func Load(path string, signer *accountprivilege.Signer, interval, timeout time.Duration) (*Manager, error) {
	if signer == nil || interval < 15*time.Second || interval > 300*time.Second || timeout < 5*time.Second || timeout > 60*time.Second {
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
		interval: interval, timeout: timeout, manifestPath: path,
		manifestHash: sha256.Sum256(raw), stopped: make(chan struct{})}, nil
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
	watchers map[uint64]func()
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
	for _, f := range callbacks {
		f()
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
			l.watchers[id] = disconnect
			l.mu.Unlock()
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
	close(m.stopped)
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
	copy(b[76:108], h.BuildID[:])
	return m.signer.SignMessage(b)
}
func verifyProof(b, challenge, image []byte, token string) bool {
	if len(b) != ProofSize || len(challenge) != ChallengeSize || string(b[:8]) != "ERUINP01" || !bytes.Equal(b[8:12], challenge[32:36]) || !hmac.Equal(b[12:44], challenge[44:76]) || !hmac.Equal(b[76:], mac(b[:76], token)) {
		return false
	}
	hash := sha256.New()
	hash.Write(challenge[:ChallengeBody])
	hash.Write(image)
	return hmac.Equal(b[44:76], hash.Sum(nil))
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
	l := &Lease{conn: conn, done: make(chan struct{}), wake: make(chan struct{}, 1), changed: make(chan struct{}), watchers: make(map[uint64]func())}
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
			b := make([]byte, ProofSize)
			_, err := io.ReadFull(conn, b)
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
		ack := append([]byte(nil), challenge[:ChallengeBody]...)
		copy(ack, "ERUINA01")
		ack, err = m.signer.SignMessage(ack)
		if err != nil {
			return err
		}
		if err = writeFull(conn, ack); err != nil {
			return err
		}
		if err = conn.SetDeadline(time.Time{}); err != nil {
			return err
		}
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
