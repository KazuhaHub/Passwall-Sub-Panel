package render

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// ComputeWeakETag returns the weak ETag the /sub handler advertises: 16 hex
// chars (8 bytes of SHA-256) of the body. It is computed once per render and
// stored on the cached Output instead of re-hashing a 0.5-2 MB body per poll.
// The digest is only an HTTP cache validator: never stored as, or compared
// with, a password verifier, so SHA-256 is appropriate for this non-password
// hash even though the body can contain proxy credentials.
func ComputeWeakETag(body []byte) string {
	// codeql[go/weak-sensitive-data-hashing]
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// subRenderCacheTTL bounds how stale a /sub response may be. The polling fleet
// re-fetches an unchanged config on a timer (Cache-Control is no-cache, forcing
// revalidation), and the body-hash ETag means every poll otherwise pays a full
// render + group/node/separator/traffic reads. Caching the rendered Output for
// this window collapses repeat polls to a map lookup; a config change (nodes,
// group, template, settings, the user's UUID/token) propagates within ≤TTL,
// which is acceptable for subscription delivery. Errors are never cached.
const subRenderCacheTTL = 60 * time.Second

// renderCacheMaxBytes caps the summed len(Body) the cache may hold. One entry
// is a whole rendered subscription (0.5-2 MB with the default rule set), so an
// entry count says nothing about memory: a few hundred active subscribers would
// otherwise pin hundreds of MB for a 60 s benefit. 64 MiB keeps roughly the
// 30-100 most recently polled configs hot (a polling fleet re-fetches on a
// timer, so recency is what matters) while bounding the worst case regardless
// of how many distinct (user, client type) keys churn through.
const renderCacheMaxBytes = 64 << 20

// renderCacheKey scopes a cached render to one user AND one client type — two
// users (or two formats for one user) never share an entry, so there is no
// cross-tenant / cross-format leakage.
type renderCacheKey struct {
	userID int64
	ct     domain.ClientType
}

type renderCacheEntry struct {
	key     renderCacheKey
	out     *Output
	expires time.Time
}

// renderCache is a small TTL cache for rendered /sub Outputs. The cached
// *Output is shared read-only across requests (the sub handler treats Output as
// immutable — it serves the body and copies headers, never mutates), so no
// per-request copy is needed.
//
// Entries live in an insertion-ordered list. Every entry gets the same TTL, so
// insertion order is (to within the render-duration skew of the `now` a caller
// captured before rendering) expiry order: expired entries are dropped from the
// front, and the byte budget evicts from the same end. Neither needs a walk of
// the whole map, so put stays O(1) amortised on the hot path.
type renderCache struct {
	mu       sync.Mutex
	m        map[renderCacheKey]*list.Element // value is *renderCacheEntry
	order    *list.List                       // front = oldest
	bytes    int
	maxBytes int
	ttl      time.Duration
	version  uint64
}

func newRenderCache(ttl time.Duration) *renderCache {
	return &renderCache{
		m:        make(map[renderCacheKey]*list.Element),
		order:    list.New(),
		maxBytes: renderCacheMaxBytes,
		ttl:      ttl,
	}
}

// removeLocked drops one element and its byte accounting. Caller holds mu.
func (c *renderCache) removeLocked(el *list.Element) {
	e := el.Value.(*renderCacheEntry)
	c.order.Remove(el)
	delete(c.m, e.key)
	c.bytes -= len(e.out.Body)
}

func (c *renderCache) get(key renderCacheKey, now time.Time) (*Output, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*renderCacheEntry)
	if !now.Before(e.expires) {
		// Free the body now rather than waiting for a later put to notice it.
		c.removeLocked(el)
		return nil, false
	}
	return e.out, true
}

func (c *renderCache) currentVersion() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// putIfVersion stores out unless the cache was cleared since version was read
// (the stale-render guard) or the body alone exceeds the whole budget (caching
// it would evict everything else and still not fit).
func (c *renderCache) putIfVersion(key renderCacheKey, out *Output, now time.Time, version uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != version {
		return false
	}
	if old, ok := c.m[key]; ok {
		c.removeLocked(old)
	}
	if len(out.Body) > c.maxBytes {
		return false
	}
	c.m[key] = c.order.PushBack(&renderCacheEntry{key: key, out: out, expires: now.Add(c.ttl)})
	c.bytes += len(out.Body)
	// Drop expired entries from the old end, then evict oldest-first until the
	// budget holds. The entry just added is at the back, so it is never the
	// victim (it fits on its own, checked above).
	for el := c.order.Front(); el != nil && el != c.order.Back(); el = c.order.Front() {
		if c.bytes <= c.maxBytes && now.Before(el.Value.(*renderCacheEntry).expires) {
			break
		}
		c.removeLocked(el)
	}
	return true
}

func (c *renderCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *renderCache) bytesUsed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

func (c *renderCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = make(map[renderCacheKey]*list.Element)
	c.order.Init()
	c.bytes = 0
	c.version++
}

// renderFlightTimeout bounds one shared render. The render runs on a context
// detached from every caller (so the first caller leaving cannot cancel it for
// the others), which also means nothing else would stop a wedged upstream read;
// this is that backstop. Generous: a cold render of the default template is
// seconds at worst.
const renderFlightTimeout = 2 * time.Minute

// renderSlots sizes the global render semaphore. A render of the default
// template allocates tens of MB and is CPU-bound, so more in flight than cores
// only adds memory pressure and latency for everyone; min 2 so one slow render
// never blocks all others on a single-core host.
func renderSlots() int { return max(2, runtime.GOMAXPROCS(0)) }

// RenderForUserCached is the /sub entry point: it returns a cached render for
// (user, clientType) within subRenderCacheTTL and otherwise renders fresh via
// RenderForUser and caches the result. Concurrent misses for the same key (and
// the same cache version) share one render and its result or error; the
// number of renders running at once is capped by a semaphore, and a caller
// waiting on either returns its own context error if its request ends first.
// Always-fresh callers must use RenderForUser directly.
func (s *Service) RenderForUserCached(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
	if ct == "" {
		ct = domain.ClientMihomo
	}
	key := renderCacheKey{userID: u.ID, ct: ct}
	now := s.now()
	if out, ok := s.cache.get(key, now); ok {
		return out, nil
	}
	version := s.cache.currentVersion()
	// The version is part of the flight key: a caller arriving after
	// InvalidateAll must not join a render that began before it (that render
	// may have read pre-invalidation nodes/rules and is not cached either).
	fkey := strconv.FormatInt(u.ID, 10) + "/" + string(ct) + "/" + strconv.FormatUint(version, 10)
	ch := s.flights.DoChan(fkey, func() (v any, err error) {
		// Only the caller that starts the flight runs this closure. Its render
		// context keeps that caller's values (logger, trace) but not its
		// cancellation, so waiters do not fail because caller #1 left. The
		// closure runs on singleflight's own goroutine, shielded by the
		// recover below instead of safego (which cannot wrap a goroutine we
		// do not start).
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), renderFlightTimeout)
		defer cancel()
		// A panic must still release every waiter (singleflight would re-raise
		// it on a bare goroutine and crash the process), so convert it here.
		defer func() {
			if r := recover(); r != nil {
				log.Error("render panic recovered", "user_id", u.ID, "panic", r, "stack", string(debug.Stack()))
				v, err = nil, fmt.Errorf("render panicked: %v", r)
			}
		}()
		// A flight that finished between our cache miss and our turn already
		// stored its result; do not render twice.
		if out, ok := s.cache.get(key, s.now()); ok {
			return out, nil
		}
		// A Service built without New (test fixtures) has no semaphore and
		// renders uncapped; production always goes through New.
		if s.renderSem != nil {
			select {
			case s.renderSem <- struct{}{}:
			case <-rctx.Done():
				return nil, rctx.Err()
			}
			defer func() { <-s.renderSem }()
		}
		render := s.renderFn
		if render == nil {
			render = s.RenderForUser
		}
		out, err := render(rctx, u, ct)
		if err != nil {
			return nil, err
		}
		if out.ETag == "" {
			out.ETag = ComputeWeakETag(out.Body)
		}
		s.cache.putIfVersion(key, out, now, version)
		return out, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*Output), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// InvalidateAll drops every rendered subscription variant. Incrementing the
// cache version also stops a render that began before this call from restoring
// a stale output after the map has been cleared.
func (s *Service) InvalidateAll() {
	if s != nil && s.cache != nil {
		s.cache.clear()
	}
}
