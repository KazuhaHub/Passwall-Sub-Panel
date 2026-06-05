package loginguard

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeEvents is a minimal ports.AuthEventRepo that returns a canned
// RecentAuthFailures result and records the arguments it was called with.
type fakeEvents struct {
	count    int64
	lastAt   time.Time
	err      error
	calls    int
	gotIP    string
	gotUPN   string
	gotSince time.Time
}

func (f *fakeEvents) RecentAuthFailures(_ context.Context, ip, upn string, since time.Time) (int64, time.Time, error) {
	f.calls++
	f.gotIP, f.gotUPN, f.gotSince = ip, upn, since
	return f.count, f.lastAt, f.err
}
func (f *fakeEvents) Insert(context.Context, *domain.AuthEvent) error { return nil }
func (f *fakeEvents) List(context.Context, ports.AuthEventFilter) ([]*domain.AuthEvent, int64, error) {
	return nil, 0, nil
}
func (f *fakeEvents) DeleteBefore(context.Context, time.Time) (int64, error) { return 0, nil }
func (f *fakeEvents) CountByReasonSince(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

func guardAt(f *fakeEvents, now time.Time) *Guard {
	g := New(f)
	g.now = func() time.Time { return now }
	return g
}

func TestEvaluate_BothDisabled_SkipsDBRead(t *testing.T) {
	f := &fakeEvents{count: 999}
	g := guardAt(f, time.Now())
	d, err := g.Evaluate(context.Background(), ports.UISettings{}, "1.1.1.1", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	if d.Locked || d.CaptchaRequired {
		t.Fatalf("disabled guard must be inert, got %+v", d)
	}
	if f.calls != 0 {
		t.Fatalf("must not touch the DB when both features are off, calls=%d", f.calls)
	}
}

func TestEvaluate_CaptchaAlways_NoCountNeeded(t *testing.T) {
	f := &fakeEvents{}
	g := guardAt(f, time.Now())
	s := ports.UISettings{CaptchaEnabled: true, CaptchaTrigger: "always"}
	d, err := g.Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	if !d.CaptchaRequired {
		t.Fatal("always-mode captcha must be required")
	}
	if f.calls != 0 {
		t.Fatalf("always mode needs no failure count, calls=%d", f.calls)
	}
}

func TestEvaluate_CaptchaAfterFailures(t *testing.T) {
	now := time.Now()
	base := ports.UISettings{
		CaptchaEnabled: true, CaptchaTrigger: "after_failures", CaptchaFailThreshold: 3,
		LockoutWindowMinutes: 15, LockoutScope: "ip_upn",
	}
	// Below threshold → not required.
	f := &fakeEvents{count: 2}
	d, _ := guardAt(f, now).Evaluate(context.Background(), base, "1.1.1.1", "a@x")
	if d.CaptchaRequired {
		t.Fatal("2 < 3 must not require captcha")
	}
	// At threshold → required.
	f = &fakeEvents{count: 3}
	d, _ = guardAt(f, now).Evaluate(context.Background(), base, "1.1.1.1", "a@x")
	if !d.CaptchaRequired {
		t.Fatal("3 >= 3 must require captcha")
	}
}

func TestEvaluate_LockoutActiveAndExpired(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	s := ports.UISettings{
		LockoutEnabled: true, LockoutThreshold: 10,
		LockoutWindowMinutes: 15, LockoutDurationMinutes: 15, LockoutScope: "ip_upn",
	}
	// 10 failures, last one 5 min ago → locked, retry ~10 min.
	f := &fakeEvents{count: 10, lastAt: now.Add(-5 * time.Minute)}
	d, err := guardAt(f, now).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Locked {
		t.Fatal("must be locked")
	}
	if want := 10 * time.Minute; (d.RetryAfter - want).Abs() > time.Second {
		t.Fatalf("RetryAfter = %v, want ~%v", d.RetryAfter, want)
	}
	// Same count but last failure 20 min ago (> duration) → lock expired.
	f = &fakeEvents{count: 10, lastAt: now.Add(-20 * time.Minute)}
	d, _ = guardAt(f, now).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if d.Locked {
		t.Fatal("lock must expire once duration has passed since the last failure")
	}
}

func TestEvaluate_ScopeIPDropsUPN(t *testing.T) {
	now := time.Now()
	f := &fakeEvents{count: 0}
	s := ports.UISettings{LockoutEnabled: true, LockoutThreshold: 10, LockoutWindowMinutes: 15, LockoutScope: "ip"}
	_, _ = guardAt(f, now).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if f.gotIP != "1.1.1.1" || f.gotUPN != "" {
		t.Fatalf("ip scope must query ip-only: gotIP=%q gotUPN=%q", f.gotIP, f.gotUPN)
	}
}

func TestEvaluate_WindowCoversDuration(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	f := &fakeEvents{count: 0}
	// window 15 but duration 60 → count window must span the longer 60 min so
	// the configured lock duration is actually enforceable.
	s := ports.UISettings{LockoutEnabled: true, LockoutThreshold: 10, LockoutWindowMinutes: 15, LockoutDurationMinutes: 60, LockoutScope: "ip_upn"}
	_, _ = guardAt(f, now).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if want := now.Add(-60 * time.Minute); !f.gotSince.Equal(want) {
		t.Fatalf("since = %v, want %v (max of window/duration)", f.gotSince, want)
	}
}

func TestEvaluate_HugeDurationDoesNotOverflowOpen(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	// An absurd duration must NOT overflow time.Duration into a negative value
	// (which would put `until` in the past and the count window in the future,
	// silently disabling the lock — a fail-open).
	s := ports.UISettings{
		LockoutEnabled: true, LockoutThreshold: 10,
		LockoutWindowMinutes: 15, LockoutDurationMinutes: 2147483647, LockoutScope: "ip_upn",
	}
	f := &fakeEvents{count: 10, lastAt: now.Add(-1 * time.Minute)}
	d, err := guardAt(f, now).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Locked {
		t.Fatal("an absurdly large lock duration must stay locked, not overflow into fail-open")
	}
	if f.gotSince.After(now) {
		t.Fatalf("count window must not overflow into the future: since=%v now=%v", f.gotSince, now)
	}
}

func TestEvaluate_DBErrorPropagates(t *testing.T) {
	f := &fakeEvents{err: context.DeadlineExceeded}
	s := ports.UISettings{LockoutEnabled: true, LockoutThreshold: 10, LockoutWindowMinutes: 15}
	_, err := guardAt(f, time.Now()).Evaluate(context.Background(), s, "1.1.1.1", "a@x")
	if err == nil {
		t.Fatal("DB error must propagate so the handler can fail open deliberately")
	}
}
