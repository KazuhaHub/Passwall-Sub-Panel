package domain

import (
	"errors"
	"strings"
	"testing"
)

func tunnelFixture() (*Node, RelayLine, *Node) {
	landing := &Node{ID: 1, PanelID: 7, DisplayName: "landing", Enabled: true}
	line := RelayLine{ID: "line-1", Name: "hk", Address: "hk.example", Enabled: true, EntryPanelID: 9, Mode: RelayModeTunnel, TunnelNodeID: 2}
	child := &Node{ID: 2, PanelID: 9, Enabled: true, ParentNodeID: 1, ParentLineID: "line-1", EgressPanelID: 7,
		ConfigSyncState: ConfigSyncSynced, EgressReady: true}
	return landing, line, child
}

func TestATunnelLineIsNotAForward(t *testing.T) {
	_, line, _ := tunnelFixture()
	if line.Managed() {
		t.Fatal("a tunnel line is not a managed forward: none of the forward machinery may pick it up")
	}
	if !line.Gated() || !line.Tunnel() {
		t.Fatal("a tunnel line is gated on its listener and link")
	}
	if _, ok := DesiredManagedForward(&Node{ID: 1, Enabled: true, ServerAddress: "l.example", DesiredPort: 443}, line); ok {
		t.Fatal("a tunnel line asked the entry for a forward")
	}
	forward := RelayLine{ID: "f", EntryPanelID: 9, Port: 20001, Enabled: true}
	if !forward.Managed() || forward.Tunnel() {
		t.Fatal("a line without a mode is a forward")
	}
}

func TestATunnelLineIsPublishedOnlyWhileItsListenerAndLinkRun(t *testing.T) {
	landing, line, child := tunnelFixture()
	for name, test := range map[string]struct {
		mutate func(*Node, *RelayLine, *Node)
		want   bool
	}{
		"running":                    {func(*Node, *RelayLine, *Node) {}, true},
		"line disabled":              {func(_ *Node, l *RelayLine, _ *Node) { l.Enabled = false }, false},
		"landing disabled":           {func(n *Node, _ *RelayLine, _ *Node) { n.Enabled = false }, false},
		"listener disabled":          {func(_ *Node, _ *RelayLine, c *Node) { c.Enabled = false }, false},
		"listener not applied":       {func(_ *Node, _ *RelayLine, c *Node) { c.ConfigSyncState = ConfigSyncPending }, false},
		"link not ready":             {func(_ *Node, _ *RelayLine, c *Node) { c.EgressReady = false }, false},
		"routes withheld":            {func(_ *Node, _ *RelayLine, c *Node) { c.RoutesWithheld = "no_capability" }, false},
		"listener gone":              {nil, false},
		"listener of another line":   {func(_ *Node, _ *RelayLine, c *Node) { c.ParentLineID = "other" }, false},
		"listener on another server": {func(_ *Node, _ *RelayLine, c *Node) { c.PanelID = 10 }, false},
		"exit is not the landing's":  {func(_ *Node, _ *RelayLine, c *Node) { c.EgressPanelID = 3 }, false},
	} {
		t.Run(name, func(t *testing.T) {
			n, l, c := *landing, line, *child
			var listener *Node = &c
			if test.mutate != nil {
				test.mutate(&n, &l, &c)
			} else {
				listener = nil
			}
			if got := TunnelRunning(&n, l, listener); got != test.want {
				t.Fatalf("TunnelRunning = %v, want %v", got, test.want)
			}
			l.ManagedReady = TunnelRunning(&n, l, listener)
			if l.Publishable() != test.want {
				t.Fatalf("Publishable = %v, want %v", l.Publishable(), test.want)
			}
		})
	}
	// A probe withhold takes a running line out as well, and nothing replaces it.
	line.ManagedReady, line.ProbeWithheld = true, true
	if line.Publishable() {
		t.Fatal("a line withheld by its probes was published")
	}
}

func TestOnlyARealityListenerIsCopiedToTheEntry(t *testing.T) {
	reality := `{"network":"tcp","security":"reality","realitySettings":{}}`
	for name, test := range map[string]struct {
		protocol, stream string
		want             bool
	}{
		"vless reality": {"vless", reality, true},
		"vmess reality": {"VMess", reality, true},
		"trojan":        {"trojan", reality, true},
		"vless tls":     {"vless", `{"security":"tls"}`, false},
		"vless none":    {"vless", `{"security":"none"}`, false},
		"ss-2022":       {"shadowsocks", reality, false},
		"hysteria2":     {"hysteria2", `{"security":"tls"}`, false},
		"broken stream": {"vless", `not json`, false},
	} {
		if got := TunnelProtocolCopyable(&Node{DesiredProtocol: test.protocol, StreamSettings: test.stream}); got != test.want {
			t.Errorf("%s: copyable = %v, want %v", name, got, test.want)
		}
	}
	if TunnelProtocolCopyable(nil) {
		t.Error("nil landing is copyable")
	}
}

func TestValidateTunnelRelays(t *testing.T) {
	landing := &Node{ID: 1, PanelID: 7}
	line := func(mutate func(*RelayLine)) []RelayLine {
		l := RelayLine{ID: "a", Address: "e.example", Enabled: true, EntryPanelID: 9, Mode: RelayModeTunnel}
		mutate(&l)
		return []RelayLine{l}
	}
	for name, test := range map[string]struct {
		lines []RelayLine
		want  string
	}{
		"fine":                  {line(func(*RelayLine) {}), ""},
		"forward is not judged": {[]RelayLine{{ID: "f", EntryPanelID: 9, Port: 1}}, ""},
		"no entry":              {line(func(l *RelayLine) { l.EntryPanelID = 0 }), "entry server"},
		"landing's own server":  {line(func(l *RelayLine) { l.EntryPanelID = 7 }), "own server"},
		"no address":            {line(func(l *RelayLine) { l.Address = " " }), "address"},
		"SNI override":          {line(func(l *RelayLine) { l.SNI = "x" }), "listener"},
		"Host override":         {line(func(l *RelayLine) { l.Host = "x" }), "listener"},
		"unknown mode":          {[]RelayLine{{ID: "x", Mode: "mux"}}, "unknown mode"},
	} {
		err := ValidateTunnelRelays(landing, test.lines)
		if test.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want a validation error mentioning %q", name, err, test.want)
		}
	}
}

func TestTheListenerStandsForItsLineInTheFactorsOfItsOwnServer(t *testing.T) {
	// The entry server's own node set does not hold the landing that owns the
	// line: the factor its clients are booked and granted at comes off the
	// listener's copy.
	milli := 2500
	child := &Node{ID: 2, PanelID: 9, ParentNodeID: 1, ParentLineID: "line-1", TrafficFactorMilli: &milli}
	client := &PSPClient{NodeID: 1, LineID: "line-1", PanelID: 9}
	if got := ClientTrafficFactor(LineFactors([]*Node{child}), client); got != 2500 {
		t.Fatalf("factor on the entry server = %d, want 2500", got)
	}
	// Where the landing is in the set, its own value wins over the copy.
	other := 500
	landing := &Node{ID: 1, PanelID: 7, Relays: []RelayLine{{ID: "line-1", TrafficFactorMilli: &other}}}
	if got := ClientTrafficFactor(LineFactors([]*Node{child, landing}), client); got != 500 {
		t.Fatalf("factor with the landing = %d, want 500", got)
	}
}

func TestChangingATunnelLineChangesTheLineSet(t *testing.T) {
	forward := []RelayLine{{ID: "a", Enabled: true, EntryPanelID: 9, Port: 1}}
	tunnel := []RelayLine{{ID: "a", Enabled: true, EntryPanelID: 9, Mode: RelayModeTunnel, TunnelNodeID: 5}}
	if !LineSetChanged(forward, tunnel) {
		t.Fatal("switching a line's mode must re-plan its users")
	}
	moved := []RelayLine{{ID: "a", Enabled: true, EntryPanelID: 9, Mode: RelayModeTunnel, TunnelNodeID: 6}}
	if !LineSetChanged(tunnel, moved) {
		t.Fatal("a line moving to another listener must re-plan its users")
	}
	if LineSetChanged(tunnel, append([]RelayLine(nil), tunnel...)) {
		t.Fatal("an unchanged line re-planned its users")
	}
}
