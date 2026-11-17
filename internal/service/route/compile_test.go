package route

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// planInput is a sync of server 1 that sends it node 11 (routed) and node 10.
func planInput(w *world, caps ...string) PlanInput {
	if len(caps) == 0 {
		caps = []string{nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitRoute}
	}
	nodes, _ := fakeNodes{w}.List(nil)
	var mine []*domain.Node
	for _, n := range nodes {
		if n.PanelID == 1 {
			mine = append(mine, n)
		}
	}
	return PlanInput{PanelID: 1, Capabilities: caps, Nodes: mine}
}

func linkTo(w *world, exit int64) nodeprotocol.LinkOut {
	l, _ := fakeLinks{w}.EnsureLink(nil, 1, exit)
	return nodeprotocol.LinkOut{Key: nodeprotocol.NewLinkKey(l.ID), Address: "192.0.2.2", Port: 20001, Protocol: nodeprotocol.LinkProtocolSS2022,
		SS: &nodeprotocol.LinkSS{Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA=="}}
}

func routedWorld() (*world, *Service) {
	w := newWorld()
	w.server(1, "entry")
	w.server(2, "tokyo")
	w.server(3, "london")
	w.node(10, 1, "plain")
	w.node(11, 1, "routed")
	return w, w.service()
}

func TestCompileNodeWritesListsAsLiteralsAndSaysWhatItLeftOut(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:netflix.com", "domain:hulu.com", "regexp:^cdn[0-9]+\\.example$", "keyword:nflx"}, []string{"198.51.100.0/24", "198.51.100.128/25"})
	w.list(5, "pending", false, nil, nil)
	w.list(6, "empty", true, nil, nil)
	node := w.node(11, 1, "routed")
	routes := []domain.DestRoute{
		{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 2}, ListIDs: []int64{4, 5}, Domains: []string{"domain:extra.example"}, Ports: "443"},
		{ID: "r2", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{5}, Ports: "25"},
		{ID: "r3", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{6}},
		{ID: "r4", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Ports: "6881-6889", Network: "udp"},
	}
	index, _ := s.listIndex(t.Context())
	out, err := s.compileNode(t.Context(), node, routes, func(panel int64) (nodeprotocol.LinkKey, bool) { return "lnk_11", panel == 2 }, index, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	// r1: the list's domains (regexp left out) and the typed one, sorted; the
	// addresses merged. r2 and r3 name only lists that gave nothing, so the port
	// alone must not stand in for them: both are left out. r4 has no list at all.
	if len(out.Rules) != 2 {
		t.Fatalf("rules = %+v", out.Rules)
	}
	r1 := out.Rules[0]
	// The /25 sits inside the /24, so the two merge into the /24.
	if r1.ID != "r1" || r1.Exit != "lnk_11" || r1.Ports != "443" ||
		!slices.Equal(r1.Domains, []string{"domain:extra.example", "domain:hulu.com", "domain:netflix.com", "keyword:nflx"}) ||
		!slices.Equal(r1.CIDRs, []string{"198.51.100.0/24"}) {
		t.Fatalf("r1 = %+v", r1)
	}
	if r4 := out.Rules[1]; r4.ID != "r4" || r4.Exit != nodeprotocol.LocalExit || r4.Network != "udp" || out.Entries["r4"] != 0 {
		t.Fatalf("r4 = %+v", r4)
	}
	notes := map[string]string{}
	for _, n := range out.Notes {
		notes[n.Route+"/"+n.Code] = n.List
	}
	for _, want := range []string{"r1/" + NoteListRegexp, "r1/" + NoteListNotReady, "r2/" + NoteListNotReady, "r2/" + NoteSkipped, "r3/" + NoteListEmpty, "r3/" + NoteSkipped} {
		if _, ok := notes[want]; !ok {
			t.Errorf("missing note %s in %+v", want, out.Notes)
		}
	}
	for _, n := range out.Notes {
		if n.Code == NoteListRegexp && n.Count != 1 {
			t.Errorf("regexp count = %d", n.Count)
		}
	}
	// Every route that is sent satisfies the wire contract.
	transit := nodeprotocol.Transit{LinksOut: []nodeprotocol.LinkOut{stubLinkOut("lnk_11")}, Routes: []nodeprotocol.ListenerRoutes{{Listener: "lst_11", Rules: out.Rules}}}
	if err := nodeprotocol.ValidateTransit(&transit); err != nil {
		t.Fatalf("compiled routes are refused by the contract: %v", err)
	}
}

func TestCompileNodeCachesByWhatItDependsOn(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:a.com"}, nil)
	node := w.node(11, 1, "routed")
	routes := []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}
	exit := func(int64) (nodeprotocol.LinkKey, bool) { return "", false }
	index, _ := s.listIndex(t.Context())
	first, err := s.compileNode(t.Context(), node, routes, exit, index, "geo1", true, true)
	if err != nil || len(first.Rules[0].Domains) != 1 {
		t.Fatalf("first = %+v %v", first, err)
	}
	// The list's content changes: its hash does, and the compile follows.
	w.list(4, "streaming", true, []string{"domain:a.com", "domain:b.com"}, nil)
	w.lists[4].ContentSHA256 = "changed"
	index, _ = s.listIndex(t.Context())
	second, _ := s.compileNode(t.Context(), node, routes, exit, index, "geo1", true, true)
	if len(second.Rules[0].Domains) != 2 {
		t.Fatalf("a changed list was served from the cache: %+v", second)
	}
	// A new geosite release is a reason to expand again with the list text unchanged.
	w.list(4, "streaming", true, []string{"domain:a.com", "domain:b.com", "domain:c.com"}, nil)
	w.lists[4].ContentSHA256 = "changed"
	index, _ = s.listIndex(t.Context())
	if same, _ := s.compileNode(t.Context(), node, routes, exit, index, "geo1", true, true); len(same.Rules[0].Domains) != 2 {
		t.Fatal("nothing changed, so the cache should answer")
	}
	if moved, _ := s.compileNode(t.Context(), node, routes, exit, index, "geo2", true, true); len(moved.Rules[0].Domains) != 3 {
		t.Fatal("a new data release should expand again")
	}
}

func TestPlanForSendsRoutesOnlyToAnAgentThatDeclaredThem(t *testing.T) {
	w, s := routedWorld()
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{serverRoute("r1", 2, "domain:a.com")}}

	// No transit.route.v1: the listener is not sent at all, since sent without
	// its routes it would send their destinations through the default exit.
	plan, err := s.PlanFor(t.Context(), planInput(w, nodeprotocol.CapabilityTransitLink))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Routes) != 0 || plan.Exits != nil || len(plan.Withheld) != 1 || plan.Withheld[0].NodeID != 11 || plan.Withheld[0].Reason != WithheldNoCapability {
		t.Fatalf("plan for an agent without the capability = %+v", plan)
	}
	if len(plan.Issues) != 1 || plan.Issues[0].Code != IssueRoutesNotSent || plan.Issues[0].Key != "lst_11" {
		t.Fatalf("issues = %+v", plan.Issues)
	}
	// Likewise without the link capability: routes are meaningless without tunnels.
	if plan, _ = s.PlanFor(t.Context(), planInput(w, nodeprotocol.CapabilityTransitRoute)); len(plan.Routes) != 0 || len(plan.Withheld) != 1 {
		t.Fatalf("routes sent to an agent without links: %+v", plan)
	}
}

func TestPlanForBuildsTheRoutesAndTheExitsOfTheListener(t *testing.T) {
	w, s := routedWorld()
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{
		serverRoute("r1", 2, "domain:a.com"), serverRoute("r2", 3, "domain:b.com"), localRoute("r5", "domain:c.com")}}
	in := planInput(w)
	out2, out3 := linkTo(w, 2), linkTo(w, 3)
	in.Out = []nodeprotocol.LinkOut{out2, out3}
	in.RouteOut = map[int64]nodeprotocol.LinkKey{2: out2.Key, 3: out3.Key}
	in.RouteKeys = in.RouteOut
	plan, err := s.PlanFor(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Routes) != 1 || plan.Routes[0].Listener != "lst_11" || len(plan.Routes[0].Rules) != 3 {
		t.Fatalf("routes = %+v", plan.Routes)
	}
	rules := plan.Routes[0].Rules
	if rules[0].Exit != out2.Key || rules[1].Exit != out3.Key || rules[2].Exit != nodeprotocol.LocalExit || rules[2].ID != "r5" {
		t.Fatalf("rules = %+v", rules)
	}
	// The node leaves locally by default, so its exits are the two links and local.
	exits := plan.Exits[11]
	if len(exits) != 3 {
		t.Fatalf("exits = %+v", exits)
	}
	panels := map[nodeprotocol.LinkKey]int64{}
	for _, e := range exits {
		panels[e.Key] = e.PanelID
	}
	if panels[out2.Key] != 2 || panels[out3.Key] != 3 || panels[nodeprotocol.LocalExit] != 1 {
		t.Fatalf("exit panels = %+v", panels)
	}
	if _, routed := plan.Exits[10]; routed {
		t.Fatal("a node without routes has exits")
	}
	// The body the sync would send is valid as a whole.
	transit := nodeprotocol.Transit{LinksOut: in.Out, Routes: plan.Routes}
	if err := nodeprotocol.ValidateTransit(&transit); err != nil {
		t.Fatal(err)
	}
	// A default exit through a tunnel is one of the exits, with the server it leaves from.
	w.nodes[11].EgressPanelID = 3
	in = planInput(w)
	in.Out, in.RouteOut, in.RouteKeys = []nodeprotocol.LinkOut{out2, out3}, map[int64]nodeprotocol.LinkKey{2: out2.Key, 3: out3.Key}, map[int64]nodeprotocol.LinkKey{2: out2.Key, 3: out3.Key}
	in.Egress = []nodeprotocol.EgressBinding{{Listener: "lst_11", Link: out3.Key}}
	in.EgressExit = map[int64]int64{11: 3}
	plan, _ = s.PlanFor(t.Context(), in)
	// local stays an exit: r5 sends there.
	if len(plan.Exits[11]) != 3 {
		t.Fatalf("exits with a default tunnel = %+v", plan.Exits[11])
	}
}

// A route whose exit has no link in the config cannot be sent, and sending the
// node without it would send those destinations through its default exit: the
// listener is withheld, and an issue names why.
func TestPlanForWithholdsAListenerWhoseRouteHasNoLink(t *testing.T) {
	w, s := routedWorld()
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{serverRoute("r1", 2, "domain:a.com")}}
	in := planInput(w)
	in.RouteKeys = map[int64]nodeprotocol.LinkKey{2: "lnk_11"} // the row exists, its outgoing half is not sent
	plan, err := s.PlanFor(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Routes) != 0 || len(plan.Withheld) != 1 || plan.Withheld[0].NodeID != 11 || plan.Withheld[0].Reason != WithheldNoLink {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Issues) != 1 || plan.Issues[0].Code != IssueRouteListenerHeld {
		t.Fatalf("issues = %+v", plan.Issues)
	}
	// An exit that has no link row at all is a different reason: the server is gone.
	in.RouteKeys = nil
	if plan, _ = s.PlanFor(t.Context(), in); len(plan.Withheld) != 1 || plan.Withheld[0].Reason != WithheldTargetGone {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanForNeverTouchesANodeWithoutRoutes(t *testing.T) {
	_, s := routedWorld()
	plan, err := s.PlanFor(t.Context(), planInput(newWorldWithPlainNode(s)))
	if err != nil || plan.Routes != nil || plan.Exits != nil || plan.Withheld != nil || plan.Issues != nil {
		t.Fatalf("plan = %+v %v", plan, err)
	}
}

func newWorldWithPlainNode(*Service) *world {
	w, _ := routedWorld()
	return w
}

func TestRoutesAreSentAsTheWireWantsThem(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "l", true, []string{"domain:b.com", "domain:a.com"}, []string{"10.0.0.0/8"})
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{{ID: "r2", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}}
	plan, err := s.PlanFor(t.Context(), planInput(w))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan.Routes)
	const want = `[{"listener":"lst_11","rules":[{"id":"r2","domains":["domain:a.com","domain:b.com"],"cidrs":["10.0.0.0/8"],"exit":"local"}]}]`
	if string(encoded) != want {
		t.Fatalf("wire = %s\nwant   %s", encoded, want)
	}
}

func TestRecordSentAndConfirmFollowTheListener(t *testing.T) {
	w, s := routedWorld()
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{localRoute("r1", "domain:a.com")}}
	lr := nodeprotocol.ListenerRoutes{Listener: "lst_11", Rules: []nodeprotocol.RouteRule{{ID: "r1", Domains: []string{"domain:a.com"}, Exit: nodeprotocol.LocalExit}}}
	nodes, _ := fakeNodes{w}.List(t.Context())
	if _, err := s.RecordSent(t.Context(), nodes, []nodeprotocol.ListenerRoutes{lr}, nil); err != nil {
		t.Fatal(err)
	}
	digest := domain.TransitRoutesDigest(lr)
	if w.routes[11].Sent != digest {
		t.Fatalf("sent = %q", w.routes[11].Sent)
	}
	held := &nodeprotocol.Transit{Routes: []nodeprotocol.ListenerRoutes{lr}}
	now := time.Now()
	// The listener rejected: the routes are not running.
	if changed, err := s.Confirm(t.Context(), held, map[string]nodeprotocol.ObjectStatus{"lst_11": {State: nodeprotocol.ObjectRejected}}, now); err != nil || changed {
		t.Fatalf("rejected: %v %v", changed, err)
	}
	if w.routes[11].Running() {
		t.Fatal("rejected routes count as running")
	}
	if changed, err := s.Confirm(t.Context(), held, map[string]nodeprotocol.ObjectStatus{"lst_11": {State: nodeprotocol.ObjectApplied}}, now); err != nil || !changed {
		t.Fatalf("applied: %v %v", changed, err)
	}
	if !w.routes[11].Running() {
		t.Fatal("applied routes do not count as running")
	}
	// The listener goes missing from the report: the confirmation is withdrawn.
	if changed, _ := s.Confirm(t.Context(), held, nil, now); !changed || w.routes[11].Running() {
		t.Fatal("a missing listener kept its confirmation")
	}
	// Not sending the routes any more clears what was confirmed.
	_, _ = s.Confirm(t.Context(), held, map[string]nodeprotocol.ObjectStatus{"lst_11": {State: nodeprotocol.ObjectApplied}}, now)
	changed, err := s.RecordSent(t.Context(), nodes, nil, map[int64]string{11: WithheldNoCapability})
	if err != nil || !changed {
		t.Fatalf("withhold: %v %v", changed, err)
	}
	if w.routes[11].Withheld != WithheldNoCapability {
		t.Fatalf("withheld = %q", w.routes[11].Withheld)
	}
	if w.routes[11].Sent != "" || w.routes[11].Applied != "" {
		t.Fatalf("withdrawn = %+v", w.routes[11])
	}
}

// An automatic refresh that leaves a list empty, regexp-only or unavailable must
// not send the destinations it gave to the default exit: the route goes on from
// the list's last good expansion and says so. Only the administrator's confirmed
// edit (Forget) lets them go.
func TestARouteKeepsTheLastGoodListWhenARefreshLeavesItNothing(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:netflix.com", "domain:hulu.com"}, []string{"198.51.100.0/24"})
	node := w.node(11, 1, "routed")
	routes := []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}
	exit := func(int64) (nodeprotocol.LinkKey, bool) { return "", false }
	compile := func() compiled {
		t.Helper()
		index, _ := s.listIndex(t.Context())
		out, err := s.compileNode(t.Context(), node, routes, exit, index, "", true, true)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hasNote := func(out compiled, code string) bool {
		for _, n := range out.Notes {
			if n.Code == code {
				return true
			}
		}
		return false
	}
	first := compile()
	if len(first.Rules) != 1 || len(first.Rules[0].Domains) != 2 || len(first.Notes) != 0 {
		t.Fatalf("first = %+v", first)
	}
	if _, ok := w.good[4]; !ok {
		t.Fatal("a usable expansion was not kept as the last good one")
	}

	for name, refresh := range map[string]func(){
		"empty":       func() { w.list(4, "streaming", true, nil, nil); w.lists[4].ContentSHA256 = "e" },
		"regexp only": func() { w.list(4, "streaming", true, []string{"regexp:^x"}, nil); w.lists[4].ContentSHA256 = "r" },
		"not ready":   func() { w.list(4, "streaming", false, nil, nil); w.lists[4].ContentSHA256 = "n" },
	} {
		refresh()
		out := compile()
		if len(out.Rules) != 1 || len(out.Rules[0].Domains) != 2 || len(out.Rules[0].CIDRs) != 1 || !hasNote(out, NoteListLastGood) {
			t.Fatalf("%s: the route lost what the list gave: %+v", name, out)
		}
		if hasNote(out, NoteSkipped) {
			t.Fatalf("%s: the route was skipped", name)
		}
	}

	// A refresh that leaves something usable replaces the last good content.
	w.list(4, "streaming", true, []string{"domain:max.com"}, nil)
	w.lists[4].ContentSHA256 = "new"
	if out := compile(); len(out.Rules[0].Domains) != 1 || out.Rules[0].Domains[0] != "domain:max.com" || hasNote(out, NoteListLastGood) {
		t.Fatalf("a usable refresh: %+v", out)
	}

	// The administrator's confirmed edit forgets it: now the list gives nothing.
	w.list(4, "streaming", true, nil, nil)
	w.lists[4].ContentSHA256 = "gone"
	if err := s.Forget(t.Context(), 4); err != nil {
		t.Fatal(err)
	}
	out := compile()
	if len(out.Rules) != 0 || !hasNote(out, NoteListEmpty) || !hasNote(out, NoteSkipped) {
		t.Fatalf("after Forget: %+v", out)
	}
}

// A list that fails to expand for a reason that is neither "empty" nor "data
// missing" (a store or decoding fault) must not fail the server's whole sync, and
// must not send the node without its route: the route goes on from the list's
// last good content, or, with none, the listener is withheld.
func TestAListThatFailsToExpandNeverSendsTheNodeWithoutItsRoute(t *testing.T) {
	w, s := routedWorld()
	w.list(4, "streaming", true, []string{"domain:netflix.com"}, nil)
	w.routes[11] = &domain.NodeRoutes{NodeID: 11, Routes: []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{4}}}}

	// No last good content: the listener is withheld, and the sync goes on.
	w.expandErr[4] = errors.New("decode failed")
	plan, err := s.PlanFor(t.Context(), planInput(w))
	if err != nil {
		t.Fatalf("a failing list failed the whole plan: %v", err)
	}
	if len(plan.Routes) != 0 || len(plan.Withheld) != 1 || plan.Withheld[0].Reason != WithheldListError {
		t.Fatalf("plan = %+v", plan)
	}

	// With last good content, the route goes on from it.
	delete(w.expandErr, 4)
	if plan, err = s.PlanFor(t.Context(), planInput(w)); err != nil || len(plan.Routes) != 1 {
		t.Fatalf("healthy: %+v %v", plan, err)
	}
	w.expandErr[4] = errors.New("decode failed")
	w.lists[4].ContentSHA256 = "changed"
	plan, err = s.PlanFor(t.Context(), planInput(w))
	if err != nil || len(plan.Withheld) != 0 || len(plan.Routes) != 1 || len(plan.Routes[0].Rules[0].Domains) != 1 {
		t.Fatalf("with last good content: %+v %v", plan, err)
	}
}
