package clientplan

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func tunnelLanding() (*domain.Node, *domain.Node) {
	milli := 1500
	child := &domain.Node{ID: 20, PanelID: 9, DesiredProtocol: "vless", Flow: "xtls-rprx-vision", ParentNodeID: 1, ParentLineID: "line-1", EgressPanelID: 7}
	landing := &domain.Node{ID: 1, PanelID: 7, DesiredProtocol: "vless", Relays: []domain.RelayLine{
		{ID: "fwd", Enabled: true, EntryPanelID: 9, Port: 20001},
		{ID: "line-1", Enabled: true, EntryPanelID: 9, Mode: domain.RelayModeTunnel, TunnelNodeID: 20, TunnelNode: child, TrafficFactorMilli: &milli},
		{ID: "off", Enabled: false, EntryPanelID: 9, Mode: domain.RelayModeTunnel, TunnelNodeID: 21, TunnelNode: child},
	}}
	return landing, child
}

func TestATunnelLineHasItsClientOnTheEntryServersListener(t *testing.T) {
	landing, child := tunnelLanding()

	// The landing's own server holds the direct line and the forward only.
	own := LineCredsFromNodes([]*domain.Node{landing})
	if len(own) != 2 {
		t.Fatalf("landing's server lines = %+v, want the direct line and the forward", own)
	}
	for _, line := range own {
		if line.Line.LineID == "line-1" {
			t.Fatal("a tunnel line was planned on its landing's listener")
		}
	}

	byServer := TunnelLineCreds([]*domain.Node{landing})
	lines := byServer[9]
	if len(byServer) != 1 || len(lines) != 1 {
		t.Fatalf("tunnel lines by server = %+v, want exactly the enabled line on server 9", byServer)
	}
	got := lines[0]
	if got.Line != (domain.LineKey{NodeID: 1, LineID: "line-1"}) || got.ListenerNodeID != child.ID ||
		got.Protocol != domain.ProtoVLESS || got.Flow != "xtls-rprx-vision" || got.FactorMilli == nil || *got.FactorMilli != 1500 {
		t.Fatalf("tunnel line cred = %+v", got)
	}

	// The row is one per (user, line) with the derived identity, attached to the
	// listener on the entry server and keyed to the landing's line.
	built := Build(5, "11111111-2222-4333-8444-555555555555", 9, domain.EmailRules{Domain: "x.test"}, lines, nil, nil)
	if len(built) != 1 {
		t.Fatalf("built %d rows", len(built))
	}
	row := built[0]
	if row.Client.PanelID != 9 || row.Client.NodeID != 1 || row.Client.LineID != "line-1" {
		t.Fatalf("row = %+v", row.Client)
	}
	if want := domain.DeriveLineUUID("11111111-2222-4333-8444-555555555555", domain.LineKey{NodeID: 1, LineID: "line-1"}, 1); row.Client.UUID != want {
		t.Fatalf("uuid = %s, want the line's derived identity %s", row.Client.UUID, want)
	}
	if row.Client.UUID == "11111111-2222-4333-8444-555555555555" {
		t.Fatal("a tunnel line reused the user's own identity")
	}
	if len(row.Inbounds) != 1 || row.Inbounds[0].NodeID != child.ID {
		t.Fatalf("attachments = %+v, want the listener %d", row.Inbounds, child.ID)
	}
	if row.Client.LastFactorMilli == nil || *row.Client.LastFactorMilli != 1500 {
		t.Fatalf("a new row must record its line's factor, got %v", row.Client.LastFactorMilli)
	}
}

func TestATunnelLineWithoutItsListenerHasNoClient(t *testing.T) {
	landing, _ := tunnelLanding()
	landing.Relays[1].TunnelNode = nil
	if got := TunnelLineCreds([]*domain.Node{landing}); len(got) != 0 {
		t.Fatalf("a line whose listener is gone was planned: %+v", got)
	}
}
