package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// P (clients per user) cannot answer the question the connection caps need
// answered. A user on four panels with one client each and a user on one panel
// split four ways both report P=4, but only the second is PSP multiplying a cap
// the admin typed once — 3X-UI budgets IPs per client email, and a split client
// carries its own copy of the cap.
//
// This pins that the per-panel histogram actually separates them, because a
// metric that quietly reports the same thing twice is worse than no metric: it
// would look like corroboration.
func TestClientsPerPanelSeparatesTheTwoMultipliers(t *testing.T) {
	cases := []struct {
		name    string
		panels  []int64 // one entry per client, naming its panel
		wantObs []float64
	}{
		{
			name:    "four panels, one client each — inherent, not PSP's doing",
			panels:  []int64{1, 2, 3, 4},
			wantObs: []float64{1, 1, 1, 1},
		},
		{
			name:    "one panel, split four ways — PSP multiplies the cap 4x",
			panels:  []int64{7, 7, 7, 7},
			wantObs: []float64{4},
		},
		{
			name:    "two panels, one split — 2x on one of them",
			panels:  []int64{1, 1, 2},
			wantObs: []float64{2, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clients := make([]*domain.PSPClient, 0, len(tc.panels))
			for _, p := range tc.panels {
				clients = append(clients, &domain.PSPClient{PanelID: p})
			}
			got := clientsPerPanel(clients)
			if len(got) != len(tc.wantObs) {
				t.Fatalf("observations = %v, want %v", got, tc.wantObs)
			}
			// Order is map-iteration order; compare as multisets.
			counts := map[float64]int{}
			for _, v := range got {
				counts[v]++
			}
			for _, w := range tc.wantObs {
				counts[w]--
			}
			for v, n := range counts {
				if n != 0 {
					t.Fatalf("observations %v do not match %v (value %v off by %d)", got, tc.wantObs, v, n)
				}
			}
		})
	}
}

// The histogram must be fed by the SYNC LOOP, not merely computable.
//
// A test that calls clientsPerPanel and Observe itself proves the arithmetic
// and nothing about whether production ever runs it — deleting the call from
// SyncUserLifecycle left such a test green, verified by mutation. So this
// drives the real entry point. The lifecycle push then fails (there is no
// panel behind the fake pool) and that is fine: the observation happens before
// any of it, which is the property under test.
func TestSyncUserLifecycleFeedsTheClientsPerPanelHistogram(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{
		{UserID: 9, PanelID: 1}, {UserID: 9, PanelID: 1}, {UserID: 9, PanelID: 2},
	}}
	svc := New(clients, fakePool{}, nil)

	before := metrics.UserClientsPerPanel.Count()
	_ = svc.SyncUserLifecycle(context.Background(), 9, domain.UserLifecycle{})
	got := metrics.UserClientsPerPanel.Count()

	// Two panels for this user, so two samples — not three (that would be P
	// again) and not one.
	if got != before+2 {
		t.Fatalf("observations %d -> %d, want +2 (one per panel the user is on)", before, got)
	}
}

// A metric nobody can read is not a measurement. NewHistogram registers into
// the shared registry, so the diagnostics endpoint should carry the new series
// without extra wiring — "should" being the word that has cost time before, so
// it is asserted rather than assumed.
func TestClientsPerPanelReachesTheDiagnosticsSnapshot(t *testing.T) {
	want := map[string]bool{
		"psp_user_clients_per_panel": false,
		"psp_user_client_count":      false, // the series it must be read beside
	}
	for _, h := range metrics.Take().Histograms {
		if _, ok := want[h.Name]; ok {
			want[h.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s is absent from the diagnostics snapshot; the measurement would be unreachable", name)
		}
	}
}
