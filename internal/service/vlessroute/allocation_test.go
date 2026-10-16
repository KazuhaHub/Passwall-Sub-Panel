package vlessroute

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAllocationReservesEarlierRulesOnlyInMatchingScope(t *testing.T) {
	template := ports.RouteTemplate{Config: json.RawMessage(`{"routing":{"rules":[{"vlessRoute":"1-10,12"},{"inboundTag":["other"],"vlessRoute":"11"},{"ruleTag":"psp-vlessroute:1:own","vlessRoute":"11"}]}}`)}
	used := map[uint16]bool{0: true}
	if err := reserveExistingRoutes(template, 1, "mine", []domain.VlessRouteLine{{ID: "own"}}, used); err != nil {
		t.Fatal(err)
	}
	if used[11] || !used[1] || !used[10] || !used[12] {
		t.Fatal(used)
	}
	target, err := makeTarget(domain.NodeRouteState{}, []LineInput{{Name: "new", OutboundTag: "direct", Enabled: true}}, used, map[uint16]bool{0: true})
	if err != nil {
		t.Fatal(err)
	}
	if target.Lines[0].Route != 11 {
		t.Fatal(target.Lines[0])
	}
}
