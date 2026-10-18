package handler

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNodeListSummaryOmitsRouteStateWithoutChangingDetail(t *testing.T) {
	n := &domain.Node{ID: 1, RouteState: domain.NodeRouteState{Revision: 3, Status: "applied", Lines: []domain.VlessRouteLine{{ID: "line", Name: "kept"}}}}
	for _, detail := range []bool{false, true} {
		dto := (&AdminNodeHandler{}).toNodeDTO(n, nil, detail)
		raw, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["route_state"]; exists != detail {
			t.Fatalf("detail=%v: %s", detail, raw)
		}
	}
	if n.RouteState.Revision != 3 || len(n.RouteState.Lines) != 1 {
		t.Fatal("presentation changed stored state")
	}
}

func TestNodeDTOExposesDesiredAndObservedEndpointDrift(t *testing.T) {
	n := &domain.Node{
		ID: 1, PanelID: 2,
		DesiredProtocol: "vless", DesiredPort: 443,
		ObservedProtocol: "trojan", ObservedPort: 8443,
	}
	dto := (&AdminNodeHandler{}).toNodeDTO(n, map[int64]string{2: "edge"}, true)
	if dto.Protocol != "vless" || dto.DesiredProtocol != "vless" || dto.DesiredPort != 443 {
		t.Fatalf("desired endpoint missing from DTO: %+v", dto)
	}
	if dto.ObservedProtocol != "trojan" || dto.ObservedPort != 8443 || dto.EndpointInSync {
		t.Fatalf("observed drift missing from DTO: %+v", dto)
	}
}
