package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The desired headroom is CHARGED bytes; a 3X-UI / S-UI panel gates each
// client's RAW counter, so the cap pushed is the counter plus the headroom
// converted through that client's LINE factor (domain.RawHeadroom).
func TestSyncLifecycle_ConvertsHeadroomThroughLineFactor(t *testing.T) {
	const GB = int64(1) << 30
	for _, tc := range []struct {
		name     string
		line     string
		direct   *int
		relay    *int
		headroom int64
		want     int64
	}{
		{"default factor unchanged", domain.DirectLineID, nil, nil, 40 * GB, 100 * GB},
		{"×2 direct line halves the raw allowance", domain.DirectLineID, intp(2000), nil, 40 * GB, 80 * GB},
		{"relay line uses its own factor", "r1", intp(2000), intp(500), 40 * GB, 140 * GB},
		{"free line is not gated by its counter", "r1", nil, intp(0), 40 * GB, 0},
		{"exhausted stays exhausted on a free line", "r1", nil, intp(0), 1, 60*GB + 1},
		{"unlimited stays unlimited", domain.DirectLineID, intp(2000), nil, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := &fakeClients{attachments: []domain.PSPClientInbound{
				{ClientID: 1, NodeID: 11, State: domain.ClientApplyApplied},
			}}
			xui := &fakeXUI{}
			node := enabledNode(11, 10, 101)
			node.TrafficFactorMilli = tc.direct
			node.Relays = []domain.RelayLine{{ID: "r1", Enabled: true, TrafficFactorMilli: tc.relay}}
			svc := New(clients, fakePool{c: xui}, fakeNodes{byID: map[int64]*domain.Node{11: node}})
			c := &domain.PSPClient{ID: 1, PanelID: 10, NodeID: 11, LineID: tc.line, Email: "u1@psp.local", UUID: "uuid-x", LastRawTotalBytes: 60 * GB}
			if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true, QuotaHeadroom: tc.headroom}); err != nil {
				t.Fatal(err)
			}
			if got := xui.updatedSpec.TotalGB; got != tc.want {
				t.Fatalf("pushed totalGB = %d, want %d", got, tc.want)
			}
		})
	}
}

// A row whose node is gone keeps the factor it was last booked at.
func TestTrafficFactorFallsBackToLastBookedFactor(t *testing.T) {
	svc := New(&fakeClients{}, fakePool{}, fakeNodes{byID: map[int64]*domain.Node{}})
	c := &domain.PSPClient{NodeID: 99, LineID: "r", LastFactorMilli: intp(3000)}
	if got := svc.trafficFactor(context.Background(), c); got != 3000 {
		t.Fatalf("factor = %d, want 3000", got)
	}
	if got := svc.trafficFactor(context.Background(), &domain.PSPClient{}); got != domain.TrafficFactorDefault {
		t.Fatalf("legacy row factor = %d", got)
	}
}

func intp(v int) *int { return &v }
