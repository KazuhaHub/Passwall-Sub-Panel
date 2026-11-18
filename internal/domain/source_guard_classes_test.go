package domain

import (
	"fmt"
	"reflect"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func milli(v int) *int { return &v }

func guardInputs() SourceInputs {
	return SourceInputs{
		EntryAddresses: map[int64][]string{9: {"93.184.216.34"}},
		Cloudflare:     []string{"173.245.48.0/20"},
		Servers:        map[int64]SourceGuardServer{1: {Capable: true}},
	}
}

func landingNode(lines ...RelayLine) *Node {
	return &Node{ID: 5, PanelID: 1, DisplayName: "L", Relays: lines}
}

func TestListenerGuardable(t *testing.T) {
	stream := func(s string) *Node { return &Node{StreamSettings: s} }
	singbox := []NodeCoreEngine{NodeCoreSingBox}
	xray := []NodeCoreEngine{NodeCoreXray}
	for _, c := range []struct {
		name    string
		n       *Node
		engines []NodeCoreEngine
		want    bool
	}{
		{"plain tcp on xray", stream(`{"network":"tcp","security":"reality"}`), xray, true},
		{"no settings", stream(""), nil, true},
		{"sing-box tcp tls", stream(`{"network":"tcp","security":"tls"}`), singbox, true},
		{"sing-box raw", stream(`{"network":"raw"}`), singbox, true},
		{"sing-box ws", stream(`{"network":"ws"}`), singbox, false},
		{"sing-box httpupgrade", stream(`{"network":"httpupgrade"}`), singbox, false},
		{"sing-box grpc", stream(`{"network":"grpc"}`), singbox, false},
		{"sing-box http", stream(`{"network":"http"}`), singbox, false},
		{"sing-box h2", stream(`{"network":"h2"}`), singbox, false},
		{"xray ws without trust", stream(`{"network":"ws"}`), xray, true},
		{"xray trustedXForwardedFor", stream(`{"network":"ws","sockopt":{"trustedXForwardedFor":["CF-Connecting-IP"]}}`), xray, false},
		{"xray empty trustedXForwardedFor", stream(`{"sockopt":{"trustedXForwardedFor":[]}}`), xray, true},
		{"xray proxy protocol", stream(`{"network":"tcp","tcpSettings":{"acceptProxyProtocol":true}}`), xray, false},
		{"xray proxy protocol in sockopt", stream(`{"sockopt":{"acceptProxyProtocol":true}}`), xray, false},
		{"xray proxy protocol in inbound settings", &Node{InboundSettings: `{"acceptProxyProtocol":true}`}, xray, false},
		{"xray proxy protocol off", stream(`{"tcpSettings":{"acceptProxyProtocol":false}}`), xray, true},
		{"xray key case: trustedXForwardedFor", stream(`{"Sockopt":{"TrustedXForwardedFor":["X-Forwarded-For"]}}`), xray, false},
		{"xray key case: all upper", stream(`{"SOCKOPT":{"TRUSTEDXFORWARDEDFOR":"X-Forwarded-For"}}`), xray, false},
		{"xray key case: acceptProxyProtocol in transport", stream(`{"TCPSettings":{"AcceptProxyProtocol":true}}`), xray, false},
		{"xray key case: acceptProxyProtocol in sockopt", stream(`{"sockopt":{"ACCEPTPROXYPROTOCOL":true}}`), xray, false},
		{"xray key case: inbound settings", &Node{InboundSettings: `{"AcceptProxyProtocol":true}`}, xray, false},
		{"xray key case: wrapped StreamSettings", stream(`{"StreamSettings":{"Sockopt":{"trustedxforwardedfor":["X"]}}}`), xray, false},
		{"xray key case: wrapped in inbound", &Node{InboundSettings: `{"StreamSettings":{"sockopt":{"TrustedXForwardedFor":["X"]}}}`}, xray, false},
		{"xray trustedXForwardedFor outside sockopt", stream(`{"network":"ws","wsSettings":{"trustedXForwardedFor":["X"]}}`), xray, false},
		{"xray trustedXForwardedFor at the stream root", stream(`{"trustedXForwardedFor":["X"]}`), xray, false},
		{"xray trustedXForwardedFor in inbound settings", &Node{InboundSettings: `{"trustedXForwardedFor":["X"]}`}, xray, false},
		{"xray key case: empty trust still fine", stream(`{"Sockopt":{"TrustedXForwardedFor":[]}}`), xray, true},
		{"sing-box key case: Network", stream(`{"Network":"WS"}`), singbox, false},
		{"sing-box key case: wrapped Network", stream(`{"StreamSettings":{"NETWORK":"grpc"}}`), singbox, false},
		{"sing-box key case: tcp", stream(`{"Network":"TCP"}`), singbox, true},
		{"both engines, one refuses", stream(`{"network":"ws"}`), append(append([]NodeCoreEngine{}, xray...), singbox...), false},
	} {
		if got := ListenerGuardable(c.n, c.engines); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestInputsCanGuardNeedsServerAndListener(t *testing.T) {
	in := guardInputs()
	n := landingNode()
	if !in.CanGuard(n) {
		t.Fatal("a capable server with a plain listener guards")
	}
	n.StreamSettings = `{"network":"ws","sockopt":{"trustedXForwardedFor":["X-Forwarded-For"]}}`
	if in.CanGuard(n) {
		t.Fatal("a listener that trusts a header does not")
	}
	n.StreamSettings = ""
	in.Servers = map[int64]SourceGuardServer{}
	if in.CanGuard(n) {
		t.Fatal("a server without the capability does not")
	}
}

func classOf(a SourceGuardAnalysis, factor int) SourceClass {
	if c, ok := a.PricedClassOf(factor); ok {
		return c
	}
	return SourceClass{}
}

func TestAnalyzeGroupsLinesByFactor(t *testing.T) {
	in := guardInputs()
	n := landingNode(
		RelayLine{ID: "same", Enabled: true},                                                  // direct class, no sources
		RelayLine{ID: "fwd", Enabled: true, EntryPanelID: 9, TrafficFactorMilli: milli(2000)}, // priced
		RelayLine{ID: "ext", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "cdn", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(3000)},
		RelayLine{ID: "off", Enabled: false, SourceCIDRs: []string{"192.0.2.0/24"}, TrafficFactorMilli: milli(4000)},
		RelayLine{ID: "tun", Enabled: true, Mode: RelayModeTunnel, EntryPanelID: 9, TrafficFactorMilli: milli(5000)},
	)
	a := n.AnalyzeSourceGuard(in)
	if !a.Guards || len(a.Problems) != 0 {
		t.Fatalf("guards=%v problems=%v", a.Guards, a.Problems)
	}
	if len(a.Priced) != 2 || a.Direct.Factor != 1000 || !reflect.DeepEqual(a.Direct.LineIDs, []string{"same"}) {
		t.Fatalf("classes = %+v direct %+v", a.Priced, a.Direct)
	}
	two := classOf(a, 2000)
	if two.Key != "src_5_f2000" || !reflect.DeepEqual(two.LineIDs, []string{"fwd", "ext"}) ||
		!reflect.DeepEqual(two.CIDRs, []string{"93.184.216.34/32", "192.0.2.0/24"}) && !reflect.DeepEqual(two.CIDRs, []string{"192.0.2.0/24", "93.184.216.34/32"}) {
		t.Fatalf("class 2000 = %+v", two)
	}
	if !two.Emitted() || !classOf(a, 3000).Emitted() {
		t.Fatal("known classes are sent")
	}
	if _, ok := a.PricedClassOf(4000); ok {
		t.Fatal("a disabled line makes no class")
	}
	if len(a.Withheld) != 0 {
		t.Fatalf("withheld = %v", a.Withheld)
	}
}

func TestAnalyzeUnknownLineIsWithheldButItsClassKeepsItsKey(t *testing.T) {
	in := guardInputs()
	n := landingNode(RelayLine{ID: "ext", Enabled: true, TrafficFactorMilli: milli(2000)})
	a := n.AnalyzeSourceGuard(in)
	if a.Withheld["ext"] != SourceWithheldUnknown {
		t.Fatalf("withheld = %v", a.Withheld)
	}
	c := classOf(a, 2000)
	if c.Key == "" || c.Emitted() {
		t.Fatalf("class = %+v: it has a key to reference and nothing to send", c)
	}
}

func TestAnalyzeRefusesCDNClassesOnTheSameDefault(t *testing.T) {
	in := guardInputs()
	n := landingNode(
		RelayLine{ID: "a", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "b", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(3000)},
	)
	a := n.AnalyzeSourceGuard(in)
	if len(a.Problems) != 1 || a.Problems[0].Err.Code != CodeSourceGuardOverlap {
		t.Fatalf("problems = %+v", a.Problems)
	}
	if a.Withheld["a"] != SourceWithheldOverlap || a.Withheld["b"] != SourceWithheldOverlap {
		t.Fatalf("withheld = %v", a.Withheld)
	}
	if classOf(a, 2000).Emitted() || classOf(a, 3000).Emitted() {
		t.Fatal("overlapping classes are not sent")
	}
	same := landingNode(
		RelayLine{ID: "a", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "b", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(2000)},
	)
	if got := same.AnalyzeSourceGuard(in); len(got.Problems) != 0 || len(got.Withheld) != 0 {
		t.Fatalf("one class: %+v", got)
	}
}

func TestAnalyzeOverlapAgainstTheDirectClassAndNestedPrefixes(t *testing.T) {
	in := guardInputs()
	n := landingNode(
		RelayLine{ID: "d", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}}, // same factor as direct
		RelayLine{ID: "p", Enabled: true, SourceCIDRs: []string{"192.0.2.128/25"}, TrafficFactorMilli: milli(2000)},
	)
	a := n.AnalyzeSourceGuard(in)
	if len(a.Problems) != 1 || a.Problems[0].Err.Code != CodeSourceGuardOverlap || a.Withheld["p"] != SourceWithheldOverlap {
		t.Fatalf("a priced class inside the direct class's addresses: %+v %v", a.Problems, a.Withheld)
	}
	disjoint := landingNode(
		RelayLine{ID: "x", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24", "2001:db8::/32"}, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "y", Enabled: true, SourceCIDRs: []string{"198.51.100.0/24", "2001:db9::/32"}, TrafficFactorMilli: milli(3000)},
	)
	if got := disjoint.AnalyzeSourceGuard(in); len(got.Problems) != 0 {
		t.Fatalf("disjoint classes: %+v", got.Problems)
	}
	// An address of one family never overlaps the other family's.
	mixed := landingNode(
		RelayLine{ID: "x", Enabled: true, SourceCIDRs: []string{"10.0.0.0/8"}, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "y", Enabled: true, SourceCIDRs: []string{"2001:db8::/32"}, TrafficFactorMilli: milli(3000)},
	)
	if got := mixed.AnalyzeSourceGuard(in); len(got.Problems) != 0 {
		t.Fatalf("mixed families: %+v", got.Problems)
	}
}

func TestAnalyzeRefusesTooManyClassesAndTooManyAddresses(t *testing.T) {
	in := guardInputs()
	var lines []RelayLine
	for i := 0; i < MaxPricedClasses+1; i++ {
		lines = append(lines, RelayLine{
			ID: fmt.Sprintf("l%d", i), Enabled: true, SourceCIDRs: []string{fmt.Sprintf("10.%d.0.0/16", i)},
			TrafficFactorMilli: milli(2000 + i),
		})
	}
	a := landingNode(lines...).AnalyzeSourceGuard(in)
	if len(a.Problems) != 1 || a.Problems[0].Err.Code != CodeSourceGuardTooManyClasses {
		t.Fatalf("problems = %+v", a.Problems)
	}
	for _, c := range a.Priced {
		if c.Emitted() {
			t.Fatal("no class is sent when they do not all fit a deny list")
		}
	}
	if len(a.Withheld) != MaxPricedClasses+1 {
		t.Fatalf("withheld %d lines", len(a.Withheld))
	}
	// 64 classes fit.
	if a := landingNode(lines[:MaxPricedClasses]...).AnalyzeSourceGuard(in); len(a.Problems) != 0 {
		t.Fatalf("64 classes: %+v", a.Problems)
	}

	// Two lines of one class together exceed a set.
	var one, two []string
	for i := 0; i < MaxSourceCIDRs; i++ {
		one = append(one, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}
	two = []string{"172.16.0.0/16"}
	big := landingNode(
		RelayLine{ID: "a", Enabled: true, SourceCIDRs: one, TrafficFactorMilli: milli(2000)},
		RelayLine{ID: "b", Enabled: true, SourceCIDRs: two, TrafficFactorMilli: milli(2000)},
	).AnalyzeSourceGuard(in)
	if len(big.Problems) != 1 || big.Problems[0].Err.Code != CodeSourceGuardTooManyCIDRs || big.Withheld["a"] != SourceWithheldLimit {
		t.Fatalf("problems = %+v withheld = %v", big.Problems, big.Withheld)
	}
}

func TestAnalyzeWithoutGuardKeepsTheEqualFactorRule(t *testing.T) {
	in := guardInputs()
	in.Servers = map[int64]SourceGuardServer{}
	n := landingNode(
		RelayLine{ID: "ok", Enabled: true},
		RelayLine{ID: "dear", Enabled: true, TrafficFactorMilli: milli(2000)},
	)
	a := n.AnalyzeSourceGuard(in)
	if a.Guards || a.Withheld["dear"] != SourceWithheldFactor || a.Withheld["ok"] != "" {
		t.Fatalf("%+v", a)
	}
	if len(a.Problems) != 1 || a.Problems[0].Err.Code != CodeSourceGuardFactorMismatch {
		t.Fatalf("problems = %+v", a.Problems)
	}
}

// A listener the core reads the source from a header on cannot guard, so the
// equal-factor rule applies to it even on a capable server.
func TestAnalyzeNonGuardableListenerKeepsTheEqualFactorRule(t *testing.T) {
	in := guardInputs()
	in.Servers[1] = SourceGuardServer{Capable: true, Engines: []NodeCoreEngine{NodeCoreSingBox}}
	n := landingNode(RelayLine{ID: "cdn", Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: milli(2000)})
	n.StreamSettings = `{"network":"ws","security":"tls"}`
	a := n.AnalyzeSourceGuard(in)
	if a.Guards || a.Withheld["cdn"] != SourceWithheldFactor || len(a.Problems) != 1 || a.Problems[0].Err.Code != CodeSourceGuardFactorMismatch {
		t.Fatalf("sing-box ws: %+v", a)
	}
	in.Servers[1] = SourceGuardServer{Capable: true, Engines: []NodeCoreEngine{NodeCoreXray}}
	n.StreamSettings = `{"network":"ws","security":"tls","sockopt":{"trustedXForwardedFor":["X-Forwarded-For"]}}`
	if a := n.AnalyzeSourceGuard(in); a.Guards || len(a.Problems) != 1 {
		t.Fatalf("xray trustedXForwardedFor: %+v", a)
	}
	n.StreamSettings = `{"network":"ws","security":"tls"}`
	if a := n.AnalyzeSourceGuard(in); !a.Guards || len(a.Problems) != 0 {
		t.Fatalf("xray ws without trust guards: %+v", a)
	}
}

func TestSourceGuardKeysAreValidForProtocol(t *testing.T) {
	if err := SourceSetKeyOf(9223372036854775807, 1<<30).Validate(); err != nil {
		t.Fatal(err)
	}
	var _ nodeprotocol.SourceSetKey = SourceSetKeyOf(1, 1000)
}
