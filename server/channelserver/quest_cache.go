package channelserver

import (
	"sync"
	"time"
)

// questCacheKey identifies a cached quest variant. Phase B of #188 added the
// language dimension so localized quest text compiled for one session does
// not leak into another session's response.
type questCacheKey struct {
	questID int
	lang    string
}

// QuestCache is a thread-safe, expiring cache for parsed quest file data,
// keyed by (questID, language). Entries for different languages are stored
// independently so a Japanese client and a French client on the same server
// never share compiled binaries.
type QuestCache struct {
	mu     sync.RWMutex
	data   map[questCacheKey][]byte
	expiry map[questCacheKey]time.Time
	ttl    time.Duration
}

// NewQuestCache creates a QuestCache with the given TTL in seconds.
// A TTL of 0 disables caching (Get always misses).
func NewQuestCache(ttlSeconds int) *QuestCache {
	return &QuestCache{
		data:   make(map[questCacheKey][]byte),
		expiry: make(map[questCacheKey]time.Time),
		ttl:    time.Duration(ttlSeconds) * time.Second,
	}
}

// Get returns cached quest data for the (questID, lang) variant if it exists
// and has not expired.
func (c *QuestCache) Get(questID int, lang string) ([]byte, bool) {
	if c.ttl <= 0 {
		return nil, false
	}
	k := questCacheKey{questID: questID, lang: lang}
	now := time.Now()
	c.mu.RLock()
	b, ok := c.data[k]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	expires := c.expiry[k]
	c.mu.RUnlock()
	if now.After(expires) {
		c.mu.Lock()
		if currentExpiry, exists := c.expiry[k]; exists && !currentExpiry.After(now) {
			delete(c.data, k)
			delete(c.expiry, k)
		}
		c.mu.Unlock()
		return nil, false
	}
	return b, true
}

// Put stores quest data for the (questID, lang) variant with the configured TTL.
func (c *QuestCache) Put(questID int, lang string, b []byte) {
	if c.ttl <= 0 {
		return
	}
	k := questCacheKey{questID: questID, lang: lang}
	c.mu.Lock()
	c.data[k] = b
	c.expiry[k] = time.Now().Add(c.ttl)
	c.mu.Unlock()
}
