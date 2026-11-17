package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// factorPool is a pool that knows each panel's traffic factor.
type factorPool struct {
	fakePool
	factor int
}

func (p factorPool) TrafficFactorOf(int64) (int, bool) { return p.factor, true }

// The desired headroom is CHARGED bytes; a 3X-UI / S-UI panel gates its RAW
// counter, so the cap pushed is the counter plus the headroom converted
// through the server's factor (domain.RawHeadroom), never the bare headroom.
func TestSyncLifecycle_ConvertsHeadroomThroughServerFactor(t *testing.T) {
	const GB = int64(1) << 30
	for _, tc := range []struct {
		name     string
		factor   int
		headroom int64
		want     int64
	}{
		{"default factor unchanged", 1000, 40 * GB, 100 * GB},
		{"×2 halves the raw allowance", 2000, 40 * GB, 80 * GB},
		{"×0.5 doubles it", 500, 40 * GB, 140 * GB},
		{"free server is not gated by its counter", 0, 40 * GB, 0},
		{"exhausted stays exhausted on a free server", 0, 1, 60*GB + 1},
		{"unlimited stays unlimited", 2000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := &fakeClients{attachments: []domain.PSPClientInbound{
				{ClientID: 1, NodeID: 11, State: domain.ClientApplyApplied},
			}}
			xui := &fakeXUI{}
			svc := New(clients, factorPool{fakePool: fakePool{c: xui}, factor: tc.factor}, fakeNodes{})
			c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", LastRawTotalBytes: 60 * GB}
			if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true, QuotaHeadroom: tc.headroom}); err != nil {
				t.Fatal(err)
			}
			if got := xui.updatedSpec.TotalGB; got != tc.want {
				t.Fatalf("pushed totalGB = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBuildSharedClientSpecConvertsHeadroom(t *testing.T) {
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u@x", UUID: "u", LastRawTotalBytes: 1000}
	c.SetDesiredLifecycle(domain.UserLifecycle{Enable: true, QuotaHeadroom: 3000})
	if got := buildSharedClientSpec(c, "", 1500).TotalGB; got != 1000+2000 {
		t.Fatalf("totalGB = %d, want 3000 (1000 counted + 3000 charged at ×1.5)", got)
	}
	if got := buildSharedClientSpec(c, "", 1000).TotalGB; got != 4000 {
		t.Fatalf("totalGB at default = %d, want 4000", got)
	}
}
