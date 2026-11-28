package handler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodecompat"
)

// A native server carries every public address its node reported, several of
// each family, next to the one address the panel saw it connect from. A server
// that never reported (a 3X-UI panel, an old node) carries none, so the field
// is absent rather than an empty list the page would have to second-guess.
func TestServerDTOCarriesTheReportedInterfaceAddresses(t *testing.T) {
	h := &AdminServersHandler{pool: fakeWebCertPool{client: &nativeCoreClientStub{}}, coreCatalog: corefixtures.Static{}}
	policy := nodecompat.Policy(nodecompat.DefaultObservationAge())
	reported := []string{"203.0.113.5", "198.51.100.9", "2001:db8::1"}
	agent := &domain.NodeAgent{ObservedAddress: "198.51.100.9", InterfaceAddresses: reported}

	dto := h.toServerDTOWithAgent(&domain.Panel{Kind: domain.PanelKindPSP}, agent, policy)
	if !reflect.DeepEqual(dto.InterfaceAddresses, reported) || dto.Address != "198.51.100.9" {
		t.Fatalf("addresses = %v, observed = %q", dto.InterfaceAddresses, dto.Address)
	}
	// A copy: the DTO must not alias the agent's slice.
	dto.InterfaceAddresses[0] = "192.0.2.1"
	if agent.InterfaceAddresses[0] != "203.0.113.5" {
		t.Fatal("the DTO shares the agent's address slice")
	}

	body, err := json.Marshal(h.toServerDTOWithAgent(&domain.Panel{Kind: domain.PanelKindPSP}, &domain.NodeAgent{ObservedAddress: "198.51.100.9"}, policy))
	if err != nil || strings.Contains(string(body), "interface_addresses") {
		t.Fatalf("a node that reported none must omit the field: %s (%v)", body, err)
	}
	other, _ := json.Marshal(h.toServerDTOWithAgent(&domain.Panel{Kind: domain.PanelKind3XUI}, agent, policy))
	if strings.Contains(string(other), "interface_addresses") {
		t.Fatalf("a 3X-UI server must not carry node addresses: %s", other)
	}
}
