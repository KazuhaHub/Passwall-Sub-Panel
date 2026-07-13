package group

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The enabled-node cache backs NodesFor (→ /sub render). It must serve the
// cached slice within the TTL and miss after, deduping the ListEnabled DB query
// across users on the render-cache-miss path. Staleness is bounded by the TTL,
// which is fine: render reads node config (not traffic/health) and /sub is
// itself short-TTL-cached.
func TestNodeListCache_TTL(t *testing.T) {
	c := newNodeListCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	if _, ok := c.get(t0); ok {
		t.Fatal("empty cache must miss")
	}
	nodes := []*domain.Node{{ID: 1}, {ID: 2}}
	c.putIfVersion(nodes, t0, c.currentVersion())
	if got, ok := c.get(t0.Add(59 * time.Second)); !ok || len(got) != 2 {
		t.Fatalf("within TTL must hit (got %d ok %v)", len(got), ok)
	}
	if _, ok := c.get(t0.Add(61 * time.Second)); ok {
		t.Fatal("after TTL must miss (stale bound exceeded)")
	}
}

func TestNodeListCache_InvalidateRejectsInFlightPut(t *testing.T) {
	c := newNodeListCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	version := c.currentVersion()
	c.putIfVersion([]*domain.Node{{ID: 1}}, t0, version)

	c.invalidate()
	if _, ok := c.get(t0); ok {
		t.Fatal("invalidated cache must miss")
	}
	if c.putIfVersion([]*domain.Node{{ID: 1}}, t0, version) {
		t.Fatal("put from a read started before invalidation must be rejected")
	}
	if _, ok := c.get(t0); ok {
		t.Fatal("rejected stale put must not repopulate cache")
	}
}
