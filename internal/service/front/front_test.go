package front

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type memFronts struct {
	rows []domain.TransitFront
	next int64
}

func (m *memFronts) ListByPanel(_ context.Context, panelID int64) ([]domain.TransitFront, error) {
	var out []domain.TransitFront
	for _, f := range m.rows {
		if f.PanelID == panelID {
			out = append(out, f)
		}
	}
	return out, nil
}
func (m *memFronts) List(context.Context) ([]domain.TransitFront, error) { return m.rows, nil }
func (m *memFronts) Get(_ context.Context, id int64) (domain.TransitFront, error) {
	for _, f := range m.rows {
		if f.ID == id {
			return f, nil
		}
	}
	return domain.TransitFront{}, domain.ErrNotFound
}
func (m *memFronts) Create(_ context.Context, f domain.TransitFront) (domain.TransitFront, error) {
	for _, other := range m.rows {
		if other.PanelID == f.PanelID && other.Port == f.Port {
			return domain.TransitFront{}, domain.ErrAlreadyExists
		}
	}
	m.next++
	f.ID = m.next
	m.rows = append(m.rows, f)
	return f, nil
}
func (m *memFronts) Update(_ context.Context, f domain.TransitFront) (domain.TransitFront, error) {
	for i := range m.rows {
		if m.rows[i].ID == f.ID {
			m.rows[i] = f
			return f, nil
		}
	}
	return domain.TransitFront{}, domain.ErrNotFound
}
func (m *memFronts) Delete(_ context.Context, panelID, id int64) (bool, error) {
	for i := range m.rows {
		if m.rows[i].ID == id && m.rows[i].PanelID == panelID {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

type fixedNodes []*domain.Node

func (f fixedNodes) List(context.Context) ([]*domain.Node, error) { return f, nil }

type fixedPanels map[int64]domain.PanelKind

func (f fixedPanels) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	kind, ok := f[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.XUIPanel{ID: id, Kind: kind}, nil
}

type fixedAgents map[int64]*domain.NodeAgent

func (f fixedAgents) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	if agent, ok := f[id]; ok {
		return agent, nil
	}
	return nil, domain.ErrNotFound
}

type countingForwards struct{ calls *int }

func (c countingForwards) Ensure(_ context.Context, nodeID int64, relayID string) (domain.TransitForward, error) {
	if c.calls != nil {
		*c.calls++
	}
	// A stable id per (node, line) for the test.
	return domain.TransitForward{ID: nodeID*100 + int64(relayID[0]-'a') + 1, NodeID: nodeID, RelayID: relayID}, nil
}

type fakeLinks []domain.TransitLink

func (f fakeLinks) ListLinks(context.Context) ([]domain.TransitLink, error) { return f, nil }

type fakeResolver map[string][]string

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if addrs, ok := f[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host}
}

const panelID = 7

var sniCaps = []string{nodeprotocol.CapabilityTransitSNI, nodeprotocol.CapabilityTransitForward}

func realityNode(id int64, name string, port int, names ...string) *domain.Node {
	quoted := ""
	for i, n := range names {
		if i > 0 {
			quoted += ","
		}
		quoted += `"` + n + `"`
	}
	return &domain.Node{
		ID: id, PanelID: panelID, DisplayName: name, DesiredProtocol: "vless", DesiredPort: port, Enabled: true,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":[` + quoted + `]}}`,
	}
}

type env struct {
	ensured int
	svc     *Service
	fronts  *memFronts
	nodes   fixedNodes
	agent   *domain.NodeAgent
}

func newEnv(nodes ...*domain.Node) *env {
	e := &env{fronts: &memFronts{}, nodes: nodes}
	e.agent = &domain.NodeAgent{
		PanelID: panelID, DesiredCoreEngine: domain.NodeCoreXray, ObservedCoreEngine: domain.NodeCoreXray, ObservedAddress: "198.51.100.10",
		ObservedCapabilities: sniCaps,
	}
	e.svc = New(e.fronts, e.nodes, fixedPanels{panelID: domain.PanelKindPSP, 8: domain.PanelKind3XUI},
		fixedAgents{panelID: e.agent}, countingForwards{calls: &e.ensured}, nil,
		fakeResolver{"camouflage.example": {"203.0.113.50"}, "self.example": {"198.51.100.10"}})
	return e
}

func external(host string, port int) domain.FrontTarget {
	return domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: host, Port: port}
}

func simpleInput() Input {
	return Input{
		Port:      443,
		Listeners: []domain.FrontListener{{NodeID: 1, LoopbackPort: 30001}},
		Routes: []domain.FrontRoute{{ServerNames: []string{"a.example.com"},
			Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 1}}},
		Default:              external("camouflage.example", 443),
		ConfirmMoveListeners: true,
	}
}

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("want a refusal, got %v", err)
	}
	return r.Code
}

func TestCreateStoresTheFrontLiterally(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	saved, err := e.svc.Create(t.Context(), panelID, simpleInput())
	if err != nil {
		t.Fatal(err)
	}
	f := saved.Front
	if f.Port != 443 || len(f.Listeners) != 1 || f.Listeners[0].LoopbackPort != 30001 || f.Default.Host != "camouflage.example" {
		t.Fatalf("stored %+v", f)
	}
}

func TestSharingAPortInUseNeedsAnExplicitConfirmation(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	in := simpleInput()
	in.ConfirmMoveListeners = false
	_, err := e.svc.Create(t.Context(), panelID, in)
	if code := refusalCode(t, err); code != CodeConfirmRequired {
		t.Fatalf("code = %s", code)
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("a confirmation request must read as a conflict")
	}
	if len(e.fronts.rows) != 0 {
		t.Fatal("nothing may be stored before the confirmation")
	}
	// An edit that keeps the listener where it already is needs none.
	in.ConfirmMoveListeners = true
	saved, err := e.svc.Create(t.Context(), panelID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ConfirmMoveListeners = false
	in.Default = external("camouflage.example", 8443)
	if _, err := e.svc.Update(t.Context(), panelID, saved.Front.ID, in); err != nil {
		t.Fatalf("unchanged listener asked for a confirmation again: %v", err)
	}
}

func TestSharingNeedsXrayAndTheCapability(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	e.agent.DesiredCoreEngine = domain.NodeCoreSingBox
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, simpleInput()))); code != CodeNotXray {
		t.Fatalf("sing-box: %s", code)
	}
	e.agent.DesiredCoreEngine = domain.NodeCoreXray
	e.agent.ObservedCapabilities = nil
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, simpleInput()))); code != CodeNoCapability {
		t.Fatalf("no capability: %s", code)
	}
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), 8, simpleInput()))); code != CodeNotNode {
		t.Fatalf("3x-ui: %s", code)
	}
}

func mustFail(_ Saved, err error) error { return err }

func TestOnlyASNIListenerCanSitBehindAFront(t *testing.T) {
	ss := &domain.Node{ID: 2, PanelID: panelID, DisplayName: "ss", DesiredProtocol: "shadowsocks", DesiredPort: 443, Enabled: true,
		StreamSettings: `{"network":"tcp","security":"none"}`}
	e := newEnv(ss)
	in := simpleInput()
	in.Listeners = []domain.FrontListener{{NodeID: 2, LoopbackPort: 30001}}
	in.Routes[0].Target.NodeID = 2
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeNotFrontable {
		t.Fatalf("code = %s", code)
	}
	hy := &domain.Node{ID: 3, PanelID: panelID, DisplayName: "hy", DesiredProtocol: "hysteria2", DesiredPort: 443, Enabled: true}
	e = newEnv(hy)
	in.Listeners = []domain.FrontListener{{NodeID: 3, LoopbackPort: 30001}}
	in.Routes[0].Target.NodeID = 3
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeNotFrontable {
		t.Fatalf("udp: %s", code)
	}
}

func TestAListenerOnThePortThatIsNotMovedIsAConflict(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), realityNode(2, "us", 8443, "b.example.com"))
	in := simpleInput()
	in.Listeners = nil
	in.Routes = []domain.FrontRoute{{ServerNames: []string{"x.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 1}}}
	// Node 1 holds 443 and is neither moved nor routed to.
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	if code := refusalCode(t, err); code != CodeListenerOnPort && code != CodeBadTarget {
		t.Fatalf("code = %s", code)
	}
}

func TestServerNameTwiceIsAConflictThatNamesTheOwner(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), realityNode(2, "us", 8443, "b.example.com"))
	// One listener, a duplicate name across two routes.
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"a.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 1}})
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	if code := refusalCode(t, err); code != CodeDuplicateName {
		t.Fatalf("code = %s", code)
	}
	var r *Refusal
	errors.As(err, &r)
	if r.Params["owner"] == "" || r.Params["name"] != "a.example.com" {
		t.Fatalf("the conflict must name the other object: %+v", r.Params)
	}
	// The same name twice inside one route, and case/trailing-dot spellings.
	in = simpleInput()
	in.Routes[0].ServerNames = []string{"A.example.com", "a.example.com."}
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeDuplicateName {
		t.Fatalf("spelling variants: %s", code)
	}
}

func TestPortConflictsNameTheOtherObject(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), realityNode(2, "us", 30001, "b.example.com"))
	in := simpleInput()
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	if code := refusalCode(t, err); code != CodePortConflict {
		t.Fatalf("loopback on another node's port: %s", code)
	}
	var r *Refusal
	errors.As(err, &r)
	if r.Params["owner"] == nil || r.Params["port"] != 30001 {
		t.Fatalf("params = %+v", r.Params)
	}
	// The node-internal probe range is never a loopback port.
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"))
	in.Listeners[0].LoopbackPort = 31500
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeLoopbackBad {
		t.Fatalf("probe range: %s", code)
	}
	// A tunnel on the shared port is a conflict.
	e = newEnv(realityNode(1, "hk", 8443, "a.example.com"))
	e.svc.links = fakeLinks{{ID: 1, EntryPanelID: 3, ExitPanelID: panelID, Port: 443}}
	in = simpleInput()
	in.Listeners[0] = domain.FrontListener{NodeID: 1, LoopbackPort: 30001}
	in.Port = 443
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodePortConflict && code != CodeListenerPortMismatch {
		t.Fatalf("link port: %s", code)
	}
	// A second front on the same port.
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"))
	if _, err := e.svc.Create(t.Context(), panelID, simpleInput()); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, simpleInput()))); code != CodePortHasFront {
		t.Fatalf("second front: %s", code)
	}
}

func TestAnyPortCanBeShared(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 8443, "a.example.com"))
	in := simpleInput()
	in.Port = 8443
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("a port other than 443: %v", err)
	}
	for _, port := range []int{0, 70000, 31000} {
		in.Port = port
		if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeBadPort {
			t.Fatalf("port %d: %s", port, code)
		}
	}
}

func TestDefaultMustBeAnExternalSiteThatIsNotThisServer(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	cases := []struct {
		name string
		def  domain.FrontTarget
		code string
	}{
		{"observed address", external("198.51.100.10", 443), CodeDefaultIsSelf},
		{"name resolving to it", external("self.example", 443), CodeDefaultIsSelf},
		{"loopback", external("127.0.0.1", 443), CodeDefaultIsSelf},
		{"localhost", external("localhost", 443), CodeDefaultIsSelf},
		{"missing", domain.FrontTarget{}, CodeDefaultMissing},
		{"a listener as default", domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 1}, CodeDefaultNotExternal},
		{"bad host", external("Bad Host!", 443), CodeDefaultBad},
		{"bad port", external("camouflage.example", 0), CodeDefaultBad},
	}
	for _, c := range cases {
		in := simpleInput()
		in.Default = c.def
		if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != c.code {
			t.Errorf("%s: code %s, want %s", c.name, code, c.code)
		}
	}
	// The same address on another port is a different service, and a name that
	// cannot be resolved is accepted with a warning.
	in := simpleInput()
	in.Default = external("198.51.100.10", 8080)
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("own address on another port: %v", err)
	}
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"))
	in.Default = external("unknown.example", 443)
	saved, err := e.svc.Create(t.Context(), panelID, in)
	if err != nil || len(saved.Warnings) != 1 || saved.Warnings[0] != WarnDefaultUnresolved {
		t.Fatalf("unresolved default: %v %+v", err, saved.Warnings)
	}
}

func TestOwnAddressIncludesNodeAddressesOnTheServer(t *testing.T) {
	n := realityNode(1, "hk", 443, "a.example.com")
	n.ServerAddress = "192.0.2.77"
	e := newEnv(n)
	in := simpleInput()
	in.Default = external("192.0.2.77", 443)
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeDefaultIsSelf {
		t.Fatalf("node address: %s", code)
	}
}

func TestLinkAndExternalRouteTargetsAreRefused(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"l.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetLink}})
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeLinkUnsupported {
		t.Fatalf("link: %s", code)
	}
	in = simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"x.example.com"}, Target: external("camouflage.example", 443)})
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeBadTarget {
		t.Fatalf("external route: %s", code)
	}
}

func forwardLanding(entryPort int) *domain.Node {
	return &domain.Node{
		ID: 5, PanelID: 3, DisplayName: "landing", ServerAddress: "203.0.113.9", DesiredProtocol: "vless", DesiredPort: 443, Enabled: true,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["land.example.com"]}}`,
		Relays:         []domain.RelayLine{{ID: "a", Name: "via hk", Address: "entry.example", Port: entryPort, Enabled: true, EntryPanelID: panelID}},
	}
}

func TestAForwardRouteNeedsALineOnTheSharedPort(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("forward route: %v", err)
	}
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(20443))
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeBadTarget {
		t.Fatalf("a line on another port: %s", code)
	}
}

func TestPlanForCompilesAContractValidFront(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatal(err)
	}
	plan, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fronts) != 1 || len(plan.Forwards) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	front := plan.Fronts[0]
	if front.Port != 443 || front.Listeners[0].LoopbackPort != 30001 || front.Default.External == nil || len(front.Routes) != 2 {
		t.Fatalf("front = %+v", front)
	}
	forward := plan.Forwards[0]
	if forward.Network != nodeprotocol.ForwardNetworkTCP || forward.Port != 443 || !plan.FrontedForwards[forward.Key] {
		t.Fatalf("forward = %+v", forward)
	}
	if !plan.Ports[443] || !plan.Ports[30001] {
		t.Fatalf("ports = %v", plan.Ports)
	}
	body := nodeprotocol.Transit{Fronts: plan.Fronts, Forwards: plan.Forwards}
	if err := nodeprotocol.ValidateTransit(&body); err != nil {
		t.Fatalf("the contract refuses what PlanFor built: %v", err)
	}
}

func TestPlanForSendsNothingWithoutTheCapabilityOrXray(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	if _, err := e.svc.Create(t.Context(), panelID, simpleInput()); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() (Plan, error){
		"no capability": func() (Plan, error) {
			return e.svc.PlanFor(t.Context(), panelID, []string{nodeprotocol.CapabilityTransitForward}, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1})
		},
		"xray desired but not running": func() (Plan, error) {
			return e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, "", []int64{1})
		},
		"sing-box": func() (Plan, error) {
			return e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreSingBox, domain.NodeCoreSingBox, []int64{1})
		},
	} {
		plan, err := run()
		if err != nil || len(plan.Fronts) != 0 || len(plan.Forwards) != 0 || len(plan.Ports) != 0 {
			t.Errorf("%s: plan = %+v err = %v", name, plan, err)
		}
	}
}

func TestPlanForWithholdsWhatCannotBeSent(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatal(err)
	}
	// The landing is disabled: its route is left out, the rest stays.
	e.nodes[1].Enabled = false
	plan, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1})
	if err != nil || len(plan.Fronts) != 1 || len(plan.Fronts[0].Routes) != 1 || len(plan.Forwards) != 0 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	// The listener is not in the server's config: with nothing left to route,
	// the front is not sent at all.
	plan, err = e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, nil)
	if err != nil || len(plan.Fronts) != 0 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
}

func TestUsageListsEveryPort(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), realityNode(2, "us", 8443, "b.example.com"), forwardLanding(443))
	e.nodes[1].InboundListen = "192.0.2.20"
	e.svc.WithProbePorts(func(_ context.Context, id int64, _ time.Time) ([]domain.ProbeState, error) {
		if id != panelID {
			t.Errorf("probe ports requested for server %d", id)
		}
		return []domain.ProbeState{{ObjectKey: "lnk_1", LoopbackPort: 31123}}, nil
	})
	e.svc.links = fakeLinks{{ID: 1, EntryPanelID: 3, ExitPanelID: panelID, Port: 22222}}
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatal(err)
	}
	usage, err := e.svc.Usage(t.Context(), panelID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	var front PortUsage
	for _, u := range usage {
		seen[fmt.Sprintf("%s:%d", u.Kind, u.Port)]++
		if u.Kind == KindListener && u.Port == 8443 && u.Listen != "192.0.2.20" {
			t.Errorf("listen address = %q", u.Listen)
		}
		if u.Kind == KindFront {
			front = u
		}
	}
	for _, want := range []string{"front:443", "front_loopback:30001", "listener:8443", "forward:443", "link:22222", "probe_range:31000", "probe_loopback:31123"} {
		if seen[want] != 1 {
			t.Errorf("usage lacks %s: %v", want, seen)
		}
	}
	if seen["listener:443"] != 0 {
		t.Error("a fronted listener must be listed once, behind its front, not on the public port as well")
	}
	if len(front.Behind) != 3 || !front.Behind[2].IsDefault {
		t.Fatalf("front behind = %+v", front.Behind)
	}
}

func TestSuggestionsAvoidWhatIsInUse(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 30000, "a.example.com"))
	port, err := e.svc.SuggestLoopbackPort(t.Context(), panelID)
	if err != nil || port < domain.FrontLoopbackSuggestLow || port > domain.FrontLoopbackSuggestHigh || port == 30000 {
		t.Fatalf("port = %d err = %v", port, err)
	}
	if domain.InNodeProbeRange(port) {
		t.Fatal("a suggestion must stay out of the probe range")
	}
	if site := e.svc.SuggestCamouflage(); site.Host == "" || site.Port != 443 {
		t.Fatalf("site = %+v", site)
	}
	// A full range is an error, not a collision.
	use := useTable{}
	for p := domain.FrontLoopbackSuggestLow; p <= domain.FrontLoopbackSuggestHigh; p++ {
		use.entries = append(use.entries, PortUsage{Port: p})
	}
	if _, err := suggestPort(use, func(n int) int { return 0 }); err == nil {
		t.Fatal("a full range must fail")
	}
}

func TestDeleteReturnsTheFront(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	saved, err := e.svc.Create(t.Context(), panelID, simpleInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Delete(t.Context(), 99, saved.Front.ID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another server's front: %v", err)
	}
	_, err = e.svc.Delete(t.Context(), panelID, saved.Front.ID, false)
	if code := refusalCode(t, err); code != CodeConfirmRequired || len(e.fronts.rows) != 1 {
		t.Fatalf("an unconfirmed delete of a front with a listener behind it: %v rows=%d", err, len(e.fronts.rows))
	}
	var r *Refusal
	errors.As(err, &r)
	if got := fmt.Sprint(r.Params["leaving_listeners"]); !strings.Contains(got, "hk") {
		t.Fatalf("the confirmation must name the listener: %v", r.Params)
	}
	if _, err := e.svc.Delete(t.Context(), panelID, saved.Front.ID, true); err != nil || len(e.fronts.rows) != 0 {
		t.Fatalf("delete: %v rows=%d", err, len(e.fronts.rows))
	}
}

func TestFrontableOfClassifiesListeners(t *testing.T) {
	tls := &domain.Node{DesiredProtocol: "trojan", StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"T.Example.com"}}`}
	if f := domain.FrontableOf(tls); !f.OK || f.Security != "tls" || len(f.ServerNames) != 1 || f.ServerNames[0] != "t.example.com" {
		t.Fatalf("tls = %+v", f)
	}
	for _, network := range []string{"", "tcp", "raw", "TCP"} {
		n := &domain.Node{DesiredProtocol: "vless", StreamSettings: `{"network":"` + network + `","security":"reality","realitySettings":{"serverNames":["a.example.com"]}}`}
		if f := domain.FrontableOf(n); !f.OK {
			t.Errorf("transport %q is one Node can carry: %+v", network, f)
		}
	}
	// A listener with no server name renders no SNI the router could match.
	anyName := &domain.Node{DesiredProtocol: "vless", StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":""}}`}
	if f := domain.FrontableOf(anyName); f.OK || f.Reason != domain.FrontRefusalNoName {
		t.Fatalf("tls without a name = %+v", f)
	}
	for name, node := range map[string]*domain.Node{
		"ss-2022":                {DesiredProtocol: "shadowsocks", StreamSettings: `{"network":"tcp"}`},
		"trojan over websocket":  {DesiredProtocol: "trojan", StreamSettings: `{"network":"ws","security":"tls","tlsSettings":{"serverName":"t.example.com"}}`},
		"grpc":                   {DesiredProtocol: "vless", StreamSettings: `{"network":"grpc","security":"reality"}`},
		"xhttp":                  {DesiredProtocol: "vless", StreamSettings: `{"network":"xhttp","security":"tls"}`},
		"accepts a PROXY header": {DesiredProtocol: "vless", StreamSettings: `{"network":"tcp","security":"reality","sockopt":{"acceptProxyProtocol":true}}`},
		"rawSettings PROXY":      {DesiredProtocol: "vless", StreamSettings: `{"network":"raw","security":"reality","rawSettings":{"acceptProxyProtocol":true}}`},
		"tcpSettings PROXY":      {DesiredProtocol: "vless", StreamSettings: `{"network":"tcp","security":"reality","tcpSettings":{"acceptProxyProtocol":true}}`},
		"plain tcp":              {DesiredProtocol: "vless", StreamSettings: `{"network":"tcp","security":"none"}`},
		"kcp":                    {DesiredProtocol: "vless", StreamSettings: `{"network":"kcp","security":"tls"}`},
		"tuic":                   {DesiredProtocol: "tuic", StreamSettings: `{}`},
		"no stream":              {DesiredProtocol: "vless"},
		"bad json":               {DesiredProtocol: "vless", StreamSettings: `{`},
	} {
		if f := domain.FrontableOf(node); f.OK || f.Reason == "" {
			t.Errorf("%s: %+v", name, f)
		}
	}
}
