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
// This is what justified the Phase 1a deadband, and now measures it. Read the
// numbers as a stress case, not a forecast: every client here sustains 3.6 to
// 25 Mbps for fifty consecutive cycles, so the sibling drift the band has to
// swallow is close to the worst a real fleet produces.
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
			// Floors, not targets, and deliberately low: this model is a
			// worst case roughly 40x harsher at p95 than the only real
			// deployment we have measured (see the production-calibrated
			// test below, which is where a meaningful floor lives). What
			// these guard is a regression to the pre-band 0%.
			if min := map[int]float64{2: 40, 3: 20}[P]; min > 0 && rate < min {
				t.Errorf("P=%d skip rate %.1f%%, want at least %.0f%% — the deadband has stopped absorbing sibling drift", P, rate, min)
			}
			if P > 1 {
				band := counterByName(t, "psp_lifecycle_quota_band_skip_total")
				if band == 0 {
					t.Errorf("P=%d skipped %d calls but the band absorbed none of them — the skips are coming from somewhere else", P, skipped)
				}
			}
		})
	}
}

// measuredDriftLadder reproduces the per-client per-cycle traffic implied by a
// real 40.9-hour production window (v3.9.2-beta.8, 25 users, 9 nodes, 5 panels,
// P=4.0, 29296 compares). The window reported the SIBLING drift a client sees:
//
//	p50 7.0 KiB   p90 3.58 MiB   p95 14.9 MiB   mean 4.19 MiB   max 607 MiB
//
// Sibling drift is the sum over the other P-1 clients, so at the measured P=4
// each client contributes about a third of it. The ladder below is that third,
// laid out so its own quantiles land on the measured ones. Deterministic by
// construction — indexed, not sampled, because scripts here cannot use rand.
//
// Why this test exists at all: the stress model above was invented before any
// deployment had been measured, and it turned out to be roughly 40x harsher at
// p95 than reality. Tuning the band against it alone would optimise for a
// regime no one runs.
var measuredDriftLadder = func() []int64 {
	const KiB, MiB = int64(1) << 10, int64(1) << 20
	l := make([]int64, 0, 100)
	add := func(n int, v int64) {
		for i := 0; i < n; i++ {
			l = append(l, v)
		}
	}
	// 34% of compares saw no drift at all in the window.
	add(34, 0)
	add(30, 2*KiB)   // through the measured p50 of ~7 KiB of sibling drift
	add(21, 300*KiB) // the long flat middle
	add(9, 1200*KiB) // up to the p90
	add(4, 5*MiB)    // up to the p95
	add(1, 60*MiB)   // the tail
	add(1, 200*MiB)  // the single worst cycle: ~607 MiB of sibling drift
	return l
}()

// The band against the traffic a real deployment actually produces, rather
// than against the worst case imagined before one had been measured. This is
// the number that says whether Phase 1a was worth shipping.
func TestSkipRateAgainstMeasuredProductionDrift(t *testing.T) {
	const GB = int64(1) << 30
	const limit = 100 * GB
	const cycles = 100

	for _, P := range []int{2, 3, 4} {
		t.Run(map[int]string{2: "P=2", 3: "P=3", 4: "P=4 (the measured value)"}[P], func(t *testing.T) {
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
				for i := range pspClients {
					// Offset per client so they do not move in lockstep,
					// which would make every sibling drift identical.
					d := measuredDriftLadder[(cycle*7+i*31)%len(measuredDriftLadder)]
					periodUsed += d
					pspClients[i].LastRawTotalBytes += d
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
			absorbed := counterByName(t, "psp_lifecycle_quota_band_skip_total")
			rate := float64(skipped) / float64(total) * 100
			t.Logf("P=%d over %d cycles on measured drift: total=%d skipped=%d writes=%d "+
				"band-absorbed=%d -> skip rate %.1f%% (production measured 33.9%% WITHOUT the band)",
				P, cycles, total, skipped, writes, absorbed, rate)

			// The production window ran at 33.9% with no band at all. Anything
			// at or below that would mean the band bought nothing on the only
			// traffic we have actually observed.
			if rate <= 33.9 {
				t.Errorf("P=%d skip rate %.1f%% is no better than the 33.9%% measured WITHOUT a band", P, rate)
			}
			if absorbed == 0 {
				t.Errorf("P=%d: the band absorbed nothing; the skips are coming from somewhere else", P)
			}
		})
	}
}
