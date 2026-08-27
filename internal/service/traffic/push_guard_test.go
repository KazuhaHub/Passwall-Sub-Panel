package traffic

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// countingPusher records how many floor pushes actually reached the pusher.
type countingPusher struct{ calls atomic.Int64 }

func (p *countingPusher) PushClientConfig(context.Context, int64) error {
	p.calls.Add(1)
	return nil
}

func (p *countingPusher) WithEmergencyLock(fn func()) { fn() }

// activeUserPoll builds the minimal fixture that produces a non-zero delta for
// one user, which is the only condition under which the floor push fires. The
// returned wait func drains the fire-and-forget push goroutines, without which
// every assertion below would race the thing it is asserting on; bump raises
// the panel's reported counters so a SECOND poll also sees a delta (after one
// poll the client's LastRaw has caught up, and an idle user never pushes).
func activeUserPoll(t *testing.T, pusher UserConfigPusher) (svc *Service, drain func(), bump func()) {
	t.Helper()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficLimitBytes: 1 << 40},
	}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
			LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200}},
	}}
	panel := &fakeXUIClient{inbounds: []ports.Inbound{
		{ID: 20, ClientStats: []ports.ClientTraffic{{Email: "u1@psp.local", Up: 700, Down: 500}}},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{10: panel}}
	svc = New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)
	svc.SetConfigPusher(pusher)
	var wg sync.WaitGroup
	svc.SetBgWG(&wg)
	bump = func() {
		st := panel.inbounds[0].ClientStats
		st[0].Up += 1000
		st[0].Down += 1000
	}
	return svc, wg.Wait, bump
}

// Baseline for the guard test below: with no backlog, an active user's floor
// push is enqueued and runs. Without this, a guard that suppressed everything
// unconditionally would still pass the suppression test.
func TestPollOnce_FloorPushRunsWhenTheQueueIsClear(t *testing.T) {
	metrics.Reset()
	pusher := &countingPusher{}
	svc, drain, _ := activeUserPoll(t, pusher)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	drain()

	if got := pusher.calls.Load(); got != 1 {
		t.Fatalf("push calls = %d, want 1", got)
	}
	if got := metrics.PushSuppressedTotal.Value(); got != 0 {
		t.Errorf("suppressed = %d, want 0", got)
	}
	if got := metrics.PushSemCarryoverTotal.Value(); got != 0 {
		t.Errorf("carryover = %d, want 0", got)
	}
}

// The guard's whole point: a cycle that opens with the previous cycle's pushes
// still QUEUED must drop this cycle's pushes rather than lengthen the queue.
// Without it, an overloaded deployment does not degrade linearly — every cycle
// hands the next one a longer queue (docs/data-plane-plan.md §1.4).
func TestPollOnce_SuppressesPushesWhileThePreviousCycleIsStillQueued(t *testing.T) {
	metrics.Reset()
	pusher := &countingPusher{}
	svc, drain, _ := activeUserPoll(t, pusher)

	// Simulate the previous cycle's leftovers. Driving the gauge directly is
	// what keeps this a test of the GUARD rather than of how many goroutines
	// it takes to saturate a semaphore.
	metrics.PushSemWaiting.Inc()
	defer metrics.PushSemWaiting.Dec()

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	drain()

	if got := pusher.calls.Load(); got != 0 {
		t.Fatalf("push calls = %d, want 0 — the guard must not enqueue onto a backed-up semaphore", got)
	}
	if got := metrics.PushSuppressedTotal.Value(); got != 1 {
		t.Errorf("suppressed = %d, want 1", got)
	}
	if got := metrics.PushSemCarryoverTotal.Value(); got != 1 {
		t.Errorf("carryover = %d, want 1", got)
	}
}

// In-flight work at cycle open is the normal tail of the previous cycle
// finishing, not a backlog. Tripping on it would suppress pushes on a healthy
// deployment every time a cycle happened to overlap a slow panel by a
// millisecond — so the trip condition is a queue, and this pins that.
func TestPollOnce_InFlightPushesAloneDoNotTripTheGuard(t *testing.T) {
	metrics.Reset()
	pusher := &countingPusher{}
	svc, drain, _ := activeUserPoll(t, pusher)

	metrics.PushSemInflight.Inc()
	defer metrics.PushSemInflight.Dec()

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	drain()

	if got := pusher.calls.Load(); got != 1 {
		t.Fatalf("push calls = %d, want 1 — in-flight work is not a backlog", got)
	}
	if got := metrics.PushSemCarryoverTotal.Value(); got != 0 {
		t.Errorf("carryover = %d, want 0", got)
	}
}

// The guard must un-trip on its own once the queue drains — otherwise one
// transient overload would silence the floor safety net permanently.
func TestPollOnce_GuardReleasesOnceTheQueueDrains(t *testing.T) {
	metrics.Reset()
	pusher := &countingPusher{}
	svc, drain, bump := activeUserPoll(t, pusher)

	metrics.PushSemWaiting.Inc()
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce (backlogged): %v", err)
	}
	drain()
	metrics.PushSemWaiting.Dec()

	bump() // the user keeps using traffic, so the next cycle has a delta again
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce (drained): %v", err)
	}
	drain()

	if got := pusher.calls.Load(); got != 1 {
		t.Fatalf("push calls = %d, want 1 (suppressed cycle, then a normal one)", got)
	}
}
