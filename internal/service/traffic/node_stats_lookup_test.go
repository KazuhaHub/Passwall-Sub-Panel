package traffic

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// bareNodeRepo is a node repo that can read a node's counters without the
// attached side state (routes, links, probes), as the SQL repo can. Reaching
// GetByPanelInbound through it is the cost the poll must not pay per inbound.
type bareNodeRepo struct {
	*fakeNodeRepo
	bareCalls int
}

func (r *bareNodeRepo) GetByPanelInboundBare(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	r.bareCalls++
	return r.fakeNodeRepo.GetByPanelInbound(ctx, panelID, inboundID)
}

func newStatsNodes() *fakeNodeRepo {
	return &fakeNodeRepo{
		nodes:   map[int64]*domain.Node{7: {ID: 7, PanelID: 3, InboundID: 9, LastInboundSeeded: true, LastInboundUpBytes: 10, LastInboundDownBytes: 20, LastInboundTotalBytes: 30, LifetimeTotalBytes: 100}},
		byMatch: map[fakeNodeKey]int64{{panelID: 3, inboundID: 9}: 7},
	}
}

// The per-inbound lookup of the poll reads only counter columns, so a repo that
// has a light reader is asked for that and not for the full node, with the same
// accounting result.
func TestRecordNodeStatsUsesTheLightNodeReader(t *testing.T) {
	for name, tc := range map[string]struct {
		light bool
	}{"light reader": {true}, "plain repo": {false}} {
		t.Run(name, func(t *testing.T) {
			plain := newStatsNodes()
			svc := New(nil, nil, nil, plain, &fakeNodeTrafficRepo{}, nil, nil)
			var bare *bareNodeRepo
			if tc.light {
				bare = &bareNodeRepo{fakeNodeRepo: plain}
				svc = New(nil, nil, nil, bare, &fakeNodeTrafficRepo{}, nil, nil)
			}
			sink := &pollSink{nodeCounterUpdates: map[int64]*domain.Node{}}
			if err := svc.recordNodeStats(context.Background(), 3, 9, 15, 25, sink); err != nil {
				t.Fatal(err)
			}
			n := sink.nodeCounterUpdates[7]
			if n == nil || n.LifetimeTotalBytes != 110 || n.LastInboundUpBytes != 15 || n.LastInboundDownBytes != 25 || n.LastInboundTotalBytes != 40 {
				t.Fatalf("counters = %+v, want lifetime 110 and last inbound 15/25/40", n)
			}
			if len(sink.nodeSnaps) != 1 {
				t.Fatalf("snapshots = %d, want 1", len(sink.nodeSnaps))
			}
			// An idle inbound writes nothing.
			if err := svc.recordNodeStats(context.Background(), 3, 9, 10, 20, &pollSink{nodeCounterUpdates: map[int64]*domain.Node{}}); err != nil {
				t.Fatal(err)
			}
			if tc.light && bare.bareCalls != 2 {
				t.Fatalf("light reader calls = %d, want 2", bare.bareCalls)
			}
			// ErrNotFound still means "skip".
			if err := svc.recordNodeStats(context.Background(), 3, 99, 1, 1, sink); err != nil {
				t.Fatalf("an unknown inbound must be skipped, got %v", err)
			}
		})
	}
}
