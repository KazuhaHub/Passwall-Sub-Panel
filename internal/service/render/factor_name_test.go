package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestApplyFactorPlaceholder(t *testing.T) {
	hk := &domain.Node{ID: 1, PanelID: 10}
	jp := &domain.Node{ID: 2, PanelID: 20}
	items := []renderItem{
		{node: hk, name: "HK {{ factor }}"},
		{node: hk, name: "HK relay {{factor}}"},
		{node: jp, name: "JP"}, // no token: untouched whatever the factor
		{node: jp, name: "JP {{ factor }}"},
		{isSeparator: true, name: "---- {{ factor }} ----"},
	}
	applyFactorPlaceholder(items, func(panelID int64) int {
		return map[int64]int{10: 1500, 20: 0}[panelID]
	})
	want := []string{"HK ×1.5", "HK relay ×1.5", "JP", "JP ×0", "---- {{ factor }} ----"}
	for i, w := range want {
		if items[i].name != w {
			t.Errorf("item %d = %q, want %q", i, items[i].name, w)
		}
	}
	if got := formatTrafficFactor(1000); got != "×1" {
		t.Errorf("formatTrafficFactor(1000) = %q", got)
	}
	if got := formatTrafficFactor(1250); got != "×1.25" {
		t.Errorf("formatTrafficFactor(1250) = %q", got)
	}
}

// Aliases registered for unavailable lines carry the resolved name too, so a
// template reference to the rendered name keeps resolving while a line is
// stopped.
func TestLineNameAliasesResolveFactorPlaceholder(t *testing.T) {
	node := &domain.Node{ID: 7, PanelID: 10, DisplayName: "HK {{ factor }}"}
	catalog, _ := newLineNameCatalog(nil, false, func(int64) int { return 1500 }, []*domain.Node{node})
	for _, line := range catalog.lines {
		if line.base == "HK ×1.5" {
			return
		}
	}
	t.Fatalf("aliases = %+v, want base %q", catalog.lines, "HK ×1.5")
}
