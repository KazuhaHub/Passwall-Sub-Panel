package auth

import (
	"fmt"
	"testing"
	"time"
)

// A flood of DISTINCT, still-valid assertion IDs must not grow the replay cache
// without bound: the expired-entry sweep frees nothing when every entry is
// valid, so the cap must additionally evict the soonest-to-expire entries.
func TestAssertionReplayCache_BoundedUnderValidFlood(t *testing.T) {
	c := &assertionReplayCache{}
	now := time.Unix(1_700_000_000, 0)
	base := now.Add(5 * time.Minute) // all entries valid (expire well after now)
	for i := 0; i < replayCacheMax+5000; i++ {
		// distinct increasing expiries so "soonest-to-expire" is well-defined
		c.SeenOrAdd(fmt.Sprintf("id-%d", i), base.Add(time.Duration(i)*time.Millisecond), now)
	}
	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()
	if n > replayCacheMax {
		t.Fatalf("replay cache grew past the cap under a flood of valid assertions: %d > %d", n, replayCacheMax)
	}
}

func TestAssertionReplayCache_SeenOrAdd(t *testing.T) {
	var c assertionReplayCache
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	exp := now.Add(5 * time.Minute)

	if c.SeenOrAdd("a1", exp, now) {
		t.Fatal("first insert reported as seen")
	}
	if !c.SeenOrAdd("a1", exp, now) {
		t.Fatal("second insert with same ID should be a replay")
	}
	// A different ID with overlapping window is independent.
	if c.SeenOrAdd("a2", exp, now) {
		t.Fatal("distinct ID reported as seen")
	}
}

func TestAssertionReplayCache_ExpiresOutOfWindow(t *testing.T) {
	var c assertionReplayCache
	t0 := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	exp := t0.Add(5 * time.Minute)
	c.SeenOrAdd("a1", exp, t0)
	// After NotOnOrAfter the assertion would already be rejected by
	// the SAML library; replay cache should accept the slot again so a
	// crafted (impossible) re-use isn't blocked by a stale entry.
	later := t0.Add(10 * time.Minute)
	if c.SeenOrAdd("a1", later.Add(5*time.Minute), later) {
		t.Fatal("expired entry should not flag a replay")
	}
}

func TestAssertionReplayCache_EmptyIDIgnored(t *testing.T) {
	var c assertionReplayCache
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	// Empty ID means "library never gave us one" — refuse to record
	// it so the global cache can't be poisoned with the empty-key
	// entry that would then flag every other empty-id check as a
	// replay.
	if c.SeenOrAdd("", now.Add(time.Minute), now) {
		t.Fatal("empty id should never report seen")
	}
	if c.SeenOrAdd("", now.Add(time.Minute), now) {
		t.Fatal("empty id should remain not-seen")
	}
}
