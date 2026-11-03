package entranceserver

import (
	"net"
	"sync"
	"time"
)

const (
	maxProfileConnections   = 32
	maxProfileIPConnections = 8
	maxProfileIdentities    = 4096
	profileIPRequests       = 240
	profileAccountRequests  = 120
	profileIPBytes          = 64 << 20
	profileAccountBytes     = 12 << 20
)

type profileWindow struct {
	start                   time.Time
	requests, bytes, active int
}
type profileLimits struct {
	mu       sync.Mutex
	active   int
	ips      map[string]*profileWindow
	accounts map[uint32]*profileWindow
}

func window[K comparable](m map[K]*profileWindow, key K, now time.Time) *profileWindow {
	b := m[key]
	if b == nil {
		if len(m) >= maxProfileIdentities {
			for k, v := range m {
				if v.active == 0 && now.Sub(v.start) >= time.Minute {
					delete(m, k)
				}
			}
		}
		if len(m) >= maxProfileIdentities {
			return nil
		}
		b = &profileWindow{start: now}
		m[key] = b
	} else if now.Sub(b.start) >= time.Minute {
		b.start = now
		b.requests = 0
		b.bytes = 0
	}
	return b
}
func profileIP(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// Called immediately after the eight-byte protocol magic, before DB lookup or
// payload allocation. Limits affect only this feature, not native game traffic.
func (l *profileLimits) enter(ip string, now time.Time) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ips == nil {
		l.ips = make(map[string]*profileWindow)
		l.accounts = make(map[uint32]*profileWindow)
	}
	b := window(l.ips, ip, now)
	if b == nil || l.active >= maxProfileConnections || b.active >= maxProfileIPConnections || b.requests >= profileIPRequests {
		return nil, false
	}
	b.requests++
	b.active++
	l.active++
	return func() { l.mu.Lock(); b.active--; l.active--; l.mu.Unlock() }, true
}

// Header HMAC has already authenticated uid. Charge worst-case LOAD size so
// repeated downloads cannot bypass the byte budget or force unbounded DB reads.
func (l *profileLimits) allow(ip string, uid uint32, bytes int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := window(l.ips, ip, now)
	a := window(l.accounts, uid, now)
	if b == nil || a == nil || a.requests >= profileAccountRequests || b.bytes+bytes > profileIPBytes || a.bytes+bytes > profileAccountBytes {
		return false
	}
	a.requests++
	a.bytes += bytes
	b.bytes += bytes
	return true
}
