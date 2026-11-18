package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestApplyFactorPlaceholderUsesEachEntrysLine(t *testing.T) {
	direct, relayFactor := 1500, 0
	hk := &domain.Node{ID: 1, PanelID: 10, TrafficFactorMilli: &direct,
		Relays: []domain.RelayLine{{ID: "r", TrafficFactorMilli: &relayFactor}}}
	jp := &domain.Node{ID: 2, PanelID: 20}
	items := []renderItem{
		{node: hk, name: "HK {{ factor }}"},
		{node: hk, relay: &hk.Relays[0], name: "HK relay {{factor}}"},
		{node: jp, name: "JP"}, // no token: untouched
		{node: jp, name: "JP {{ factor }}"},
		{isSeparator: true, name: "---- {{ factor }} ----"},
	}
	applyFactorPlaceholder(items)
	want := []string{"HK ×1.5", "HK relay ×0", "JP", "JP ×1", "---- {{ factor }} ----"}
	for i, w := range want {
		if items[i].name != w {
			t.Errorf("item %d = %q, want %q", i, items[i].name, w)
		}
	}
	if got := formatTrafficFactor(1250); got != "×1.25" {
		t.Errorf("formatTrafficFactor(1250) = %q", got)
	}
}

// Aliases registered for unavailable lines carry the resolved name too, so a
// template reference to the rendered name keeps resolving while a line is
// stopped.
func TestLineNameAliasesResolveFactorPlaceholder(t *testing.T) {
	f := 1500
	node := &domain.Node{ID: 7, PanelID: 10, DisplayName: "HK {{ factor }}", TrafficFactorMilli: &f}
	catalog, _ := newLineNameCatalog(nil, false, []*domain.Node{node})
	for _, line := range catalog.lines {
		if line.base == "HK ×1.5" {
			return
		}
	}
	t.Fatalf("aliases = %+v, want base %q", catalog.lines, "HK ×1.5")
}
