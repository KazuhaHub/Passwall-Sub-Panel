package front

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// tunnelListener is the hidden listener of the tunnel line "t" of landing 5: an
// ordinary REALITY listener on this server, except for who it belongs to.
func tunnelListener(id int64, port int, names ...string) *domain.Node {
	n := realityNode(id, "landing · hk", port, names...)
	n.ParentNodeID, n.ParentLineID, n.EgressPanelID = 5, "t", 3
	return n
}

func tunnelLanding() *domain.Node {
	return &domain.Node{
		ID: 5, PanelID: 3, DisplayName: "landing", ServerAddress: "203.0.113.9", DesiredProtocol: "vless", DesiredPort: 443, Enabled: true,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["land.example.com"]}}`,
		Relays: []domain.RelayLine{{ID: "t", Name: "Hong Kong", Address: "entry.example", Enabled: true, EntryPanelID: panelID,
			Mode: domain.RelayModeTunnel, TunnelNodeID: 30}},
	}
}

// A tunnel listener is an ordinary TLS or REALITY listener on its server, so it
// can sit behind a shared port like any other; the port-usage view shows it as
// the tunnel line of its landing, with the line it belongs to.
func TestATunnelListenerSitsBehindASharedPortAndReadsAsALineOfItsLanding(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 8443, "a.example.com"), tunnelLanding(), tunnelListener(30, 443, "www.asus.com"))
	in := simpleInput()
	in.Listeners = []domain.FrontListener{{NodeID: 30, LoopbackPort: 30001}}
	in.Routes = []domain.FrontRoute{{ServerNames: []string{"www.asus.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 30}}}
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("a front over a tunnel listener: %v", err)
	}
	usage, err := e.svc.Usage(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, entry := range usage {
		if entry.Kind == KindFrontLoopback && entry.NodeID == 30 {
			seen = true
			if !strings.Contains(entry.Label, "tunnel line") || !strings.Contains(entry.Label, "landing") && !strings.Contains(entry.Label, "30") {
				t.Fatalf("loopback label = %q", entry.Label)
			}
		}
		if entry.Kind == KindFront {
			for _, behind := range entry.Behind {
				if behind.Target.Kind == domain.FrontTargetListener && behind.Target.NodeID == 30 && !strings.Contains(behind.Label, "tunnel line \"Hong Kong\" of node \"landing\"") {
					t.Fatalf("what is behind the port reads %q", behind.Label)
				}
			}
		}
	}
	if !seen {
		t.Fatalf("usage = %+v: the listener's loopback port is not listed", usage)
	}
}

func TestThePortUsageViewNamesATunnelListenerAsTheLineOfItsLanding(t *testing.T) {
	e := newEnv(tunnelLanding(), tunnelListener(30, 24443, "www.asus.com"))
	usage, err := e.svc.Usage(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range usage {
		if entry.Kind == KindListener && entry.NodeID == 30 {
			if entry.ParentNodeID != 5 || entry.LineID != "t" || entry.Label != `tunnel line "Hong Kong" of node "landing"` {
				t.Fatalf("entry = %+v", entry)
			}
			return
		}
	}
	t.Fatalf("usage = %+v: the listener's port is not listed", usage)
}

func TestTheEditorOffersATunnelListenerAndMarksItsLine(t *testing.T) {
	e := newEnv(tunnelLanding(), tunnelListener(30, 24443, "www.asus.com"))
	options, err := e.svc.Options(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range options.Listeners {
		if o.NodeID == 30 {
			if o.ParentNodeID != 5 || o.ParentLineID != "t" || !o.Frontable.OK {
				t.Fatalf("listener option = %+v", o)
			}
			return
		}
	}
	t.Fatalf("options = %+v", options)
}

// A tunnel line is served by a listener of its own, which is what a shared port
// routes to: routing to the line as if it were a forward is refused, saying so.
func TestAShareRoutesToTheTunnelListenerNotToTheLine(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), tunnelLanding(), tunnelListener(30, 24443, "www.asus.com"))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "t"}})
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	if code := refusalCode(t, err); code != CodeBadTarget || !strings.Contains(err.Error(), "tunnel line") {
		t.Fatalf("err = %v", err)
	}
	// The line is not offered as a forward target either.
	options, err := e.svc.Options(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range options.Lines {
		if line.LineID == "t" {
			t.Fatalf("a tunnel line is offered as a forward target: %+v", line)
		}
	}
}

func TestAPublicPortIsSuggestedOnlyWhenNothingOnTheServerHoldsIt(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 24443, "a.example.com"), tunnelListener(30, 24444, "www.asus.com"))
	for i := 0; i < 50; i++ {
		port, err := e.svc.SuggestPublicPort(t.Context(), panelID)
		if err != nil {
			t.Fatal(err)
		}
		if port < 20000 || port > 59999 || port == 24443 || port == 24444 {
			t.Fatalf("suggested %d, a port that is out of range or in use", port)
		}
	}
}

// A port promised to a tunnel being set up into the server is a used port: the
// usage view lists it and the picker never offers it.
func TestPortsPromisedToTunnelsAreUsedPorts(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 24443, "a.example.com"))
	e.svc.WithReservations(func(panel int64) []int {
		if panel == panelID {
			return []int{31000, 31001}
		}
		return nil
	})
	usage, err := e.svc.Usage(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[int]bool{}
	for _, entry := range usage {
		if entry.Kind == KindLinkPlanned {
			found[entry.Port] = true
		}
	}
	if !found[31000] || !found[31001] {
		t.Fatalf("usage = %+v: the promised ports are not listed", usage)
	}
	table, err := e.svc.collectUse(t.Context(), panelID, e.nodes, e.nodes, nil)
	if err != nil || !table.has(31000) {
		t.Fatalf("the picker's table does not hold the promised port (%v)", err)
	}
}
