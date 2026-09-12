package traffic

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakePSPClientRepo struct {
	ports.PSPClientRepo
	byUser       map[int64][]*domain.PSPClient
	inbounds     map[int64][]domain.PSPClientInbound // clientID -> attachments
	batchUpdated []*domain.PSPClient                 // last BatchUpdateCounters payload
}

func (f *fakePSPClientRepo) ListByUser(_ context.Context, uid int64) ([]*domain.PSPClient, error) {
	return f.byUser[uid], nil
}
func (f *fakePSPClientRepo) ListInbounds(_ context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	return f.inbounds[clientID], nil
}
func (f *fakePSPClientRepo) BatchUpdateCounters(_ context.Context, items []*domain.PSPClient) error {
	f.batchUpdated = items
	return nil
}
func (f *fakePSPClientRepo) ListAll(_ context.Context) ([]*domain.PSPClient, error) {
	var all []*domain.PSPClient
	for _, cs := range f.byUser {
		all = append(all, cs...)
	}
	return all, nil
}

// SetPeriodUsage's per-client reseed must ALSO rebaseline psp_client rows for a
// MIGRATED user (one with NO ownership rows). Without it, UserServerUsage keeps
// computing per-server period = lifetime − stale-baseline and visibly
// contradicts the user-level override shown right above it.
func TestReseedClientBaselines_SharedClientsForMigratedUser(t *testing.T) {
	psp := &fakePSPClientRepo{
		byUser: map[int64][]*domain.PSPClient{1: {
			{ID: 1, UserID: 1, PanelID: 10, LifetimeUpBytes: 600, LifetimeDownBytes: 400, LifetimeTotalBytes: 1000},
			{ID: 2, UserID: 1, PanelID: 11, LifetimeUpBytes: 60, LifetimeDownBytes: 40, LifetimeTotalBytes: 100},
		}},
	}
	svc := &Service{} // no ownership repo wired → a fully-migrated user
	svc.SetPSPClientRepo(psp)

	// Σlifetime = 1100, admin override used = 550 → f = (1100-550)/1100 = 0.5, so
	// each client's period baseline becomes half its lifetime and Σ(period) =
	// Σ(lifetime·(1-f)) = 550, matching the override.
	svc.reseedClientBaselines(context.Background(), 1, 550)

	if psp.batchUpdated == nil {
		t.Fatal("psp_client baselines were not reseeded for a migrated user")
	}
	got := map[int64]*domain.PSPClient{}
	for _, c := range psp.batchUpdated {
		got[c.ID] = c
	}
	if c := got[1]; c == nil || c.PeriodBaselineTotalBytes != 500 {
		t.Fatalf("client 1 period baseline = %+v, want total 500", c)
	}
	if c := got[2]; c == nil || c.PeriodBaselineTotalBytes != 50 {
		t.Fatalf("client 2 period baseline = %+v, want total 50", c)
	}
}

// UserServerUsage must reconstruct the per-server breakdown from psp_client for a
// migrated user (who has NO ownership rows): period = lifetime − baseline, and
// node_count = distinct attached nodes per panel.
func TestUserServerUsage_FromSharedClients(t *testing.T) {
	psp := &fakePSPClientRepo{
		byUser: map[int64][]*domain.PSPClient{1: {
			{ID: 1, UserID: 1, PanelID: 10, LifetimeUpBytes: 600, LifetimeDownBytes: 400, LifetimeTotalBytes: 1000,
				PeriodBaselineUpBytes: 200, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 300},
			{ID: 2, UserID: 1, PanelID: 11, LifetimeUpBytes: 50, LifetimeDownBytes: 50, LifetimeTotalBytes: 100},
		}},
		inbounds: map[int64][]domain.PSPClientInbound{
			1: {{NodeID: 5}, {NodeID: 6}}, // panel 10 → 2 nodes
			2: {{NodeID: 7}},              // panel 11 → 1 node
		},
	}
	svc := New(nil, nil, nil, nil, nil, &fakeXUIPool{}, nil)
	svc.SetPSPClientRepo(psp)

	rows, err := svc.UserServerUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 server rows, got %d: %+v", len(rows), rows)
	}
	byPanel := map[int64]ServerUsageRow{}
	for _, r := range rows {
		byPanel[r.PanelID] = r
	}
	if p := byPanel[10]; p.LifetimeTotalBytes != 1000 || p.PeriodTotalBytes != 700 || p.NodeCount != 2 {
		t.Fatalf("panel 10 = %+v, want lifetime 1000 / period 700 / nodes 2", p)
	}
	if p := byPanel[11]; p.LifetimeTotalBytes != 100 || p.PeriodTotalBytes != 100 || p.NodeCount != 1 {
		t.Fatalf("panel 11 = %+v, want lifetime 100 / period 100 / nodes 1", p)
	}
}

// recordSharedClientStats must seed (zero delta, no lifetime advance) on the
// FIRST observation so a shared client read mid-stream can't spike the user's
// quota, then report real monotonic deltas, then no-op when idle.
func TestRecordSharedClientStats_SeedThenDeltaThenIdle(t *testing.T) {
	s := &Service{}
	sink := &pollSink{}
	c := &domain.PSPClient{ID: 1}

	// First observation with a non-zero counter → seed only.
	d := s.recordSharedClientStats(context.Background(), c, 100, 50, sink)
	if d.up != 0 || d.down != 0 || d.total != 0 || d.hadPrev {
		t.Fatalf("first obs must seed with zero delta: %+v", d)
	}
	if c.LifetimeTotalBytes != 0 {
		t.Fatalf("first obs must NOT advance lifetime, got %d", c.LifetimeTotalBytes)
	}
	if c.LastRawUpBytes != 100 || c.LastRawDownBytes != 50 || c.LastRawTotalBytes != 150 {
		t.Fatalf("first obs must set the raw baseline: %+v", c)
	}

	// Second observation → real delta, lifetime advances by exactly the delta.
	d = s.recordSharedClientStats(context.Background(), c, 180, 70, sink)
	if d.up != 80 || d.down != 20 || d.total != 100 || !d.hadPrev {
		t.Fatalf("delta = %+v, want up80 down20 total100 hadPrev=true", d)
	}
	if c.LifetimeUpBytes != 80 || c.LifetimeDownBytes != 20 || c.LifetimeTotalBytes != 100 {
		t.Fatalf("lifetime must advance by the delta: %+v", c)
	}

	// Same counter again → idle no-op (no further lifetime change).
	d = s.recordSharedClientStats(context.Background(), c, 180, 70, sink)
	if d.up != 0 || d.down != 0 || d.total != 0 {
		t.Fatalf("idle must be a no-op delta, got %+v", d)
	}
	if c.LifetimeTotalBytes != 100 {
		t.Fatalf("idle must not change lifetime, got %d", c.LifetimeTotalBytes)
	}
}

// A genuinely-idle client (0/0) on first sight writes nothing at all.
func TestRecordSharedClientStats_IdleZeroFirstObsNoWrite(t *testing.T) {
	s := &Service{}
	sink := &pollSink{}
	d := s.recordSharedClientStats(context.Background(), &domain.PSPClient{ID: 2}, 0, 0, sink)
	if d.up != 0 || d.down != 0 || d.total != 0 || d.hadPrev {
		t.Fatalf("idle-zero first obs must be a pure no-op: %+v", d)
	}
	if len(sink.pspClientUpdates) != 0 {
		t.Fatalf("idle-zero first obs must not queue a counter write, got %d", len(sink.pspClientUpdates))
	}
}

func TestRecordSharedClientStats_NativeEpochResetSeedsThenAccrues(t *testing.T) {
	s := &Service{}
	sink := &pollSink{}
	c := &domain.PSPClient{
		ID: 3, LifetimeUpBytes: 50, LifetimeDownBytes: 50, LifetimeTotalBytes: 100,
		LastRawUpBytes: 900, LastRawDownBytes: 800, LastRawTotalBytes: 1700,
		LastCounterEpoch: 4,
	}

	reset := s.recordSharedClientStats(context.Background(), c, 20, 30, sink, 5)
	if reset.total != 0 || c.LifetimeTotalBytes != 100 || c.LastCounterEpoch != 5 ||
		c.LastRawUpBytes != 20 || c.LastRawDownBytes != 30 {
		t.Fatalf("epoch change must seed without charging: delta=%+v client=%+v", reset, c)
	}
	delta := s.recordSharedClientStats(context.Background(), c, 27, 41, sink, 5)
	if delta.up != 7 || delta.down != 11 || delta.total != 18 || c.LifetimeTotalBytes != 118 {
		t.Fatalf("same native epoch must accrue monotonic delta: delta=%+v client=%+v", delta, c)
	}

	// A decrease without an epoch change is malformed native observation, not
	// a reset signal. Clamp it instead of applying the legacy "take current"
	// heuristic and overcharging the user.
	malformed := s.recordSharedClientStats(context.Background(), c, 1, 2, sink, 5)
	if malformed.total != 0 || c.LifetimeTotalBytes != 118 {
		t.Fatalf("same-epoch decrease must not charge: delta=%+v client=%+v", malformed, c)
	}
}

func TestRecordSharedClientStats_ReinstallationEpochNeedNotIncrease(t *testing.T) {
	s := &Service{}
	c := &domain.PSPClient{
		ID: 4, LifetimeUpBytes: 100, LifetimeDownBytes: 200, LifetimeTotalBytes: 300,
		LastRawUpBytes: 900, LastRawDownBytes: 800, LastRawTotalBytes: 1700,
		LastCounterEpoch: 9000, PeriodBaselineTotalBytes: 50,
	}
	// A new data directory receives an independent counter namespace. Equality,
	// not global numeric ordering, identifies a continued cumulative counter.
	reset := s.recordSharedClientStats(t.Context(), c, 10, 20, &pollSink{}, 2)
	if reset.total != 0 || c.LifetimeTotalBytes != 300 || c.PeriodBaselineTotalBytes != 50 || c.LastCounterEpoch != 2 {
		t.Fatalf("reinstall reset lost PSP usage or charged old baseline: %+v %+v", reset, c)
	}
	delta := s.recordSharedClientStats(t.Context(), c, 15, 27, &pollSink{}, 2)
	if delta.total != 12 || c.LifetimeTotalBytes != 312 || c.PeriodBaselineTotalBytes != 50 {
		t.Fatalf("reinstalled counter did not accrue: %+v %+v", delta, c)
	}
}
