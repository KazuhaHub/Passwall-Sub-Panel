package mysql

import (
	"context"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeSettingsInner is a controllable ports.SettingsRepo stand-in so the cache
// decorator's discipline can be exercised in isolation — no DB needed. loadHook,
// if set, fires inside Load AFTER the current value is snapshotted but BEFORE it
// is returned, which is exactly the window cachingSettingsRepo's miss-path runs
// the inner read in. That lets a test deterministically land a Save between the
// inner read and the cache populate (the gen-mismatch path).
type fakeSettingsInner struct {
	mu       sync.Mutex
	val      ports.UISettings
	loads    int
	saves    int
	loadHook func()
}

func (f *fakeSettingsInner) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	f.mu.Lock()
	f.loads++
	v := f.val
	hook := f.loadHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return v, nil
}

func (f *fakeSettingsInner) Save(_ context.Context, s ports.UISettings) error {
	f.mu.Lock()
	f.val = s
	f.saves++
	f.mu.Unlock()
	return nil
}

func (f *fakeSettingsInner) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads
}

func (f *fakeSettingsInner) setHook(h func()) {
	f.mu.Lock()
	f.loadHook = h
	f.mu.Unlock()
}

// TestCachingSettings_LoadMissThenHit pins the core win: a populated cache serves
// subsequent Loads without re-hitting the inner repo (the /sub hot path reason
// the decorator exists). First Load is a miss (one inner read); the second is a
// hit (zero further inner reads).
func TestCachingSettings_LoadMissThenHit(t *testing.T) {
	inner := &fakeSettingsInner{val: ports.UISettings{SiteTitle: "Panel"}}
	cache := NewCachingSettingsRepo(inner)
	ctx := context.Background()

	if _, err := cache.Load(ctx, ports.UISettings{}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if got := inner.loadCount(); got != 1 {
		t.Fatalf("miss should hit inner exactly once, got %d", got)
	}
	out, err := cache.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if got := inner.loadCount(); got != 1 {
		t.Errorf("hit must NOT hit inner again, inner loads = %d (want 1)", got)
	}
	if out.SiteTitle != "Panel" {
		t.Errorf("cached value lost: SiteTitle = %q, want Panel", out.SiteTitle)
	}
}

// TestCachingSettings_SaveInvalidates pins TTL=0 invalidate-on-write: after a
// Save, the next Load must round-trip the inner repo (not serve the pre-save
// cached value) so an admin edit is visible immediately on /sub.
func TestCachingSettings_SaveInvalidates(t *testing.T) {
	inner := &fakeSettingsInner{val: ports.UISettings{SiteTitle: "before"}}
	cache := NewCachingSettingsRepo(inner)
	ctx := context.Background()

	if _, err := cache.Load(ctx, ports.UISettings{}); err != nil { // populate
		t.Fatalf("load: %v", err)
	}
	loadsAfterPopulate := inner.loadCount()

	if err := cache.Save(ctx, ports.UISettings{SiteTitle: "after"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := cache.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("post-save load: %v", err)
	}
	if inner.loadCount() != loadsAfterPopulate+1 {
		t.Errorf("Save must invalidate so the next Load round-trips; inner loads = %d, want %d", inner.loadCount(), loadsAfterPopulate+1)
	}
	if out.SiteTitle != "after" {
		t.Errorf("post-save Load served stale value %q, want after", out.SiteTitle)
	}
}

// TestCachingSettings_HitReappliesCallerDefaults pins that the cache stores the
// canonical value with EMPTY defaults and re-overlays the caller's defaults on
// every read — so callers passing fallbacks (render's SiteTitle, mailer's
// AppTitle) still get them on a cache hit, not just on the first miss.
func TestCachingSettings_HitReappliesCallerDefaults(t *testing.T) {
	inner := &fakeSettingsInner{val: ports.UISettings{}} // SiteTitle empty in the "DB"
	cache := NewCachingSettingsRepo(inner)
	ctx := context.Background()

	// Miss populates the cache with the empty-default value.
	if _, err := cache.Load(ctx, ports.UISettings{SiteTitle: "FallbackA"}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	// Hit: a DIFFERENT caller default must still land for the empty field.
	out, err := cache.Load(ctx, ports.UISettings{SiteTitle: "FallbackB"})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if inner.loadCount() != 1 {
		t.Fatalf("expected a cache hit (1 inner load), got %d", inner.loadCount())
	}
	if out.SiteTitle != "FallbackB" {
		t.Errorf("hit did not re-apply caller default: SiteTitle = %q, want FallbackB", out.SiteTitle)
	}
}

// TestCachingSettings_GenMismatchSkipsStalePopulate is the load-bearing one: the
// gen snapshot prevents a Save that lands BETWEEN the miss-path inner read and
// the populate from durably caching the now-stale value. Without the gen check
// the cache would serve the pre-save value forever (no TTL). This is the exact
// single-gen discipline the v3.8.0 per-scope seqlock cache reuses, so it must be
// pinned before it's multiplied.
//
// Deterministic, not timing-based: the inner load hook fires a Save in the very
// window the cache reads the inner repo.
func TestCachingSettings_GenMismatchSkipsStalePopulate(t *testing.T) {
	inner := &fakeSettingsInner{val: ports.UISettings{SiteTitle: "A"}}
	cache := NewCachingSettingsRepo(inner)
	ctx := context.Background()

	var once sync.Once
	inner.setHook(func() {
		once.Do(func() {
			// A concurrent admin save commits B and bumps the cache gen while our
			// miss-path Load is mid inner-read (it already snapshotted "A").
			if err := cache.Save(ctx, ports.UISettings{SiteTitle: "B"}); err != nil {
				t.Errorf("save in hook: %v", err)
			}
		})
	})

	// This Load read "A" (a valid past snapshot) but MUST NOT cache it, because
	// the gen moved under it. Returning A here is fine; caching A is the bug.
	if _, err := cache.Load(ctx, ports.UISettings{}); err != nil {
		t.Fatalf("racing load: %v", err)
	}
	inner.setHook(nil)
	loadsBefore := inner.loadCount()

	// If A had been cached, this serves stale A with no further inner read. The
	// gen check forces a round-trip that returns the committed B.
	got, err := cache.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("post-race load: %v", err)
	}
	if got.SiteTitle != "B" {
		t.Fatalf("served %q after a Save raced the populate; stale value was cached (want B)", got.SiteTitle)
	}
	if inner.loadCount() == loadsBefore {
		t.Errorf("expected a DB round-trip after the skipped populate, but no inner load happened")
	}
}

// TestCachingSettings_ConcurrentLoadSaveRace is a race-detector probe: many
// goroutines interleave Load and Save. Asserts only that nothing deadlocks /
// races and a final read works — run with `go test -race`.
func TestCachingSettings_ConcurrentLoadSaveRace(t *testing.T) {
	inner := &fakeSettingsInner{val: ports.UISettings{SiteTitle: "init"}}
	cache := NewCachingSettingsRepo(inner)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if j%5 == 0 {
					_ = cache.Save(ctx, ports.UISettings{SiteTitle: "v"})
				} else {
					_, _ = cache.Load(ctx, ports.UISettings{})
				}
			}
		}()
	}
	wg.Wait()
	if _, err := cache.Load(ctx, ports.UISettings{}); err != nil {
		t.Fatalf("final load after concurrent churn: %v", err)
	}
}
