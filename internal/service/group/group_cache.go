package group

import (
	"context"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// enabledNodesTTL bounds how stale the cached enabled-node set may be. NodesFor
// is its only consumer (→ /sub render, which reads node CONFIG — protocol /
// address / port / stream settings / remark / region / tags — never the
// traffic or health columns), and /sub is itself short-TTL-cached, so this adds
// no staleness beyond that. It dedups the ListEnabled DB query across users on
// the render-cache-miss path. Node ordering/config writes explicitly invalidate
// it so the next subscription render observes the committed values immediately.
//
// Caching the *domain.Node slice is safe because render treats nodes as
// read-only: sortNodes copies the slice before sorting, applyLayout copies the
// display name into a per-render renderItem, applyRegionFlagPrefix mutates that
// renderItem (not the node), and no render code writes a node field. If a
// future caller needs fresh traffic/health off ListEnabled, revisit this.
const enabledNodesTTL = 60 * time.Second

type nodeListCache struct {
	mu      sync.Mutex
	nodes   []*domain.Node
	ok      bool
	expires time.Time
	ttl     time.Duration
	version uint64
}

func newNodeListCache(ttl time.Duration) *nodeListCache {
	return &nodeListCache{ttl: ttl}
}

func (c *nodeListCache) get(now time.Time) ([]*domain.Node, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok || !now.Before(c.expires) {
		return nil, false
	}
	return c.nodes, true
}

func (c *nodeListCache) currentVersion() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *nodeListCache) putIfVersion(nodes []*domain.Node, now time.Time, version uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != version {
		return false
	}
	c.nodes = nodes
	c.ok = true
	c.expires = now.Add(c.ttl)
	return true
}

func (c *nodeListCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes = nil
	c.ok = false
	c.expires = time.Time{}
	c.version++
}

// listEnabledCached returns nodes.ListEnabled, cached for enabledNodesTTL. The
// returned slice is shared read-only across callers (see the safety note on
// enabledNodesTTL). Degrades to a direct repo read when the cache is unset
// (struct-literal construction in tests).
func (s *Service) listEnabledCached(ctx context.Context) ([]*domain.Node, error) {
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	if s.enabledCache != nil {
		if nodes, ok := s.enabledCache.get(now); ok {
			return nodes, nil
		}
	}
	version := uint64(0)
	if s.enabledCache != nil {
		version = s.enabledCache.currentVersion()
	}
	nodes, err := s.nodes.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if s.enabledCache != nil {
		s.enabledCache.putIfVersion(nodes, now, version)
	}
	return nodes, nil
}

// InvalidateNodeCache makes the next NodesFor call reload enabled nodes from
// the repository. The cache version also prevents a DB read that began before
// invalidation from repopulating the cache with stale rows afterward.
func (s *Service) InvalidateNodeCache() {
	if s != nil && s.enabledCache != nil {
		s.enabledCache.invalidate()
	}
}
