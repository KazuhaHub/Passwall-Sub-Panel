package nodesync

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

var frontCaps = []string{
	nodeprotocol.CapabilityTransitForward, nodeprotocol.CapabilityTransitLink,
	nodeprotocol.CapabilityTransitProbe, nodeprotocol.CapabilityTransitSNI,
}

// withFronts binds transit, probes and the front planner, and makes the
// fixture's listener a REALITY one on its port 444.
func (f *configAppliedFixture) withFronts(t *testing.T) {
	t.Helper()
	f.withProbes()
	f.service.SetFronts(front.New(f.repos.TransitFronts, f.repos.Node, f.repos.XUIPanel, f.repos.NodeAgent, f.repos.Transit, f.repos.TransitLinks, nil))
	f.makeReality(t)
}

// frontReport is a report from an agent that runs Xray, which a front needs it
// to say.
func (f *configAppliedFixture) frontReport(caps []string, seq int64) nodeprotocol.NodeReport {
	report := f.agentReport(caps, seq)
	report.CoreEngine = "xray"
	return report
}

func (f *configAppliedFixture) makeReality(t *testing.T) {
	t.Helper()
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("stream_settings",
		`{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]}}`).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *configAppliedFixture) addFront(t *testing.T, landing *domain.Node) {
	t.Helper()
	routes := []domain.FrontRoute{{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: f.node.ID}}}
	if landing != nil {
		// A relay line is routed by its landing's SNI, so the landing must have one.
		if err := f.db.Table("nodes").Where("id = ?", landing.ID).Update("stream_settings",
			`{"network":"tcp","security":"reality","realitySettings":{"serverNames":["land.example.com"]}}`).Error; err != nil {
			t.Fatal(err)
		}
		routes = append(routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
			Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: landing.ID, LineID: "a"}})
	}
	if _, err := f.repos.TransitFronts.Create(t.Context(), domain.TransitFront{
		PanelID: f.agent.PanelID, Port: 444,
		Listeners: []domain.FrontListener{{NodeID: f.node.ID, LoopbackPort: 30001}},
		Routes:    routes,
		Default:   domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}); err != nil {
		t.Fatal(err)
	}
}

// With no fronts stored, a build that can plan them sends the bytes a build
// that cannot does, whatever the agent declares.
func TestNoFrontsLeaveTheConfigByteIdentical(t *testing.T) {
	plain := newConfigAppliedFixture(t)
	plain.withProbes()
	plain.makeReality(t)
	plain.managedLanding(t, 20443)
	capable := newConfigAppliedFixture(t)
	capable.withFronts(t)
	capable.managedLanding(t, 20443)

	a := plain.syncReport(t, plain.frontReport(frontCaps, 1)).Config.Body
	b := capable.syncReport(t, capable.frontReport(frontCaps, 1)).Config.Body
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	if !bytes.Equal(rawA, rawB) {
		t.Fatalf("config differs:\n%s\n%s", rawA, rawB)
	}
	if bytes.Contains(rawA, []byte(`"fronts"`)) || bytes.Contains(rawB, []byte(`"fronts"`)) {
		t.Fatal("a fronts field appeared with no front stored")
	}
}

func TestFrontsAreSentOnlyToAnXrayAgentThatDeclaredTheCapability(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	landing := f.managedLanding(t, 444)
	f.addFront(t, landing)

	// Without the capability: no fronts, and the forward on the shared port is
	// withheld because the listener holds it.
	without := f.syncReport(t, f.agentReport([]string{nodeprotocol.CapabilityTransitForward, nodeprotocol.CapabilityTransitProbe}, 1))
	if tr := without.Config.Body.Transit; tr != nil && (len(tr.Fronts) > 0 || len(tr.Forwards) > 0) {
		t.Fatalf("transit without the capability = %+v", tr)
	}

	response := f.syncReport(t, f.frontReport(frontCaps, 2))
	tr := response.Config.Body.Transit
	if tr == nil || len(tr.Fronts) != 1 || len(tr.Forwards) != 1 {
		t.Fatalf("transit = %+v", tr)
	}
	front := tr.Fronts[0]
	if front.Port != 444 || len(front.Listeners) != 1 || front.Listeners[0].LoopbackPort != 30001 || front.Default.External == nil {
		t.Fatalf("front = %+v", front)
	}
	if tr.Forwards[0].Network != nodeprotocol.ForwardNetworkTCP || tr.Forwards[0].Port != 444 {
		t.Fatalf("forward = %+v", tr.Forwards[0])
	}
	// A forward behind a front is probed with a TLS handshake.
	if tr.Probe == nil || len(tr.Probe.Forwards) != 1 || tr.Probe.Forwards[0].Method != nodeprotocol.ProbeMethodTLS {
		t.Fatalf("probe = %+v, want tls for the fronted forward", tr.Probe)
	}
	if err := nodeprotocol.ValidateTransit(tr); err != nil {
		t.Fatalf("the contract refuses what was sent: %v", err)
	}
	if linkprobe.Method(false, true) != nodeprotocol.ProbeMethodTLS {
		t.Fatal("method table moved")
	}

	// The listener keeps its public port in its own config.
	if len(response.Config.Body.Listeners) == 0 {
		t.Fatal("no listeners")
	}
	var intent domain.NodeConfigIntent
	if err := json.Unmarshal(response.Config.Body.Listeners[0].Config, &intent); err != nil || intent.Port != 444 {
		t.Fatalf("listener intent = %+v err = %v", intent, err)
	}
}

func TestFrontsAreNotSentToASingBoxAgent(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	f.addFront(t, nil)
	if err := f.repos.NodeAgent.UpdateCoreSelection(t.Context(), f.agent.AgentID, domain.NodeCoreSingBox, "1.14.0", false); err != nil {
		t.Fatal(err)
	}
	response, err := f.service.Sync(t.Context(), f.frontReport(frontCaps, 3))
	if err != nil {
		t.Fatal(err)
	}
	if response.Config.Body == nil || response.Config.Body.Core.Engine != "sing-box" {
		t.Fatalf("the agent was not on sing-box: %+v", response.Config.Body)
	}
	if tr := response.Config.Body.Transit; tr != nil && len(tr.Fronts) > 0 {
		t.Fatalf("fronts sent to sing-box: %+v", tr.Fronts)
	}
}

func TestFrontsNeedTheAgentToReportXrayRunning(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	f.addFront(t, nil)
	for name, engine := range map[string]string{"nothing reported": "", "sing-box running": "sing-box"} {
		report := f.agentReport(frontCaps, 1)
		report.CoreEngine = engine
		response, err := f.service.Sync(t.Context(), report)
		if err != nil {
			t.Fatal(err)
		}
		if tr := response.Config.Body.Transit; tr != nil && len(tr.Fronts) > 0 {
			t.Errorf("%s: fronts sent: %+v", name, tr.Fronts)
		}
	}
	response := f.syncReport(t, f.frontReport(frontCaps, 2))
	if tr := response.Config.Body.Transit; tr == nil || len(tr.Fronts) != 1 {
		t.Fatalf("Xray running: %+v", tr)
	}
}

// A listener on the shared port that is not behind the front would bind it too
// and Node would refuse the whole config: the front is not sent, an issue says
// so for the server, and it is counted.
func TestAWithheldFrontIsRecordedAsAnIssueAndCounted(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	f.addFront(t, nil)
	stray := &domain.Node{PanelID: f.agent.PanelID, InboundID: 2, DisplayName: "stray", ServerAddress: "x.example", DesiredProtocol: "vless",
		DesiredPort: 444, Enabled: true, Region: "JP"}
	if err := f.repos.Node.Create(t.Context(), stray); err != nil {
		t.Fatal(err)
	}
	before := metrics.FrontsWithheldTotal.With(front.WithheldListenerOnPort).Value()
	response := f.syncReport(t, f.frontReport(frontCaps, 1))
	if tr := response.Config.Body.Transit; tr != nil && len(tr.Fronts) > 0 {
		t.Fatalf("a front Node would refuse was sent: %+v", tr.Fronts)
	}
	if metrics.FrontsWithheldTotal.With(front.WithheldListenerOnPort).Value() != before+1 {
		t.Fatal("the withheld front was not counted")
	}
	issues, _, err := f.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{AgentID: f.agent.AgentID, Code: IssueFrontWithheld, Pagination: ports.Pagination{Page: 1, PageSize: 10}})
	if err != nil {
		t.Fatal(err)
	}
	var forFront *domain.NodeAgentIssue
	for _, issue := range issues {
		if strings.HasPrefix(issue.Key, "fnt_") {
			forFront = issue
		}
	}
	if forFront == nil || !strings.Contains(forFront.Detail, "stray") {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestADisabledListenerIsNotSentAsFronted(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	landing := f.managedLanding(t, 444)
	f.addFront(t, landing)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	tr := f.syncReport(t, f.frontReport(frontCaps, 1)).Config.Body.Transit
	if tr == nil || len(tr.Fronts) != 1 || len(tr.Fronts[0].Listeners) != 0 || len(tr.Fronts[0].Routes) != 1 || tr.Fronts[0].Routes[0].Target.Forward == "" {
		t.Fatalf("transit = %+v: the disabled listener and its route must go, the forward route stays", tr)
	}
	if err := nodeprotocol.ValidateTransit(tr); err != nil {
		t.Fatalf("the contract refuses it: %v", err)
	}
}

// Each front is held to the contract alone: another object's fault is not
// charged to any, one front's fault withholds only that front, and a clash of
// the remaining fronts with the rest is charged to them.
func TestFrontContractFaultsIsolateEachFront(t *testing.T) {
	routeTo := func(id int64, name string, forward int64) nodeprotocol.Front {
		return nodeprotocol.Front{
			Key: nodeprotocol.NewFrontKey(id), Port: 440 + int(id),
			Routes:  []nodeprotocol.FrontRoute{{ServerNames: []string{name}, Target: nodeprotocol.FrontTarget{Forward: nodeprotocol.NewForwardKey(forward)}}},
			Default: nodeprotocol.FrontTarget{External: &nodeprotocol.FrontExternal{Host: "www.example.com", Port: 443}},
		}
	}
	forwardOn := func(id int64, port int) nodeprotocol.Forward {
		return nodeprotocol.Forward{Key: nodeprotocol.NewForwardKey(id), Port: port, Target: "203.0.113.9", TargetPort: 443, Network: nodeprotocol.ForwardNetworkTCP}
	}
	one, two := routeTo(1, "a.example.com", 1), routeTo(2, "b.example.com", 2)
	plan := front.Plan{
		Fronts:          []nodeprotocol.Front{one, two},
		Forwards:        []nodeprotocol.Forward{forwardOn(1, 441), forwardOn(2, 442)},
		FrontedForwards: map[nodeprotocol.ForwardKey]bool{nodeprotocol.NewForwardKey(1): true, nodeprotocol.NewForwardKey(2): true},
	}
	body := nodeprotocol.Transit{Fronts: plan.Fronts, Forwards: plan.Forwards}
	if faulty := frontContractFaults(body, plan); len(faulty) != 0 {
		t.Fatalf("a good body: %v", faulty)
	}
	// Some other forward is invalid: nothing to do with the fronts.
	unrelated := body
	unrelated.Forwards = append(slices.Clone(body.Forwards), nodeprotocol.Forward{Key: nodeprotocol.NewForwardKey(9), Port: 0, Target: "203.0.113.9", TargetPort: 1, Network: nodeprotocol.ForwardNetworkTCP})
	if faulty := frontContractFaults(unrelated, plan); len(faulty) != 0 {
		t.Fatalf("an unrelated fault was charged to the fronts: %v", faulty)
	}
	// Another forward bound where front 1 is and not its target: front 1 cannot be
	// sent next to it, but front 2 can.
	clash := body
	clash.Forwards = append(slices.Clone(body.Forwards), nodeprotocol.Forward{Key: nodeprotocol.NewForwardKey(3), Port: 441, Target: "203.0.113.8", TargetPort: 1, Network: nodeprotocol.ForwardNetworkTCP})
	faulty := frontContractFaults(clash, plan)
	if len(faulty) != 1 || faulty[one.Key] == "" {
		t.Fatalf("clash: %v (only the front it clashes with may be withheld)", faulty)
	}
	// One front is invalid on its own (no routes); the other is left alone.
	broken := plan
	broken.Fronts = []nodeprotocol.Front{{Key: one.Key, Port: 441, Default: one.Default}, two}
	faulty = frontContractFaults(nodeprotocol.Transit{Fronts: broken.Fronts, Forwards: plan.Forwards}, broken)
	if len(faulty) != 1 || faulty[one.Key] == "" {
		t.Fatalf("invalid front: %v", faulty)
	}
}

// A shared port nobody serves must not leave two listeners on one port: Node
// refuses a config where two bind the same port, so both are left out and an
// issue names them. One listener alone falls back to its public port.
func TestListenersOfAFrontThatIsNotSentAreNotSentTwiceOnOnePort(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withFronts(t)
	second := &domain.Node{PanelID: f.agent.PanelID, InboundID: 2, DisplayName: "second", ServerAddress: "x.example", DesiredProtocol: "vless",
		DesiredPort: 444, Enabled: true, Region: "JP",
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["b.example.com"]}}`}
	if err := f.repos.Node.Create(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repos.TransitFronts.Create(t.Context(), domain.TransitFront{
		PanelID: f.agent.PanelID, Port: 444,
		Listeners: []domain.FrontListener{{NodeID: f.node.ID, LoopbackPort: 30001}, {NodeID: second.ID, LoopbackPort: 30002}},
		Routes: []domain.FrontRoute{
			{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: f.node.ID}},
			{ServerNames: []string{"b.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: second.ID}},
		},
		Default: domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}); err != nil {
		t.Fatal(err)
	}
	listenersOn := func(response nodeprotocol.SyncResponse, port int) []int64 {
		var out []int64
		for _, l := range response.Config.Body.Listeners {
			var intent domain.NodeConfigIntent
			if err := json.Unmarshal(l.Config, &intent); err != nil {
				t.Fatal(err)
			}
			if intent.Enabled && intent.Port == port {
				id, _ := l.Key.RowID()
				out = append(out, id)
			}
		}
		return out
	}

	// Served: both are behind the front and sent, their public port in each config
	// (Node binds them to loopback ports).
	served := f.syncReport(t, f.frontReport(frontCaps, 1))
	if tr := served.Config.Body.Transit; tr == nil || len(tr.Fronts) != 1 || len(tr.Fronts[0].Listeners) != 2 {
		t.Fatalf("served: %+v", tr)
	}

	// Not served (the agent does not declare the capability): the fronts are not
	// sent, and the two listeners must not both be, or they would bind port 444.
	plain := f.syncReport(t, f.agentReport([]string{nodeprotocol.CapabilityTransitForward}, 2))
	if tr := plain.Config.Body.Transit; tr != nil && len(tr.Fronts) > 0 {
		t.Fatalf("fronts sent: %+v", tr.Fronts)
	}
	if got := listenersOn(plain, 444); len(got) > 1 {
		t.Fatalf("listeners %v would bind the same port", got)
	}
	issues, _, err := f.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{AgentID: f.agent.AgentID, Code: IssueFrontWithheld, Pagination: ports.Pagination{Page: 1, PageSize: 20}})
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, issue := range issues {
		if strings.Contains(issue.Detail, "listener_unserved") && strings.Contains(issue.Detail, "second") {
			named++
		}
	}
	if named == 0 {
		t.Fatalf("no issue names the withheld listeners: %+v", issues)
	}
	// And the roster does not refer to a listener that is not in the config.
	for _, client := range plain.Roster.Body.Clients {
		for _, key := range client.Listeners {
			found := false
			for _, l := range plain.Config.Body.Listeners {
				found = found || l.Key == key
			}
			if !found {
				t.Fatalf("roster client %s refers to the missing listener %s", client.Key, key)
			}
		}
	}

	// A single listener behind the front falls back to its public port.
	if _, err := f.repos.TransitFronts.Update(t.Context(), domain.TransitFront{
		ID: 1, PanelID: f.agent.PanelID, Port: 444,
		Listeners: []domain.FrontListener{{NodeID: f.node.ID, LoopbackPort: 30001}},
		Routes:    []domain.FrontRoute{{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: f.node.ID}}},
		Default:   domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Table("nodes").Where("id = ?", second.ID).Update("desired_port", 8443).Error; err != nil {
		t.Fatal(err)
	}
	single := f.syncReport(t, f.agentReport([]string{nodeprotocol.CapabilityTransitForward}, 3))
	if got := listenersOn(single, 444); len(got) != 1 {
		t.Fatalf("a single listener must fall back to its public port: %v", got)
	}
}

func TestSharedTunnelExitSyncsThroughTheFront(t *testing.T) {
	f := newConfigAppliedFixture(t)
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	fronts := front.New(f.repos.TransitFronts, f.repos.Node, f.repos.XUIPanel, f.repos.NodeAgent, f.repos.Transit, f.repos.TransitLinks, nil)
	links := transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent)
	links.WithFrontLinkPorts(fronts.SavedLinkPorts)
	f.service.SetTransit(links)
	f.service.SetFronts(fronts)
	ctx := t.Context()
	if _, err := links.UpdateLink(ctx, f.agent.PanelID, exit.PanelID, domain.TransitLinkSettings{Protocol: nodeprotocol.LinkProtocolVLESSReality}); err != nil {
		t.Fatal(err)
	}
	link, err := f.repos.TransitLinks.GetLink(ctx, f.agent.PanelID, exit.PanelID)
	if err != nil {
		t.Fatal(err)
	}
	const sharedPort = 8443
	if _, err := f.repos.TransitFronts.Create(ctx, domain.TransitFront{
		PanelID: exit.PanelID, Port: sharedPort,
		Routes:  []domain.FrontRoute{{ServerNames: []string{link.RealityServerName}, Target: domain.FrontTarget{Kind: domain.FrontTargetLink, LinkID: link.ID}}},
		Default: domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}); err != nil {
		t.Fatal(err)
	}
	fromExit := sourceaddr.With(ctx, "198.51.100.7")
	report := nodeprotocol.NodeReport{
		AgentID: exit.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		CoreEngine: "xray", Capabilities: frontCaps, ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(),
	}
	in := f.syncAs(t, fromExit, report).Config.Body.Transit
	if in == nil || len(in.Fronts) != 1 || len(in.LinksIn) != 1 ||
		in.LinksIn[0].Port != sharedPort || in.Fronts[0].Routes[0].Target.Link != in.LinksIn[0].Key {
		t.Fatalf("exit transit = %+v", in)
	}
	if err := nodeprotocol.ValidateTransit(in); err != nil {
		t.Fatal(err)
	}
	out := f.syncAs(t, ctx, f.frontReport(frontCaps, 2)).Config.Body.Transit
	if out == nil || len(out.LinksOut) != 1 || out.LinksOut[0].Port != sharedPort ||
		out.LinksOut[0].Reality.ServerName != link.RealityServerName {
		t.Fatalf("entry transit = %+v", out)
	}
	report.CoreEngine = "sing-box"
	report.ReportedAtMS++
	withheld := f.syncAs(t, fromExit, report).Config.Body.Transit
	if withheld != nil && (len(withheld.LinksIn) > 0 || len(withheld.Fronts) > 0) {
		t.Fatalf("unsupported exit got a bind: %+v", withheld)
	}
}
