package handler

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNodeDTOExposesDesiredAndObservedEndpointDrift(t *testing.T) {
	n := &domain.Node{
		ID: 1, PanelID: 2,
		DesiredProtocol: "vless", DesiredPort: 443,
		ObservedProtocol: "trojan", ObservedPort: 8443,
	}
	dto := (&AdminNodeHandler{}).toNodeDTO(n, map[int64]string{2: "edge"})
	if dto.Protocol != "vless" || dto.DesiredProtocol != "vless" || dto.DesiredPort != 443 {
		t.Fatalf("desired endpoint missing from DTO: %+v", dto)
	}
	if dto.ObservedProtocol != "trojan" || dto.ObservedPort != 8443 || dto.EndpointInSync {
		t.Fatalf("observed drift missing from DTO: %+v", dto)
	}
}
