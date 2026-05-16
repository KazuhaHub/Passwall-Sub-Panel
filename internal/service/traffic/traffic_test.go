package traffic

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- fakes for recordAndEnforce tests ---

type fakeUserRepo struct {
	users map[int64]*domain.User
}

func (r *fakeUserRepo) Update(ctx context.Context, u *domain.User) error {
	cp := *u
	r.users[u.ID] = &cp
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

func (r *fakeTrafficRepo) LatestForClient(ctx context.Context, userID int64, panelID int64, inboundID int, email string) (*domain.ClientTrafficSnapshot, error) {
	var latest *domain.ClientTrafficSnapshot
	for _, s := range r.clientSnapshots {
		if s.UserID != userID || s.PanelID != panelID || s.InboundID != inboundID || s.ClientEmail != email {
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
	repo := &fakeTrafficRepo{clientSnapshots: []*domain.ClientTrafficSnapshot{
		{UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "a@example.test", UpBytes: 700, DownBytes: 300, TotalBytes: 1_000, CapturedAt: time.Now().Add(-time.Minute)},
	}}
	svc := New(nil, nil, repo, nil, nil, nil, nil)

	delta, err := svc.recordClientStats(context.Background(), 1, 10, 20, "a@example.test", 50, 70)
	if err != nil {
		t.Fatal(err)
	}
	if delta.total != 120 {
		t.Fatalf("delta total = %d, want current value 120 after reset", delta.total)
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
func (r *fakeNodeRepo) UpdatePanelName(ctx context.Context, panelID int64, panelName string) error {
	return nil
}
func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.Node) error {
	cp := *n
	r.nodes[n.ID] = &cp
	return nil
}
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
func (r *fakeOwnershipRepo) UpdatePanelName(ctx context.Context, panelID int64, panelName string) error {
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
