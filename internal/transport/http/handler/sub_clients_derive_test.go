package handler

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestEnabledImportApps is the core consistency guarantee of the v3.2.2 model:
// an app surfaces in the portal iff BOTH its family and the app itself are
// enabled, ordered by Sort. A blocked family hides all its apps even when the
// apps are individually enabled — so a disallowed client can never still be
// advertised for one-click import.
func TestEnabledImportApps(t *testing.T) {
	fams := []ports.SubClientFamily{
		{
			Name: "Clash", Enabled: true,
			Apps: []ports.SubClientApp{
				{Name: "Verge Rev", Enabled: true, Sort: 10},
				{Name: "Disabled App", Enabled: false, Sort: 5},
			},
		},
		{
			// Family disabled → its (enabled) app must NOT surface.
			Name: "V2rayNG", Enabled: false,
			Apps: []ports.SubClientApp{{Name: "V2rayNG", Enabled: true, Sort: 1}},
		},
		{
			Name: "sing-box", Enabled: true,
			Apps: []ports.SubClientApp{{Name: "sing-box", Enabled: true, Sort: 40}},
		},
	}
	got := enabledImportApps(fams)
	if len(got) != 2 {
		t.Fatalf("want 2 apps, got %d: %+v", len(got), got)
	}
	// Ordered by Sort: Verge Rev (10) then sing-box (40); the disabled app and
	// the blocked-family app are excluded.
	if got[0].Name != "Verge Rev" || got[1].Name != "sing-box" {
		t.Fatalf("wrong apps/order: %+v", got)
	}
}
