package sqlstore

import (
	"context"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeScopeInner is a controllable ports.ScopeSettingsRepo stand-in so the
// per-scope cache decorator's discipline can be exercised without a DB. loadHook,
// if set, fires inside ListOverrides AFTER the value is snapshotted but BEFORE it
// is returned — the exact window cachingScopeRepo's miss-path runs the inner read
// in, so a test can deterministically land a write between the read and the
// populate (the gen-mismatch path).
type fakeScopeInner struct {
	mu       sync.Mutex
	rows     map[string][]ports.ScopeOverride
	lists    int
	loadHook func()
}

func newFakeScopeInner() *fakeScopeInner {
	return &fakeScopeInner{rows: map[string][]ports.ScopeOverride{}}
}

func (f *fakeScopeInner) key(scopeType string, scopeID int64) string {
	return scopeCacheKey(scopeType, scopeID)
}

func (f *fakeScopeInner) ListOverrides(_ context.Context, scopeType string, scopeID int64) ([]ports.ScopeOverride, error) {
	f.mu.Lock()
	f.lists++
	src := f.rows[f.key(scopeType, scopeID)]
	out := make([]ports.ScopeOverride, len(src))
	copy(out, src)
	hook := f.loadHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

func (f *fakeScopeInner) SetOverride(_ context.Context, scopeType string, scopeID int64, o ports.ScopeOverride) error {
	f.mu.Lock()
	f.rows[f.key(scopeType, scopeID)] = []ports.ScopeOverride{o} // replace, enough for cache tests
	f.mu.Unlock()
	return nil
}

func (f *fakeScopeInner) DeleteOverride(_ context.Context, scopeType string, scopeID int64, _, _ string) error {
	f.mu.Lock()
	delete(f.rows, f.key(scopeType, scopeID))
	f.mu.Unlock()
	return nil
}

func (f *fakeScopeInner) DeleteScope(_ context.Context, scopeType string, scopeID int64) error {
	f.mu.Lock()
	delete(f.rows, f.key(scopeType, scopeID))
	f.mu.Unlock()
	return nil
}

func (f *fakeScopeInner) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

func (f *fakeScopeInner) setHook(h func()) {
	f.mu.Lock()
	f.loadHook = h
	f.mu.Unlock()
}

// TestCachingScope_MissThenHit pins the core win: a populated cache serves
// subsequent ListOverrides without re-hitting the inner repo (the /sub hot-path
// reason the decorator exists). First call is a miss (one inner read); the second
// is a hit (zero further inner reads).
func TestCachingScope_MissThenHit(t *testing.T) {
	inner := newFakeScopeInner()
	_ = inner.SetOverride(context.Background(), "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "1"})
	cache := NewCachingScopeRepo(inner)
	ctx := context.Background()

	listsBefore := inner.listCount()
	if _, err := cache.ListOverrides(ctx, "group", 1); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if got := inner.listCount(); got != listsBefore+1 {
		t.Fatalf("miss should hit inner exactly once, got %d", got-listsBefore)
	}
	out, err := cache.ListOverrides(ctx, "group", 1)
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := inner.listCount(); got != listsBefore+1 {
		t.Errorf("hit must NOT hit inner again, inner lists = %d (want %d)", got, listsBefore+1)
	}
	if len(out) != 1 || out[0].Value != "1" {
		t.Errorf("cached overrides lost: %+v", out)
	}
}

// TestCachingScope_EmptyIsCached: the common case (a group with NO overrides)
// must also be a cache hit — otherwise every inheriting group re-queries the DB
// on every /sub, which is precisely the cost the cache removes.
func TestCachingScope_EmptyIsCached(t *testing.T) {
	inner := newFakeScopeInner()
	cache := NewCachingScopeRepo(inner)
	ctx := context.Background()

	if _, err := cache.ListOverrides(ctx, "group", 9); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if _, err := cache.ListOverrides(ctx, "group", 9); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if inner.listCount() != 1 {
		t.Errorf("an empty (no-override) scope must be cached too; inner lists = %d, want 1", inner.listCount())
	}
}

// TestCachingScope_WriteInvalidates pins TTL=0 invalidate-on-write: after any
// mutation the next ListOverrides must round-trip the inner repo so an admin's
// per-group edit is visible immediately on /sub.
func TestCachingScope_WriteInvalidates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(c ports.ScopeSettingsRepo) error
	}{
		{"SetOverride", func(c ports.ScopeSettingsRepo) error {
			return c.SetOverride(context.Background(), "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "0"})
		}},
		{"DeleteOverride", func(c ports.ScopeSettingsRepo) error {
			return c.DeleteOverride(context.Background(), "group", 1, "sub", "sub_region_flag_prefix")
		}},
		{"DeleteScope", func(c ports.ScopeSettingsRepo) error {
			return c.DeleteScope(context.Background(), "group", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := newFakeScopeInner()
			_ = inner.SetOverride(context.Background(), "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "1"})
			cache := NewCachingScopeRepo(inner)
			ctx := context.Background()

			if _, err := cache.ListOverrides(ctx, "group", 1); err != nil { // populate
				t.Fatalf("populate list: %v", err)
			}
			listsAfterPopulate := inner.listCount()
			if err := tc.write(cache); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := cache.ListOverrides(ctx, "group", 1); err != nil {
				t.Fatalf("post-write list: %v", err)
			}
			if inner.listCount() != listsAfterPopulate+1 {
				t.Errorf("%s must invalidate so the next list round-trips; inner lists = %d, want %d",
					tc.name, inner.listCount(), listsAfterPopulate+1)
			}
		})
	}
}

// TestCachingScope_GenMismatchSkipsStalePopulate is the load-bearing one: the gen
// snapshot prevents a write that lands BETWEEN the miss-path inner read and the
// populate from durably caching the now-stale override set. Same single-gen
// discipline as cachingSettingsRepo, multiplied per scope.
func TestCachingScope_GenMismatchSkipsStalePopulate(t *testing.T) {
	inner := newFakeScopeInner()
	_ = inner.SetOverride(context.Background(), "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "A"})
	cache := NewCachingScopeRepo(inner)
	ctx := context.Background()

	var once sync.Once
	inner.setHook(func() {
		once.Do(func() {
			// A concurrent admin write commits B and bumps the cache gen while our
			// miss-path list is mid inner-read (it already snapshotted "A").
			if err := cache.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "B"}); err != nil {
				t.Errorf("write in hook: %v", err)
			}
		})
	})

	// This list read "A" (a valid past snapshot) but MUST NOT cache it, because the
	// gen moved under it. Returning A here is fine; caching A is the bug.
	if _, err := cache.ListOverrides(ctx, "group", 1); err != nil {
		t.Fatalf("racing list: %v", err)
	}
	inner.setHook(nil)
	listsBefore := inner.listCount()

	// If A had been cached this serves stale A with no further inner read. The gen
	// check forces a round-trip that returns the committed B.
	got, err := cache.ListOverrides(ctx, "group", 1)
	if err != nil {
		t.Fatalf("post-race list: %v", err)
	}
	if len(got) != 1 || got[0].Value != "B" {
		t.Fatalf("served %+v after a write raced the populate; stale set was cached (want Value=B)", got)
	}
	if inner.listCount() == listsBefore {
		t.Errorf("expected a DB round-trip after the skipped populate, but no inner list happened")
	}
}

// TestCachingScope_ReturnedSliceIsolated: a caller mutating the returned slice
// must not corrupt the cached copy (the resolver iterates it, but defense in
// depth — the global cache returns by value for the same reason).
func TestCachingScope_ReturnedSliceIsolated(t *testing.T) {
	inner := newFakeScopeInner()
	_ = inner.SetOverride(context.Background(), "group", 1, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "1"})
	cache := NewCachingScopeRepo(inner)
	ctx := context.Background()

	first, _ := cache.ListOverrides(ctx, "group", 1) // populate
	if len(first) == 1 {
		first[0].Value = "MUTATED"
	}
	second, _ := cache.ListOverrides(ctx, "group", 1) // hit
	if len(second) != 1 || second[0].Value != "1" {
		t.Errorf("cache served a caller-mutated value: %+v", second)
	}
}

// TestCachingScope_ConcurrentRace is a race-detector probe: many goroutines
// interleave list and write across scopes. Asserts only that nothing deadlocks /
// races and a final read works.
func TestCachingScope_ConcurrentRace(t *testing.T) {
	inner := newFakeScopeInner()
	cache := NewCachingScopeRepo(inner)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			gid := int64(id%3 + 1)
			for j := 0; j < 100; j++ {
				if j%5 == 0 {
					_ = cache.SetOverride(ctx, "group", gid, ports.ScopeOverride{Type: "sub", Name: "sub_region_flag_prefix", Value: "v"})
				} else {
					_, _ = cache.ListOverrides(ctx, "group", gid)
				}
			}
		}(i)
	}
	wg.Wait()
	if _, err := cache.ListOverrides(ctx, "group", 1); err != nil {
		t.Fatalf("final list after concurrent churn: %v", err)
	}
}
