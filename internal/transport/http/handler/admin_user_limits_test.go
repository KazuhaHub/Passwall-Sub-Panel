package handler

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sharedclient"
)

func panelLookup(rows map[int64]*domain.XUIPanel) func(int64) *domain.XUIPanel {
	return func(id int64) *domain.XUIPanel { return rows[id] }
}

// The two halves come from different places on purpose, and this pins WHICH.
//
// Capabilities are the adapter's answer, read through the pool. The fail2ban
// verdict is the panel ROW's, because UpdateIPLimitEnforcement writes only the
// row: a pool-cached copy is frozen at panel registration, so reading
// enforcement from there would report a node as enforcing long after its probe
// stopped agreeing — the precise failure this endpoint exists to expose.
func TestBuildLimitEnforcementDTO(t *testing.T) {
	probed := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	rows := map[int64]*domain.XUIPanel{
		10: {ID: 10, Name: "de-fra", Kind: domain.PanelKind("xui"),
			IPLimitEnforcement: domain.IPLimitEnforcementEnforced, IPLimitProbedAt: &probed},
		20: {ID: 20, Name: "jp-tyo", Kind: domain.PanelKind("sui")},
	}
	got := buildLimitEnforcementDTO(2, 3, []sharedclient.PanelLimitFacts{
		{PanelID: 10, Clients: 2, CanStoreIP: true, CanStoreDevice: true},
		{PanelID: 20, Clients: 1},
	}, panelLookup(rows))

	if got.IPLimit != 2 || got.DeviceLimit != 3 {
		t.Fatalf("caps = %d/%d, want 2/3", got.IPLimit, got.DeviceLimit)
	}
	if got.DeviceLimitEnforcedAnywhere {
		t.Fatal("no panel enforces limitHwid for a PSP user on any version — PSP serves the subscriptions upstream would have counted devices at")
	}
	if len(got.Panels) != 2 {
		t.Fatalf("panels = %d, want 2", len(got.Panels))
	}
	if got.Panels[0].Name != "de-fra" || got.Panels[0].IPLimitEnforcement != "enforced" {
		t.Fatalf("panel 10 = %+v", got.Panels[0])
	}
	if got.Panels[0].IPLimitProbedAt == nil || !got.Panels[0].IPLimitProbedAt.Equal(probed) {
		t.Fatalf("probed-at must ride along so a stale verdict is visible: %+v", got.Panels[0])
	}
	// A panel with no stored verdict is "unknown", never blank: an absent
	// verdict and an unknown one mean the same thing, and a blank invites a
	// client to read it as "nothing to worry about".
	if got.Panels[1].IPLimitEnforcement != string(domain.IPLimitEnforcementUnknown) {
		t.Fatalf("panel 20 verdict = %q, want %q", got.Panels[1].IPLimitEnforcement, domain.IPLimitEnforcementUnknown)
	}
	if got.Panels[1].CanStoreIP || got.Panels[1].CanStoreDevice {
		t.Fatalf("panel 20 carries neither cap: %+v", got.Panels[1])
	}
}

// A client whose panel row is gone must still appear. Dropping it would shrink
// the list silently, hiding the single panel whose state nobody can vouch for
// — and the operator would read the shorter list as a complete one.
func TestBuildLimitEnforcementDTOKeepsAPanelWhoseRowIsGone(t *testing.T) {
	got := buildLimitEnforcementDTO(2, 0, []sharedclient.PanelLimitFacts{
		{PanelID: 404, Clients: 1, CanStoreIP: true},
	}, panelLookup(nil))

	if len(got.Panels) != 1 {
		t.Fatalf("panels = %d, want 1", len(got.Panels))
	}
	if !got.Panels[0].PanelMissing {
		t.Fatalf("want PanelMissing: %+v", got.Panels[0])
	}
	if got.Panels[0].IPLimitEnforcement != string(domain.IPLimitEnforcementUnknown) {
		t.Fatalf("an unreadable panel's verdict must be unknown: %+v", got.Panels[0])
	}
}
