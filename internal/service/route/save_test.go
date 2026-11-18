package route

import (
	"errors"
	"slices"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("not a refusal: %v", err)
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a refusal is a validation error: %v", err)
	}
	return refusal.Code
}

func TestSaveStoresNormalisedRoutesAndMakesTheTunnels(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:netflix.com"}, nil)
	saved, err := s.Save(t.Context(), 11, []domain.DestRoute{
		{Name: " streaming ", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 2}, ListIDs: []int64{4, 4},
			Domains: []string{" Domain:B.com ", "domain:a.com", "domain:a.com"}, CIDRs: []string{"192.0.2.77/24", "198.51.100.9"}, Ports: "443, 80", Network: "TCP"},
		{Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: []string{"keyword:mail"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := w.routes[11].Routes
	if len(stored) != 2 || stored[0].ID != "r1" || stored[1].ID != "r2" {
		t.Fatalf("ids = %+v", stored)
	}
	r := stored[0]
	if r.Name != "streaming" || !slices.Equal(r.ListIDs, []int64{4}) || !slices.Equal(r.Domains, []string{"domain:a.com", "domain:b.com"}) ||
		!slices.Equal(r.CIDRs, []string{"192.0.2.0/24", "198.51.100.9/32"}) || r.Ports != "80,443" || r.Network != "tcp" {
		t.Fatalf("normalised = %+v", r)
	}
	// The tunnel to the exit exists, the same way an egress link is made, so the
	// administrator sees it before the first sync.
	if _, ok := w.links[[2]int64{1, 2}]; !ok {
		t.Fatal("no tunnel was made to the exit server")
	}
	if len(saved.Routes) != 2 {
		t.Fatalf("saved = %+v", saved)
	}
	// An edit keeps the ids of the routes that were there, in the new order, and
	// gives a new one the next number.
	_, err = s.Save(t.Context(), 11, []domain.DestRoute{
		{ID: "r2", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: []string{"keyword:mail"}},
		{ID: "r1", Target: stored[0].Target, Domains: []string{"domain:a.com"}},
		{Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Ports: "25"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, r := range w.routes[11].Routes {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"r2", "r1", "r3"}) {
		t.Fatalf("ids after an edit = %v", ids)
	}
}

func TestCheckWritesNothing(t *testing.T) {
	w, s := routedWorld()
	saved, err := s.Check(t.Context(), 11, []domain.DestRoute{serverRoute("", 2, "domain:a.com")})
	if err != nil || len(saved.Routes) != 1 || saved.Routes[0].ID != "r1" {
		t.Fatalf("check = %+v %v", saved, err)
	}
	if len(w.routes) != 0 || len(w.links) != 0 {
		t.Fatalf("a dry run wrote routes %v or links %v", w.routes, w.links)
	}
}

func TestCheckSetupValidatesRoutesForAnUnsavedListener(t *testing.T) {
	w, s := routedWorld()
	n := &domain.Node{PanelID: 1, DisplayName: "draft", Enabled: true}
	input := []domain.DestRoute{serverRoute("", 2, "domain:a.com")}
	saved, err := s.CheckSetup(t.Context(), n, input)
	if err != nil || len(saved.Routes) != 1 || saved.Routes[0].ID != "r1" {
		t.Fatalf("check setup = %+v, %v", saved, err)
	}
	if n.ID != 0 || len(w.routes) != 0 || len(w.links) != 0 {
		t.Fatalf("setup preview wrote data: id %d, routes %v, links %v", n.ID, w.routes, w.links)
	}
}

// A preview writes nothing, the last good content of the lists of the server's
// other nodes included: only a sync keeps that.
func TestCheckKeepsNoListContentForOtherNodes(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:a.com"}, nil)
	w.node(12, 1, "other")
	w.routes[12] = &domain.NodeRoutes{NodeID: 12, Routes: []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}}
	if _, err := s.Check(t.Context(), 11, []domain.DestRoute{localRoute("", "domain:b.com")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.View(t.Context(), 12); err != nil {
		t.Fatal(err)
	}
	if len(w.good) != 0 {
		t.Fatalf("a preview kept list content: %v", w.good)
	}
	// A sync does.
	if _, err := s.PlanFor(t.Context(), planInput(w)); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.good[4]; !ok {
		t.Fatal("the sync did not keep the list's content")
	}
}

// Deleting a node removes the tunnels only its routes used, by the rule a save
// follows, under both servers' locks (lower id first).
func TestNodeDeletedRemovesTheTunnelsOnlyItsRoutesUsed(t *testing.T) {
	w, s := routedWorld()
	var locked []int64
	s.ServerLock = func(panel int64) func() { locked = append(locked, panel); return func() {} }
	node := w.nodes[11]
	node.DestRoutes = []domain.DestRoute{serverRoute("r1", 2, "domain:a.com"), serverRoute("r2", 3, "domain:b.com")}
	w.links[[2]int64{1, 2}] = domain.TransitLink{ID: 12, EntryPanelID: 1, ExitPanelID: 2}
	w.links[[2]int64{1, 3}] = domain.TransitLink{ID: 13, EntryPanelID: 1, ExitPanelID: 3}
	w.nodes[10].EgressPanelID = 3 // node 10 still leaves through server 3 by default
	delete(w.nodes, 11)           // the deleted node is gone from the fleet
	if err := s.NodeDeleted(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.links[[2]int64{1, 2}]; ok {
		t.Fatal("a tunnel only the deleted node's routes used was kept")
	}
	if _, ok := w.links[[2]int64{1, 3}]; !ok {
		t.Fatal("a tunnel another node leaves through by default was removed")
	}
	if len(locked) != 4 || locked[0] != 1 || locked[1] != 2 || locked[2] != 1 || locked[3] != 3 {
		t.Fatalf("locks taken = %v, want entry and exit, lower first, per tunnel", locked)
	}
}

// A front that stops routing to a tunnel hands the decision here: the tunnel
// goes when nothing of its entry server uses the pair, under the entry's route
// lock and then both servers' locks.
func TestLinksReleasedByAFrontAreDroppedOnlyWhenTheEntryDoesNotUseThem(t *testing.T) {
	w, s := routedWorld()
	var locked []int64
	s.ServerLock = func(panel int64) func() { locked = append(locked, panel); return func() {} }
	w.links[[2]int64{1, 2}] = domain.TransitLink{ID: 12, EntryPanelID: 1, ExitPanelID: 2}
	w.links[[2]int64{1, 3}] = domain.TransitLink{ID: 13, EntryPanelID: 1, ExitPanelID: 3}
	w.nodes[10].EgressPanelID = 3 // still leaves through server 3 by default
	s.LinksReleased(t.Context(), []int64{12, 13, 99})
	if _, ok := w.links[[2]int64{1, 2}]; ok {
		t.Fatal("an unused tunnel was kept")
	}
	if _, ok := w.links[[2]int64{1, 3}]; !ok {
		t.Fatal("a tunnel the entry leaves through by default was removed")
	}
	w.nodes[11].DestRoutes = []domain.DestRoute{serverRoute("r1", 3, "domain:a.com")}
	w.nodes[10].EgressPanelID = 0
	s.LinksReleased(t.Context(), []int64{13})
	if _, ok := w.links[[2]int64{1, 3}]; !ok {
		t.Fatal("a tunnel a route of the entry targets was removed")
	}
	if want := []int64{1, 2, 1, 3, 1, 3}; len(locked) != len(want) || locked[0] != 1 || locked[1] != 2 || locked[2] != 1 || locked[3] != 3 || locked[4] != 1 || locked[5] != 3 {
		t.Fatalf("locks taken = %v, want %v", locked, want)
	}
}

func TestSaveRefusesWhatTheServerCannotRun(t *testing.T) {
	w, s := routedWorld()
	route := []domain.DestRoute{serverRoute("", 2, "domain:a.com")}

	w.agents[1].ObservedCapabilities = []string{nodeprotocol.CapabilityTransitLink}
	if _, err := s.Save(t.Context(), 11, route); refusalCode(t, err) != CodeNoCapability {
		t.Fatalf("no route capability: %v", err)
	}
	w.agents[1].ObservedCapabilities = []string{nodeprotocol.CapabilityTransitRoute}
	if _, err := s.Save(t.Context(), 11, route); refusalCode(t, err) != CodeNoLinkCapability {
		t.Fatalf("no link capability: %v", err)
	}
	delete(w.agents, 1)
	if _, err := s.Save(t.Context(), 11, route); refusalCode(t, err) != CodeNoAgent {
		t.Fatalf("no agent: %v", err)
	}
	w.panels[1].Kind = domain.PanelKind3XUI
	if _, err := s.Save(t.Context(), 11, route); refusalCode(t, err) != CodeNotNode {
		t.Fatalf("not a Passwall Node server: %v", err)
	}
	// Clearing is always allowed, so routes saved before a server lost the
	// capability can be removed.
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{serverRoute("r1", 2, "domain:a.com")}}
	if _, err := s.Save(t.Context(), 11, nil); err != nil || len(w.routes[11].Routes) != 0 {
		t.Fatalf("clearing: %v %+v", err, w.routes[11])
	}
	w.nodes[12] = &domain.Node{ID: 12, PanelID: 1, Kind: domain.NodeKindSeparator}
	if _, err := s.Save(t.Context(), 12, route); refusalCode(t, err) != CodeSeparator {
		t.Fatalf("separator: %v", err)
	}
}

func TestSaveRefusesBadRoutes(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "ok", true, []string{"domain:a.com"}, nil)
	w.list(5, "source", true, nil, nil)
	w.lists[5].Side = domain.DestSideSource
	w.list(6, "owned", true, nil, nil)
	w.lists[6].OwnerGroupID = 3
	local := domain.DestRouteTarget{Kind: domain.DestRouteLocal}
	server2 := domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 2}
	cases := []struct {
		name  string
		route domain.DestRoute
		code  string
	}{
		{"a typed regexp", domain.DestRoute{Target: local, Domains: []string{"regexp:^a"}}, CodeRegexp},
		{"matches nothing", domain.DestRoute{Target: local}, CodeBadRoute},
		{"unknown prefix", domain.DestRoute{Target: local, Domains: []string{"suffix:a.com"}}, CodeBadRoute},
		{"bad cidr", domain.DestRoute{Target: local, CIDRs: []string{"nonsense"}}, CodeBadEntry},
		{"mapped cidr", domain.DestRoute{Target: local, CIDRs: []string{"::ffff:10.0.0.0/104"}}, CodeBadEntry},
		{"bad ports", domain.DestRoute{Target: local, Ports: "99999"}, CodeBadEntry},
		{"bad network", domain.DestRoute{Target: local, Ports: "1", Network: "icmp"}, CodeBadRoute},
		{"a bad hostname", domain.DestRoute{Target: local, Domains: []string{"domain:not a host"}}, CodeBadEntry},
		{"its own server", domain.DestRoute{Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 1}, Ports: "1"}, CodeBadTarget},
		{"no such server", domain.DestRoute{Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 99}, Ports: "1"}, CodeBadTarget},
		{"no target", domain.DestRoute{Ports: "1"}, CodeBadRoute},
		{"a missing list", domain.DestRoute{Target: server2, ListIDs: []int64{77}}, CodeBadList},
		{"a source list", domain.DestRoute{Target: server2, ListIDs: []int64{5}}, CodeBadList},
		{"a group's list", domain.DestRoute{Target: server2, ListIDs: []int64{6}}, CodeBadList},
	}
	for _, tc := range cases {
		if _, err := s.Save(t.Context(), 11, []domain.DestRoute{tc.route}); refusalCode(t, err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
	w.panels[9] = &domain.Panel{ID: 9, Name: "3x-ui", Kind: domain.PanelKind3XUI}
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{{Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 9}, Ports: "1"}}); refusalCode(t, err) != CodeBadTarget {
		t.Errorf("a server that is not a Passwall Node server: %v", err)
	}
	if len(w.routes) != 0 || len(w.links) != 0 {
		t.Fatalf("a refused save wrote %v %v", w.routes, w.links)
	}
}

// A route to a server nobody can dial would have its listener withheld at the
// sync, so the save refuses it, naming the server.
func TestSaveRefusesAnExitWithNoAddress(t *testing.T) {
	w, s := routedWorld()
	w.address[2] = ""
	_, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 2, "domain:a.com")})
	if code := refusalCode(t, err); code != CodeNoAddress {
		t.Fatalf("code = %s", code)
	}
	var refusal *Refusal
	errors.As(err, &refusal)
	if refusal.Params["server"] != "tokyo" {
		t.Fatalf("params = %+v", refusal.Params)
	}
	// The administrator typed the tunnel's own address: that is an address.
	w.links[[2]int64{1, 2}] = domain.TransitLink{ID: 12, EntryPanelID: 1, ExitPanelID: 2, ExitAddress: "tokyo.example.net"}
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 2, "domain:a.com")}); err != nil {
		t.Fatalf("with the link's own address: %v", err)
	}
}

func TestSaveWarnsAboutWhatItCanSee(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "cdn", true, []string{"domain:a.com", "regexp:^x"}, nil)
	w.list(5, "later", false, nil, nil)
	w.agents[2].ObservedCapabilities = nil
	delete(w.agents, 3)
	w.nodes[11].EgressPanelID = 2
	saved, err := s.Save(t.Context(), 11, []domain.DestRoute{
		{Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 3}, ListIDs: []int64{4, 5}},
		serverRoute("", 2, "domain:b.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, warning := range saved.Warnings {
		codes[warning.Code] = true
		if warning.Message == "" {
			t.Errorf("a warning without wording: %+v", warning)
		}
	}
	for _, want := range []string{WarnListRegexp, WarnListNotReady, WarnExitNotReady, WarnExitNoLink, WarnRedundant} {
		if !codes[want] {
			t.Errorf("no %s warning in %v", want, saved.Warnings)
		}
	}
}

func TestSaveBudgetRefusesWhatTheNodeWouldRejectAndNamesTheListener(t *testing.T) {
	w, s := routedWorld()
	var rules []protocolRuleAlias
	for i := 0; i < 70; i++ {
		rules = append(rules, protocolRuleAlias{ID: "p" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Action: nodeprotocol.RuleBlock,
			Domains: generated(string(rune('a'+i%26))+string(rune('a'+i/26)), 15000)})
	}
	w.plan = destpolicy.Plan{Policy: &nodeprotocol.DestinationPolicy{Rules: rules}, Engine: "xray", Resolve: nodeprotocol.ResolveAsIs}
	_, err := s.Save(t.Context(), 11, []domain.DestRoute{localRoute("", "domain:a.example")})
	if code := refusalCode(t, err); code != CodeOverBudget {
		t.Fatalf("code = %s (%v)", code, err)
	}
	var refusal *Refusal
	errors.As(err, &refusal)
	if refusal.Params["node"] != "routed" || refusal.Params["listener"] != "lst_11" || refusal.Params["limit"] != "entries" {
		t.Fatalf("params = %+v", refusal.Params)
	}
	if len(w.routes) != 0 {
		t.Fatal("a refused save wrote routes")
	}
	// A node that is already over the limit is not made the refusal of an
	// unrelated edit: the save says so and goes through.
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{localRoute("r1", "domain:a.example")}}
	saved, err := s.Save(t.Context(), 11, []domain.DestRoute{localRoute("r1", "domain:a.example"), localRoute("", "domain:b.example")})
	if err != nil {
		t.Fatalf("an edit of a listener that was already over: %v", err)
	}
	if !slices.ContainsFunc(saved.Warnings, func(w Warning) bool { return w.Code == WarnBudget }) {
		t.Fatalf("warnings = %+v", saved.Warnings)
	}
}

type protocolRuleAlias = nodeprotocol.DestinationRule

func TestSaveReportsXrayApproximations(t *testing.T) {
	w, s := routedWorld()
	w.plan = destpolicy.Plan{Engine: "xray", Resolve: nodeprotocol.ResolveAsIs, Policy: &nodeprotocol.DestinationPolicy{Rules: []nodeprotocol.DestinationRule{
		{ID: "p7", Action: nodeprotocol.RuleAllow, Domains: []string{"regexp:^cdn"}},
	}}}
	saved, err := s.Save(t.Context(), 11, []domain.DestRoute{localRoute("", "domain:example.com")})
	if err != nil {
		t.Fatal(err)
	}
	var got *Warning
	for i := range saved.Warnings {
		if saved.Warnings[i].Code == WarnApproximate {
			got = &saved.Warnings[i]
		}
	}
	if got == nil || got.Listener != "lst_11" || got.Params["count"] != 1 {
		t.Fatalf("warnings = %+v", saved.Warnings)
	}
	// sing-box intersects exactly: no warning.
	w.plan.Engine = "sing-box"
	saved, _ = s.Save(t.Context(), 11, []domain.DestRoute{localRoute("r1", "domain:example.com")})
	for _, warning := range saved.Warnings {
		if warning.Code == WarnApproximate {
			t.Fatalf("sing-box warned: %+v", warning)
		}
	}
}

func TestSaveRefusesRoutesBeyondTheServersWireLimits(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "big", true, generated("a", nodeprotocol.MaxPolicyDomains/2+1), nil)
	w.node(12, 1, "second")
	w.routes[12] = &domain.NodeRoutes{NodeID: 12, Routes: []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}}
	// 25,001 domains on node 12 and as many again on node 11 pass the 50,000 the
	// entry accepts for the whole server.
	w.list(5, "big2", true, generated("b", nodeprotocol.MaxPolicyDomains/2+1), nil)
	_, err := s.Save(t.Context(), 11, []domain.DestRoute{{Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{5}}})
	if code := refusalCode(t, err); code != CodeTooManyEntries {
		t.Fatalf("code = %s (%v)", code, err)
	}
}

func TestViewShowsTheRoutesLinksAndDelivery(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "cdn", true, []string{"domain:a.com", "regexp:^x"}, nil)
	w.links[[2]int64{1, 2}] = domain.TransitLink{ID: 12, EntryPanelID: 1, ExitPanelID: 2, InSent: "a", InApplied: "a", OutSent: "b", OutApplied: ""}
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Sent: "d", Applied: "d", Routes: []domain.DestRoute{
		{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 2}, ListIDs: []int64{4}},
		localRoute("r2", "domain:b.com"),
	}}
	w.nodes[11].EgressPanelID = 3
	w.probes = []domain.ProbeState{{PanelID: 1, ObjectKey: "lnk_12", Kind: domain.ProbeKindE2E, State: domain.ProbeOK, ReportedAt: s.Now(), ValidUntil: s.Now().Add(time.Hour)}}
	view, err := s.View(t.Context(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Support.Supported || !view.Sent || !view.Running || view.Default.Kind != domain.DestRouteServer || view.Default.PanelID != 3 {
		t.Fatalf("view = %+v", view)
	}
	if len(view.Items) != 2 || view.Items[0].Entries != 1 || !view.Items[0].Sent || view.Items[1].Link != nil {
		t.Fatalf("items = %+v", view.Items)
	}
	link := view.Items[0].Link
	if link == nil || !link.Exists || link.Ready || !link.InApplied || link.OutApplied || link.ServerName != "tokyo" {
		t.Fatalf("link = %+v", link)
	}
	if link.Quality == nil || link.Quality.State != domain.ProbeOK {
		t.Fatalf("quality = %+v", link.Quality)
	}
	if view.DefaultLink == nil || view.DefaultLink.Exists || view.DefaultLink.PanelID != 3 {
		t.Fatalf("default link = %+v", view.DefaultLink)
	}
	if !slices.ContainsFunc(view.Warnings, func(w Warning) bool { return w.Code == WarnListRegexp }) {
		t.Fatalf("warnings = %+v", view.Warnings)
	}
	// A server that lost the capability still shows the routes, and says they are not sent.
	w.agents[1].ObservedCapabilities = nil
	view, _ = s.View(t.Context(), 11)
	if view.Support.Supported || view.Support.Reason != CodeNoLinkCapability || len(view.Routes) != 2 ||
		!slices.ContainsFunc(view.Warnings, func(w Warning) bool { return w.Code == "routes_not_sent" }) {
		t.Fatalf("unsupported view = %+v", view)
	}
}

func TestListUsesNamesTheNodesThatHoldAList(t *testing.T) {
	w, s := routedWorld()
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4, 4, 8}}}}
	uses, err := s.ListUses(t.Context())
	if err != nil || len(uses[4]) != 1 || uses[4][0].NodeID != 11 || uses[4][0].Name != "routed" || len(uses[8]) != 1 || len(uses[5]) != 0 {
		t.Fatalf("uses = %+v %v", uses, err)
	}
}

// The tunnel a route needed goes when no route and no default exit uses it, and
// stays while any does: removing it frees its port on the exit server.
func TestSaveRemovesTheTunnelNothingUsesAnyMore(t *testing.T) {
	w, s := routedWorld()
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 2, "domain:a.com"), serverRoute("", 3, "domain:b.com")}); err != nil {
		t.Fatal(err)
	}
	if len(w.links) != 2 {
		t.Fatalf("links = %v", w.links)
	}
	// Server 2 is dropped from the routes; 3 stays.
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("r2", 3, "domain:b.com")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.links[[2]int64{1, 2}]; ok || len(w.links) != 1 {
		t.Fatalf("links after dropping server 2 = %v", w.links)
	}
	// Another node of the server reaches 3 by its default exit: clearing this
	// node's routes keeps the tunnel.
	w.nodes[10].EgressPanelID = 3
	if _, err := s.Save(t.Context(), 11, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.links[[2]int64{1, 3}]; !ok {
		t.Fatal("a tunnel a node leaves through by default was removed")
	}
	w.nodes[10].EgressPanelID = 0
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 3, "domain:b.com")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(t.Context(), 11, nil); err != nil {
		t.Fatal(err)
	}
	if len(w.links) != 0 {
		t.Fatalf("links after clearing = %v", w.links)
	}
}

// The tunnels are made under the locks of both their servers (lower id first),
// after the routes are stored.
func TestSaveMakesTunnelsUnderTheExitServersLock(t *testing.T) {
	w, s := routedWorld()
	var locked []int64
	s.ServerLock = func(panel int64) func() { locked = append(locked, panel); return func() {} }
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 3, "domain:b.com"), serverRoute("", 2, "domain:a.com")}); err != nil {
		t.Fatal(err)
	}
	if len(locked) != 4 || locked[0] != 1 || locked[1] != 2 || locked[2] != 1 || locked[3] != 3 || len(w.links) != 2 {
		t.Fatalf("locked %v links %v", locked, w.links)
	}
}

// When a tunnel cannot be made the save fails as a whole: the routes that were
// there stay, and the tunnels the failed save made are not left behind.
func TestSaveThatCannotMakeATunnelKeepsThePreviousRoutes(t *testing.T) {
	w, s := routedWorld()
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("", 2, "domain:a.com")}); err != nil {
		t.Fatal(err)
	}
	before := w.routes[11].Routes
	w.ensureErr[3] = errors.New("no free port")
	if _, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("r1", 2, "domain:a.com"), serverRoute("", 3, "domain:b.com")}); err == nil {
		t.Fatal("the save went through without the tunnel")
	}
	if !slices.EqualFunc(w.routes[11].Routes, before, func(a, b domain.DestRoute) bool {
		return a.ID == b.ID && a.Target == b.Target && slices.Equal(a.Domains, b.Domains)
	}) {
		t.Fatalf("routes = %+v, want the previous ones %+v", w.routes[11].Routes, before)
	}
	if _, ok := w.links[[2]int64{1, 2}]; !ok {
		t.Fatal("the tunnel the previous routes use was removed")
	}
}

// With two new exits, the first tunnel is made and the second fails: the save is
// refused as a whole, the tunnel it did make is removed again and the previous
// routes are back.
func TestSaveThatFailsOnASecondNewTunnelRemovesTheFirstOne(t *testing.T) {
	w, s := routedWorld()
	w.server(4, "paris")
	w.nodes[11].DestRoutes = []domain.DestRoute{serverRoute("r1", 4, "domain:old.com")}
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: w.nodes[11].DestRoutes}
	w.links[[2]int64{1, 4}] = domain.TransitLink{ID: 14, EntryPanelID: 1, ExitPanelID: 4}
	before := w.routes[11].Routes
	w.ensureErr[3] = errors.New("no free port")
	_, err := s.Save(t.Context(), 11, []domain.DestRoute{serverRoute("r1", 4, "domain:old.com"),
		serverRoute("", 2, "domain:a.com"), serverRoute("", 3, "domain:b.com")})
	if err == nil {
		t.Fatal("the save went through without its second tunnel")
	}
	if _, ok := w.links[[2]int64{1, 2}]; ok {
		t.Fatalf("the first tunnel this save made was left behind: %v", w.links)
	}
	if _, ok := w.links[[2]int64{1, 4}]; !ok {
		t.Fatal("the tunnel the previous routes use was removed")
	}
	if !slices.EqualFunc(w.routes[11].Routes, before, func(a, b domain.DestRoute) bool {
		return a.ID == b.ID && a.Target == b.Target && slices.Equal(a.Domains, b.Domains)
	}) {
		t.Fatalf("routes = %+v, want the previous ones %+v", w.routes[11].Routes, before)
	}
}
