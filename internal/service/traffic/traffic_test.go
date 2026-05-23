package traffic

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- fakes for recordAndEnforce tests ---

type fakeUserRepo struct {
	users map[int64]*domain.User
	// batchTrafficStateCalls counts BatchUpdateTrafficState invocations so the
	// v3.5.0-beta.9 perf-behavior test can assert PollOnce now flushes user
	// traffic state ONCE per cycle instead of N times. Inline UpdateTrafficState
	// calls don't bump this — that's the whole point.
	batchTrafficStateCalls int
}

func (r *fakeUserRepo) Update(ctx context.Context, u *domain.User) error {
	cp := *u
	r.users[u.ID] = &cp
	return nil
}

// UpdateTrafficState mirrors the production narrow write: only the
// traffic-owned columns are persisted onto the stored row, so the test
// also exercises that the poll touches nothing else.
func (r *fakeUserRepo) UpdateTrafficState(ctx context.Context, u *domain.User) error {
	cur, ok := r.users[u.ID]
	if !ok {
		return nil
	}
	cur.LifetimeUpBytes = u.LifetimeUpBytes
	cur.LifetimeDownBytes = u.LifetimeDownBytes
	cur.LifetimeTotalBytes = u.LifetimeTotalBytes
	cur.PeriodBaselineBytes = u.PeriodBaselineBytes
	cur.LifetimeBaselineAt = u.LifetimeBaselineAt
	cur.TrafficPeriodStart = u.TrafficPeriodStart
	return nil
}

// BatchUpdateTrafficState mirrors production: apply the narrow per-row write
// to every entry, bump the call counter so perf-behavior tests can assert the
// poll flushes ONCE per cycle (not N times).
func (r *fakeUserRepo) BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error {
	r.batchTrafficStateCalls++
	for _, u := range users {
		if err := r.UpdateTrafficState(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakeUserRepo) ClearEmergencyAccess(ctx context.Context, userID int64) error {
	if cur, ok := r.users[userID]; ok {
		cur.EmergencyUntil = nil
		cur.EmergencyBaselineBytes = 0
	}
	return nil
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// Stubs to satisfy the rest of the UserRepo interface (unused here).
func (r *fakeUserRepo) Create(ctx context.Context, u *domain.User) error { return nil }
func (r *fakeUserRepo) Delete(ctx context.Context, id int64) error       { return nil }
func (r *fakeUserRepo) GetByUPN(ctx context.Context, upn string) (*domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) GetBySSO(ctx context.Context, provider, subject string) (*domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) GetBySubToken(ctx context.Context, t string) (*domain.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) List(ctx context.Context, f ports.UserFilter) ([]*domain.User, int64, error) {
	ids := make([]int64, 0, len(r.users))
	for id := range r.users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*domain.User, 0, len(ids))
	for _, id := range ids {
		cp := *r.users[id]
		out = append(out, &cp)
	}
	return out, int64(len(out)), nil
}
func (r *fakeUserRepo) ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error) {
	return nil, nil
}

type fakeDisabler struct {
	calls []bool
}

func (d *fakeDisabler) SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, _ domain.AutoDisabledReason, _ string) error {
	d.calls = append(d.calls, enabled)
	return nil
}

type fakeTrafficRepo struct {
	snapshots       []*domain.TrafficSnapshot
	clientSnapshots []*domain.ClientTrafficSnapshot
	// latestForUsersCalls counts the batched pre-fetch so the perf-behavior
	// test can assert PollOnce now reads via the batch path (ONCE per cycle).
	latestForUsersCalls int
}

func (r *fakeTrafficRepo) Insert(ctx context.Context, s *domain.TrafficSnapshot) error {
	r.snapshots = append(r.snapshots, s)
	return nil
}

func (r *fakeTrafficRepo) LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error) {
	var latest *domain.TrafficSnapshot
	for _, s := range r.snapshots {
		if s.UserID != userID {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}

// LatestForUsers mirrors the mysql batched read: one call returns every
// listed user's most-recent snapshot, omitting users with no rows. Reuses
// the single-user logic so its semantics stay in lockstep.
func (r *fakeTrafficRepo) LatestForUsers(ctx context.Context, userIDs []int64) (map[int64]*domain.TrafficSnapshot, error) {
	r.latestForUsersCalls++
	out := make(map[int64]*domain.TrafficSnapshot, len(userIDs))
	for _, id := range userIDs {
		s, err := r.LatestForUser(ctx, id)
		if err != nil {
			// ErrNotFound — omit from the map (caller treats absence as "no prev").
			continue
		}
		out[id] = s
	}
	return out, nil
}

func (r *fakeTrafficRepo) LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error) {
	var latest *domain.TrafficSnapshot
	for _, s := range r.snapshots {
		if s.UserID != userID || !s.CapturedAt.Before(before) {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}

func (r *fakeTrafficRepo) ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error) {
	out := []*domain.TrafficSnapshot{}
	for _, s := range r.snapshots {
		if s.UserID == userID && !s.CapturedAt.Before(since) && s.CapturedAt.Before(until) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CapturedAt.Before(out[j].CapturedAt)
	})
	return out, nil
}

func (r *fakeTrafficRepo) InsertClient(ctx context.Context, s *domain.ClientTrafficSnapshot) error {
	r.clientSnapshots = append(r.clientSnapshots, s)
	return nil
}

func (r *fakeTrafficRepo) InsertBatch(ctx context.Context, snaps []*domain.TrafficSnapshot) error {
	r.snapshots = append(r.snapshots, snaps...)
	return nil
}

func (r *fakeTrafficRepo) InsertClientBatch(ctx context.Context, snaps []*domain.ClientTrafficSnapshot) error {
	r.clientSnapshots = append(r.clientSnapshots, snaps...)
	return nil
}

func (r *fakeTrafficRepo) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	kept := r.snapshots[:0]
	for _, s := range r.snapshots {
		if s.CapturedAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, s)
	}
	r.snapshots = kept
	keptC := r.clientSnapshots[:0]
	for _, s := range r.clientSnapshots {
		if s.CapturedAt.Before(cutoff) {
			deleted++
			continue
		}
		keptC = append(keptC, s)
	}
	r.clientSnapshots = keptC
	return deleted, nil
}

// PruneHourlyBefore: rollup tables are not exercised by traffic.Service
// unit tests, so this fake is a no-op satisfier for the interface.
func (r *fakeTrafficRepo) PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func snap(userID int64, at string, up, down int64) *domain.TrafficSnapshot {
	t, err := time.ParseInLocation("2006-01-02 15:04", at, time.Local)
	if err != nil {
		panic(err)
	}
	return &domain.TrafficSnapshot{
		UserID:     userID,
		UpBytes:    up,
		DownBytes:  down,
		TotalBytes: up + down,
		CapturedAt: t,
	}
}

func day(date string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestHistoryForUsesBaselineBeforeSince(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-04-30 23:55", 40, 60),
		snap(1, "2026-05-01 12:00", 70, 100),
		snap(1, "2026-05-02 12:00", 90, 130),
	}}
	svc := New(nil, nil, repo, nil, nil, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(report.Items))
	}
	if got := report.Items[0].TotalBytes; got != 70 {
		t.Fatalf("first day total = %d, want 70", got)
	}
	if got := report.Items[1].TotalBytes; got != 50 {
		t.Fatalf("second day total = %d, want 50", got)
	}
}

func TestHistoryForFillsEmptyBuckets(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-01 12:00", 10, 20),
		snap(1, "2026-05-03 12:00", 30, 60),
	}}
	svc := New(nil, nil, repo, nil, nil, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-03"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(report.Items))
	}
	if got := report.Items[1].TotalBytes; got != 0 {
		t.Fatalf("empty day total = %d, want 0", got)
	}
	if got := report.Items[2].TotalBytes; got != 60 {
		t.Fatalf("third day total = %d, want 60", got)
	}
}

func TestHistoryForHandlesCounterReset(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-04-30 23:55", 200, 300),
		snap(1, "2026-05-01 12:00", 20, 30),
	}}
	svc := New(nil, nil, repo, nil, nil, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Items[0].TotalBytes; got != 50 {
		t.Fatalf("reset day total = %d, want 50", got)
	}
}

func TestHistoryForWeekAndMonthLabels(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-15 12:00", 10, 20),
	}}
	svc := New(nil, nil, repo, nil, nil, nil, nil)

	weekly, err := svc.HistoryFor(context.Background(), 1, HistoryWeek, day("2026-05-15"), day("2026-05-15"))
	if err != nil {
		t.Fatal(err)
	}
	if got := weekly.Items[0].Date; got != "2026-05-11" {
		t.Fatalf("week label = %s, want 2026-05-11", got)
	}

	monthly, err := svc.HistoryFor(context.Background(), 1, HistoryMonth, day("2026-05-15"), day("2026-05-15"))
	if err != nil {
		t.Fatal(err)
	}
	if got := monthly.Items[0].Date; got != "2026-05" {
		t.Fatalf("month label = %s, want 2026-05", got)
	}
}

// TestSetPeriodUsageSetsBaseline pins the v3.3.0-beta.6 fix: after an admin
// sets this period's usage to X, PeriodUsed() (= lifetime - PeriodBaselineBytes,
// the value the dashboard and the next poll's auto-disable check both read)
// must equal X. Before the fix PeriodBaselineBytes was left stale, so the next
// poll recomputed a wrong usage and could flip the enable state.
func TestSetPeriodUsageSetsBaseline(t *testing.T) {
	const gb = int64(1) << 30
	t.Run("from zero lifetime", func(t *testing.T) {
		users := &fakeUserRepo{users: map[int64]*domain.User{
			1: {ID: 1, Enabled: true, TrafficLimitBytes: 10 * gb, PeriodBaselineBytes: 999, LifetimeTotalBytes: 0},
		}}
		svc := New(users, nil, &fakeTrafficRepo{}, nil, nil, nil, &fakeDisabler{})
		if err := svc.SetPeriodUsage(context.Background(), 1, 3*gb); err != nil {
			t.Fatal(err)
		}
		if got := users.users[1].PeriodUsed(); got != 3*gb {
			t.Fatalf("PeriodUsed() = %d, want %d (=usedBytes)", got, 3*gb)
		}
	})
	t.Run("with existing higher lifetime", func(t *testing.T) {
		users := &fakeUserRepo{users: map[int64]*domain.User{
			1: {ID: 1, Enabled: true, TrafficLimitBytes: 10 * gb, PeriodBaselineBytes: 0, LifetimeTotalBytes: 10 * gb},
		}}
		repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
			{UserID: 1, TotalBytes: 10 * gb, UpBytes: 4 * gb, CapturedAt: time.Now()},
		}}
		svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})
		if err := svc.SetPeriodUsage(context.Background(), 1, 2*gb); err != nil {
			t.Fatal(err)
		}
		if got := users.users[1].PeriodUsed(); got != 2*gb {
			t.Fatalf("PeriodUsed() = %d, want %d (=usedBytes)", got, 2*gb)
		}
	})
}

func TestRecordAndEnforceLifetimeMonotonicAcrossCounterReset(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, LifetimeTotalBytes: 0},
	}}
	repo := &fakeTrafficRepo{}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	// First poll: 100 GB cumulative.
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 60_000_000_000, down: 40_000_000_000,
		deltaUp: 60_000_000_000, deltaDown: 40_000_000_000, deltaTotal: 100_000_000_000,
		hits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if got := users.users[1].LifetimeTotalBytes; got != 100_000_000_000 {
		t.Fatalf("after first poll lifetime = %d, want 100GB", got)
	}

	// Second poll: 3X-UI restarted, counter reset to 0.5 GB.
	u = users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 200_000_000, down: 300_000_000,
		deltaUp: 200_000_000, deltaDown: 300_000_000, deltaTotal: 500_000_000,
		hits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Lifetime should grow by the post-reset value (0.5 GB), not shrink.
	if got := users.users[1].LifetimeTotalBytes; got != 100_500_000_000 {
		t.Fatalf("after counter reset lifetime = %d, want 100.5GB (no rollback)", got)
	}
}

func TestRecordAndEnforceUsesPerClientDeltasForPartialReset(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            true,
			LifetimeUpBytes:    1_000,
			LifetimeDownBytes:  2_000,
			LifetimeTotalBytes: 3_000,
		},
	}}
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		{UserID: 1, UpBytes: 1_000, DownBytes: 2_000, TotalBytes: 3_000, CapturedAt: time.Now().Add(-time.Minute)},
	}}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		// Raw aggregate only moved from 3000 to 3100, because one client reset.
		// Correct added traffic is the sum of per-client deltas: 1100.
		up: 100, down: 3_000,
		deltaUp: 100, deltaDown: 1_000, deltaTotal: 1_100,
		hits: 2,
	}); err != nil {
		t.Fatal(err)
	}

	if got := users.users[1].LifetimeTotalBytes; got != 4_100 {
		t.Fatalf("lifetime total = %d, want 4100", got)
	}
	latest, err := repo.LatestForUser(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := latest.TotalBytes; got != 4_100 {
		t.Fatalf("snapshot total = %d, want lifetime total 4100", got)
	}
}

func TestRecordAndEnforceDoesNotDoubleCountFirstClientBaselineAfterMigration(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
	}}
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		{UserID: 1, UpBytes: 1_000, DownBytes: 2_000, TotalBytes: 3_000, CapturedAt: time.Now().Add(-time.Minute)},
	}}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 1_100, down: 2_100,
		bootstrap: []bootstrapClientDelta{{
			delta:     trafficDelta{up: 1_100, down: 2_100, total: 3_200},
			createdAt: time.Now().Add(-time.Hour),
		}},
		hits: 1,
	}); err != nil {
		t.Fatal(err)
	}

	if got := users.users[1].LifetimeTotalBytes; got != 3_000 {
		t.Fatalf("lifetime total = %d, want previous aggregate baseline 3000", got)
	}
}

func TestRecordClientStatsHandlesOneClientReset(t *testing.T) {
	// Ownership row carrying the last-observed raw counter (the v3 baseline).
	// The current raw is far below the baseline, simulating an Xray restart
	// that zeroed the upstream counter. monotonicDelta falls back to the
	// current cumulative as the delta.
	entry := &domain.XUIClientEntry{
		ID:                1,
		UserID:            1,
		PanelID:           10,
		InboundID:         20,
		ClientEmail:       "a@example.test",
		LastRawUpBytes:    700,
		LastRawDownBytes:  300,
		LastRawTotalBytes: 1_000,
	}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{1: {entry}}}
	repo := &fakeTrafficRepo{}
	svc := New(nil, ownership, repo, nil, nil, nil, nil)

	delta, err := svc.recordClientStats(context.Background(), entry, 50, 70, nil)
	if err != nil {
		t.Fatal(err)
	}
	if delta.total != 120 {
		t.Fatalf("delta total = %d, want current value 120 after reset", delta.total)
	}
	// Lifetime advanced by the delta; the new raw becomes the baseline.
	if entry.LifetimeTotalBytes != 120 {
		t.Fatalf("lifetime total = %d, want 120", entry.LifetimeTotalBytes)
	}
	if entry.LastRawTotalBytes != 120 {
		t.Fatalf("last-raw total = %d, want 120", entry.LastRawTotalBytes)
	}
}

// TestRecordClientStatsSkipsZeroDeltaSteadyState — P1-2 optimization core:
// an offline client returns the same raw counters every cycle, so neither
// the ownership row nor the snapshot table should be touched. Steady-state
// branch (hadPrev=true, raw counter unchanged).
func TestRecordClientStatsSkipsZeroDeltaSteadyState(t *testing.T) {
	entry := &domain.XUIClientEntry{
		ID: 7, UserID: 1, PanelID: 10, InboundID: 20,
		ClientEmail:        "idle@example.test",
		LifetimeUpBytes:    700,
		LifetimeDownBytes:  300,
		LifetimeTotalBytes: 1_000,
		LastRawUpBytes:     700,
		LastRawDownBytes:   300,
		LastRawTotalBytes:  1_000,
	}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{1: {entry}}}
	repo := &fakeTrafficRepo{}
	sink := &pollSink{}
	svc := New(nil, ownership, repo, nil, nil, nil, nil)

	delta, err := svc.recordClientStats(context.Background(), entry, 700, 300, sink)
	if err != nil {
		t.Fatal(err)
	}
	if delta.up != 0 || delta.down != 0 || delta.total != 0 {
		t.Fatalf("delta = %+v, want all zero", delta)
	}
	if !delta.hadPrev {
		t.Fatalf("hadPrev should propagate true through the skip (caller bootstrap classification depends on it)")
	}
	if len(sink.clientSnaps) != 0 {
		t.Fatalf("sink.clientSnaps = %d, want 0 (zero-delta should not emit snapshot)", len(sink.clientSnaps))
	}
	// Lifetime untouched — sanity check that the ownership row was NOT
	// rewritten with the same values (UpdateCounters skipped).
	if entry.LifetimeTotalBytes != 1_000 {
		t.Fatalf("lifetime should be unchanged, got %d", entry.LifetimeTotalBytes)
	}
}

// TestRecordClientStatsSkipsZeroDeltaIdleNewClient — the bootstrap-leak case
// caught in the 5th audit pass. A freshly-imported client whose 3X-UI counter
// is still 0 must NOT generate a zero-valued lifetime snapshot every cycle
// (the pre-fix code did exactly that because hadPrev stayed false forever
// when LastRawXxx remained 0).
func TestRecordClientStatsSkipsZeroDeltaIdleNewClient(t *testing.T) {
	entry := &domain.XUIClientEntry{
		ID: 8, UserID: 1, PanelID: 10, InboundID: 20,
		ClientEmail: "new-idle@example.test",
		// All counter fields default-zero — never seen any traffic.
	}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{1: {entry}}}
	repo := &fakeTrafficRepo{}
	sink := &pollSink{}
	svc := New(nil, ownership, repo, nil, nil, nil, nil)

	delta, err := svc.recordClientStats(context.Background(), entry, 0, 0, sink)
	if err != nil {
		t.Fatal(err)
	}
	if delta.up != 0 || delta.down != 0 || delta.total != 0 {
		t.Fatalf("delta = %+v, want all zero", delta)
	}
	if delta.hadPrev {
		t.Fatalf("hadPrev should remain false for a never-observed client")
	}
	if len(sink.clientSnaps) != 0 {
		t.Fatalf("idle new client should not emit a zero-snapshot every cycle, got %d", len(sink.clientSnaps))
	}
	if entry.LifetimeTotalBytes != 0 {
		t.Fatalf("lifetime should stay 0, got %d", entry.LifetimeTotalBytes)
	}
}

func TestRecordAndEnforceSkipsEmptyHits(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, LifetimeTotalBytes: 50_000_000_000},
	}}
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-15 10:00", 30_000_000_000, 20_000_000_000),
	}}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	// 3X-UI returned no matching client rows for this user.
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{hits: 0}); err != nil {
		t.Fatal(err)
	}
	// No new snapshot — empty data must not pollute history with a 0 row.
	if len(repo.snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1 (no zero insert)", len(repo.snapshots))
	}
	// Lifetime untouched.
	if got := users.users[1].LifetimeTotalBytes; got != 50_000_000_000 {
		t.Fatalf("lifetime moved to %d, want unchanged 50GB", got)
	}
}

func TestRecordAndEnforcePersistsRolloverBeforeDisablerCall(t *testing.T) {
	oldStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            false,
			AutoDisabledReason: domain.DisabledTrafficExceeded,
			TrafficResetPeriod: domain.ResetMonthly,
			TrafficPeriodStart: &oldStart,
		},
	}}
	repo := &fakeTrafficRepo{}
	disabler := &fakeDisabler{}
	svc := New(users, nil, repo, nil, nil, nil, disabler)

	// In-memory copy passed in; recordAndEnforce should persist the new
	// periodStart BEFORE invoking the disabler (which re-reads the user).
	u := users.users[1]
	cp := *u
	if err := svc.recordAndEnforce(context.Background(), &cp, trafficTotals{up: 1, down: 1, deltaUp: 1, deltaDown: 1, deltaTotal: 2, hits: 1}); err != nil {
		t.Fatal(err)
	}

	saved := users.users[1]
	if saved.TrafficPeriodStart == nil || saved.TrafficPeriodStart.Equal(oldStart) {
		t.Fatalf("periodStart not advanced; saved = %v, was %v", saved.TrafficPeriodStart, oldStart)
	}
	if len(disabler.calls) != 1 || !disabler.calls[0] {
		t.Fatalf("disabler should have been called with enabled=true, got %v", disabler.calls)
	}
}

// fakeNodeTrafficRepo lets us hit NodeHistoryFor / NodeReportFor in unit tests.
type fakeNodeTrafficRepo struct {
	snapshots []*domain.NodeTrafficSnapshot
}

func (r *fakeNodeTrafficRepo) Insert(ctx context.Context, s *domain.NodeTrafficSnapshot) error {
	r.snapshots = append(r.snapshots, s)
	return nil
}
func (r *fakeNodeTrafficRepo) InsertBatch(ctx context.Context, snaps []*domain.NodeTrafficSnapshot) error {
	r.snapshots = append(r.snapshots, snaps...)
	return nil
}
func (r *fakeNodeTrafficRepo) LatestForNode(ctx context.Context, nodeID int64) (*domain.NodeTrafficSnapshot, error) {
	var latest *domain.NodeTrafficSnapshot
	for _, s := range r.snapshots {
		if s.NodeID != nodeID {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}
func (r *fakeNodeTrafficRepo) LastBefore(ctx context.Context, nodeID int64, before time.Time) (*domain.NodeTrafficSnapshot, error) {
	var latest *domain.NodeTrafficSnapshot
	for _, s := range r.snapshots {
		if s.NodeID != nodeID || !s.CapturedAt.Before(before) {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}
func (r *fakeNodeTrafficRepo) ListByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]*domain.NodeTrafficSnapshot, error) {
	out := []*domain.NodeTrafficSnapshot{}
	for _, s := range r.snapshots {
		if s.NodeID == nodeID && !s.CapturedAt.Before(since) && s.CapturedAt.Before(until) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CapturedAt.Before(out[j].CapturedAt) })
	return out, nil
}

func (r *fakeNodeTrafficRepo) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	kept := r.snapshots[:0]
	for _, s := range r.snapshots {
		if s.CapturedAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, s)
	}
	r.snapshots = kept
	return deleted, nil
}

func (r *fakeNodeTrafficRepo) PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

type fakeNodeKey struct {
	panelID   int64
	inboundID int
}

type fakeNodeRepo struct {
	nodes   map[int64]*domain.Node
	byMatch map[fakeNodeKey]int64
}

func (r *fakeNodeRepo) Create(ctx context.Context, n *domain.Node) error { return nil }
func (r *fakeNodeRepo) Delete(ctx context.Context, id int64) error       { return nil }
func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.Node) error {
	cp := *n
	r.nodes[n.ID] = &cp
	return nil
}
func (r *fakeNodeRepo) UpdateTrafficCounters(ctx context.Context, n *domain.Node) error {
	cur, ok := r.nodes[n.ID]
	if !ok {
		cp := *n
		r.nodes[n.ID] = &cp
		return nil
	}
	cur.LifetimeUpBytes = n.LifetimeUpBytes
	cur.LifetimeDownBytes = n.LifetimeDownBytes
	cur.LifetimeTotalBytes = n.LifetimeTotalBytes
	cur.LastTrafficUpBytes = n.LastTrafficUpBytes
	cur.LastTrafficDownBytes = n.LastTrafficDownBytes
	cur.LastTrafficTotalBytes = n.LastTrafficTotalBytes
	return nil
}
func (r *fakeNodeRepo) UpdateHealth(ctx context.Context, n *domain.Node) error {
	cur, ok := r.nodes[n.ID]
	if !ok {
		return nil
	}
	cur.HealthState = n.HealthState
	cur.HealthDetail = n.HealthDetail
	cur.HealthCheckedAt = n.HealthCheckedAt
	return nil
}
func (r *fakeNodeRepo) UpdateInboundConfig(ctx context.Context, n *domain.Node) error { return nil }
func (r *fakeNodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *n
	return &cp, nil
}
func (r *fakeNodeRepo) GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	id, ok := r.byMatch[fakeNodeKey{panelID: panelID, inboundID: inboundID}]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}
func (r *fakeNodeRepo) List(ctx context.Context) ([]*domain.Node, error) {
	out := make([]*domain.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		cp := *n
		out = append(out, &cp)
	}
	return out, nil
}
func (r *fakeNodeRepo) ListEnabled(ctx context.Context) ([]*domain.Node, error) {
	return r.List(ctx)
}
func (r *fakeNodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	return nil
}

type fakeOwnershipRepo struct {
	byUser map[int64][]*domain.XUIClientEntry
	// batchUpdateCountersCalls counts batched flushes for the v3.5.0-beta.9
	// PollOnce perf-behavior test (ONCE per cycle instead of N×M times).
	batchUpdateCountersCalls int
}

func (r *fakeOwnershipRepo) Add(ctx context.Context, e *domain.XUIClientEntry) error { return nil }
func (r *fakeOwnershipRepo) Remove(ctx context.Context, id int64) error              { return nil }
func (r *fakeOwnershipRepo) RemoveByMatch(ctx context.Context, panelID int64, inboundID int, email string) error {
	return nil
}
func (r *fakeOwnershipRepo) GetByMatch(ctx context.Context, panelID int64, inboundID int, email string) (*domain.XUIClientEntry, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeOwnershipRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error) {
	entries := r.byUser[userID]
	out := make([]*domain.XUIClientEntry, len(entries))
	for i, e := range entries {
		cp := *e
		out[i] = &cp
	}
	return out, nil
}
func (r *fakeOwnershipRepo) ListByInbound(ctx context.Context, panelID int64, inboundID int) ([]*domain.XUIClientEntry, error) {
	return nil, nil
}
func (r *fakeOwnershipRepo) Exists(ctx context.Context, panelID int64, inboundID int, email string) (bool, error) {
	return false, nil
}
func (r *fakeOwnershipRepo) UpdateUUID(ctx context.Context, panelID int64, inboundID int, email, newUUID string) error {
	return nil
}
func (r *fakeOwnershipRepo) UpdateCounters(ctx context.Context, e *domain.XUIClientEntry) error {
	// Mirror the change back into byUser so subsequent ListByUser calls in
	// the same test cycle see the freshly-updated lifetime + last-raw values.
	for _, entries := range r.byUser {
		for i, entry := range entries {
			if entry.ID == e.ID {
				cp := *e
				entries[i] = &cp
				return nil
			}
		}
	}
	return nil
}

// BatchUpdateCounters mirrors UpdateCounters for every item in the slice;
// bumps the call counter so perf-behavior tests can assert this is the only
// counter-write path PollOnce takes per cycle.
func (r *fakeOwnershipRepo) BatchUpdateCounters(ctx context.Context, items []*domain.XUIClientEntry) error {
	r.batchUpdateCountersCalls++
	for _, e := range items {
		if err := r.UpdateCounters(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

type fakeXUIPool struct {
	clients map[int64]ports.XUIClient
}

func (p *fakeXUIPool) Get(panelID int64) (ports.XUIClient, error) {
	c, ok := p.clients[panelID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}
func (p *fakeXUIPool) List() []*domain.XUIPanel         { return nil }
func (p *fakeXUIPool) Add(panel *domain.XUIPanel) error { return nil }
func (p *fakeXUIPool) Remove(panelID int64) error       { return nil }

type fakeXUIClient struct {
	inbounds []ports.Inbound
}

func (c *fakeXUIClient) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	return c.inbounds, nil
}
func (c *fakeXUIClient) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	return nil, domain.ErrNotFound
}
func (c *fakeXUIClient) AddInbound(ctx context.Context, spec ports.InboundSpec) (int, error) {
	return 0, nil
}
func (c *fakeXUIClient) UpdateInbound(ctx context.Context, id int, spec ports.InboundSpec) error {
	return nil
}
func (c *fakeXUIClient) DelInbound(ctx context.Context, id int) error { return nil }
func (c *fakeXUIClient) SetInboundEnable(ctx context.Context, id int, enable bool) error {
	return nil
}
func (c *fakeXUIClient) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return nil
}
func (c *fakeXUIClient) UpdateClient(ctx context.Context, inboundID int, clientUUID string, spec ports.ClientSpec) error {
	return nil
}
func (c *fakeXUIClient) DelClient(ctx context.Context, inboundID int, clientUUID string) error {
	return nil
}
func (c *fakeXUIClient) DelClientByEmail(ctx context.Context, inboundID int, email string) error {
	return nil
}
func (c *fakeXUIClient) CopyClients(ctx context.Context, srcInboundID, dstInboundID int, emails []string) error {
	return nil
}
func (c *fakeXUIClient) GetClientTraffic(ctx context.Context, email string) ([]ports.ClientTraffic, error) {
	return nil, nil
}
func (c *fakeXUIClient) GetInboundTraffics(ctx context.Context, id int) ([]ports.ClientTraffic, error) {
	for _, inb := range c.inbounds {
		if inb.ID == id {
			return inb.ClientStats, nil
		}
	}
	return nil, nil
}
func (c *fakeXUIClient) ResetClientTraffic(ctx context.Context, inboundID int, email string) error {
	return nil
}
func (c *fakeXUIClient) GetInboundClients(ctx context.Context, inboundID int) ([]ports.ClientDetail, error) {
	return nil, nil
}

// When snapshots have been wiped but the user still carries a non-zero
// LifetimeTotalBytes, bootstrap deltas for ownerships created AFTER the
// recorded LifetimeBaselineAt must still be folded in (regression for the
// "lifetime > 0 + prev == nil" edge case).
func TestRecordAndEnforceUsesLifetimeBaselineWhenSnapshotsCleared(t *testing.T) {
	cutoff := time.Now().Add(-2 * time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            true,
			LifetimeUpBytes:    1_000,
			LifetimeDownBytes:  2_000,
			LifetimeTotalBytes: 3_000,
			LifetimeBaselineAt: &cutoff,
		},
	}}
	repo := &fakeTrafficRepo{} // snapshots table empty (wiped manually)
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 100, down: 400, hits: 1,
		bootstrap: []bootstrapClientDelta{{
			delta:     trafficDelta{up: 100, down: 400, total: 500},
			createdAt: time.Now().Add(-1 * time.Hour), // AFTER the baseline cutoff
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := users.users[1].LifetimeTotalBytes; got != 3_500 {
		t.Fatalf("lifetime total = %d, want 3500 (bootstrap delta added)", got)
	}
}

// Mirror: an ownership created BEFORE the baseline cutoff must NOT be
// re-counted (it was already in lifetime).
func TestRecordAndEnforceSkipsBootstrapBeforeBaseline(t *testing.T) {
	cutoff := time.Now().Add(-1 * time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            true,
			LifetimeTotalBytes: 3_000,
			LifetimeBaselineAt: &cutoff,
		},
	}}
	repo := &fakeTrafficRepo{}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	u := users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 100, down: 400, hits: 1,
		bootstrap: []bootstrapClientDelta{{
			delta:     trafficDelta{up: 100, down: 400, total: 500},
			createdAt: time.Now().Add(-2 * time.Hour), // BEFORE the cutoff
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := users.users[1].LifetimeTotalBytes; got != 3_000 {
		t.Fatalf("lifetime total = %d, want 3000 (bootstrap before baseline must be ignored)", got)
	}
}

// LifetimeBaselineAt must advance after every successful snapshot so the
// next poll's bootstrap-cutoff is current. Without this, ownerships added
// in a no-traffic cycle would later be lumped into "before baseline" and
// silently dropped.
func TestRecordAndEnforceAdvancesLifetimeBaseline(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
	}}
	repo := &fakeTrafficRepo{}
	svc := New(users, nil, repo, nil, nil, nil, &fakeDisabler{})

	before := time.Now()
	u := users.users[1]
	if err := svc.recordAndEnforce(context.Background(), u, trafficTotals{
		up: 1, down: 1, deltaUp: 1, deltaDown: 1, deltaTotal: 2, hits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	saved := users.users[1]
	if saved.LifetimeBaselineAt == nil {
		t.Fatal("LifetimeBaselineAt not set")
	}
	if saved.LifetimeBaselineAt.Before(before) {
		t.Fatalf("baseline %v is older than poll start %v", saved.LifetimeBaselineAt, before)
	}
}

// ReportFor must surface the latest snapshot's cumulative when Lifetime is
// still 0 (pre-migration users). Without the fallback, the dashboard would
// show "0 累计" until the next poll seeded Lifetime.
func TestReportForFallsBackToLatestSnapshotWhenLifetimeZero(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true}, // Lifetime fields all zero
	}}
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-15 10:00", 5_000_000_000, 7_000_000_000),
	}}
	svc := New(users, nil, repo, nil, nil, nil, nil)

	rep, err := svc.ReportFor(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PermanentTotalBytes != 12_000_000_000 {
		t.Fatalf("permanent = %d, want fallback to latest snapshot total 12GB", rep.PermanentTotalBytes)
	}
	if rep.PeriodUsedBytes != 12_000_000_000 {
		t.Fatalf("period = %d, want latest snapshot total 12GB when no period baseline is set", rep.PeriodUsedBytes)
	}
}

// Repro for the panic at /api/admin/traffic/nodes/history when a node has no
// snapshots yet — NodeHistoryFor must return a zero-filled report, not nil.
func TestNodeHistoryForEmptyDoesNotPanic(t *testing.T) {
	nodeRepo := &fakeNodeTrafficRepo{}
	svc := New(nil, nil, nil, nil, nodeRepo, nil, nil)

	rep, err := svc.NodeHistoryFor(context.Background(), 2, HistoryDay, day("2026-04-16"), day("2026-05-15"))
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil {
		t.Fatal("report nil")
	}
	if len(rep.Items) != 30 {
		t.Fatalf("items len = %d, want 30 (29-day inclusive range)", len(rep.Items))
	}
	for _, it := range rep.Items {
		if it.TotalBytes != 0 {
			t.Fatalf("expected zero-filled bucket, got %+v", it)
		}
	}
}

// Repro the production panic path: NodeReportFor must guard against the
// service being constructed without a node-traffic repo (or pre-snapshot
// state where nodeRepo lookup fails).
func TestNodeReportForNilNodesRepoDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NodeReportFor panicked: %v", r)
		}
	}()
	nodeRepo := &fakeNodeTrafficRepo{}
	svc := New(nil, nil, nil, nil, nodeRepo, nil, nil)
	rep, err := svc.NodeReportFor(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil {
		t.Fatal("report nil")
	}
}

// TestPollOnceBatchesPerCycleWrites pins the v3.5.0-beta.9 perf contract:
// regardless of user count N or per-user client count M, ONE PollOnce cycle
// must produce ONE BatchUpdateCounters + ONE BatchUpdateTrafficState +
// ONE LatestForUsers call. Before the refactor each user × client pair
// produced its own UpdateCounters / UpdateTrafficState / LatestForUser
// statement, which is what made "Poll Now" take ~10s on a SQLite panel.
//
// This is a BEHAVIOR test (not a wall-clock benchmark): it asserts the
// poll takes the batched code path at all, which is what makes the
// SQLite-commit-count math work out to a single-digit-percent of the
// pre-refactor cost.
func TestPollOnceBatchesPerCycleWrites(t *testing.T) {
	// 3 users × 4 clients = 12 ownership rows. Pre-refactor this would
	// produce 12 UpdateCounters + 3 UpdateTrafficState + 3 LatestForUser
	// inline calls. Post-refactor: exactly 1 + 1 + 1.
	const userCount = 3
	const clientsPerUser = 4

	users := &fakeUserRepo{users: map[int64]*domain.User{}}
	ownershipByUser := map[int64][]*domain.XUIClientEntry{}
	inboundStats := []ports.ClientTraffic{}
	nextOwnershipID := int64(0)
	for uid := int64(1); uid <= userCount; uid++ {
		users.users[uid] = &domain.User{ID: uid, Enabled: true}
		entries := make([]*domain.XUIClientEntry, 0, clientsPerUser)
		for c := 0; c < clientsPerUser; c++ {
			nextOwnershipID++
			email := fmt.Sprintf("u%d-c%d@example.test", uid, c)
			entries = append(entries, &domain.XUIClientEntry{
				ID: nextOwnershipID, UserID: uid,
				PanelID: 10, InboundID: 20,
				ClientEmail: email,
				CreatedAt:   time.Now(),
			})
			// Non-zero traffic so the inner loop actually exercises the
			// counter-write path (zero-delta short-circuits skip the write).
			inboundStats = append(inboundStats, ports.ClientTraffic{
				Email: email,
				Up:    int64(100 + c),
				Down:  int64(200 + c),
			})
		}
		ownershipByUser[uid] = entries
	}
	ownership := &fakeOwnershipRepo{byUser: ownershipByUser}
	repo := &fakeTrafficRepo{}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: inboundStats}}},
	}}
	svc := New(users, ownership, repo, nil, nil, pool, &fakeDisabler{})

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	// One BatchUpdateCounters covering all 12 ownership rows, instead of 12
	// inline UpdateCounters.
	if ownership.batchUpdateCountersCalls != 1 {
		t.Errorf("ownership.BatchUpdateCounters calls = %d, want 1 (one end-of-cycle flush)", ownership.batchUpdateCountersCalls)
	}
	// One BatchUpdateTrafficState covering all 3 users, instead of 3 inline
	// UpdateTrafficState.
	if users.batchTrafficStateCalls != 1 {
		t.Errorf("users.BatchUpdateTrafficState calls = %d, want 1 (one end-of-cycle flush)", users.batchTrafficStateCalls)
	}
	// One LatestForUsers pre-fetch up front, instead of 3 inline LatestForUser.
	if repo.latestForUsersCalls != 1 {
		t.Errorf("traffic.LatestForUsers calls = %d, want 1 (single pre-fetch)", repo.latestForUsersCalls)
	}

	// Sanity: the batched writes actually moved the data, not just the call
	// counters. Every user's lifetime should reflect the per-client traffic
	// (4 clients each contributing 100+c up + 200+c down → sum is uid-invariant).
	wantPerUser := int64(0)
	for c := 0; c < clientsPerUser; c++ {
		wantPerUser += int64(100+c) + int64(200+c)
	}
	for uid := int64(1); uid <= userCount; uid++ {
		if got := users.users[uid].LifetimeTotalBytes; got != wantPerUser {
			t.Errorf("user %d LifetimeTotalBytes = %d, want %d (batch flush did not land)", uid, got, wantPerUser)
		}
	}
}

func TestPollOnceRecordsNodeTrafficFromMatchedClientStats(t *testing.T) {
	email := "u1-n2@example.test"
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
	}}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {{
			UserID:      1,
			PanelID:     10,
			InboundID:   20,
			ClientEmail: email,
			CreatedAt:   time.Now(),
		}},
	}}
	nodes := &fakeNodeRepo{
		nodes: map[int64]*domain.Node{
			2: {ID: 2, PanelID: 10, InboundID: 20, DisplayName: "node-2", Enabled: true},
		},
		byMatch: map[fakeNodeKey]int64{
			{panelID: 10, inboundID: 20}: 2,
		},
	}
	nodeTraffic := &fakeNodeTrafficRepo{}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{
			ID:   20,
			Up:   0,
			Down: 0,
			ClientStats: []ports.ClientTraffic{{
				Email: email,
				Up:    123,
				Down:  456,
			}},
		}}},
	}}
	svc := New(users, ownership, &fakeTrafficRepo{}, nodes, nodeTraffic, pool, &fakeDisabler{})

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	node := nodes.nodes[2]
	if got := node.LifetimeTotalBytes; got != 579 {
		t.Fatalf("node lifetime total = %d, want clientStats sum 579", got)
	}
	if len(nodeTraffic.snapshots) != 1 {
		t.Fatalf("node snapshots = %d, want 1", len(nodeTraffic.snapshots))
	}
	if got := nodeTraffic.snapshots[0].TotalBytes; got != 579 {
		t.Fatalf("node snapshot total = %d, want 579", got)
	}
}

func TestRecordAndEnforceRechecksLimitAfterRolloverReenable(t *testing.T) {
	oldStart := time.Now().AddDate(0, -1, 0)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {
			ID:                 1,
			Enabled:            false,
			AutoDisabledReason: domain.DisabledTrafficExceeded,
			TrafficLimitBytes:  1,
			TrafficResetPeriod: domain.ResetMonthly,
			TrafficPeriodStart: &oldStart,
		},
	}}
	repo := &fakeTrafficRepo{}
	disabler := &fakeDisabler{}
	svc := New(users, nil, repo, nil, nil, nil, disabler)

	cp := *users.users[1]
	if err := svc.recordAndEnforce(context.Background(), &cp, trafficTotals{
		up: 2, deltaUp: 2, deltaTotal: 2, hits: 1,
	}); err != nil {
		t.Fatal(err)
	}

	if len(disabler.calls) != 2 || !disabler.calls[0] || disabler.calls[1] {
		t.Fatalf("disabler calls = %v, want re-enable then disable", disabler.calls)
	}
}

// TestBucketStartFor_RespectsCallerLocation guards the traffic-chart
// timezone fix: a Shanghai-zoned input and the same wall-clock moment
// expressed in Los Angeles must bucket to different calendar days,
// and the returned bucket boundary must carry the caller's location
// (not silently get rewritten to server local). Without this guarantee
// the chart bucketing would collapse to whatever timezone the panel
// process happens to run in, the very root of the "missing today's
// traffic" bug.
func TestBucketStartFor_RespectsCallerLocation(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load America/Los_Angeles: %v", err)
	}

	// Same instant, two locations: 2026-05-17 02:00 Asia/Shanghai
	// equals 2026-05-16 11:00 America/Los_Angeles equals 18:00 UTC.
	instant := time.Date(2026, 5, 17, 2, 0, 0, 0, sh)

	cases := []struct {
		name     string
		in       time.Time
		want     time.Time
		wantLoc  *time.Location
	}{
		{
			name:    "shanghai puts the moment on 5/17",
			in:      instant.In(sh),
			want:    time.Date(2026, 5, 17, 0, 0, 0, 0, sh),
			wantLoc: sh,
		},
		{
			name:    "los_angeles puts the same moment on 5/16",
			in:      instant.In(la),
			want:    time.Date(2026, 5, 16, 0, 0, 0, 0, la),
			wantLoc: la,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bucketStartFor(tc.in, HistoryDay)
			if !got.Equal(tc.want) {
				t.Errorf("bucketStartFor(%v) = %v, want %v", tc.in, got, tc.want)
			}
			if got.Location().String() != tc.wantLoc.String() {
				t.Errorf("bucketStartFor(%v).Location() = %q, want %q",
					tc.in, got.Location(), tc.wantLoc)
			}
		})
	}
}

// TestStartOfDay_PreservesLocation is the lower-level counterpart:
// startOfDay must not drop the caller's location when zeroing the
// clock, otherwise downstream bucket arithmetic silently shifts.
func TestStartOfDay_PreservesLocation(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	in := time.Date(2026, 5, 17, 15, 42, 7, 0, tokyo)
	got := startOfDay(in)
	if got.Location().String() != "Asia/Tokyo" {
		t.Errorf("startOfDay location = %q, want Asia/Tokyo", got.Location())
	}
	want := time.Date(2026, 5, 17, 0, 0, 0, 0, tokyo)
	if !got.Equal(want) {
		t.Errorf("startOfDay = %v, want %v", got, want)
	}
}

func TestShouldRollPeriodYearly(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name        string
		periodStart time.Time
		now         time.Time
		want        bool
	}{
		{"same year mid-year", time.Date(2026, 3, 4, 0, 0, 0, 0, loc), time.Date(2026, 11, 30, 23, 59, 59, 0, loc), false},
		{"year rollover by one second", time.Date(2026, 12, 31, 23, 59, 59, 0, loc), time.Date(2027, 1, 1, 0, 0, 0, 0, loc), true},
		{"multi-year gap", time.Date(2024, 6, 1, 0, 0, 0, 0, loc), time.Date(2026, 1, 1, 0, 0, 0, 0, loc), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRollPeriod(tc.now, tc.periodStart, domain.ResetYearly); got != tc.want {
				t.Errorf("shouldRollPeriod(yearly) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCurrentPeriodStartYearly(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	// Yearly boundary must snap to Jan 1 00:00 in the input's own location —
	// otherwise the period_baseline rolls a few hours late for non-UTC tz.
	now := time.Date(2026, 7, 4, 15, 30, 0, 0, tokyo)
	got := currentPeriodStart(now, domain.ResetYearly)
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, tokyo)
	if !got.Equal(want) {
		t.Errorf("currentPeriodStart(yearly) = %v, want %v", got, want)
	}
	if got.Location().String() != tokyo.String() {
		t.Errorf("currentPeriodStart(yearly) location = %q, want %q", got.Location(), tokyo)
	}
}
