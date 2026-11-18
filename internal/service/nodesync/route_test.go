package nodesync

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

var routeCaps = []string{
	nodeprotocol.CapabilityTransitForward, nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitRoute,
}

// withRoutes binds the transit and routing services, as the application does.
func (f *configAppliedFixture) withRoutes(t *testing.T) *route.Service {
	t.Helper()
	f.withProbes()
	svc := route.New(route.Deps{
		Routes: f.repos.TransitRoutes, Nodes: f.repos.Node, Panels: f.repos.XUIPanel, Agents: f.repos.NodeAgent,
		Links: transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent),
		Now:   func() time.Time { return f.now },
	})
	f.service.SetRoutes(svc)
	return svc
}

// serverRow gives a server of the fixture its panel row, which a route's exit
// needs: a route to a server that does not exist is withheld.
func (f *configAppliedFixture) serverRow(t *testing.T, id int64, name string) {
	t.Helper()
	if err := f.repos.XUIPanel.Save(t.Context(), &domain.Panel{ID: id, Kind: domain.PanelKindPSP, Name: name, URL: "psp://" + name}); err != nil {
		t.Fatal(err)
	}
}

func (f *configAppliedFixture) routeTo(t *testing.T, exit int64, entries ...string) {
	t.Helper()
	if err := f.repos.TransitRoutes.Replace(t.Context(), f.node.ID, []domain.DestRoute{{
		ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: exit}, Domains: entries,
	}}); err != nil {
		t.Fatal(err)
	}
}

// reportOf is a full report that holds response's config and says how its
// listener fared.
func (f *configAppliedFixture) reportOf(response nodeprotocol.SyncResponse, caps []string, state nodeprotocol.ObjectState) nodeprotocol.NodeReport {
	report := f.receipt(response)
	report.Capabilities = caps
	report.Objects[0].State = state
	if state == nodeprotocol.ObjectRejected {
		report.Objects[0].FirstFailedAtMS, report.Objects[0].IssueCode = f.now.UnixMilli(), "core_config_rejected"
	}
	return report
}

func (f *configAppliedFixture) issueCodes(t *testing.T) map[string]string {
	t.Helper()
	items, _, err := f.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, item := range items {
		out[item.Code+"/"+item.Key] = item.Detail
	}
	return out
}

// With no routes stored, a build that can plan them sends the bytes a build
// that cannot does, whatever the agent declares, a tunnel included.
func TestNoRoutesLeaveTheConfigByteIdentical(t *testing.T) {
	f := newConfigAppliedFixture(t)
	routing := f.withRoutes(t)
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	f.syncAs(t, sourceaddr.With(t.Context(), "198.51.100.7"), nodeprotocol.NodeReport{AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: routeCaps})

	// Every capability set the build could meet: the bytes are the same with the
	// routing service bound and without it.
	for seq, caps := range [][]string{routeCaps, linkCapabilities, {nodeprotocol.CapabilityTransitForward}} {
		f.service.SetRoutes(nil)
		a := f.syncReport(t, f.agentReport(caps, int64(2*seq+1))).Config.Body
		f.service.SetRoutes(routing)
		b := f.syncReport(t, f.agentReport(caps, int64(2*seq+2))).Config.Body
		rawA, _ := json.Marshal(a)
		rawB, _ := json.Marshal(b)
		if !bytes.Equal(rawA, rawB) {
			t.Fatalf("caps %v: config differs:\n%s\n%s", caps, rawA, rawB)
		}
		if bytes.Contains(rawB, []byte(`"routes"`)) {
			t.Fatal("a routes field appeared with no route stored")
		}
	}
}

func TestRoutesAreSentOnlyToAnAgentThatDeclaredThem(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withRoutes(t)
	exit := f.exitServer(t)
	fromExit := sourceaddr.With(t.Context(), "198.51.100.7")
	f.syncAs(t, fromExit, nodeprotocol.NodeReport{AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: routeCaps})
	f.serverRow(t, exit.PanelID, "exit")
	f.routeTo(t, exit.PanelID, "domain:streaming.example")

	// Links but no routes capability: the listener is NOT sent (an agent that
	// does not know routes would run it and send the routed destinations through
	// the default exit), the node is not published, and an issue says why.
	withoutCap := f.syncReport(t, f.agentReport(linkCapabilities, 1))
	if n := len(withoutCap.Config.Body.Listeners); n != 0 {
		t.Fatalf("a routed listener was sent to an agent that cannot route it: %d listeners", n)
	}
	if tr := withoutCap.Config.Body.Transit; tr != nil && (len(tr.Routes) > 0 || len(tr.LinksOut) > 0) {
		t.Fatalf("transit without the capability = %+v", tr)
	}
	if detail, ok := f.issueCodes(t)[route.IssueRoutesNotSent+"/"+string(nodeprotocol.NewListenerKey(f.node.ID))]; !ok || !strings.Contains(detail, route.WithheldNoCapability) {
		t.Fatalf("issues = %v", f.issueCodes(t))
	}
	stored, _ := f.repos.Node.GetByID(t.Context(), f.node.ID)
	if stored.RoutesSent != "" || stored.RoutesWithheld != route.WithheldNoCapability || stored.EgressPublishable() {
		t.Fatalf("sent %q withheld %q publishable %v", stored.RoutesSent, stored.RoutesWithheld, stored.EgressPublishable())
	}

	// With it: the route and the link it leaves through are sent, and the node
	// still leaves locally by default, so it is published without waiting for
	// the routed tunnel.
	response := f.syncReport(t, f.agentReport(routeCaps, 2))
	tr := response.Config.Body.Transit
	if tr == nil || len(tr.Routes) != 1 || len(tr.LinksOut) != 1 || len(tr.Egress) != 0 {
		t.Fatalf("transit = %+v", tr)
	}
	lr := tr.Routes[0]
	if lr.Listener != nodeprotocol.NewListenerKey(f.node.ID) || len(lr.Rules) != 1 || lr.Rules[0].Exit != tr.LinksOut[0].Key ||
		len(lr.Rules[0].Domains) != 1 || lr.Rules[0].Domains[0] != "domain:streaming.example" {
		t.Fatalf("routes = %+v", tr.Routes)
	}
	if err := nodeprotocol.ValidateTransit(tr); err != nil {
		t.Fatalf("the contract refuses what was sent: %v", err)
	}
	stored, _ = f.repos.Node.GetByID(t.Context(), f.node.ID)
	if stored.RoutesSent == "" || stored.RoutesWithheld != "" || stored.RoutesRunning() || !stored.EgressPublishable() {
		t.Fatalf("sent %q running %v publishable %v", stored.RoutesSent, stored.RoutesRunning(), stored.EgressPublishable())
	}
	// The exit now holds the incoming half too.
	exitResponse := f.syncAs(t, fromExit, nodeprotocol.NodeReport{AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli() + 3, Have: emptyProtocolHave(), Capabilities: routeCaps})
	if x := exitResponse.Config.Body.Transit; x == nil || len(x.LinksIn) != 1 || x.LinksIn[0].Key != tr.LinksOut[0].Key {
		t.Fatalf("exit transit = %+v", x)
	}

	// The entry applies its listener: the routes are running.
	f.syncReport(t, f.reportOf(response, routeCaps, nodeprotocol.ObjectApplied))
	stored, _ = f.repos.Node.GetByID(t.Context(), f.node.ID)
	if !stored.RoutesRunning() {
		t.Fatalf("applied routes are not running: %+v", stored)
	}
	// The entry rejects the listener (a routed budget, say): they are not.
	f.syncReport(t, f.reportOf(response, routeCaps, nodeprotocol.ObjectRejected))
	stored, _ = f.repos.Node.GetByID(t.Context(), f.node.ID)
	if stored.RoutesRunning() {
		t.Fatal("a rejected listener's routes count as running")
	}
}

// A route that cannot be sent withholds its listener: sent without it, its
// routed destinations would leave through the default exit.
func TestAListenerWhoseRouteCannotBeSentIsWithheld(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withRoutes(t)
	exit := f.exitServer(t) // has never synced: PSP has no address to dial
	f.serverRow(t, exit.PanelID, "exit")
	f.routeTo(t, exit.PanelID, "domain:streaming.example")
	response := f.syncReport(t, f.agentReport(routeCaps, 1))
	if n := len(response.Config.Body.Listeners); n != 0 {
		t.Fatalf("the listener was sent: %d listeners", n)
	}
	if tr := response.Config.Body.Transit; tr != nil && len(tr.Routes) > 0 {
		t.Fatalf("routes = %+v", tr.Routes)
	}
	if _, ok := f.issueCodes(t)[route.IssueRouteListenerHeld+"/"+string(nodeprotocol.NewListenerKey(f.node.ID))]; !ok {
		t.Fatalf("issues = %v", f.issueCodes(t))
	}
	// A listener that is not being sent is not running its routes, whatever it
	// confirmed before: nothing of an earlier confirmation survives.
	if err := f.repos.TransitRoutes.Replace(t.Context(), f.node.ID, []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: []string{"domain:a.com"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repos.TransitRoutes.RecordSent(t.Context(), f.node.ID, "old", "", f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repos.TransitRoutes.SetApplied(t.Context(), f.node.ID, "old", f.now); err != nil {
		t.Fatal(err)
	}
	f.serverRow(t, exit.PanelID, "exit")
	f.routeTo(t, exit.PanelID, "domain:streaming.example")
	f.syncReport(t, f.agentReport(routeCaps, 3))
	if stored, _ := f.repos.Node.GetByID(t.Context(), f.node.ID); stored.RoutesSent != "" || stored.RoutesRunning() {
		t.Fatalf("routes of a withheld listener: sent %q applied %q", stored.RoutesSent, stored.RoutesApplied)
	}
	// Once the exit has synced the listener is sent with its route.
	f.syncAs(t, sourceaddr.With(t.Context(), "198.51.100.7"), nodeprotocol.NodeReport{AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: routeCaps})
	response = f.syncReport(t, f.agentReport(routeCaps, 4))
	if len(response.Config.Body.Listeners) != 1 || response.Config.Body.Transit == nil || len(response.Config.Body.Transit.Routes) != 1 {
		t.Fatalf("config = %+v", response.Config.Body)
	}
}

// A routed listener behind an SNI front is still routed: the front moves it to a
// loopback port and the routes follow its key, not its bind.
func TestARoutedListenerBehindAFrontKeepsItsRoutes(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	f.service.SetRoutes(route.New(route.Deps{
		Routes: f.repos.TransitRoutes, Nodes: f.repos.Node, Panels: f.repos.XUIPanel, Agents: f.repos.NodeAgent,
		Links: transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent),
		Now:   func() time.Time { return f.now },
	}))
	exit := f.exitServer(t)
	f.syncAs(t, sourceaddr.With(t.Context(), "198.51.100.7"), nodeprotocol.NodeReport{AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: routeCaps})
	f.addFront(t, nil)
	f.serverRow(t, exit.PanelID, "exit")
	f.routeTo(t, exit.PanelID, "domain:streaming.example")

	caps := append(append([]string{}, frontCaps...), nodeprotocol.CapabilityTransitRoute)
	response := f.syncReport(t, f.frontReport(caps, 1))
	tr := response.Config.Body.Transit
	if tr == nil || len(tr.Fronts) != 1 || tr.Fronts[0].Listeners[0].LoopbackPort != 30001 {
		t.Fatalf("fronts = %+v", tr)
	}
	if len(tr.Routes) != 1 || tr.Routes[0].Listener != tr.Fronts[0].Listeners[0].Listener {
		t.Fatalf("routes = %+v, front listeners = %+v", tr.Routes, tr.Fronts[0].Listeners)
	}
	if err := nodeprotocol.ValidateTransit(tr); err != nil {
		t.Fatalf("the contract refuses what was sent: %v", err)
	}
}

// A listener whose routes cannot be sent is decided before the shared ports are
// planned, so it drops out of its front like a disabled member instead of being
// named by a front the node would refuse whole: the others stay behind it.
func TestARouteWithheldListenerDropsOutOfItsFront(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	f.service.SetRoutes(route.New(route.Deps{
		Routes: f.repos.TransitRoutes, Nodes: f.repos.Node, Panels: f.repos.XUIPanel, Agents: f.repos.NodeAgent,
		Links: transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent),
		Now:   func() time.Time { return f.now },
	}))
	exit := f.exitServer(t) // never synced: no address, so its tunnel cannot be sent
	f.serverRow(t, exit.PanelID, "exit")
	var members []domain.FrontListener
	var routes []domain.FrontRoute
	members = append(members, domain.FrontListener{NodeID: f.node.ID, LoopbackPort: 30001})
	routes = append(routes, domain.FrontRoute{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: f.node.ID}})
	for i, name := range []string{"b.example.com", "c.example.com"} {
		n := &domain.Node{PanelID: f.agent.PanelID, InboundID: 2 + i, DisplayName: name, ServerAddress: "x.example", DesiredProtocol: "vless",
			DesiredPort: 444, Enabled: true, Region: "JP",
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["` + name + `"]}}`}
		if err := f.repos.Node.Create(t.Context(), n); err != nil {
			t.Fatal(err)
		}
		members = append(members, domain.FrontListener{NodeID: n.ID, LoopbackPort: 30002 + i})
		routes = append(routes, domain.FrontRoute{ServerNames: []string{name}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: n.ID}})
	}
	if _, err := f.repos.TransitFronts.Create(t.Context(), domain.TransitFront{
		PanelID: f.agent.PanelID, Port: 444, Listeners: members, Routes: routes,
		Default: domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}); err != nil {
		t.Fatal(err)
	}
	f.routeTo(t, exit.PanelID, "domain:streaming.example")

	caps := append(append([]string{}, frontCaps...), nodeprotocol.CapabilityTransitRoute)
	response := f.syncReport(t, f.frontReport(caps, 1))
	tr := response.Config.Body.Transit
	if tr == nil || len(tr.Fronts) != 1 {
		t.Fatalf("transit = %+v", tr)
	}
	var behind []string
	for _, l := range tr.Fronts[0].Listeners {
		behind = append(behind, string(l.Listener))
	}
	if len(behind) != 2 || slices.Contains(behind, string(nodeprotocol.NewListenerKey(f.node.ID))) {
		t.Fatalf("listeners behind the front = %v", behind)
	}
	for _, r := range tr.Fronts[0].Routes {
		if r.Target.Listener == nodeprotocol.NewListenerKey(f.node.ID) {
			t.Fatalf("a route of the front still names the withheld listener: %+v", r)
		}
	}
	for _, l := range response.Config.Body.Listeners {
		if l.Key == nodeprotocol.NewListenerKey(f.node.ID) {
			t.Fatal("the withheld listener was sent")
		}
	}
	if err := nodeprotocol.ValidateTransit(tr); err != nil {
		t.Fatalf("the contract refuses what was sent: %v", err)
	}
	// The listeners still behind the front each name only listeners of this config.
	served := map[nodeprotocol.ListenerKey]bool{}
	for _, l := range response.Config.Body.Listeners {
		served[l.Key] = true
	}
	for _, l := range tr.Fronts[0].Listeners {
		if !served[l.Listener] {
			t.Fatalf("front names %s, which is not in the config", l.Listener)
		}
	}
}
