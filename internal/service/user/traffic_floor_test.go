package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeFloorSettingsRepo is the tiniest SettingsRepo stub: only Load is
// exercised by trafficFloor. Other methods return zero values so the
// type still satisfies the interface.
type fakeFloorSettingsRepo struct {
	cfg ports.UISettings
	err error
}

func (r *fakeFloorSettingsRepo) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	if r.err != nil {
		return ports.UISettings{}, r.err
	}
	return r.cfg, nil
}

func (r *fakeFloorSettingsRepo) Save(_ context.Context, _ ports.UISettings) error { return nil }

func futureTime(offsetHours int) *time.Time {
	t := time.Now().Add(time.Duration(offsetHours) * time.Hour)
	return &t
}

func pastTime(offsetHours int) *time.Time {
	t := time.Now().Add(-time.Duration(offsetHours) * time.Hour)
	return &t
}

// fakeUsageReader is the smallest possible TrafficUsageReader stub: lets a
// test pin "current period usage" without standing up the traffic service.
type fakeUsageReader struct {
	used int64
	err  error
}

func (f *fakeUsageReader) CurrentPeriodUsage(_ context.Context, _ *domain.User) (int64, error) {
	return f.used, f.err
}

func TestTrafficFloor_DelegatesToReader(t *testing.T) {
	s := &Service{trafficUsage: &fakeUsageReader{used: 3_000}}
	u := &domain.User{ID: 1, TrafficLimitBytes: 10_000}
	if got := s.trafficFloor(context.Background(), u); got != 7_000 {
		t.Fatalf("limit=10000 used=3000 → got %d, want 7000", got)
	}
}

func TestTrafficFloor_NilUserSafe(t *testing.T) {
	s := &Service{trafficUsage: &fakeUsageReader{used: 999}}
	if got := s.trafficFloor(context.Background(), nil); got != 0 {
		t.Fatalf("nil user must short-circuit to 0, got %d", got)
	}
}

func TestTrafficFloor_UnlimitedUserSkipsRead(t *testing.T) {
	// Reader returns a poisoned error; trafficFloor must short-circuit
	// before calling it because TrafficLimitBytes == 0.
	s := &Service{trafficUsage: &fakeUsageReader{err: errors.New("must not be called")}}
	u := &domain.User{ID: 1, TrafficLimitBytes: 0}
	if got := s.trafficFloor(context.Background(), u); got != 0 {
		t.Fatalf("unlimited user must return 0 without reading usage, got %d", got)
	}
}

func TestTrafficFloor_NilReaderDegradesToUnlimited(t *testing.T) {
	// Early-start path: trafficUsage hasn't been wired yet. Must NOT
	// crash; degrades to "unlimited on 3X-UI side" (= status quo).
	s := &Service{trafficUsage: nil}
	u := &domain.User{ID: 1, TrafficLimitBytes: 10_000}
	if got := s.trafficFloor(context.Background(), u); got != 0 {
		t.Fatalf("nil reader must degrade to 0, got %d", got)
	}
}

func TestTrafficFloor_ReaderErrorDegradesToUnlimited(t *testing.T) {
	// Snapshot table hiccup must not stop the rest of the push: degrade
	// to 0 (3X-UI unlimited) and let the next poll re-try.
	s := &Service{trafficUsage: &fakeUsageReader{err: errors.New("db boom")}}
	u := &domain.User{ID: 1, TrafficLimitBytes: 10_000}
	if got := s.trafficFloor(context.Background(), u); got != 0 {
		t.Fatalf("reader err must degrade to 0, got %d", got)
	}
}

func TestTrafficFloor_AtOrPastLimitReturnsOne(t *testing.T) {
	// Same edge case TestTrafficFloorBytes covers at the pure-func level,
	// but verified through the integration path (reader + lookup + math).
	s := &Service{trafficUsage: &fakeUsageReader{used: 10_000}}
	u := &domain.User{ID: 1, TrafficLimitBytes: 10_000}
	if got := s.trafficFloor(context.Background(), u); got != 1 {
		t.Fatalf("limit==used → got %d, want 1", got)
	}
}

// --- Emergency-access interplay ---
//
// These tests pin the invariant that the floor pushed to 3X-UI honors
// an active emergency window. Without these checks, the floor reverts
// to the over-limit sentinel (1 byte) and 3X-UI disables the user on
// its next traffic tick, silently undoing the panel-side emergency
// grant — the bug introduced when fad13a3 first added the floor.

func TestTrafficFloor_EmergencyActive_UnlimitedQuotaReturnsZero(t *testing.T) {
	// Admin configured "emergency window is the only cap, no extra
	// byte cap on top" (EmergencyAccessQuotaGB == 0). Floor should
	// match: 0 (unlimited on 3X-UI side) so the user can actually use
	// the window the panel just opened.
	s := &Service{
		trafficUsage: &fakeUsageReader{used: 999_999},
		settings:     &fakeFloorSettingsRepo{cfg: ports.UISettings{EmergencyAccessQuotaGB: 0}},
	}
	u := &domain.User{
		ID: 1, TrafficLimitBytes: 10_000, // already over
		EmergencyUntil: futureTime(2),
	}
	if got := s.trafficFloor(context.Background(), u); got != 0 {
		t.Fatalf("emergency active + quota=0 → got %d, want 0 (unlimited)", got)
	}
}

func TestTrafficFloor_EmergencyActive_QuotaRemainingReturnsRemaining(t *testing.T) {
	// 5 GB quota window, user has burned 2 GB inside the window.
	// Expect 3 GB pushed to 3X-UI as the floor so 3X-UI itself will
	// flip the user off after another 3 GB of use even if the panel
	// goes offline.
	quotaGB := 5
	usedSinceWindowOpened := int64(2) * 1024 * 1024 * 1024
	s := &Service{
		trafficUsage: &fakeUsageReader{used: 999_999_999_999}, // poisoned, must not be consulted
		settings:     &fakeFloorSettingsRepo{cfg: ports.UISettings{EmergencyAccessQuotaGB: quotaGB}},
	}
	u := &domain.User{
		ID:                     1,
		TrafficLimitBytes:      10_000,
		EmergencyUntil:         futureTime(2),
		LifetimeTotalBytes:     usedSinceWindowOpened, // baseline is 0
		EmergencyBaselineBytes: 0,
	}
	want := int64(quotaGB)*1024*1024*1024 - usedSinceWindowOpened
	if got := s.trafficFloor(context.Background(), u); got != want {
		t.Fatalf("emergency active + 5GB quota + 2GB used → got %d, want %d", got, want)
	}
}

func TestTrafficFloor_EmergencyActive_QuotaExhaustedReturnsSentinel(t *testing.T) {
	// User crossed the in-window quota. Floor flips to 1 (the same
	// "you're over, disable" sentinel the over-limit path uses). The
	// traffic poll's own quota check will tear down EmergencyUntil
	// shortly after; until then 3X-UI doing it locally is fine.
	quotaGB := 5
	exhausted := int64(quotaGB)*1024*1024*1024 + 100
	s := &Service{
		settings: &fakeFloorSettingsRepo{cfg: ports.UISettings{EmergencyAccessQuotaGB: quotaGB}},
	}
	u := &domain.User{
		ID:                     1,
		TrafficLimitBytes:      10_000,
		EmergencyUntil:         futureTime(2),
		LifetimeTotalBytes:     exhausted,
		EmergencyBaselineBytes: 0,
	}
	if got := s.trafficFloor(context.Background(), u); got != 1 {
		t.Fatalf("emergency active + quota exhausted → got %d, want 1", got)
	}
}

func TestTrafficFloor_EmergencyExpired_FallsBackToNormalMath(t *testing.T) {
	// EmergencyUntil is in the past — treat as ordinary over-limit
	// user: TrafficFloorBytes(limit, used) wins.
	s := &Service{
		trafficUsage: &fakeUsageReader{used: 12_000}, // over limit
		settings:     &fakeFloorSettingsRepo{cfg: ports.UISettings{EmergencyAccessQuotaGB: 5}},
	}
	u := &domain.User{
		ID: 1, TrafficLimitBytes: 10_000,
		EmergencyUntil: pastTime(1), // already lapsed
	}
	if got := s.trafficFloor(context.Background(), u); got != 1 {
		t.Fatalf("emergency expired → expected fallback to over-limit sentinel 1, got %d", got)
	}
}

func TestTrafficFloor_EmergencyActive_SettingsLoadErrorDefaultsUnlimited(t *testing.T) {
	// Settings load hiccup while emergency is open: fail OPEN, not
	// closed — silently re-disabling a user the admin just granted
	// access to would be the worse of the two errors. The traffic
	// poll independently re-checks the quota each cycle so the cap
	// gets re-enforced server-side regardless.
	s := &Service{
		settings: &fakeFloorSettingsRepo{err: errors.New("db down")},
	}
	u := &domain.User{
		ID: 1, TrafficLimitBytes: 10_000, EmergencyUntil: futureTime(2),
	}
	if got := s.trafficFloor(context.Background(), u); got != 0 {
		t.Fatalf("emergency + settings err → got %d, want 0 (fail open)", got)
	}
}

func TestTrafficFloorBytes(t *testing.T) {
	cases := []struct {
		name        string
		limit, used int64
		want        int64
	}{
		{"unlimited user → 3X-UI unlimited", 0, 0, 0},
		{"unlimited user with usage → still unlimited", 0, 5_000_000, 0},
		{"unlimited user with negative-leak limit", -1, 100, 0},
		{"limit > used → remaining", 10_000, 3_000, 7_000},
		{"limit = used → 1 (not 0, would mean unlimited)", 10_000, 10_000, 1},
		{"used over limit → 1 (forces 3X-UI disable on next tick)", 10_000, 15_000, 1},
		{"used over limit by tiny amount → 1", 10_000, 10_001, 1},
		{"fresh user, no usage yet → full limit", 5_000_000, 0, 5_000_000},
		{"realistic 10GB cap, 4GB used → 6GB", 10 * 1024 * 1024 * 1024, 4 * 1024 * 1024 * 1024, 6 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TrafficFloorBytes(tc.limit, tc.used)
			if got != tc.want {
				t.Fatalf("TrafficFloorBytes(%d, %d) = %d, want %d", tc.limit, tc.used, got, tc.want)
			}
		})
	}
}
