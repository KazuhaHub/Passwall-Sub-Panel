package sqlstore

import (
	"context"
	"strconv"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// cachingScopeRepo wraps a ports.ScopeSettingsRepo with a per-scope in-process
// cache of each scope's sparse override set, so the /sub hot path doesn't run a
// `SELECT … FROM scope_settings` for every request once a group's overrides are
// known. It is the per-scope sibling of cachingSettingsRepo and reuses the exact
// same single-gen seqlock discipline pinned by settings_cache_test.go:
//
//   - ListOverrides: RLock fast path; a miss snapshots `gen`, runs the inner read
//     OUTSIDE the lock, then populates ONLY if no write landed in between
//     (gen unchanged) and no concurrent populate already filled the key. Without
//     the gen check, a write that committed NEW overrides between the inner read
//     (which saw OLD) and the populate would durably cache the stale set — there
//     is no TTL, so cache=OLD/DB=NEW would persist until the next write.
//   - SetOverride / DeleteOverride / DeleteScope: forward to inner, then clear the
//     WHOLE map and bump gen. Clearing all (rather than the one touched key) keeps
//     the invalidation trivially correct — writes are rare admin actions, the
//     re-fetch cost is one inner read per affected scope on its next /sub.
//
// The override set is cached merged-NOTHING: it holds the raw per-scope rows. The
// effective value is still computed fresh by the resolver (global ⊕ overrides) on
// every LoadForGroup, so a global Save (which invalidates cachingSettingsRepo)
// needs no coordination here — the two layers cache independent data and the merge
// re-runs each call.
type cachingScopeRepo struct {
	inner ports.ScopeSettingsRepo
	mu    sync.RWMutex
	// cached maps scopeCacheKey -> that scope's override set. Presence of the key
	// (even with a nil/empty slice) is a hit; absence is a miss. Stored as a clone
	// so a caller mutating the returned slice can't corrupt the cache.
	cached map[string][]ports.ScopeOverride
	gen    uint64
}

// NewCachingScopeRepo wraps inner with the per-scope in-process cache.
func NewCachingScopeRepo(inner ports.ScopeSettingsRepo) ports.ScopeSettingsRepo {
	return &cachingScopeRepo{inner: inner, cached: map[string][]ports.ScopeOverride{}}
}

func scopeCacheKey(scopeType string, scopeID int64) string {
	return scopeType + "/" + strconv.FormatInt(scopeID, 10)
}

func cloneOverrides(in []ports.ScopeOverride) []ports.ScopeOverride {
	if in == nil {
		return nil
	}
	// ScopeOverride is a flat value struct (strings + bool), so a shallow element
	// copy is a deep copy.
	out := make([]ports.ScopeOverride, len(in))
	copy(out, in)
	return out
}

func (r *cachingScopeRepo) ListOverrides(ctx context.Context, scopeType string, scopeID int64) ([]ports.ScopeOverride, error) {
	key := scopeCacheKey(scopeType, scopeID)

	r.mu.RLock()
	if v, ok := r.cached[key]; ok {
		out := cloneOverrides(v)
		r.mu.RUnlock()
		return out, nil
	}
	gen := r.gen
	r.mu.RUnlock()

	loaded, err := r.inner.ListOverrides(ctx, scopeType, scopeID)
	if err != nil {
		// Don't populate on error — the next call retries the inner repo, same
		// error path the uncached resolver had.
		return nil, err
	}

	r.mu.Lock()
	// Populate only if (a) no other ListOverrides already filled this key, and
	// (b) no write landed since our gen snapshot. On a gen mismatch we skip caching
	// and let the next call re-fetch the post-write truth.
	if _, exists := r.cached[key]; !exists && r.gen == gen {
		r.cached[key] = cloneOverrides(loaded)
	}
	r.mu.Unlock()
	return loaded, nil
}

func (r *cachingScopeRepo) SetOverride(ctx context.Context, scopeType string, scopeID int64, o ports.ScopeOverride) error {
	if err := r.inner.SetOverride(ctx, scopeType, scopeID, o); err != nil {
		return err
	}
	r.invalidate()
	return nil
}

func (r *cachingScopeRepo) DeleteOverride(ctx context.Context, scopeType string, scopeID int64, typ, name string) error {
	if err := r.inner.DeleteOverride(ctx, scopeType, scopeID, typ, name); err != nil {
		return err
	}
	r.invalidate()
	return nil
}

func (r *cachingScopeRepo) DeleteScope(ctx context.Context, scopeType string, scopeID int64) error {
	if err := r.inner.DeleteScope(ctx, scopeType, scopeID); err != nil {
		return err
	}
	r.invalidate()
	return nil
}

// invalidate clears the whole cache and bumps gen. Mirrors cachingSettingsRepo's
// nil-on-write: clearing rather than surgically evicting one key keeps the
// invalidation trivially correct, and writes are rare enough that the extra
// re-fetch on other scopes' next /sub is immaterial.
func (r *cachingScopeRepo) invalidate() {
	r.mu.Lock()
	r.cached = map[string][]ports.ScopeOverride{}
	r.gen++
	r.mu.Unlock()
}
