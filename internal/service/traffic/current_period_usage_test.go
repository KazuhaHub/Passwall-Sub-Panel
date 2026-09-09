package traffic

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const gib = int64(1) << 30

// The enforcement hole this closes. periodUsage became O(1) off the user row
// (lifetime - baseline) and stopped consulting the snapshot, but its public
// wrapper kept loading one and branching on that read. LatestForUser answers
// ErrNotFound when the user has no snapshot row and (nil, err) on a DB failure,
// and BOTH were swallowed into (0, nil).
//
// Usage then read as zero for a user whose own counters said otherwise. The one
// caller is user.trafficFloor, so the floor became TrafficFloorBytes(limit, 0)
// = the FULL limit, and PSP pushed a panel cap of panelLifetime + entire quota
// — handing a user who had already burned most of their period a second one to
// spend during the next PSP outage.
//
// A user with no snapshot is ordinary: retention prunes them
// (rawTrafficRetentionDays), and a v2 import carries period counters without
// any snapshot history.
func TestPeriodUsage_IgnoresMissingSnapshot(t *testing.T) {
	svc := &Service{traffic: &fakeTrafficRepo{}} // no snapshots → ErrNotFound
	u := &domain.User{ID: 7, LifetimeTotalBytes: 190 * gib, PeriodBaselineBytes: 0}

	got, err := svc.periodUsage(context.Background(), u, nil)
	if err != nil {
		t.Fatalf("periodUsage = %v, want nil error", err)
	}
	if want := u.PeriodUsed(); got != want {
		t.Fatalf("usage = %d, want %d (the user row's own counters); a missing snapshot "+
			"says nothing about consumption, and reading 0 here pushes a full-quota floor", got, want)
	}
}

// The period baseline is what makes this a PERIOD figure rather than a lifetime
// one, so it has to survive the same path.
func TestPeriodUsage_SubtractsPeriodBaseline(t *testing.T) {
	svc := &Service{traffic: &fakeTrafficRepo{}}
	u := &domain.User{ID: 8, LifetimeTotalBytes: 500 * gib, PeriodBaselineBytes: 460 * gib}

	got, err := svc.periodUsage(context.Background(), u, nil)
	if err != nil {
		t.Fatalf("periodUsage = %v, want nil error", err)
	}
	if got != 40*gib {
		t.Fatalf("usage = %d, want %d", got, 40*gib)
	}
}

// A nil user is still 0 — the floor path calls this before it has decided
// whether the user is worth pushing for.
func TestPeriodUsage_NilUser(t *testing.T) {
	svc := &Service{traffic: &fakeTrafficRepo{}}
	got, err := svc.periodUsage(context.Background(), nil, nil)
	if err != nil || got != 0 {
		t.Fatalf("periodUsage(nil) = (%d, %v), want (0, nil)", got, err)
	}
}
