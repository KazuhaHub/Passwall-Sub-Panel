package render

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The /sub render cache must serve the same Output for a (user, clientType)
// within the TTL and miss after it — the bound on staleness the polling fleet
// trades for skipping a full re-render on every poll.
func TestRenderCache_HitWithinTTLMissAfter(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	key := renderCacheKey{userID: 1, ct: domain.ClientMihomo}
	out := &Output{Body: []byte("config")}

	if _, ok := c.get(key, t0); ok {
		t.Fatal("empty cache must miss")
	}
	c.putIfVersion(key, out, t0, c.currentVersion())
	if got, ok := c.get(key, t0.Add(59*time.Second)); !ok || got != out {
		t.Fatalf("within TTL must hit with the same Output (got %v ok %v)", got, ok)
	}
	if _, ok := c.get(key, t0.Add(61*time.Second)); ok {
		t.Fatal("after TTL must miss (stale bound exceeded)")
	}
}

// A different (user, clientType) must never collide — no cross-tenant or
// cross-format leakage of a cached subscription body.
func TestRenderCache_KeyIsolation(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	mine := &Output{Body: []byte("mine")}
	c.putIfVersion(renderCacheKey{userID: 1, ct: domain.ClientMihomo}, mine, t0, c.currentVersion())

	if _, ok := c.get(renderCacheKey{userID: 2, ct: domain.ClientMihomo}, t0); ok {
		t.Fatal("different user must miss")
	}
	if _, ok := c.get(renderCacheKey{userID: 1, ct: domain.ClientSingBox}, t0); ok {
		t.Fatal("different client type must miss")
	}
	if got, ok := c.get(renderCacheKey{userID: 1, ct: domain.ClientMihomo}, t0); !ok || got != mine {
		t.Fatal("own key must hit")
	}
}

// put sweeps expired entries once the map grows past the threshold so a churn
// of distinct (user,ct) keys can't grow the cache without bound.
func TestRenderCache_SweepsExpired(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	for i := 0; i < renderCacheSweepThreshold+10; i++ {
		c.putIfVersion(renderCacheKey{userID: int64(i), ct: domain.ClientMihomo}, &Output{}, t0, c.currentVersion())
	}
	// All above were inserted at t0 (expire at t0+60s). A put well past their
	// expiry must sweep them, leaving far fewer than we inserted.
	c.putIfVersion(renderCacheKey{userID: -1, ct: domain.ClientMihomo}, &Output{}, t0.Add(120*time.Second), c.currentVersion())
	if n := c.size(); n > 10 {
		t.Fatalf("expired entries not swept: size=%d", n)
	}
}

func TestRenderCache_ClearRejectsInFlightPut(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	key := renderCacheKey{userID: 1, ct: domain.ClientMihomo}
	version := c.currentVersion()
	c.putIfVersion(key, &Output{Body: []byte("old")}, t0, version)

	c.clear()
	if _, ok := c.get(key, t0); ok {
		t.Fatal("cleared cache must miss")
	}
	if c.putIfVersion(key, &Output{Body: []byte("stale")}, t0, version) {
		t.Fatal("render started before clear must not repopulate the cache")
	}
	if c.size() != 0 {
		t.Fatalf("stale put repopulated cache: size=%d", c.size())
	}
}
