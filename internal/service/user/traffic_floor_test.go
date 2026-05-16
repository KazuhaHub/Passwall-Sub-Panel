package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

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
