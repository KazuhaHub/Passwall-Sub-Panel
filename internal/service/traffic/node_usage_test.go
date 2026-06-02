package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestUserNodeUsage covers the read path: per-node lifetime is the ownership
// counter, period is lifetime minus the per-client baseline, and "today" is a
// delta against the last per-client snapshot before local midnight — with the
// born-today (full lifetime) vs pre-existing-idle (zero) fallbacks when no such
// snapshot exists.
func TestUserNodeUsage(t *testing.T) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tenDaysAgo := todayStart.AddDate(0, 0, -10)

	// Node A (inbound 20): created long ago, has a pre-today snapshot.
	// Node B (inbound 21): created today, no prior snapshot, baseline still 0.
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {
			{
				ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x",
				CreatedAt:          tenDaysAgo,
				LifetimeUpBytes:    100, LifetimeDownBytes: 300, LifetimeTotalBytes: 400,
				PeriodBaselineUpBytes: 40, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 140,
			},
			{
				ID: 2, UserID: 1, PanelID: 10, InboundID: 21, ClientEmail: "u1-n2@x",
				CreatedAt:          now, // born today
				LifetimeUpBytes:    10, LifetimeDownBytes: 20, LifetimeTotalBytes: 30,
				PeriodBaselineUpBytes: 0, PeriodBaselineDownBytes: 0, PeriodBaselineTotalBytes: 0,
			},
		},
	}}

	// Yesterday's snapshot for node A → today = lifetime - this.
	repo := &fakeTrafficRepo{clientSnapshots: []*domain.ClientTrafficSnapshot{
		{
			UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x",
			UpBytes: 90, DownBytes: 280, TotalBytes: 370,
			CapturedAt: todayStart.AddDate(0, 0, -1),
		},
	}}

	nodes := &fakeNodeRepo{
		nodes: map[int64]*domain.Node{
			101: {ID: 101, PanelID: 10, InboundID: 20, DisplayName: "Tokyo", Region: "JP"},
			102: {ID: 102, PanelID: 10, InboundID: 21, DisplayName: "Singapore", Region: "SG"},
		},
		byMatch: map[fakeNodeKey]int64{
			{panelID: 10, inboundID: 20}: 101,
			{panelID: 10, inboundID: 21}: 102,
		},
	}

	svc := New(nil, ownership, repo, nodes, nil, nil, nil)
	rows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	byNode := map[int64]NodeUsageRow{}
	for _, r := range rows {
		byNode[r.NodeID] = r
	}

	a, ok := byNode[101]
	if !ok {
		t.Fatal("node 101 (Tokyo) missing from result")
	}
	if a.DisplayName != "Tokyo" || a.Region != "JP" {
		t.Errorf("node A identity = %q/%q, want Tokyo/JP", a.DisplayName, a.Region)
	}
	if a.LifetimeTotalBytes != 400 {
		t.Errorf("A lifetime total = %d, want 400", a.LifetimeTotalBytes)
	}
	// period = lifetime - baseline
	if a.PeriodUpBytes != 60 || a.PeriodDownBytes != 200 || a.PeriodTotalBytes != 260 {
		t.Errorf("A period = %d/%d/%d, want 60/200/260", a.PeriodUpBytes, a.PeriodDownBytes, a.PeriodTotalBytes)
	}
	// today = lifetime - yesterday snapshot
	if a.TodayUpBytes != 10 || a.TodayDownBytes != 20 || a.TodayTotalBytes != 30 {
		t.Errorf("A today = %d/%d/%d, want 10/20/30", a.TodayUpBytes, a.TodayDownBytes, a.TodayTotalBytes)
	}

	b, ok := byNode[102]
	if !ok {
		t.Fatal("node 102 (Singapore) missing from result")
	}
	// baseline 0 → period == lifetime
	if b.PeriodTotalBytes != 30 {
		t.Errorf("B period total = %d, want 30 (baseline 0)", b.PeriodTotalBytes)
	}
	// born today, no prior snapshot → today == full lifetime
	if b.TodayTotalBytes != 30 {
		t.Errorf("B today total = %d, want 30 (born today)", b.TodayTotalBytes)
	}
}

// TestUserNodeUsageTodayIdleFallback pins the pre-existing-but-no-snapshot
// branch: a client created before today whose pre-today snapshot has aged out
// reads as 0 today (idle), NOT its whole lifetime.
func TestUserNodeUsageTodayIdleFallback(t *testing.T) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {{
			ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x",
			CreatedAt:          todayStart.AddDate(0, 0, -30), // pre-existing
			LifetimeTotalBytes: 999, LifetimeUpBytes: 333, LifetimeDownBytes: 666,
		}},
	}}
	// No client snapshots at all → no pre-today baseline.
	svc := New(nil, ownership, &fakeTrafficRepo{}, nil, nil, nil, nil)
	rows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].TodayTotalBytes != 0 {
		t.Errorf("pre-existing idle client today = %d, want 0 (not lifetime)", rows[0].TodayTotalBytes)
	}
}

// TestPollOncePerClientPeriodBaselineRollover is the core consistency check:
// after a user's period rolls over inside PollOnce, each owned client's period
// baseline is reseeded so that Σ(per-client period usage) equals the user's
// own period usage exactly (each = this cycle's delta).
func TestPollOncePerClientPeriodBaselineRollover(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0) // a year ago → monthly rollover fires

	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            true,
			TrafficResetPeriod: domain.ResetMonthly,
			TrafficPeriodStart: &oldStart,
			// Lifetime = sum of the two clients' prior lifetimes; baseline
			// irrelevant (the rollover overwrites it).
			LifetimeUpBytes:    1100,
			LifetimeDownBytes:  2200,
			LifetimeTotalBytes: 3300,
		},
	}}

	// Two clients with a prior baseline (LastRaw set → hadPrev, so this cycle's
	// delta is exactly newRaw - LastRaw, no bootstrap path).
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {
			{
				ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x", CreatedAt: oldStart,
				LifetimeUpBytes: 100, LifetimeDownBytes: 200, LifetimeTotalBytes: 300,
				LastRawUpBytes: 100, LastRawDownBytes: 200, LastRawTotalBytes: 300,
			},
			{
				ID: 2, UserID: 1, PanelID: 10, InboundID: 21, ClientEmail: "u1-n2@x", CreatedAt: oldStart,
				LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000,
				LastRawUpBytes: 1000, LastRawDownBytes: 2000, LastRawTotalBytes: 3000,
			},
		},
	}}

	// New cumulative raw counters → per-client deltas: A {50,50}=100, B {100,200}=300.
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1-n1@x", Up: 150, Down: 250},
		}}, {ID: 21, ClientStats: []ports.ClientTraffic{
			{Email: "u1-n2@x", Up: 1100, Down: 2200},
		}}}},
	}}

	repo := &fakeTrafficRepo{}
	svc := New(users, ownership, repo, nil, nil, pool, &fakeDisabler{})
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	// User-level period usage = this cycle's total delta = 100 + 300 = 400.
	u, _ := users.GetByID(context.Background(), 1)
	if got := u.PeriodUsed(); got != 400 {
		t.Fatalf("user PeriodUsed after rollover = %d, want 400 (this cycle's delta)", got)
	}

	rows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	var sumPeriod int64
	perEmail := map[string]int64{}
	for _, r := range rows {
		sumPeriod += r.PeriodTotalBytes
		perEmail[r.ClientEmail] = r.PeriodTotalBytes
	}
	// Σ per-client period == user period: the reseed kept them in lockstep.
	if sumPeriod != u.PeriodUsed() {
		t.Errorf("Σ per-client period = %d, but user period = %d — reseed broke the invariant", sumPeriod, u.PeriodUsed())
	}
	// And each client's period == its own this-cycle delta.
	if perEmail["u1-n1@x"] != 100 {
		t.Errorf("client A period = %d, want 100", perEmail["u1-n1@x"])
	}
	if perEmail["u1-n2@x"] != 300 {
		t.Errorf("client B period = %d, want 300", perEmail["u1-n2@x"])
	}
}

// TestSetPeriodUsageReseedsClientBaselines pins the manual-override fix: after
// an admin sets a user's period usage, the per-node breakdown's period footer
// sums to (about) that override — proportional to each client's lifetime —
// instead of contradicting the user-level figure with full lifetimes.
func TestSetPeriodUsageReseedsClientBaselines(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly,
			// Lifetime = Σ client lifetimes (kept in sync by the poll).
			LifetimeUpBytes: 1100, LifetimeDownBytes: 2200, LifetimeTotalBytes: 3300,
		},
	}}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {
			{ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x", CreatedAt: time.Now().AddDate(0, 0, -40),
				LifetimeUpBytes: 100, LifetimeDownBytes: 200, LifetimeTotalBytes: 300},
			{ID: 2, UserID: 1, PanelID: 10, InboundID: 21, ClientEmail: "u1-n2@x", CreatedAt: time.Now().AddDate(0, 0, -40),
				LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000},
		},
	}}
	svc := New(users, ownership, &fakeTrafficRepo{}, nil, nil, nil, &fakeDisabler{})

	// Admin sets this period's usage to 330 bytes (10% of the 3300 lifetime).
	if err := svc.SetPeriodUsage(context.Background(), 1, 330); err != nil {
		t.Fatalf("SetPeriodUsage: %v", err)
	}

	u, _ := users.GetByID(context.Background(), 1)
	if got := u.PeriodUsed(); got != 330 {
		t.Fatalf("user PeriodUsed = %d, want 330", got)
	}

	rows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	var sumPeriod int64
	for _, r := range rows {
		sumPeriod += r.PeriodTotalBytes
	}
	// Σ per-node period must track the override (±byte-level float rounding),
	// NOT the 3300 full lifetime it would show without the reseed.
	if sumPeriod < 328 || sumPeriod > 330 {
		t.Errorf("Σ per-node period = %d, want ~330 (the override), not full lifetime — reseed missing", sumPeriod)
	}
}

// TestSetPeriodUsageUpDownNoOverflow guards the int64 overflow in the up/down
// split: total*latestUp can exceed maxint64 for multi-GB users, wrapping the
// up byte count negative and writing garbage into the snapshot. With a 4GiB/8GiB
// latest split and an 8GiB override, the product 8GiB*4GiB overflows int64.
func TestSetPeriodUsageUpDownNoOverflow(t *testing.T) {
	const giB = int64(1) << 30
	past := time.Now().Add(-time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly,
			LifetimeUpBytes: 4 * giB, LifetimeDownBytes: 4 * giB, LifetimeTotalBytes: 8 * giB},
	}}
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		{UserID: 1, UpBytes: 4 * giB, DownBytes: 4 * giB, TotalBytes: 8 * giB, CapturedAt: past},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, repo, nil, nil, nil, &fakeDisabler{})

	if err := svc.SetPeriodUsage(context.Background(), 1, 8*giB); err != nil {
		t.Fatalf("SetPeriodUsage: %v", err)
	}

	// The newest snapshot is the one SetPeriodUsage just wrote.
	var snap *domain.TrafficSnapshot
	for _, s := range repo.snapshots {
		if snap == nil || s.CapturedAt.After(snap.CapturedAt) {
			snap = s
		}
	}
	if snap == nil {
		t.Fatal("no snapshot written")
	}
	if snap.UpBytes < 0 || snap.DownBytes < 0 {
		t.Fatalf("overflow: up=%d down=%d (both must be >= 0)", snap.UpBytes, snap.DownBytes)
	}
	if snap.UpBytes+snap.DownBytes != snap.TotalBytes {
		t.Errorf("up+down=%d != total=%d", snap.UpBytes+snap.DownBytes, snap.TotalBytes)
	}
	// 4GiB/8GiB split → up should be ~4GiB, not a wrapped value.
	if snap.UpBytes != 4*giB {
		t.Errorf("up=%d, want %d (preserve the 50%% up ratio)", snap.UpBytes, 4*giB)
	}
}

// TestPollOnceBootstrapBeforeCutoffRolloverInvariant guards a narrow edge of
// the rollover reseed: a bootstrap client (LastRaw all 0 → hadPrev=false)
// created BEFORE the user's LifetimeBaselineAt cutoff, transmitting for the
// first time in the same cycle that rolls the period. The user-level path drops
// such a bootstrap delta (createdAt <= cutoff → "already in lifetime"), so the
// reseed must NOT count it either, or Σ(per-client period) would exceed the
// user's period by that delta.
func TestPollOnceBootstrapBeforeCutoffRolloverInvariant(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0)          // a year ago → monthly rollover fires
	cutoff := time.Now().Add(-7 * 24 * time.Hour)     // baseline = last week
	createdAt := time.Now().Add(-30 * 24 * time.Hour) // client created last month, BEFORE cutoff

	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly,
			TrafficPeriodStart: &oldStart, LifetimeBaselineAt: &cutoff,
			LifetimeUpBytes: 0, LifetimeDownBytes: 0, LifetimeTotalBytes: 0,
		},
	}}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {{
			ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x", CreatedAt: createdAt,
			LifetimeUpBytes: 0, LifetimeDownBytes: 0, LifetimeTotalBytes: 0,
			LastRawUpBytes: 0, LastRawDownBytes: 0, LastRawTotalBytes: 0,
		}},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1-n1@x", Up: 200, Down: 300}, // first transmission this cycle
		}}}},
	}}
	svc := New(users, ownership, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	u, _ := users.GetByID(context.Background(), 1)
	rows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	var sumPeriod int64
	for _, r := range rows {
		sumPeriod += r.PeriodTotalBytes
	}
	if sumPeriod != u.PeriodUsed() {
		t.Errorf("invariant violated: Σ per-client period = %d, user period = %d (the dropped bootstrap delta must not be counted by the reseed)", sumPeriod, u.PeriodUsed())
	}
}
