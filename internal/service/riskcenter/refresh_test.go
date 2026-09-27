package riskcenter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// outcomes reads psp_live_conn_refresh_total per outcome, so a test can
// assert on the difference its own calls made.
func outcomes() map[string]int64 {
	out := map[string]int64{}
	for _, o := range []string{"ok", "partial", "just_polled", "cooldown", "in_progress", "error"} {
		out[o] = metrics.LiveConnRefreshTotal.With(o).Value()
	}
	return out
}

func assertCounted(t *testing.T, before map[string]int64, outcome string) {
	t.Helper()
	after := outcomes()
	for o, n := range after {
		want := before[o]
		if o == outcome {
			want++
		}
		if n != want {
			t.Fatalf("psp_live_conn_refresh_total{outcome=%s} moved %d, want %d (only %s counted once)", o, n-before[o], want-before[o], outcome)
		}
	}
}

func throttled(t *testing.T, err error) *RefreshThrottled {
	t.Helper()
	var th *RefreshThrottled
	if !errors.As(err, &th) || !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("err = %v, want a *RefreshThrottled that is ErrResourceExhausted (a 429)", err)
	}
	return th
}

// One refresh reads every panel, so refreshes are rationed for the whole
// fleet: the second one inside the cooldown is refused with the time left,
// without a panel read, and the setting sets the cooldown.
func TestRefresh_CooldownRefusesWithRetryAfter(t *testing.T) {
	h := newHarness()
	if res, err := h.svc.Refresh(t.Context()); err != nil || !res.Refreshed {
		t.Fatalf("first refresh = %+v, %v; want a refresh", res, err)
	}
	h.now = h.now.Add(10 * time.Second)
	before := outcomes()
	_, err := h.svc.Refresh(t.Context())
	th := throttled(t, err)
	if th.Reason != "cooldown" || th.RetryAfter != 20*time.Second {
		t.Fatalf("refused with %s, retry after %v; want cooldown and the 20s left of 30s", th.Reason, th.RetryAfter)
	}
	assertCounted(t, before, "cooldown")
	if h.live.refreshCount() != 1 {
		t.Fatalf("panels read %d times, want once (the refused refresh read nothing)", h.live.refreshCount())
	}

	// The setting: a 60-second cooldown, 10 seconds in.
	h.settings.set.RiskLiveRefreshCooldownSeconds = 60
	_, err = h.svc.Refresh(t.Context())
	if th = throttled(t, err); th.RetryAfter != 50*time.Second {
		t.Fatalf("retry after %v, want the 50s left of the configured 60s", th.RetryAfter)
	}
	// Past it, the next one runs.
	h.now = h.now.Add(51 * time.Second)
	if res, err := h.svc.Refresh(t.Context()); err != nil || !res.Refreshed {
		t.Fatalf("refresh after the cooldown = %+v, %v", res, err)
	}
}

// One refresh at a time: a second request while the panels are still being
// read is refused as in progress, with a short retry, and never starts a
// second read of every panel.
func TestRefresh_InProgressRefuses(t *testing.T) {
	h := newHarness()
	started, release := make(chan struct{}), make(chan struct{})
	h.live.refresh = func(context.Context) (*domain.LiveConnSnapshot, error) {
		close(started)
		<-release
		return &domain.LiveConnSnapshot{TakenAt: testNow, Source: domain.LiveSnapshotFromRefresh}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Refresh(context.Background())
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("the first refresh returned (%v) without reading the panels", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the first refresh never read the panels")
	}
	before := outcomes()
	_, err := h.svc.Refresh(t.Context())
	th := throttled(t, err)
	if th.Reason != "in_progress" || th.RetryAfter != inProgressRetry {
		t.Fatalf("refused with %s, retry after %v; want in_progress and %v", th.Reason, th.RetryAfter, inProgressRetry)
	}
	assertCounted(t, before, "in_progress")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if h.live.refreshCount() != 1 {
		t.Fatalf("panels read %d times, want once", h.live.refreshCount())
	}
}

// Right after a poll advanced the per-node references, a refresh would read
// what the poll just read: 3X-UI rescans every ten seconds, so under one
// scan later every node it advanced still reads "not rescanned" against
// them, and the reading would publish an empty view. The traffic service
// decides that where it copies the references (domain.ErrLiveJustPolled),
// and the stored snapshot is the answer: not refreshed, reason
// just_polled, counted as such and not as an error — and the cooldown is
// not consumed, since no panel was asked.
//
// The snapshot on display does not decide it: a fresh poll snapshot is
// handed to the traffic service like any other, because a refresh that
// finished after a poll can hide the poll's snapshot while its references
// still stand, and a poll can merge its references before it stores its
// snapshot.
func TestRefresh_JustPolledReadsNoPanelAndKeepsTheCooldown(t *testing.T) {
	h := newHarness()
	polled := pollSnapshot(testNow.Add(-5 * time.Second))
	h.live.snap = polled
	h.live.refresh = func(context.Context) (*domain.LiveConnSnapshot, error) {
		return nil, fmt.Errorf("refresh live connections: %w", domain.ErrLiveJustPolled)
	}
	before := outcomes()
	res, err := h.svc.Refresh(t.Context())
	if err != nil {
		t.Fatalf("err = %v; a refresh the traffic service declined as just polled is an answer, not an error", err)
	}
	if res.Refreshed || res.Reason != "just_polled" || res.Snapshot != polled {
		t.Fatalf("result = %+v, want not refreshed, just_polled, the stored snapshot", res)
	}
	assertCounted(t, before, "just_polled")
	if h.live.refreshCount() != 1 {
		t.Fatalf("the traffic service was asked %d times, want once: it decides just_polled, not the snapshot on display", h.live.refreshCount())
	}

	// The cooldown was not consumed: a second later the panels are read.
	h.now = h.now.Add(time.Second)
	h.live.refresh = nil
	if res, err = h.svc.Refresh(t.Context()); err != nil || !res.Refreshed {
		t.Fatalf("refresh a second after a just_polled answer = %+v, %v; want a refresh", res, err)
	}
	// And that one did consume it.
	h.now = h.now.Add(time.Second)
	if _, err := h.svc.Refresh(t.Context()); throttled(t, err).Reason != "cooldown" {
		t.Fatal("a refresh that read the panels left the cooldown unconsumed")
	}
}

// A refresh that failed still read (or tried to read) every panel, so it
// consumes the cooldown like one that succeeded: a failing panel is exactly
// when an admin clicks again and again.
func TestRefresh_FailedRefreshStillConsumesTheCooldown(t *testing.T) {
	h := newHarness()
	boom := errors.New("shared clients unreadable")
	h.live.refresh = func(context.Context) (*domain.LiveConnSnapshot, error) { return nil, boom }
	before := outcomes()
	if _, err := h.svc.Refresh(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the refresh's error", err)
	}
	assertCounted(t, before, "error")
	h.now = h.now.Add(time.Second)
	if _, err := h.svc.Refresh(t.Context()); throttled(t, err).Reason != "cooldown" {
		t.Fatal("a failed refresh left the cooldown unconsumed")
	}
}

// A panel whose adapter has no live read (S-UI) is not a failed read: a
// refresh over it is ok, not partial — partial is kept for the reading that
// really missed a panel, which is when an admin must not trust the view as
// complete. The result names the panels either way.
func TestRefresh_UnsupportedPanelsAreNotPartial(t *testing.T) {
	h := newHarness()
	h.panels.panels = []*domain.XUIPanel{{ID: 2, Name: "hk-1"}, {ID: 4, Name: "sui-1"}}
	h.live.refresh = func(context.Context) (*domain.LiveConnSnapshot, error) {
		return &domain.LiveConnSnapshot{TakenAt: testNow, Source: domain.LiveSnapshotFromRefresh, PanelsAsked: 2, Unsupported: []int64{4}}, nil
	}
	before := outcomes()
	res, err := h.svc.Refresh(t.Context())
	if err != nil || !res.Refreshed {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	assertCounted(t, before, "ok")
	if len(res.Panels) != 2 || res.Panels[0] != (PanelRef{ID: 2, Name: "hk-1"}) {
		t.Fatalf("panels = %+v, want every panel named, by id", res.Panels)
	}

	h.now = h.now.Add(time.Hour)
	h.live.refresh = func(context.Context) (*domain.LiveConnSnapshot, error) {
		return &domain.LiveConnSnapshot{TakenAt: h.now, Source: domain.LiveSnapshotFromRefresh, PanelsAsked: 2, Unread: []int64{2}, Unsupported: []int64{4}}, nil
	}
	before = outcomes()
	if _, err := h.svc.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertCounted(t, before, "partial")
}

// A refresh is bounded: however long a panel takes to answer, the request
// gets its answer within liveRefreshTimeout, the refresh ends, and the next
// one (after the cooldown) can start — a hung panel must not leave the
// button "in progress" for good.
func TestRefresh_TimesOut(t *testing.T) {
	h := newHarness()
	var deadline time.Time
	h.live.refresh = func(ctx context.Context) (*domain.LiveConnSnapshot, error) {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	h.svc.refreshTimeout = 20 * time.Millisecond
	start := time.Now()
	if _, err := h.svc.Refresh(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if deadline.IsZero() || deadline.Sub(start) > time.Second {
		t.Fatalf("the refresh ran without its own deadline (deadline %v)", deadline)
	}
	h.now = h.now.Add(time.Hour)
	h.live.refresh = nil
	if res, err := h.svc.Refresh(t.Context()); err != nil || !res.Refreshed {
		t.Fatalf("the refresh after a timed-out one = %+v, %v; want it to run", res, err)
	}
	if New(Deps{}).refreshTimeout != liveRefreshTimeout || liveRefreshTimeout != 45*time.Second {
		t.Fatalf("a refresh is bounded by %v, want 45s", New(Deps{}).refreshTimeout)
	}
}

// A refresh is one reading of every panel for every admin, bought with the
// fleet-wide cooldown. An admin closing the tab mid-read (net/http cancels
// the request's context) must not cut it short: cut, every panel still
// being read would answer "cancelled" and the reading would list them all
// as unread — failures that never happened — or be lost, the cooldown spent
// either way. So the panels are read on the request's values (the backend
// operation gate's admission rides on them) but not its cancellation, still
// bounded by liveRefreshTimeout.
func TestRefresh_ClientDisconnectDoesNotCutTheReading(t *testing.T) {
	h := newHarness()
	type admission struct{}
	reqCtx, disconnect := context.WithCancel(context.WithValue(t.Context(), admission{}, "admitted"))
	defer disconnect()
	started, release := make(chan struct{}), make(chan struct{})
	var value any
	var deadline time.Time
	h.live.refresh = func(ctx context.Context) (*domain.LiveConnSnapshot, error) {
		value = ctx.Value(admission{})
		deadline, _ = ctx.Deadline()
		close(started)
		<-release
		// A real adapter's request fails once its context is done.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &domain.LiveConnSnapshot{TakenAt: testNow, Source: domain.LiveSnapshotFromRefresh}, nil
	}
	type result struct {
		res RefreshResult
		err error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		res, err := h.svc.Refresh(reqCtx)
		done <- result{res, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never read the panels")
	}
	disconnect()
	close(release)
	got := <-done
	if got.err != nil || !got.res.Refreshed {
		t.Fatalf("refresh = %+v, %v; want the reading completed although the admin went away", got.res, got.err)
	}
	if value != "admitted" {
		t.Fatalf("the reading's context carries %v, want the request's values (the operation gate's admission)", value)
	}
	if deadline.IsZero() || deadline.Sub(start) > liveRefreshTimeout+time.Second {
		t.Fatalf("the reading ran without its %v bound (deadline %v)", liveRefreshTimeout, deadline)
	}
}
