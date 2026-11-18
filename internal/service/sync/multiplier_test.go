package sync

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type factorNodeRepo struct {
	ports.NodeRepo
	node *domain.Node
}

func (r factorNodeRepo) GetByPanelInbound(context.Context, int64, int) (*domain.Node, error) {
	if r.node == nil {
		return nil, domain.ErrNotFound
	}
	return r.node, nil
}

// A legacy per-node client is its node's direct line: the charged headroom is
// converted through that line's factor before the rebase onto its counter.
func TestPanelWantConvertsHeadroomThroughDirectLineFactor(t *testing.T) {
	for _, tc := range []struct {
		factor   int
		headroom int64
		want     int64
	}{
		{1000, 3000, 3000},
		{1500, 3000, 2000},
		{0, 3000, 0},
		{0, 1, 1},
	} {
		f := tc.factor
		svc := New(&fakePool{}, nil)
		svc.SetNodeRepo(factorNodeRepo{node: &domain.Node{ID: 1, TrafficFactorMilli: &f}})
		got := svc.panelWant(context.Background(), 10, 20, domain.UserLifecycle{QuotaHeadroom: tc.headroom})
		if got.QuotaHeadroom != tc.want {
			t.Errorf("factor %d headroom %d: raw headroom = %d, want %d", tc.factor, tc.headroom, got.QuotaHeadroom, tc.want)
		}
	}
	if got := New(&fakePool{}, nil).panelWant(context.Background(), 10, 20, domain.UserLifecycle{QuotaHeadroom: 3000}); got.QuotaHeadroom != 3000 {
		t.Fatalf("no node repo: %d", got.QuotaHeadroom)
	}
}
