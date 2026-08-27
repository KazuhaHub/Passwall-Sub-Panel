package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// storingXUI models the one panel behaviour this measurement depends on:
// an UpdateClient stores what PSP wrote, and the next GetClient returns it.
// That fidelity is not assumed — it is established separately against a real
// 3X-UI 3.7.0 panel by TestLive_XUITrafficFloorMatrix, whose "the pushed cap
// does not move as traffic accrues" case round-trips the value through the
// panel itself. This test supplies the other half: given a panel that behaves
// that way, how often does the no-op skip actually fire?
type storingXUI struct {
	fakeXUI
	stored *ports.ClientDetail
	writes int
}

func (c *storingXUI) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{
		ports.CapabilityClientWrite,
		ports.CapabilityClientIPLimit,
		ports.CapabilityClientDeviceLimit,
	}
}

func (c *storingXUI) GetClient(context.Context, string) (*ports.ClientDetail, error) {
	return c.stored, nil
}

func (c *storingXUI) UpdateClient(_ context.Context, spec ports.ClientSpec) error {
	c.writes++
	c.stored = &ports.ClientDetail{
		ID: spec.ID, Email: spec.Email, Enable: spec.Enable, Flow: spec.Flow,
		Password: spec.Password, Auth: spec.Auth,
		ExpiryTime: spec.ExpiryTime, TotalGB: spec.TotalGB,
		LimitIP: spec.LimitIP, LimitHwid: spec.LimitHwid,
	}
	return nil
}

// TestSkipRateForAnActiveUser is the Phase 0 question asked in miniature:
// docs/data-plane-plan.md §1.2 claimed the no-op skip is structurally defeated
// for every active user, because the pushed quota floor shrinks every cycle.
//
// That shrinking turned out to BE the traffic-floor defect
// (docs/traffic-floor-defect.md). Rebased, the pushed value is
// lastRaw + (limit - periodUsed), and the panel counter grows by exactly the
// delta the headroom loses — so the cap should not move at all, and the skip
// should fire on every cycle after the first.
//
// This drives the real SyncLifecycle over many cycles of accruing traffic and
// measures it, rather than reasoning about it.
func TestSkipRateForAnActiveUser(t *testing.T) {
	const GB = int64(1) << 30
	const limit = 100 * GB
	const cycles = 50

	metrics.Reset()
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, Provisioned: true},
	}}
	xui := &storingXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x"}
	var periodUsed int64

	for cycle := 0; cycle < cycles; cycle++ {
		// Uneven deltas so nothing cancels by coincidence.
		delta := int64(cycle%7+1) * 137 * (1 << 20)
		periodUsed += delta
		// The poll folds the same delta into the client's panel-side counter
		// before the push goroutine reads it.
		c.LastRawTotalBytes += delta

		if err := svc.SyncLifecycle(context.Background(), c, domain.UserLifecycle{
			Enable: true, QuotaHeadroom: limit - periodUsed,
		}); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
	}

	total := counterByName(t, "psp_lifecycle_sync_total")
	skipped := counterByName(t, "psp_lifecycle_sync_skipped_total")
	writes := counterByName(t, "psp_lifecycle_sync_write_total")
	t.Logf("over %d cycles: total=%d skipped=%d writes=%d (panel saw %d UpdateClient calls)",
		cycles, total, skipped, writes, xui.writes)

	// One write to establish the value on a panel that has never seen it; every
	// later cycle must skip. Before the floor fix this was writes=cycles.
	if writes != 1 {
		t.Errorf("writes = %d, want 1 — an active user whose cap does not move must be written once, then skipped", writes)
	}
	if skipped != cycles-1 {
		t.Errorf("skipped = %d, want %d", skipped, cycles-1)
	}
	if got := counterByName(t, "psp_lifecycle_sync_write_reason_total{reason=total_gb}"); got != 0 {
		t.Errorf("total_gb defeated the skip %d times — the quota floor is still moving", got)
	}
}

// The counterpart: the skip must NOT swallow a change that matters. A quota
// the admin actually raises moves the cap and has to reach the panel.
func TestSkipDoesNotSwallowARealQuotaChange(t *testing.T) {
	const GB = int64(1) << 30
	metrics.Reset()
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, Provisioned: true},
	}}
	xui := &storingXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", LastRawTotalBytes: 40 * GB}

	push := func(limit int64) {
		t.Helper()
		if err := svc.SyncLifecycle(context.Background(), c, domain.UserLifecycle{
			Enable: true, QuotaHeadroom: limit - 40*GB,
		}); err != nil {
			t.Fatal(err)
		}
	}
	push(100 * GB) // establish
	push(100 * GB) // unchanged -> skip
	if xui.writes != 1 {
		t.Fatalf("writes = %d, want 1 after an unchanged repeat", xui.writes)
	}
	push(200 * GB) // the admin doubled the quota -> must reach the panel
	if xui.writes != 2 {
		t.Fatalf("writes = %d, want 2 — a real quota change must not be skipped", xui.writes)
	}
	if xui.stored.TotalGB != 40*GB+160*GB {
		t.Errorf("stored cap = %d, want %d", xui.stored.TotalGB, 200*GB)
	}
}

// The caveat recorded in docs/traffic-floor-defect.md §6.3, measured rather
// than asserted: with more than one client the cap is NOT invariant, because
// QuotaHeadroom is user-level (it shrinks by every client's traffic) while
// each client rebases onto only its OWN panel counter. One client's cap
// therefore drifts by the other clients' traffic.
//
// This quantifies the residue that survives the floor fix — the only input
// left that could still justify a Phase 1a deadband.
func TestSkipRateWithSeveralClientsPerUser(t *testing.T) {
	const GB = int64(1) << 30
	const limit = 100 * GB
	const cycles = 50

	for _, P := range []int{1, 2, 3} {
		t.Run(map[int]string{1: "P=1", 2: "P=2", 3: "P=3"}[P], func(t *testing.T) {
			metrics.Reset()
			clients := &fakeClients{attachments: []domain.PSPClientInbound{
				{ClientID: 1, NodeID: 11, Provisioned: true},
			}}
			panels := make([]*storingXUI, P)
			pspClients := make([]*domain.PSPClient, P)
			for i := range panels {
				panels[i] = &storingXUI{}
				pspClients[i] = &domain.PSPClient{
					ID: int64(i + 1), PanelID: int64(10 + i),
					Email: "u1@psp.local", UUID: "uuid-x",
				}
			}

			var periodUsed int64
			for cycle := 0; cycle < cycles; cycle++ {
				// Every client moves some traffic this cycle.
				for i := range pspClients {
					delta := int64((cycle+i)%7+1) * 137 * (1 << 20)
					periodUsed += delta // user-level: the sum
					pspClients[i].LastRawTotalBytes += delta
				}
				for i, pc := range pspClients {
					svc := New(clients, fakePool{c: panels[i]}, fakeNodes{})
					if err := svc.SyncLifecycle(context.Background(), pc, domain.UserLifecycle{
						Enable: true, QuotaHeadroom: limit - periodUsed,
					}); err != nil {
						t.Fatalf("P=%d cycle %d client %d: %v", P, cycle, i, err)
					}
				}
			}

			total := counterByName(t, "psp_lifecycle_sync_total")
			skipped := counterByName(t, "psp_lifecycle_sync_skipped_total")
			writes := counterByName(t, "psp_lifecycle_sync_write_total")
			rate := float64(skipped) / float64(total) * 100
			t.Logf("P=%d over %d cycles: total=%d skipped=%d writes=%d -> skip rate %.1f%% "+
				"(pre-fix this was 0%%)", P, cycles, total, skipped, writes, rate)

			if P == 1 && rate < 95 {
				t.Errorf("P=1 skip rate %.1f%%, want ~98%% — the cap should be invariant", rate)
			}
		})
	}
}
