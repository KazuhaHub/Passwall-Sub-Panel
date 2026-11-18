package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fixedFronts struct {
	ports.TransitFrontRepo
	rows []domain.TransitFront
}

func (f fixedFronts) List(context.Context) ([]domain.TransitFront, error) { return f.rows, nil }
func (f fixedFronts) ListByPanel(_ context.Context, panelID int64) ([]domain.TransitFront, error) {
	var out []domain.TransitFront
	for _, row := range f.rows {
		if row.PanelID == panelID {
			out = append(out, row)
		}
	}
	return out, nil
}

// A front of server 9 on port 443 with node 2's listener behind it and node
// 1's relay line "a" routed.
func guardFront() domain.TransitFront {
	return domain.TransitFront{
		ID: 1, PanelID: 9, Port: 443,
		Listeners: []domain.FrontListener{{NodeID: 2, LoopbackPort: 30001}},
		Routes: []domain.FrontRoute{
			{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 2}},
			{ServerNames: []string{"b.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 1, LineID: "a"}},
		},
		Default: domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}
}

func fronted(n domain.Node) domain.Node {
	n.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]}}`
	n.DesiredProtocol = "vless"
	return n
}

func TestRelayLinesMayShareAFrontedPortButNotItsLoopbackPorts(t *testing.T) {
	pool := panelListPool{panels: []*domain.Panel{{ID: 7, Kind: domain.PanelKindPSP}, {ID: 9, Kind: domain.PanelKindPSP}}}
	stored := domain.Node{ID: 1, PanelID: 7, DisplayName: "landing", DesiredPort: 443, Enabled: true, Relays: []domain.RelayLine{
		{ID: "a", Address: "a.example", Port: 20443, Enabled: true, EntryPanelID: 9, Tags: []string{}},
		{ID: "b", Address: "a.example", Port: 20444, Enabled: true, EntryPanelID: 9, Tags: []string{}},
	}}
	listener := fronted(domain.Node{ID: 2, PanelID: 9, DisplayName: "entry inbound", DesiredPort: 443})
	fleet := []*domain.Node{&stored, &listener}
	line := func(id string, port int) domain.RelayLine {
		return domain.RelayLine{ID: id, Address: "a.example", Port: port, Enabled: true, EntryPanelID: 9, Tags: []string{}}
	}
	for name, test := range map[string]struct {
		fronts []domain.TransitFront
		lines  []domain.RelayLine
		want   string
	}{
		"443 without a front is the listener's": {lines: []domain.RelayLine{line("a", 443)}, want: "inbound of node"},
		"443 behind a front is shareable":       {fronts: []domain.TransitFront{guardFront()}, lines: []domain.RelayLine{line("a", 443)}},
		"two lines on the shared port":          {fronts: []domain.TransitFront{guardFront()}, lines: []domain.RelayLine{line("a", 443), line("b", 443)}},
		"a fronted listener's loopback port":    {fronts: []domain.TransitFront{guardFront()}, lines: []domain.RelayLine{line("a", 443), line("b", 30001)}, want: "loopback port"},
		"the routed line moved off the port":    {fronts: []domain.TransitFront{guardFront()}, lines: []domain.RelayLine{line("a", 20443)}, want: "is routed by shared port"},
		"the routed line removed":               {fronts: []domain.TransitFront{guardFront()}, lines: nil, want: "is routed by shared port"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fleetNodeRepo{labelNodeRepo: labelNodeRepo{stored: stored}, fleet: fleet}
			svc := &Service{nodes: repo, pool: pool, fronts: fixedFronts{rows: test.fronts}}
			next := stored
			next.Relays = test.lines
			err := svc.UpdateMetadata(context.Background(), &next)
			if test.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want a validation error mentioning %q", err, test.want)
			}
		})
	}
}

func TestInboundsAreHeldToTheFrontsOfTheirServer(t *testing.T) {
	svc := &Service{fronts: fixedFronts{rows: []domain.TransitFront{guardFront()}}}
	behind := fronted(domain.Node{ID: 2, PanelID: 9, DisplayName: "behind", DesiredPort: 443})
	for name, test := range map[string]struct {
		node domain.Node
		want string
	}{
		"the fronted listener unchanged": {node: behind},
		"moved off the shared port":      {node: func() domain.Node { n := behind; n.DesiredPort = 8443; return n }(), want: "remove it from that front"},
		"turned into a plain listener": {node: func() domain.Node {
			n := behind
			n.StreamSettings = `{"network":"tcp","security":"none"}`
			return n
		}(), want: "no longer be a listener a front can carry"},
		"moved to websocket": {node: func() domain.Node {
			n := behind
			n.StreamSettings = `{"network":"ws","security":"tls","tlsSettings":{"serverName":"a.example.com"}}`
			return n
		}(), want: "no longer be a listener a front can carry"},
		"renamed so a routed name is lost": {node: func() domain.Node {
			n := behind
			n.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["new.example.com"]}}`
			return n
		}(), want: "no longer serves"},
		"a new node on the shared port": {node: fronted(domain.Node{ID: 5, PanelID: 9, DisplayName: "new", DesiredPort: 443}), want: "shared port"},
		"a node on a loopback port":     {node: fronted(domain.Node{ID: 6, PanelID: 9, DisplayName: "lb", DesiredPort: 30001}), want: "loopback port"},
		"another server's 443":          {node: fronted(domain.Node{ID: 7, PanelID: 3, DisplayName: "elsewhere", DesiredPort: 443})},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.checkInboundAgainstFronts(context.Background(), &test.node)
			if test.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want a validation error mentioning %q", err, test.want)
			}
		})
	}
}

func TestANodeAFrontUsesCannotBeRemoved(t *testing.T) {
	svc := &Service{fronts: fixedFronts{rows: []domain.TransitFront{guardFront()}}}
	for name, node := range map[string]domain.Node{
		"fronted listener": {ID: 2, PanelID: 9, DisplayName: "behind"},
		"routed landing":   {ID: 1, PanelID: 7, DisplayName: "landing"},
	} {
		if _, err := svc.checkRemovalAgainstFronts(context.Background(), &node); err == nil || !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	free := domain.Node{ID: 9, PanelID: 9, DisplayName: "free"}
	if _, err := svc.checkRemovalAgainstFronts(context.Background(), &free); err != nil {
		t.Fatalf("an unrelated node: %v", err)
	}
	// Without a store bound nothing is checked.
	if _, err := (&Service{}).checkRemovalAgainstFronts(context.Background(), &domain.Node{ID: 2, PanelID: 9}); err != nil {
		t.Fatal(err)
	}
}
