package render

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func bodyOf(n int) *Output { return &Output{Body: make([]byte, n)} }

// An expired entry must be freed by the get that discovers it, not linger
// until a size threshold is crossed.
func TestRenderCache_GetFreesExpiredEntry(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	key := renderCacheKey{userID: 1, ct: domain.ClientMihomo}
	c.putIfVersion(key, bodyOf(1000), t0, c.currentVersion())
	if _, ok := c.get(key, t0.Add(61*time.Second)); ok {
		t.Fatal("expired entry must miss")
	}
	if c.size() != 0 || c.bytesUsed() != 0 {
		t.Fatalf("expired entry still held: size=%d bytes=%d", c.size(), c.bytesUsed())
	}
}

// Many distinct keys must never push the held body bytes past the budget, and
// the newest entries must be the survivors.
func TestRenderCache_ByteBudgetHolds(t *testing.T) {
	c := newRenderCache(time.Hour)
	c.maxBytes = 10_000
	t0 := time.Unix(1_700_000_000, 0)
	for i := 0; i < 500; i++ {
		c.putIfVersion(renderCacheKey{userID: int64(i), ct: domain.ClientMihomo}, bodyOf(900), t0, c.currentVersion())
		if c.bytesUsed() > c.maxBytes {
			t.Fatalf("budget exceeded after put %d: %d > %d", i, c.bytesUsed(), c.maxBytes)
		}
	}
	if _, ok := c.get(renderCacheKey{userID: 499, ct: domain.ClientMihomo}, t0); !ok {
		t.Fatal("newest entry must survive eviction")
	}
	if _, ok := c.get(renderCacheKey{userID: 0, ct: domain.ClientMihomo}, t0); ok {
		t.Fatal("oldest entry must have been evicted")
	}
}

// An entry larger than the whole budget is not cached (it would evict
// everything and still not fit); replacing a key must not double-count bytes.
func TestRenderCache_OversizeAndReplaceAccounting(t *testing.T) {
	c := newRenderCache(time.Hour)
	c.maxBytes = 1000
	t0 := time.Unix(1_700_000_000, 0)
	key := renderCacheKey{userID: 1, ct: domain.ClientMihomo}
	if c.putIfVersion(key, bodyOf(5000), t0, c.currentVersion()) {
		t.Fatal("oversize entry must not be cached")
	}
	c.putIfVersion(key, bodyOf(400), t0, c.currentVersion())
	c.putIfVersion(key, bodyOf(600), t0, c.currentVersion())
	if c.bytesUsed() != 600 || c.size() != 1 {
		t.Fatalf("replace accounting wrong: bytes=%d size=%d", c.bytesUsed(), c.size())
	}
	c.clear()
	if c.bytesUsed() != 0 {
		t.Fatalf("clear must zero the byte count, got %d", c.bytesUsed())
	}
}

// A put sweeps expired entries from the front of the expiry-ordered list
// without walking live ones.
func TestRenderCache_PutDropsExpiredFront(t *testing.T) {
	c := newRenderCache(60 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	for i := 0; i < 50; i++ {
		c.putIfVersion(renderCacheKey{userID: int64(i), ct: domain.ClientMihomo}, bodyOf(10), t0, c.currentVersion())
	}
	c.putIfVersion(renderCacheKey{userID: -1, ct: domain.ClientMihomo}, bodyOf(10), t0.Add(120*time.Second), c.currentVersion())
	if c.size() != 1 || c.bytesUsed() != 10 {
		t.Fatalf("expired entries not dropped: size=%d bytes=%d", c.size(), c.bytesUsed())
	}
}

func newTestService(fn func(context.Context, *domain.User, domain.ClientType) (*Output, error)) *Service {
	s := New(ports.Repos{}, nil, nil)
	s.renderFn = fn
	s.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return s
}

// N concurrent callers for one key trigger exactly one render and share its
// result.
func TestRenderForUserCached_MergesConcurrentCallers(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		calls.Add(1)
		<-release
		return &Output{Body: []byte("x")}, nil
	})
	const n = 20
	outs := make([]*Output, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := s.RenderForUserCached(context.Background(), &domain.User{ID: 7}, domain.ClientMihomo)
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
			outs[i] = o
		}()
	}
	time.Sleep(50 * time.Millisecond) // let callers pile onto the flight
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("renders=%d, want 1", got)
	}
	for i := 1; i < n; i++ {
		if outs[i] != outs[0] {
			t.Fatal("callers must share one Output")
		}
	}
	if outs[0].ETag == "" {
		t.Fatal("ETag must be computed once at render time")
	}
}

// A render error reaches every waiter and is not cached.
func TestRenderForUserCached_ErrorSharedNotCached(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	boom := errors.New("boom")
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		if calls.Add(1) == 1 {
			<-release
			return nil, boom
		}
		return &Output{Body: []byte("ok")}, nil
	})
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	for i, e := range errs {
		if !errors.Is(e, boom) {
			t.Fatalf("waiter %d got %v", i, e)
		}
	}
	if _, err := s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo); err != nil {
		t.Fatalf("error must not be cached: %v", err)
	}
}

// The first caller going away must not cancel the shared render for the rest.
func TestRenderForUserCached_FirstCallerCancelDoesNotCancelOthers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		close(started)
		select {
		case <-release:
			return &Output{Body: []byte("x")}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := s.RenderForUserCached(ctx1, &domain.User{ID: 3}, domain.ClientMihomo)
		first <- err
	}()
	<-started
	second := make(chan error, 1)
	go func() {
		_, err := s.RenderForUserCached(context.Background(), &domain.User{ID: 3}, domain.ClientMihomo)
		second <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel1()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller should see its own cancellation, got %v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("second caller must be unaffected: %v", err)
	}
}

// A panic inside the render releases every waiter with an error instead of
// deadlocking them.
func TestRenderForUserCached_PanicReleasesWaiters(t *testing.T) {
	release := make(chan struct{})
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		<-release
		panic("render blew up")
	})
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, err := s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo)
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for i := 0; i < 4; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("panic must surface as an error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter deadlocked after render panic")
		}
	}
}

// Distinct keys render in parallel but never more than the semaphore size.
func TestRenderForUserCached_SemaphoreCapsConcurrency(t *testing.T) {
	var cur, peak atomic.Int32
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return &Output{Body: []byte("x")}, nil
	})
	limit := cap(s.renderSem)
	if limit < 2 || limit != max(2, runtime.GOMAXPROCS(0)) {
		t.Fatalf("semaphore size %d", limit)
	}
	var wg sync.WaitGroup
	for i := 0; i < limit*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.RenderForUserCached(context.Background(), &domain.User{ID: int64(i + 1)}, domain.ClientMihomo); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if int(peak.Load()) > limit {
		t.Fatalf("peak concurrent renders %d > cap %d", peak.Load(), limit)
	}
}

// A waiter whose context ends while queued for a render slot returns the
// context error.
func TestRenderForUserCached_WaiterContextEndsWhileQueued(t *testing.T) {
	release := make(chan struct{})
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		<-release
		return &Output{Body: []byte("x")}, nil
	})
	defer close(release)
	for i := 0; i < cap(s.renderSem); i++ {
		s.renderSem <- struct{}{} // all slots busy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.RenderForUserCached(ctx, &domain.User{ID: 99}, domain.ClientMihomo)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

// InvalidateAll during an in-flight render must not cache the stale result,
// and a caller arriving after the invalidation must not join the pre-
// invalidation flight.
func TestRenderForUserCached_InvalidateDuringRender(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	s := newTestService(func(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
		n := calls.Add(1)
		started <- struct{}{}
		if n == 1 {
			<-release
			return &Output{Body: []byte("stale")}, nil
		}
		return &Output{Body: []byte("fresh")}, nil
	})
	done := make(chan *Output, 1)
	go func() {
		o, _ := s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo)
		done <- o
	}()
	<-started
	s.InvalidateAll()
	o2, err := s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo)
	if err != nil || string(o2.Body) != "fresh" {
		t.Fatalf("post-invalidate caller joined the stale flight: %v %v", o2, err)
	}
	close(release)
	<-done
	o3, _ := s.RenderForUserCached(context.Background(), &domain.User{ID: 1}, domain.ClientMihomo)
	if string(o3.Body) != "fresh" {
		t.Fatalf("stale render was cached over the fresh one: %s", o3.Body)
	}
}

// A render waiting on an upstream panel must not hold a render slot: the slots
// bound CPU and memory, and with every slot parked on a dead panel's 30 s
// timeout every other subscriber's cold render would queue behind them.
func TestRenderForUserCached_UpstreamWaitYieldsItsSlot(t *testing.T) {
	s := &Service{cache: newRenderCache(time.Minute), renderSem: make(chan struct{}, 2)}
	s.now = time.Now
	release := make(chan struct{})
	parked := make(chan struct{}, 2)
	s.renderFn = func(ctx context.Context, u *domain.User, _ domain.ClientType) (*Output, error) {
		if u.ID <= 2 { // these two wait on "the panel"
			withoutRenderSlot(ctx, func() {
				parked <- struct{}{}
				<-release
			})
		}
		return &Output{Body: []byte("ok")}, nil
	}
	var wg sync.WaitGroup
	for id := int64(1); id <= 2; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if _, err := s.RenderForUserCached(context.Background(), &domain.User{ID: id}, domain.ClientMihomo); err != nil {
				t.Errorf("user %d: %v", id, err)
			}
		}(id)
	}
	<-parked
	<-parked // both slots' holders are now waiting upstream

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.RenderForUserCached(ctx, &domain.User{ID: 3}, domain.ClientMihomo); err != nil {
		t.Fatalf("a third user's render queued behind renders that are only waiting on a panel: %v", err)
	}
	close(release)
	wg.Wait()
	if n := len(s.renderSem); n != 0 {
		t.Fatalf("%d slot(s) still held after every render finished", n)
	}
}

// Yielding outside a capped render (a preview, a test fixture) is a no-op, and
// a slot is never released twice.
func TestWithoutRenderSlot_NoSlotIsANoOp(t *testing.T) {
	ran := false
	withoutRenderSlot(context.Background(), func() { ran = true })
	if !ran {
		t.Fatal("fn did not run")
	}
}
