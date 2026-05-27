package mysql

import (
	"context"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// cachingSettingsRepo wraps a ports.SettingsRepo with a single-value
// in-process cache so the hot paths (sub render, traffic poll,
// reconcile, mailer, paneltz) don't fan into the DB for the same row
// dozens of times per request / per cycle.
//
// Pre-v3.6.1-beta.4 each `Settings.Load` ran a full `SELECT * FROM
// settings` + unmarshaled ~40 KV descriptors. The render package alone
// hits Load 4–6 times per `/sub/:token` request (region-flag check,
// profile placeholders, update-interval header, buildProxies,
// buildProfileName, traffic snapshot lookup), so on a polling fleet
// the settings table was the dominant per-request cost.
//
// Semantics this decorator preserves:
//
//   - Load(ctx, defaults): returns the same shape as the inner repo's
//     Load. On a cache hit the cached value is overlaid with the
//     caller's defaults via applyUISettingsDefaults so callers
//     supplying fallbacks (render's SiteTitle / LogoURL, mailer's
//     AppTitle) still get them when DB rows are absent.
//   - Save(ctx, s): forwards to inner, then refreshes the cache with
//     `s` so an admin save is immediately visible on the next Load —
//     no TTL window where /sub serves the pre-save value. This mirrors
//     subPathCache's invalidate-on-write contract in router.go.
//   - errors on inner.Load: the cache is NOT populated; the next call
//     retries the inner repo. callers see the same error path they had
//     pre-cache.
//
// Concurrency: RWMutex guards the cached pointer. Reads take RLock;
// writes (Save + cache populate after a Load miss) take Lock. The
// per-Load critical section is just a pointer copy.
type cachingSettingsRepo struct {
	inner ports.SettingsRepo
	mu    sync.RWMutex
	// cached is the most-recent successfully-loaded UISettings with
	// EMPTY defaults applied. nil = miss (initial state, or after an
	// uncached error path). Stored by value, not pointer to the
	// caller's struct, so a caller mutating the returned value can't
	// race the cache.
	cached *ports.UISettings
}

// NewCachingSettingsRepo wraps inner with the in-process cache.
func NewCachingSettingsRepo(inner ports.SettingsRepo) ports.SettingsRepo {
	return &cachingSettingsRepo{inner: inner}
}

func (r *cachingSettingsRepo) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	r.mu.RLock()
	if r.cached != nil {
		out := *r.cached
		r.mu.RUnlock()
		// Re-apply caller's defaults: cached value was loaded with
		// empty defaults so caller-supplied fallbacks (e.g. render's
		// SiteTitle) still need to land for fields where the DB row
		// is empty. applyUISettingsDefaults is idempotent for hardcoded
		// numeric fallbacks (the cached value already has them) and
		// fills the 5 caller-controlled string fields.
		return applyUISettingsDefaults(out, defaults), nil
	}
	r.mu.RUnlock()

	// Miss. Inner Load runs with EMPTY defaults so the cached value is
	// canonical across callers regardless of who triggered the first
	// load. Apply caller's defaults on top of the result we return now.
	loaded, err := r.inner.Load(ctx, ports.UISettings{})
	if err != nil {
		return defaults, err
	}
	r.mu.Lock()
	// Double-check: another concurrent Load may have populated while
	// we were on the inner call. Either wins; both produce the same
	// canonical value (deterministic given the DB state at fetch time).
	if r.cached == nil {
		cp := loaded
		r.cached = &cp
	}
	r.mu.Unlock()
	return applyUISettingsDefaults(loaded, defaults), nil
}

func (r *cachingSettingsRepo) Save(ctx context.Context, s ports.UISettings) error {
	if err := r.inner.Save(ctx, s); err != nil {
		// Don't touch the cache on save failure — the next Load should
		// fall back to whatever the DB actually holds.
		return err
	}
	// Invalidate rather than overwrite. Pre-v3.6.1-beta.5 we wrote `s`
	// into the cache directly, but `inner.Save` and the cache write are
	// two unsynchronized steps: concurrent Save(A) + Save(B) could land
	// at the DB in (A, B) order but at the cache in (B, A) order,
	// leaving DB=vB / cache=vA persistently. Setting cached=nil forces
	// the next Load to round-trip the DB and re-populate from truth.
	// Admin edits remain visible immediately (same TTL=0 semantic) —
	// the cost is one extra Load per Save, which is fine because Save
	// is rare and Load is the path we want fast.
	r.mu.Lock()
	r.cached = nil
	r.mu.Unlock()
	return nil
}
