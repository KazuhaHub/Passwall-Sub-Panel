package destpolicy

import (
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// HOW HOSTNAMES MEET THE IP RULES IS A PER-NODE CHOICE THAT IS COMPILED ONLY
// WHERE IT CHANGES THE ANSWER. Policy.Resolve makes the node run its core with a
// different routing strategy, which changes the policy digest and so restarts
// the core once. It is therefore emitted only when the panel asks for on_demand,
// the node declares the capability, some rule has CIDRs or Private, and no
// inbound would substitute the name the router resolves.

const resolveCap = protocol.CapabilityDestinationResolve

func TestEffectiveResolve_Matrix(t *testing.T) {
	cases := []struct {
		panel domain.DestResolve
		caps  []string
		want  protocol.ResolveMode
	}{
		{domain.DestResolveAsIs, nil, protocol.ResolveAsIs},
		{domain.DestResolveAsIs, []string{resolveCap}, protocol.ResolveAsIs},
		{domain.DestResolveOnDemand, nil, protocol.ResolveAsIs},
		{domain.DestResolveOnDemand, []string{policyCap}, protocol.ResolveAsIs},
		{domain.DestResolveOnDemand, []string{resolveCap}, protocol.ResolveOnDemand},
		{domain.DestResolveOnDemand, []string{policyCap, hitsCap, resolveCap}, protocol.ResolveOnDemand},
		// A row with no value is as_is; junk, and the mode that is not offered, are as_is.
		{"", []string{resolveCap}, protocol.ResolveAsIs},
		{"junk", []string{resolveCap}, protocol.ResolveAsIs},
		{domain.DestResolveIfNonMatch, []string{resolveCap}, protocol.ResolveAsIs},
	}
	for _, c := range cases {
		if got := EffectiveResolve(c.panel, c.caps); got != c.want {
			t.Errorf("EffectiveResolve(%q, %v) = %q, want %q", c.panel, c.caps, got, c.want)
		}
	}
}

func TestResolveUnavailableForVersion(t *testing.T) {
	if !resolveUnavailableVersion(domain.DestResolveOnDemand, []string{policyCap}) {
		t.Error("on_demand without the capability is unavailable for its version")
	}
	if resolveUnavailableVersion(domain.DestResolveOnDemand, []string{resolveCap}) ||
		resolveUnavailableVersion(domain.DestResolveAsIs, nil) || resolveUnavailableVersion("", nil) {
		t.Error("only a panel that asks for on_demand and lacks the capability is held back for its version")
	}
}

func TestWithResolve_OnlyWhereSomeRuleHasIPRules(t *testing.T) {
	ip := func(r protocol.DestinationRule) *protocol.DestinationPolicy {
		return &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "25"}, r}}
	}
	for name, p := range map[string]*protocol.DestinationPolicy{
		"cidrs":   ip(protocol.DestinationRule{ID: "p2", Action: protocol.RuleBlock, CIDRs: []string{"10.0.0.0/8"}}),
		"private": ip(protocol.DestinationRule{ID: "p2", Action: protocol.RuleBlock, Private: true}),
		"observe": ip(protocol.DestinationRule{ID: "p2", Action: protocol.RuleObserve, CIDRs: []string{"10.0.0.0/8"}}),
		"allow":   ip(protocol.DestinationRule{ID: "p2", Action: protocol.RuleAllow, CIDRs: []string{"10.0.0.0/8"}}),
	} {
		withResolve(p, protocol.ResolveOnDemand)
		if p.Resolve != protocol.ResolveOnDemand {
			t.Errorf("%s: resolve = %q, want on_demand", name, p.Resolve)
		}
		withResolve(p, protocol.ResolveAsIs)
		if p.Resolve != protocol.ResolveAsIs {
			t.Errorf("%s: resolve = %q after as_is, want it cleared", name, p.Resolve)
		}
	}
	// Ports, domains, protocols and source ranges are not IP rules: nothing to resolve for.
	none := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "s1", Action: protocol.RuleBlock, SourceCIDRs: []string{"203.0.113.0/24"}},
		{ID: "p1", Action: protocol.RuleBlock, Ports: "25"},
		{ID: "p2", Action: protocol.RuleBlock, Domains: []string{"domain:x.example.com"}},
		{ID: "p3", Action: protocol.RuleBlock, Protocols: []string{"bittorrent"}},
	}}
	withResolve(none, protocol.ResolveOnDemand)
	if none.Resolve != protocol.ResolveAsIs {
		t.Errorf("a policy with no CIDRs or Private was sent resolve %q: it would restart the core for nothing", none.Resolve)
	}
	withResolve(nil, protocol.ResolveOnDemand) // must not panic
}

func TestLowerResolve(t *testing.T) {
	for _, c := range []struct{ a, b, want protocol.ResolveMode }{
		{protocol.ResolveAsIs, protocol.ResolveAsIs, protocol.ResolveAsIs},
		{protocol.ResolveAsIs, protocol.ResolveOnDemand, protocol.ResolveAsIs},
		{protocol.ResolveOnDemand, protocol.ResolveAsIs, protocol.ResolveAsIs},
		{protocol.ResolveOnDemand, protocol.ResolveOnDemand, protocol.ResolveOnDemand},
	} {
		if got := lowerResolve(c.a, c.b); got != c.want {
			t.Errorf("lowerResolve(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestResolveFindings_PerListenerNeed(t *testing.T) {
	roNodes := []*domain.Node{node(1, goodSniffing), node(2, routeOnlySniffing), node(3, `{"enabled":true,"destOverride":["fakedns"],"routeOnly":true}`), node(4, `{"enabled":true,"metadataOnly":true}`), node(5, ``)}
	has := func(f []sniffFinding, listener string) bool {
		return slices.ContainsFunc(f, func(x sniffFinding) bool { return x.Listener == listener })
	}
	everywhere := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Private: true}}}
	f := resolveFindings(everywhere, roNodes)
	if len(f) != 1 || !has(f, "lst_2") || f[0].Reason != "route_only_resolve" {
		t.Fatalf("an IP rule that applies everywhere: only the routeOnly inbound that substitutes is a finding, got %+v", f)
	}
	// An IP rule that does not apply to the routeOnly inbound does not need it.
	scoped := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, CIDRs: []string{"10.0.0.0/8"}, Inbounds: []protocol.ListenerKey{"lst_1"}}}}
	if f := resolveFindings(scoped, roNodes); len(f) != 0 {
		t.Fatalf("an IP rule scoped away from the routeOnly inbound produced %+v", f)
	}
	// No IP rule, no need.
	if f := resolveFindings(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "25"}}}, roNodes); len(f) != 0 {
		t.Fatalf("no IP rule produced %+v", f)
	}
	// A disabled inbound is not run.
	off := node(2, routeOnlySniffing)
	off.Enabled = false
	if f := resolveFindings(everywhere, []*domain.Node{off}); len(f) != 0 {
		t.Fatalf("a disabled inbound produced %+v", f)
	}
}

// ---- compile ----

func (h *harness) compileResolve(f fleet, panel domain.DestResolve, engine string, caps ...string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: "agt_9", Capabilities: caps, Snapshot: f.snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: engine}}, DestResolve: panel,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func privatePolicy(h *harness, name string) *domain.DestPolicy {
	return h.policy(name, domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Private: true}
	})
}

func hasResolveIssue(res Result, key, detail string) bool {
	return slices.ContainsFunc(res.Issues, func(i domain.NodeAgentIssue) bool {
		return i.Code == IssueResolveNotDeployed && i.Key == key && i.Detail == detail
	})
}

func TestResolve_OnDemandIsSentToANodeThatCanRunIt(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()

	res := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("resolve = %+v", res.Policy)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	if m := h.meta(); m.ResolveEffective != "on_demand" || m.ResolveUnavailable != "" {
		t.Fatalf("recorded %q / %q, want on_demand / empty", m.ResolveEffective, m.ResolveUnavailable)
	}
	if len(res.Issues) != 0 {
		t.Fatalf("issues = %+v", res.Issues)
	}
}

// A node without the capability would accept the field and run it as AsIs, so
// it is not sent it. The digest is the one AsIs gives.
func TestResolve_NotSentToANodeThatCannotRunIt(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()
	asIs := h.compileResolve(base1(), domain.DestResolveAsIs, "", policyCap)
	old := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap)
	if old.Policy == nil || old.Policy.Resolve != protocol.ResolveAsIs || old.Digest != asIs.Digest {
		t.Fatalf("a node without the capability was sent resolve: %+v", old.Policy)
	}
	if m := h.meta(); m.ResolveEffective != "" || m.ResolveUnavailable != "" {
		t.Fatalf("a missing capability is derived where it is shown, not stored: %q / %q", m.ResolveEffective, m.ResolveUnavailable)
	}
}

func TestResolve_NotSentWhereNoRuleHasIPRules(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "smtp")
	h.publishNow()
	asIs := h.compileResolve(base1(), domain.DestResolveAsIs, "", policyCap, resolveCap)
	on := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if on.Policy == nil || on.Policy.Resolve != protocol.ResolveAsIs || on.Digest != asIs.Digest {
		t.Fatalf("a policy with no IP rules was sent resolve: %+v", on.Policy)
	}
	if m := h.meta(); m.ResolveEffective != "" {
		t.Fatalf("resolve_effective = %q, want empty", m.ResolveEffective)
	}
}

func TestResolve_AReportOnlyIPRuleAlsoNeedsIt(t *testing.T) {
	h := newHarness(t)
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Private: true} })
	h.publishNow()
	res := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("a report-only IP rule must see hostnames the way an enforced one would: %+v", res.Policy)
	}
}

// An inbound with sniffing routeOnly:true that would substitute the name makes
// the whole NODE stay as_is: Xray's domainStrategy is router-wide. The policy
// is still delivered whole, and the node is flagged.
func TestResolve_RouteOnlyInboundDowngradesTheWholeNode(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	smtpPolicy(h, "smtp")
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, routeOnlySniffing)}, users: []int64{10}}

	asIs := h.compileResolve(f, domain.DestResolveAsIs, "", policyCap, resolveCap)
	res := h.compileResolve(f, domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveAsIs || res.Digest != asIs.Digest {
		t.Fatalf("the node was not held at as_is: %+v", res.Policy)
	}
	if len(res.Policy.Rules) != 2 {
		t.Fatalf("the downgrade must not restrict or drop rules: %v", ruleIDs(res.Policy))
	}
	if !hasResolveIssue(res, "lst_2", "route_only") {
		t.Fatalf("issues = %+v, want %s on lst_2", res.Issues, IssueResolveNotDeployed)
	}
	if m := h.meta(); m.ResolveEffective != "" || m.ResolveUnavailable != "route_only" {
		t.Fatalf("recorded %q / %q, want empty / route_only", m.ResolveEffective, m.ResolveUnavailable)
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("a downgrade is not a fallback: %q", m.FallbackReason)
	}

	// The operator fixes the inbound: the node gets on_demand, the flag clears.
	fixed := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, goodSniffing)}, users: []int64{10}}
	res = h.compileResolve(fixed, domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveOnDemand || len(res.Issues) != 0 {
		t.Fatalf("after the fix: %+v %+v", res.Policy, res.Issues)
	}
	if m := h.meta(); m.ResolveEffective != "on_demand" || m.ResolveUnavailable != "" {
		t.Fatalf("recorded %q / %q after the fix", m.ResolveEffective, m.ResolveUnavailable)
	}
}

func TestResolve_RouteOnlyThatCannotSubstituteIsFine(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{
		node(1, `{"enabled":true,"destOverride":["fakedns"],"routeOnly":true}`),
		node(2, `{"enabled":true,"destOverride":["fakedns+others"],"routeOnly":true}`),
		node(3, `{"enabled":true,"routeOnly":true}`),
		node(4, `{"enabled":true,"metadataOnly":true}`),
		node(5, ``),
	}, users: []int64{10}}
	res := h.compileResolve(f, domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveOnDemand || len(res.Issues) != 0 {
		t.Fatalf("%+v %+v", res.Policy, res.Issues)
	}
}

// Under on_demand a routeOnly inbound makes the node as_is, it does not get
// weak-sniffing restriction rules for it.
func TestResolve_RouteOnlyDoesNotRestrictIPRules(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, routeOnlySniffing)}, users: []int64{10}}
	res := h.compileResolve(f, domain.DestResolveOnDemand, "", policyCap, resolveCap)
	for _, r := range res.Policy.Rules {
		if len(r.Inbounds) != 0 {
			t.Fatalf("an IP rule was restricted to inbounds: %+v", r)
		}
	}
}

// sing-box has no sniffer to substitute names: its inbounds' sniffing does not
// matter, and the mode is sent.
func TestResolve_SingBoxIgnoresInboundSniffing(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, routeOnlySniffing)}, users: []int64{10}}
	res := h.compileResolve(f, domain.DestResolveOnDemand, "sing-box", policyCap, resolveCap)
	if res.Policy == nil || res.Policy.Resolve != protocol.ResolveOnDemand || len(res.Issues) != 0 {
		t.Fatalf("%+v %+v", res.Policy, res.Issues)
	}
}

// The mode is part of what a compile depends on: a change must recompile (the
// node has to be told), and no change must not.
func TestResolve_AChangeOfModeIsANewPolicyAndNoChangeIsNot(t *testing.T) {
	h := newHarness(t)
	privatePolicy(h, "lan")
	h.publishNow()
	asIs := h.compileResolve(base1(), domain.DestResolveAsIs, "", policyCap, resolveCap)
	on := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if on.Digest == asIs.Digest {
		t.Fatal("switching to on_demand did not change the digest: the node would never be told")
	}
	h.rec.Lock()
	h.rec.compile = nil
	h.rec.Unlock()
	again := h.compileResolve(base1(), domain.DestResolveOnDemand, "", policyCap, resolveCap)
	if again.Digest != on.Digest || h.rec.count(ResultCacheHit) != 1 {
		t.Fatalf("an unchanged setting recompiled: %v", h.rec.compile)
	}
	back := h.compileResolve(base1(), domain.DestResolveAsIs, "", policyCap, resolveCap)
	if back.Digest != asIs.Digest {
		t.Fatal("switching back did not restore the as_is bytes")
	}
}

// The upgrade story: with as_is everywhere (a panel that never touched the
// setting, a node that does not know it, or a policy without IP rules) the
// compile output and the digest are the bytes of before the feature existed.
func TestResolve_AsIsEverywhereIsByteIdentical(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	sites := h.list("sites", "DOMAIN-SUFFIX,b.example.com\nDOMAIN-SUFFIX,a.example.com")
	ok := h.list("ok", "DOMAIN-SUFFIX,fine.example.com")
	h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{sites.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}, Ports: "6881-6889"}
	})
	h.policy("scoped", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.Scope, p.GroupIDs = domain.DestScopeGroups, []int64{5}
	})
	h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{ok.ID} })
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{sites.ID} })
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, CIDRs: []string{"203.0.113.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	for name, in := range map[string]struct {
		mode domain.DestResolve
		caps []string
	}{
		"default":                   {"", []string{policyCap, groupsCap}},
		"as_is":                     {domain.DestResolveAsIs, []string{policyCap, groupsCap, resolveCap}},
		"on_demand, old node":       {domain.DestResolveOnDemand, []string{policyCap, groupsCap}},
		"on_demand, no IP rules":    {domain.DestResolveOnDemand, []string{policyCap, groupsCap, resolveCap}},
		"on_demand, new node, caps": {domain.DestResolveOnDemand, []string{policyCap, groupsCap, resolveCap, hitsCap}},
	} {
		res := h.compileResolve(base1(), in.mode, "", in.caps...)
		want := goldenDigest
		if name == "on_demand, new node, caps" {
			continue // recording is on there, which is its own change
		}
		if res.Digest != want {
			t.Errorf("%s: digest = %s, want the pre-feature %s", name, res.Digest, want)
		}
	}
}

// ---- the fallback only ever loosens ----

func TestResolve_FallbackNeverRaisesTheMode(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "IP-CIDR,203.0.113.0/24")
	a := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	caps := []string{policyCap, resolveCap}
	first := h.compileResolve(base1(), domain.DestResolveAsIs, "", caps...)
	h.observe(applied(first.Digest), caps...)

	// The panel is switched to on_demand and the next policy is refused: the node
	// falls back to what it ran, as_is, and must not be sent a mode it never applied.
	h.editPolicy(a, "IP-CIDR,198.51.100.0/24")
	next := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	if next.Policy == nil || next.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("setup: %+v", next.Policy)
	}
	h.observe(rejected(next.Digest), caps...)
	fb := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	if fb.Digest != first.Digest || fb.Policy == nil || fb.Policy.Resolve != protocol.ResolveAsIs {
		t.Fatalf("the fallback raised the mode: %+v", fb.Policy)
	}
}

func TestResolve_FallbackLowersToThePanelsChoiceAndKeepsAnApplied_OnDemand(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "IP-CIDR,203.0.113.0/24")
	a := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	caps := []string{policyCap, resolveCap}
	first := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	if first.Policy == nil || first.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("setup: %+v", first.Policy)
	}
	h.observe(applied(first.Digest), caps...)
	h.editPolicy(a, "IP-CIDR,198.51.100.0/24")
	next := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	h.observe(rejected(next.Digest), caps...)

	// Still on_demand: the LKG is sent as it ran.
	fb := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	if fb.Digest != first.Digest || fb.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("an applied on_demand LKG lost its mode: %+v", fb.Policy)
	}
	// The panel goes back to as_is: the fallback follows it down.
	down := h.compileResolve(base1(), domain.DestResolveAsIs, "", caps...)
	if down.Policy == nil || down.Policy.Resolve != protocol.ResolveAsIs {
		t.Fatalf("the fallback did not follow the panel down: %+v", down.Policy)
	}
	if m := h.meta(); m.ResolveEffective != "" {
		t.Fatalf("resolve_effective = %q, want empty", m.ResolveEffective)
	}
}

// ---- retry triggers ----

func TestResolve_CapabilityIsPartOfTheRetryToken(t *testing.T) {
	without := capsToken([]string{policyCap})
	with := capsToken([]string{policyCap, resolveCap})
	if without == with {
		t.Fatalf("gaining or losing the capability must be a reason to try a refused policy again: %q == %q", without, with)
	}
	if len(with) > 8 {
		t.Fatalf("the token %q is wider than the rejected_caps column", with)
	}
}

// An inbound turned routeOnly after the LKG applied holds the fallback at as_is
// too: the node would refuse an on_demand policy for it.
func TestResolve_FallbackIsHeldAtAsIsByARouteOnlyInbound(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "IP-CIDR,203.0.113.0/24")
	a := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	caps := []string{policyCap, resolveCap}
	first := h.compileResolve(base1(), domain.DestResolveOnDemand, "", caps...)
	if first.Policy == nil || first.Policy.Resolve != protocol.ResolveOnDemand {
		t.Fatalf("setup: %+v", first.Policy)
	}
	h.observe(applied(first.Digest), caps...)

	ro := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, routeOnlySniffing)}, users: []int64{10, 11, 12}}
	h.editPolicy(a, "IP-CIDR,198.51.100.0/24")
	next := h.compileResolve(ro, domain.DestResolveOnDemand, "", caps...)
	if next.Policy == nil || next.Policy.Resolve != protocol.ResolveAsIs {
		t.Fatalf("setup: %+v", next.Policy)
	}
	h.observe(rejected(next.Digest), caps...)
	fb := h.compileResolve(ro, domain.DestResolveOnDemand, "", caps...)
	if fb.Policy == nil || fb.Policy.Resolve != protocol.ResolveAsIs || !hasResolveIssue(fb, "lst_2", "route_only") {
		t.Fatalf("the fallback was not held at as_is: %+v %+v", fb.Policy, fb.Issues)
	}
	if m := h.meta(); m.ResolveEffective != "" || m.ResolveUnavailable != "route_only" {
		t.Fatalf("recorded %q / %q", m.ResolveEffective, m.ResolveUnavailable)
	}
}
