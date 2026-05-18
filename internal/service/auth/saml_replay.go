package auth

import (
	"sync"
	"time"
)

// assertionReplayCache records SAML Assertion IDs we have already
// consumed so a captured SAMLResponse can't be re-submitted within its
// signature-validity window. Entries expire after the assertion's
// NotOnOrAfter — there's no point holding the ID once the signed
// window has closed anyway.
//
// Storage: bounded map + lazy eviction at write time. SAML logins are
// rare enough (every assertion ≤ ~5 min lifetime) that even a busy
// panel sees at most a few thousand entries; an unbounded cap would
// be a memory leak, a per-entry TTL eviction is the minimum complexity
// that closes the replay window cleanly.
type assertionReplayCache struct {
	mu    sync.Mutex
	items map[string]time.Time
}

const replayCacheMax = 65536

// SeenOrAdd returns true if the id has been seen since its expiresAt;
// otherwise records it and returns false. expiresAt is the assertion's
// NotOnOrAfter (or a sensible bound — caller's choice). A zero or past
// expiresAt is rejected as a replay-cache miss because such an
// assertion would already be expired and rejected by the SAML library.
func (c *assertionReplayCache) SeenOrAdd(id string, expiresAt time.Time, now time.Time) bool {
	if id == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]time.Time, 128)
	}
	if exp, ok := c.items[id]; ok && now.Before(exp) {
		return true
	}
	// Periodic GC: when the map grows past the cap, sweep expired
	// entries. Sweep cost is amortised across many writes.
	if len(c.items) >= replayCacheMax {
		for k, exp := range c.items {
			if !now.Before(exp) {
				delete(c.items, k)
			}
		}
	}
	c.items[id] = expiresAt
	return false
}
