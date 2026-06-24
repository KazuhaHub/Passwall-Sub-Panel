package traffic

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakePSPClientRepo struct {
	ports.PSPClientRepo
	byUser   map[int64][]*domain.PSPClient
	inbounds map[int64][]domain.PSPClientInbound // clientID -> attachments
}

func (f *fakePSPClientRepo) ListByUser(_ context.Context, uid int64) ([]*domain.PSPClient, error) {
	return f.byUser[uid], nil
}
func (f *fakePSPClientRepo) ListInbounds(_ context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	return f.inbounds[clientID], nil
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
