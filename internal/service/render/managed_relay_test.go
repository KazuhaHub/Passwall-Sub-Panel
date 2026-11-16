package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A managed relay line enters a subscription only once its entry confirmed
// the forward; an external line follows Enabled alone.
func TestExpandRelaysWithholdsUnconfirmedManagedLines(t *testing.T) {
	node := &domain.Node{ID: 1, DisplayName: "JP", Enabled: true, Relays: []domain.RelayLine{
		{ID: "external", Name: "ext", Address: "e.example", Enabled: true},
		{ID: "pending", Name: "pending", Address: "p.example", Port: 1, Enabled: true, EntryPanelID: 9},
		{ID: "ready", Name: "ready", Address: "r.example", Port: 2, Enabled: true, EntryPanelID: 9, ManagedReady: true},
		{ID: "ready-off", Name: "off", Address: "o.example", Port: 3, Enabled: false, EntryPanelID: 9, ManagedReady: true},
	}}
	items := expandRelays([]renderItem{{node: node, name: "JP"}})
	var got []string
	for _, item := range items {
		if item.relay != nil {
			got = append(got, item.relay.ID)
		}
	}
	if len(got) != 2 || got[0] != "external" || got[1] != "ready" {
		t.Fatalf("rendered relay lines = %v", got)
	}
}
