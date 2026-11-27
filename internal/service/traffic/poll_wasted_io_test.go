package traffic

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// wastedIOFixture builds n users with one owned client each. lastOnlineMs is
// what the fake panel reports as every client's lastOnline (unix ms).
func wastedIOFixture(n int, lastOnlineMs int64) (*fakeUserRepo, *fakeTrafficRepo, *Service, *fakeXUIClient) {
	users := &fakeUserRepo{users: map[int64]*domain.User{}}
	byUser := map[int64][]*domain.XUIClientEntry{}
	stats := make([]ports.ClientTraffic, 0, n)
	for uid := int64(1); uid <= int64(n); uid++ {
		users.users[uid] = &domain.User{ID: uid, Enabled: true}
		email := fmt.Sprintf("u%d@example.test", uid)
		byUser[uid] = []*domain.XUIClientEntry{{
			ID: uid, UserID: uid, PanelID: 10, InboundID: 20,
			ClientEmail: email, CreatedAt: time.Now(),
		}}
		stats = append(stats, ports.ClientTraffic{Email: email, Up: 100, Down: 200, LastOnline: lastOnlineMs})
	}
	repo := &fakeTrafficRepo{}
	xc := &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: stats}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{10: xc}}
	svc := New(users, &fakeOwnershipRepo{byUser: byUser}, repo, nil, nil, pool, &fakeDisabler{})
	return users, repo, svc, xc
}

// Steady state: once every user is bootstrapped (baseline stamped, lifetime
// seeded) and the panel reports the same lastOnline as already stored, a tick
// must neither scan the snapshot table nor rewrite last_online_at.
func TestPollOnce_SteadyStateSkipsSnapshotScanAndLastOnlineWrites(t *testing.T) {
	const n = 50
	ms := time.Now().Add(-time.Minute).UnixMilli()
	users, repo, svc, _ := wastedIOFixture(n, ms)

	// Tick 1: bootstrap. Every user is fresh, so the prefetch must cover them
	// all and every last_online_at is a first write.
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce 1: %v", err)
	}
	if repo.latestForUsersCalls != 1 || len(repo.latestForUsersIDs) != n {
		t.Fatalf("bootstrap tick: LatestForUsers calls=%d ids=%d, want 1 call over %d users",
			repo.latestForUsersCalls, len(repo.latestForUsersIDs), n)
	}
	if users.lastOnlineRowsWritten != n {
		t.Fatalf("bootstrap tick: last_online writes = %d, want %d", users.lastOnlineRowsWritten, n)
	}

	// Tick 2..3: steady state.
	repo.latestForUsersCalls, repo.latestForUsersIDs = 0, nil
	users.lastOnlineRowsWritten = 0
	for i := 0; i < 2; i++ {
		if err := svc.PollOnce(context.Background()); err != nil {
			t.Fatalf("PollOnce steady: %v", err)
		}
	}
	if len(repo.latestForUsersIDs) != 0 {
		t.Errorf("steady state fetched snapshots for %d users, want 0", len(repo.latestForUsersIDs))
	}
	if repo.latestForUserCalls != 0 {
		t.Errorf("steady state hit the per-user LatestForUser fallback %d times, want 0", repo.latestForUserCalls)
	}
	if users.lastOnlineRowsWritten != 0 {
		t.Errorf("steady state wrote last_online_at %d times, want 0", users.lastOnlineRowsWritten)
	}
}

// A user still on the one-time migration path (lifetime 0, but old aggregate
// snapshots exist) must still receive its prev snapshot, while a settled
// neighbour is not fetched at all.
func TestPollOnce_PrefetchesOnlyBootstrapUsers(t *testing.T) {
	users, repo, svc, _ := wastedIOFixture(2, 0)
	base := time.Now().Add(-time.Hour)
	// User 1: settled. User 2: baseline stamped but lifetime 0 + an old snapshot.
	users.users[1].LifetimeBaselineAt = &base
	users.users[1].LifetimeTotalBytes = 5
	users.users[1].RawLifetimeTotalBytes = 5
	users.users[2].LifetimeBaselineAt = &base
	repo.snapshots = []*domain.TrafficSnapshot{
		{UserID: 2, UpBytes: 1000, DownBytes: 2000, TotalBytes: 3000, CapturedAt: base},
	}
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(repo.latestForUsersIDs) != 1 || repo.latestForUsersIDs[0] != 2 {
		t.Fatalf("prefetched ids = %v, want [2]", repo.latestForUsersIDs)
	}
	if repo.latestForUserCalls != 0 {
		t.Errorf("per-user fallback fired %d times, want 0", repo.latestForUserCalls)
	}
	// Seeded from prev (3000) plus this tick's delta (300).
	if got := users.users[2].LifetimeTotalBytes; got != 3300 {
		t.Errorf("migrated user lifetime = %d, want 3300", got)
	}
}

// last_online_at is written exactly once per real advance, and never goes
// backwards on a stale/lower reading. Millisecond precision matters: a value
// that differs from the stored one only below one second is still an advance.
func TestPollOnce_LastOnlineWrittenOnlyOnAdvance(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	users, _, svc, xc := wastedIOFixture(1, t0.UnixMilli())
	run := func() {
		t.Helper()
		if err := svc.PollOnce(context.Background()); err != nil {
			t.Fatalf("PollOnce: %v", err)
		}
	}
	setMs := func(ms int64) {
		st := xc.inbounds[0].ClientStats
		for i := range st {
			st[i].LastOnline = ms
		}
	}

	run() // nil stored -> first write
	if users.lastOnlineRowsWritten != 1 {
		t.Fatalf("first reading: writes = %d, want 1", users.lastOnlineRowsWritten)
	}
	run() // unchanged
	if users.lastOnlineRowsWritten != 1 {
		t.Fatalf("unchanged reading: writes = %d, want 1", users.lastOnlineRowsWritten)
	}
	setMs(t0.UnixMilli() - 5000) // stale reading (panel outage): no write, no regression
	run()
	if users.lastOnlineRowsWritten != 1 {
		t.Fatalf("older reading: writes = %d, want 1", users.lastOnlineRowsWritten)
	}
	if got := users.users[1].LastOnlineAt.UnixMilli(); got != t0.UnixMilli() {
		t.Fatalf("stored last_online regressed to %d, want %d", got, t0.UnixMilli())
	}
	setMs(t0.UnixMilli() + 1) // sub-second advance is a real advance
	run()
	if users.lastOnlineRowsWritten != 2 {
		t.Fatalf("1ms advance: writes = %d, want 2", users.lastOnlineRowsWritten)
	}
	run() // and is not rewritten
	if users.lastOnlineRowsWritten != 2 {
		t.Fatalf("repeat of advanced value: writes = %d, want 2", users.lastOnlineRowsWritten)
	}
}
