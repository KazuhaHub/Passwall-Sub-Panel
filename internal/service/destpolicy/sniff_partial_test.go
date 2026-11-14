package destpolicy

import (
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A node is sent whatever it can enforce: the rules that need the sniffer run
// only on the inbounds whose sniffing can supply it, and every other rule keeps
// applying everywhere. Nothing here is a fallback.

const (
	weakSniffing      = `{"enabled":true,"metadataOnly":true}`
	routeOnlySniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
)

// mixedFleet is one good inbound (lst_1) and one that cannot sniff (lst_2).
func mixedFleet(weak string) fleet {
	return fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, weak)}, users: []int64{10}}
}

func ruleByID(t *testing.T, p *protocol.DestinationPolicy, id string) protocol.DestinationRule {
	t.Helper()
	if p != nil {
		for _, r := range p.Rules {
			if r.ID == id {
				return r
			}
		}
	}
	t.Fatalf("no rule %s in %v", id, ruleIDs(p))
	return protocol.DestinationRule{}
}

func hasSniffIssue(res Result, key string) bool {
	for _, i := range res.Issues {
		if i.Code == IssueSniffInsufficient && i.Key == key {
			return true
		}
	}
	return false
}

func TestSniffPartial_DomainRuleRunsOnlyOnTheInboundsThatCanSniff(t *testing.T) {
	h := newHarness(t)
	doms := h.list("doms", "DOMAIN-SUFFIX,x.example.com")
	cidrs := h.list("cidrs", "IP-CIDR,203.0.113.0/24")
	dp := h.policy("doms", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{doms.ID} })
	cp := h.policy("cidrs", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{cidrs.ID} })
	sp := smtpPolicy(h, "smtp")
	h.publishNow()

	res := h.compile(mixedFleet(weakSniffing))
	if res.Policy == nil {
		t.Fatal("nothing sent")
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	if got := ruleByID(t, res.Policy, idOf(dp)).Inbounds; !slices.Equal(got, []protocol.ListenerKey{"lst_1"}) {
		t.Fatalf("domain rule inbounds = %v, want [lst_1]", got)
	}
	for _, p := range []*domain.DestPolicy{cp, sp} {
		if got := ruleByID(t, res.Policy, idOf(p)).Inbounds; len(got) != 0 {
			t.Fatalf("%s needs no sniffing and must run everywhere, got inbounds %v", idOf(p), got)
		}
	}
	// The status still names the inbound, through the existing issue.
	if !hasSniffIssue(res, "lst_2") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	m := h.meta()
	if m.FallbackReason != domain.DestFallbackNone || !strings.Contains(m.PrecheckListeners, `"lst_2"`) {
		t.Fatalf("state = %+v: not a fallback, and the inbound is still listed", m)
	}
	if m.MintedSHA256 != res.Digest || m.DesiredSHA256 != res.Digest {
		t.Fatalf("what is sent IS the desired policy: %+v", m)
	}
	if h.rec.count(ResultCompiled) != 1 {
		t.Fatalf("results = %v", h.rec.compile)
	}
}

// A list that holds domains AND addresses is one rule that matches either: only
// its domain half needs the sniffer, so the address half stays on every inbound.
func TestSniffPartial_ARuleWithDomainsAndAddressesKeepsItsAddressHalfEverywhere(t *testing.T) {
	h := newHarness(t)
	l := h.list("mixed", "DOMAIN-SUFFIX,x.example.com\nIP-CIDR,203.0.113.0/24")
	p := h.policy("mixed", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{l.ID}
		p.Inline.Ports = "443"
	})
	h.publishNow()
	res := h.compile(mixedFleet(weakSniffing))
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	var addr, doms *protocol.DestinationRule
	for i := range res.Policy.Rules {
		r := &res.Policy.Rules[i]
		switch {
		case len(r.Domains) > 0:
			doms = r
		case len(r.CIDRs) > 0:
			addr = r
		}
	}
	if addr == nil || doms == nil || len(res.Policy.Rules) != 2 {
		t.Fatalf("rules = %+v, want an address rule and a domain rule", res.Policy.Rules)
	}
	if len(addr.Inbounds) != 0 || addr.Ports != "443" || !strings.HasPrefix(addr.ID, idOf(p)) {
		t.Fatalf("address rule = %+v", *addr)
	}
	if !slices.Equal(doms.Inbounds, []protocol.ListenerKey{"lst_1"}) || doms.Ports != "443" || len(doms.CIDRs) != 0 || !strings.HasPrefix(doms.ID, idOf(p)) || doms.ID == addr.ID {
		t.Fatalf("domain rule = %+v", *doms)
	}
}

func TestSniffPartial_EveryInboundInsufficientDropsOnlyTheRulesThatNeedSniffing(t *testing.T) {
	h := newHarness(t)
	doms := h.list("doms", "DOMAIN-SUFFIX,x.example.com")
	h.policy("doms", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{doms.ID} })
	h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline.Protocols = []string{"bittorrent"} })
	sp := smtpPolicy(h, "smtp")
	h.publishNow()

	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, weakSniffing), node(2, weakSniffing)}, users: []int64{10}}
	res := h.compile(f)
	if res.Policy == nil || joinIDs(ruleIDs(res.Policy)) != idOf(sp) {
		t.Fatalf("rules = %v, want only the ports rule %s (a node with no sniffing still enforces the rest)", ruleIDs(res.Policy), idOf(sp))
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	if !hasSniffIssue(res, "lst_1") || !hasSniffIssue(res, "lst_2") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("fallback = %q", m.FallbackReason)
	}
}

// BitTorrent is a protocol rule: it needs the sniffer too, but not everything a
// domain rule needs, so routeOnly (which only breaks domains) does not cost it.
func TestSniffPartial_AProtocolRuleIsRestrictedByItsOwnNeed(t *testing.T) {
	h := newHarness(t)
	bt := h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline.Protocols = []string{"bittorrent"} })
	h.publishNow()

	res := h.compile(mixedFleet(weakSniffing))
	if got := ruleByID(t, res.Policy, idOf(bt)).Inbounds; !slices.Equal(got, []protocol.ListenerKey{"lst_1"}) {
		t.Fatalf("bittorrent inbounds = %v, want [lst_1]", got)
	}
	// routeOnly cannot supply domains but supplies the protocol: nothing to restrict.
	res = h.compile(mixedFleet(routeOnlySniffing))
	if got := ruleByID(t, res.Policy, idOf(bt)).Inbounds; len(got) != 0 {
		t.Fatalf("a routeOnly inbound sniffs protocols; inbounds = %v", got)
	}
}

func TestSniffPartial_ASplitPolicyRestrictsBothHalves(t *testing.T) {
	h := newHarness(t)
	doms := h.list("sites", "DOMAIN-SUFFIX,a.example.com")
	bt := h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{doms.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}, Ports: "6881-6889"}
	})
	h.publishNow()
	res := h.compile(mixedFleet(weakSniffing))
	x1, x2 := ruleByID(t, res.Policy, idOf(bt)+"x1"), ruleByID(t, res.Policy, idOf(bt)+"x2")
	want := []protocol.ListenerKey{"lst_1"}
	if !slices.Equal(x1.Inbounds, want) || !slices.Equal(x2.Inbounds, want) || x1.Ports != "6881-6889" || x2.Ports != "6881-6889" {
		t.Fatalf("x1 = %+v x2 = %+v", x1, x2)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	// routeOnly: the domain half goes, the protocol half stays everywhere.
	res = h.compile(mixedFleet(routeOnlySniffing))
	if got := ruleByID(t, res.Policy, idOf(bt)+"x1").Inbounds; !slices.Equal(got, want) {
		t.Fatalf("x1 inbounds = %v", got)
	}
	if got := ruleByID(t, res.Policy, idOf(bt)+"x2").Inbounds; len(got) != 0 {
		t.Fatalf("x2 inbounds = %v, want none (every inbound)", got)
	}
}

func TestSniffPartial_ReportOnlyRulesAreRestrictedAndSaySo(t *testing.T) {
	h := newHarness(t)
	l := h.list("watch", "DOMAIN-SUFFIX,watched.example")
	w := h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	smtpPolicy(h, "smtp")
	h.publishNow()

	res := h.compile(mixedFleet(weakSniffing))
	r := ruleByID(t, res.Policy, idOf(w))
	if r.Action != protocol.RuleObserve || !slices.Equal(r.Inbounds, []protocol.ListenerKey{"lst_1"}) {
		t.Fatalf("observe rule = %+v", r)
	}
	if !hasIssue(res, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if m := h.meta(); m.ReportOnlyShed != shedSniffing {
		t.Fatalf("report_only_not_deployed = %q, want sniffing", m.ReportOnlyShed)
	}
	// Every inbound sniffs: nothing is restricted, nothing is said.
	good := h.compile(base1())
	if got := ruleByID(t, good.Policy, idOf(w)).Inbounds; len(got) != 0 || hasIssue(good, IssueReportOnlyNotDeployed, "") {
		t.Fatalf("rule inbounds %v, issues %+v", got, good.Issues)
	}
	if m := h.meta(); m.ReportOnlyShed != "" {
		t.Fatalf("report_only_not_deployed = %q after the sniffing was fixed", m.ReportOnlyShed)
	}
}

// What the node applies of a restricted policy is the last known good, as for
// any other: this is not a fallback.
func TestSniffPartial_TheRestrictedPolicyBecomesTheLKGWhenApplied(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	res := h.compile(mixedFleet(weakSniffing))
	h.observe(applied(res.Digest))
	m := h.meta()
	if m.AppliedSHA256 != res.Digest {
		t.Fatalf("state = %+v", m)
	}
	body, err := h.repos.Agents.AppliedBody(h.ctx, "agt_9")
	if err != nil || !strings.Contains(string(body), `"lst_1"`) || strings.Contains(string(body), `"lst_2"`) {
		t.Fatalf("body = %q (%v)", body, err)
	}
}

func TestSniffPartial_TheOutputsWithNothingToRestrictAreUnchanged(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	cb, err := h.svc.baseFor(h.ctx, h.state().PublishedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.PolicyDigest(cb.base.assemble(nodeView{panelID: 9, nodeIDs: map[int64]bool{1: true, 2: true}, groupOf: nil}))

	// Fully sufficient, sniffing off (the node injects it), and a disabled
	// inbound that cannot sniff all leave the bytes exactly as assembled.
	dead := node(2, weakSniffing)
	dead.Enabled = false
	for name, f := range map[string]fleet{
		"sufficient": base1(),
		"inject":     {panelID: 9, nodes: []*domain.Node{node(1, ""), node(2, `{"enabled":false}`)}, users: []int64{10}},
		"disabled":   {panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), dead}, users: []int64{10}},
	} {
		if got := h.compile(f).Digest; got != want {
			t.Fatalf("%s: digest %.12s, want the unrestricted %.12s", name, got, want)
		}
	}
	// sing-box sniffs by itself: no restriction there either.
	if got := h.compileEngine(mixedFleet(weakSniffing), "sing-box").Digest; got != want {
		t.Fatalf("sing-box: digest %.12s, want %.12s", got, want)
	}
	// And a node with no policy in force still gets none.
	h2 := newHarness(t)
	if res := h2.compile(mixedFleet(weakSniffing)); res.Policy != nil || res.Digest != "" {
		t.Fatalf("no policy: %+v", res)
	}
}

func TestSniffPartial_TheCompileCacheFollowsTheSniffing(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	full := h.compile(mixedFleet(goodSniffing))
	part := h.compile(mixedFleet(weakSniffing))
	if full.Digest == part.Digest {
		t.Fatal("the sniffing changed and the cached output did not")
	}
	if again := h.compile(mixedFleet(goodSniffing)); again.Digest != full.Digest {
		t.Fatalf("recovery: %.12s, want %.12s", again.Digest, full.Digest)
	}
	if m := h.meta(); m.PrecheckListeners != "" {
		t.Fatalf("the listed inbounds must clear: %+v", m)
	}
}

// The restriction itself, on policies the compiler does not build yet.
func TestRestrictForSniffing_GroupRulesThatNeedSniffingAreDropped(t *testing.T) {
	nodes := mixedFleet(weakSniffing).nodes
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:a.example.com"}},
		{ID: "p2", Action: protocol.RuleBlock, Ports: "25"},
		{ID: "g7x1", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_1"}, Domains: []string{"domain:g.example.com"}},
		{ID: "g7x2", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_1"}, Ports: "443"},
		{ID: "g7", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_1"}, CatchAll: true},
	}}
	out, info := restrictForSniffing(p, nodes, xrayEngine)
	if got := joinIDs(ruleIDs(out)); got != "p1,p2" {
		t.Fatalf("rules = %s, want p1,p2 (a group rule cannot be restricted to inbounds, so the whole allowlist goes)", got)
	}
	if !slices.Equal(out.Rules[0].Inbounds, []protocol.ListenerKey{"lst_1"}) || len(out.Rules[1].Inbounds) != 0 {
		t.Fatalf("rules = %+v", out.Rules)
	}
	if err := protocol.ValidateDestinationPolicy(out); err != nil {
		t.Fatal(err)
	}
	// A group section that needs no sniffer is left alone.
	keep := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "g7x1", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_1"}, Ports: "443"},
		{ID: "g7", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_1"}, CatchAll: true},
	}}
	if out, _ := restrictForSniffing(keep, nodes, xrayEngine); out != keep {
		t.Fatalf("out = %+v", out)
	}
	if !info.changed {
		t.Fatal("changed = false")
	}
	// An inbound a rule already names is kept only where it can sniff.
	q := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "s1", Action: protocol.RuleBlock, SourceCIDRs: []string{"203.0.113.0/24"}, Inbounds: []protocol.ListenerKey{"lst_1", "lst_2"}},
	}}
	if out, info := restrictForSniffing(q, nodes, xrayEngine); out != q || info.changed {
		t.Fatalf("a source rule needs no sniffing: %+v %+v", out, info)
	}
	// Nothing left to run: nil.
	only := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Protocols: []string{"bittorrent"}}}}
	if out, _ := restrictForSniffing(only, []*domain.Node{node(1, weakSniffing)}, xrayEngine); out != nil {
		t.Fatalf("out = %+v, want nil", out)
	}
}

// The check the node runs, per listener, passes on whatever is sent.
func TestRestrictForSniffing_TheResultPassesTheNodesPerListenerCheck(t *testing.T) {
	nodes := []*domain.Node{node(1, goodSniffing), node(2, weakSniffing), node(3, routeOnlySniffing), node(4, "")}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:a.example.com"}},
		{ID: "p2", Action: protocol.RuleBlock, Protocols: []string{"bittorrent"}},
		{ID: "p3", Action: protocol.RuleObserve, Domains: []string{"domain:w.example.com"}, CIDRs: []string{"198.51.100.0/24"}},
	}}
	out, _ := restrictForSniffing(p, nodes, xrayEngine)
	if err := protocol.ValidateDestinationPolicy(out); err != nil {
		t.Fatal(err)
	}
	if bad := perListenerSniffFindings(out, nodes); len(bad) != 0 {
		t.Fatalf("findings on the result: %+v", bad)
	}
	if bad := perListenerSniffFindings(p, nodes); len(bad) == 0 {
		t.Fatal("the unrestricted policy must fail the per-listener check")
	}
	if got := ruleByID(t, out, "p1").Inbounds; !slices.Equal(got, []protocol.ListenerKey{"lst_1", "lst_4"}) {
		t.Fatalf("p1 inbounds = %v", got)
	}
	if got := ruleByID(t, out, "p2").Inbounds; !slices.Equal(got, []protocol.ListenerKey{"lst_1", "lst_3", "lst_4"}) {
		t.Fatalf("p2 inbounds = %v", got)
	}
}

func TestSniffPartial_TestTraceShowsAWeakInboundAsNotApplying(t *testing.T) {
	ah := newAdminHarness(t)
	l := ah.list("l", "DOMAIN-SUFFIX,x.example.com")
	ah.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	ah.admin.Desired = fakeDesired{snap: mixedFleet(weakSniffing).snapshot()}
	for node, want := range map[int64]string{1: "block", 2: "default"} {
		res, err := ah.admin.Test(ah.ctx, TestInput{Target: "www.x.example.com", Port: 443, PanelID: 9, InboundNodeID: node})
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != want {
			t.Fatalf("inbound %d: verdict %q, want %q (steps %+v)", node, res.Verdict, want, res.Steps)
		}
	}
}
