package ports

import "testing"

type capabilityFixture []PanelCapability

func (c capabilityFixture) Capabilities() []PanelCapability { return c }

func TestSupportsCapability(t *testing.T) {
	if !SupportsCapability(struct{}{}, CapabilityInboundEnable) {
		t.Fatal("legacy implementation without capability provider was rejected")
	}
	fixture := capabilityFixture{CapabilityInboundRead, CapabilityInboundCreate}
	if !SupportsCapability(fixture, CapabilityInboundCreate) {
		t.Fatal("advertised capability was rejected")
	}
	if SupportsCapability(fixture, CapabilityInboundEnable) {
		t.Fatal("missing capability was accepted")
	}
}
