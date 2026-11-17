package sync

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type factorPool struct {
	fakePool
	factor int
}

func (p *factorPool) TrafficFactorOf(int64) (int, bool) { return p.factor, true }

// The legacy per-node push converts the charged headroom into the panel's raw
// bytes before rebasing onto its counter.
func TestPanelWantConvertsHeadroomThroughServerFactor(t *testing.T) {
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
		svc := New(&factorPool{factor: tc.factor}, nil)
		got := svc.panelWant(10, domain.UserLifecycle{QuotaHeadroom: tc.headroom})
		if got.QuotaHeadroom != tc.want {
			t.Errorf("factor %d headroom %d: raw headroom = %d, want %d", tc.factor, tc.headroom, got.QuotaHeadroom, tc.want)
		}
		if spec := buildClientSpec(domain.ProtoVLESS, "", "u", "e", "", got, 500); tc.want > 0 && spec.TotalGB != 500+tc.want {
			t.Errorf("factor %d: totalGB = %d, want %d", tc.factor, spec.TotalGB, 500+tc.want)
		}
	}
}
