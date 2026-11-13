package destpolicy

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func base1() fleet {
	return fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, goodSniffing)}, users: []int64{10, 11, 12}}
}

// WITHOUT A POLICY THE CONFIG IS BYTE-IDENTICAL TO WHAT IT WAS BEFORE THE
// FEATURE EXISTED. A panel upgrade then moves no config ETag and restarts no
// core; that is the whole upgrade story, so it is pinned with the literal
// bytes a v0.2.x panel minted.
func TestCompile_NoPolicyLeavesTheConfigBodyByteIdentical(t *testing.T) {
	h := newHarness(t)
	f := base1()

	body := protocol.ConfigBody{
		Listeners: []protocol.Listener{{Key: "lst_1", Config: protocol.RawConfig(`{"enabled":true}`)}},
		Coverage:  protocol.SegmentCounts{Entries: 1},
		Core:      protocol.CoreSelection{Engine: "xray", Version: "26.9.9"},
	}
	golden, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"listeners":[{"key":"lst_1","config":"eyJlbmFibGVkIjp0cnVlfQ=="}],"coverage":{"entries":1,"entries_stale":0},"core":{"engine":"xray","version":"26.9.9"}}`
	if string(golden) != want {
		t.Fatalf("the golden moved: %s", golden)
	}

	// Never any definition: nil.
	res := h.compile(f)
	if res.Policy != nil || res.Digest != "" {
		t.Fatalf("no definitions produced %+v", res)
	}
	body.Policy = res.Policy
	withPolicyField, _ := json.Marshal(body)
	if string(withPolicyField) != want {
		t.Fatalf("a nil Policy changed the config bytes:\n%s\nwant\n%s", withPolicyField, want)
	}

	// A policy that is off, or whose lists compile to nothing: still nil.
	l := h.list("empty", "")
	h.policy("off", domain.DestActionBlock, domain.DestStateOff, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	if res := h.compile(f); res.Policy != nil {
		t.Fatalf("an off policy produced %+v", res.Policy)
	}
}

func TestCompile_AnAgentWithoutTheCapabilityGetsNoPolicy(t *testing.T) {
	h := newHarness(t)
	l := h.list("bt", "DOMAIN-SUFFIX,tracker.example.com")
	h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()

	if res := h.compile(base1(), "task.execution.v1"); res.Policy != nil {
		t.Fatalf("an agent without policy.destination.v1 was sent a policy: %+v", res.Policy)
	}
	if res := h.compile(base1()); res.Policy == nil {
		t.Fatal("the same agent with the capability must get it")
	}
}

func TestCompile_PhaseOrderIDsAndSplit(t *testing.T) {
	h := newHarness(t)
	h.mmdb(map[string]string{"1.0.0.0/24": "CN", "1.0.1.0/24": "CN", "8.8.8.0/24": "US"})
	doms := h.list("sites", "DOMAIN-SUFFIX,b.example.com\nDOMAIN-SUFFIX,a.example.com\nDOMAIN-SUFFIX,a.example.com")
	allowL := h.list("ok", "DOMAIN-SUFFIX,fine.example.com")

	// Created in an order that is not the emission order.
	blockBT := h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{doms.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}, Ports: "6881-6889"}
	})
	blockSMTP := h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25,465,587"}
	})
	allow := h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{allowL.ID} })
	h.policy("off", domain.DestActionBlock, domain.DestStateOff, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "22"} })
	for _, n := range []int64{1, 2} {
		if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: n, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.publishNow()

	res := h.compile(base1())
	if res.Policy == nil {
		t.Fatal("no policy")
	}
	want := []string{"s1", "p" + itoa(allow.ID), "p" + itoa(blockBT.ID) + "x1", "p" + itoa(blockBT.ID) + "x2", "p" + itoa(blockSMTP.ID)}
	if got := ruleIDs(res.Policy); joinIDs(got) != joinIDs(want) {
		t.Fatalf("rule ids = %v, want %v", got, want)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatalf("the output does not validate: %v", err)
	}
	if res.Digest != protocol.PolicyDigest(res.Policy) || res.Digest == "" {
		t.Fatalf("digest = %q", res.Digest)
	}

	source := res.Policy.Rules[0]
	if source.Action != protocol.RuleBlock || !slices.Equal(source.Inbounds, []protocol.ListenerKey{"lst_1", "lst_2"}) ||
		!slices.Equal(source.SourceCIDRs, []string{"1.0.0.0/23"}) {
		t.Fatalf("source rule = %+v (both inbounds share one rule, the two /24s merge)", source)
	}
	x1, x2 := res.Policy.Rules[2], res.Policy.Rules[3]
	if !slices.Equal(x1.Domains, []string{"domain:a.example.com", "domain:b.example.com"}) || x1.Ports != "6881-6889" || len(x1.Protocols) != 0 {
		t.Fatalf("x1 = %+v: the domain part, sorted and without repeats, with the ports", x1)
	}
	if !slices.Equal(x2.Protocols, []string{"bittorrent"}) || len(x2.Domains) != 0 || x2.Ports != "6881-6889" {
		t.Fatalf("x2 = %+v: the protocol part carries the ports too", x2)
	}
	if last := res.Policy.Rules[4]; last.Ports != "25,465,587" || len(last.Domains) != 0 {
		t.Fatalf("ports-only rule = %+v", last)
	}
	if res.Policy.Collect != "" || res.Policy.Exempt != nil {
		t.Fatalf("collect/exempt = %q/%v, want none", res.Policy.Collect, res.Policy.Exempt)
	}
	// Same input, same bytes.
	h2 := h.compile(base1())
	if h2.Digest != res.Digest {
		t.Fatal("compiling twice gave two digests")
	}
}

func TestCompile_SourceRulesGroupByIdenticalSetAndKeepTheirOwnInbounds(t *testing.T) {
	h := newHarness(t)
	h.mmdb(map[string]string{"1.0.0.0/24": "CN", "8.8.8.0/24": "US"})
	up := func(n int64, countries []string, cidrs []string) {
		if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: n, Countries: countries, CIDRs: cidrs, State: domain.DestStateOn}, nil); err != nil {
			t.Fatal(err)
		}
	}
	up(2, []string{"CN"}, nil)
	up(3, []string{"CN"}, nil)
	up(4, []string{"US"}, []string{"203.0.113.0/24"})
	up(5, nil, nil) // nothing to deny: not emitted
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 6, Countries: []string{"CN"}, State: domain.DestStateOff}, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()

	f := fleet{panelID: 9, nodes: []*domain.Node{node(3, goodSniffing), node(4, goodSniffing), node(5, goodSniffing), node(6, goodSniffing)}, users: []int64{10}}
	res := h.compile(f)
	got := ruleIDs(res.Policy)
	// Node 2 is not on this panel: the CN group's lowest node HERE is 3.
	if joinIDs(got) != "s3,s4" {
		t.Fatalf("rules = %v, want s3,s4", got)
	}
	if !slices.Equal(res.Policy.Rules[0].Inbounds, []protocol.ListenerKey{"lst_3"}) {
		t.Fatalf("the CN rule names the inbounds of THIS node only: %+v", res.Policy.Rules[0].Inbounds)
	}
	if !slices.Equal(res.Policy.Rules[1].SourceCIDRs, []string{"203.0.113.0/24", "8.8.8.0/24"}) {
		t.Fatalf("US rule = %v (country plus its own ranges, bytewise sorted)", res.Policy.Rules[1].SourceCIDRs)
	}
}

// A source rule whose country cannot be expanded is skipped and said so; it is
// never emitted empty and never treated as compiled.
func TestCompile_MissingGeoIPSkipsTheCountryAndSaysSo(t *testing.T) {
	h := newHarness(t) // no database
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 2, Countries: []string{"CN"}, CIDRs: []string{"203.0.113.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	res := h.compile(base1())
	if got := ruleIDs(res.Policy); joinIDs(got) != "s2" {
		t.Fatalf("rules = %v: node 1 has nothing left to deny, node 2 keeps its own range", got)
	}
	info, err := h.svc.Info(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.GeoIPUnavailable || info.SourceNotes[1] != "geoip_unavailable" {
		t.Fatalf("info = %+v: the missing database must be surfaced", info)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

func TestCompile_ListsThatAreNotReadySkipThatPartOfTheRule(t *testing.T) {
	h := newHarness(t)
	ready := h.list("ready", "DOMAIN-SUFFIX,ready.example.com")
	empty := h.list("emptylist", "# nothing here")
	geo := h.list("geo", "GEOSITE,category-ads-all")
	policy := h.policy("mix", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{ready.ID, empty.ID, geo.ID}
	})
	// Every list unusable: the rule is left empty and skipped. The ports that
	// narrow it must NOT stand in for it.
	dead := h.policy("dead", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{empty.ID, geo.ID}
		p.Inline = domain.DestPolicyInline{Ports: "25"}
	})
	// Dead lists, but the policy also has a protocol part: that part stays.
	proto := h.policy("proto", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{geo.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}}
	})
	h.publishNow()

	res := h.compile(base1())
	if got := joinIDs(ruleIDs(res.Policy)); got != "p"+itoa(policy.ID)+",p"+itoa(proto.ID) {
		t.Fatalf("rules = %s: the dead policy's ports must not widen it into a rule of its own", got)
	}
	if !slices.Equal(res.Policy.Rules[0].Domains, []string{"domain:ready.example.com"}) {
		t.Fatalf("domains = %v", res.Policy.Rules[0].Domains)
	}
	info, _ := h.svc.Info(h.ctx)
	if n := info.Policies[policy.ID]; n == nil || n.State != "lists_not_ready" || !slices.Equal(n.NotReady, []int64{geo.ID}) || !slices.Equal(n.Empty, []int64{empty.ID}) {
		t.Fatalf("note = %+v", n)
	}
	if n := info.Policies[dead.ID]; n == nil || n.State != "empty" {
		t.Fatalf("dead policy note = %+v, want empty", n)
	}
}

func TestCompile_ScopeAllGroupsAndPanelsWithRosterFilteringAndExemptions(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	for _, c := range []struct {
		name  string
		scope domain.DestScope
		g, p  []int64
	}{
		{"everyone", domain.DestScopeAll, nil, nil},
		{"groups", domain.DestScopeGroups, []int64{5}, nil},
		{"elsewhere", domain.DestScopePanels, nil, []int64{77}},
		{"here", domain.DestScopePanels, nil, []int64{9, 10}},
	} {
		h.policy(c.name, domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
			p.ListIDs, p.Scope, p.GroupIDs, p.PanelIDs = []int64{l.ID}, c.scope, c.g, c.p
		})
	}
	// Users 10 and 12 are in group 5; user 11 is in group 6.
	h.setGroups(map[int64]int64{10: 5, 11: 6, 12: 5, 99: 5})
	for _, u := range []int64{10, 99} { // 99 has no client on this panel
		if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: u}); err != nil {
			t.Fatal(err)
		}
	}
	h.publishNow()

	res := h.compile(base1())
	if len(res.Policy.Rules) != 3 {
		t.Fatalf("rules = %v: all + groups + the panel-scoped one that lists this panel", ruleIDs(res.Policy))
	}
	byName := map[string]protocol.DestinationRule{}
	for i, name := range []string{"everyone", "groups", "here"} {
		byName[name] = res.Policy.Rules[i]
	}
	if len(byName["everyone"].Subjects) != 0 || len(byName["here"].Subjects) != 0 {
		t.Fatal("scope all and scope panels name no subjects")
	}
	if !slices.Equal(byName["groups"].Subjects, []protocol.SubjectKey{"usr_10", "usr_12"}) {
		t.Fatalf("group subjects = %v: the roster members of group 5, nobody else", byName["groups"].Subjects)
	}
	if !slices.Equal(res.Policy.Exempt, []protocol.SubjectKey{"usr_10"}) {
		t.Fatalf("exempt = %v: usr_99 has no client here and must not be in this node's config", res.Policy.Exempt)
	}

	// An exemption for somebody who is not on this panel changes nothing here.
	before := res.Digest
	if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: 4242}); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	if after := h.compile(base1()); after.Digest != before {
		t.Fatal("an exemption for an account that is not on this panel changed this panel's config: every core in the fleet would restart")
	}

	// A group nobody on the roster is in drops the rule.
	only11 := base1()
	only11.users = []int64{11}
	if n := len(h.compile(only11).Policy.Rules); n != 2 {
		t.Fatalf("rules = %d: with nobody of group 5 on the roster the group rule must drop, leaving 2", n)
	}
}

func TestCompile_ExpiredExemptionsAreNotPublished(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	past := h.clock.Now().Add(-time.Hour)
	future := h.clock.Now().Add(time.Hour)
	for u, exp := range map[int64]*time.Time{10: &past, 11: &future, 12: nil} {
		if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: u, ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	h.publishNow()
	res := h.compile(base1())
	if !slices.Equal(res.Policy.Exempt, []protocol.SubjectKey{"usr_11", "usr_12"}) {
		t.Fatalf("exempt = %v", res.Policy.Exempt)
	}
}

func TestCompile_SniffingPrecheck(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()

	// Sniffing off is fine: the node turns it on.
	off := fleet{panelID: 9, nodes: []*domain.Node{node(1, ""), node(2, `{"enabled":false}`)}, users: []int64{10}}
	if res := h.compile(off); res.Policy == nil || len(res.Issues) != 0 {
		t.Fatalf("sniffing off must be accepted (the node injects it): %+v", res)
	}

	// routeOnly with domains rules is insufficient; the node is held at no
	// policy (there is no last-known-good) and the operator is told which
	// inbound.
	bad := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`)}, users: []int64{10}}
	res := h.compile(bad)
	if res.Policy != nil {
		t.Fatalf("a policy was sent to a node whose inbound cannot sniff domains: %+v", res.Policy)
	}
	if len(res.Issues) != 1 || res.Issues[0].Code != IssueSniffInsufficient || res.Issues[0].Key != "lst_2" || res.Issues[0].Detail != "route_only" {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if h.rec.count(ResultFallbackNil) != 1 {
		t.Fatalf("results = %v", h.rec.compile)
	}
	meta, _ := h.repos.Agents.Get(h.ctx, "agt_9")
	if meta.FallbackReason != domain.DestFallbackSniffing || !strings.Contains(meta.PrecheckListeners, `"lst_2"`) {
		t.Fatalf("delivery state = %+v", meta)
	}

	// The operator fixes the inbound; the next sync recovers by itself.
	if res := h.compile(base1()); res.Policy == nil || len(res.Issues) != 0 {
		t.Fatalf("recovery: %+v", res)
	}
	meta, _ = h.repos.Agents.Get(h.ctx, "agt_9")
	if meta.FallbackReason != domain.DestFallbackNone || meta.PrecheckListeners != "" {
		t.Fatalf("the fallback must clear: %+v", meta)
	}

	// sing-box takes its sniff rule from the route: no precheck.
	if res := h.compileEngine(bad, "sing-box"); res.Policy == nil {
		t.Fatal("sing-box is not prechecked")
	}
	// A policy with no domain or protocol rules needs no sniffing at all.
	h2 := newHarness(t)
	h2.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h2.publishNow()
	if res := h2.compile(bad); res.Policy == nil {
		t.Fatal("a ports-only policy needs no sniffing")
	}
	// Malformed sniffing is the operator's problem, reported as one.
	h3 := newHarness(t)
	l3 := h3.list("l", "DOMAIN-SUFFIX,x.example.com")
	h3.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l3.ID} })
	h3.publishNow()
	malformed := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":`)}, users: []int64{10}}
	if res := h3.compile(malformed); res.Policy != nil || len(res.Issues) != 1 || res.Issues[0].Detail != "malformed" {
		t.Fatalf("malformed sniffing: %+v", res)
	}
}

func TestCompile_AnotherInboundsDisabledSniffingDoesNotHoldTheNodeBack(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	dead := node(2, `{"enabled":true,"destOverride":["http"]}`)
	dead.Enabled = false
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), dead}, users: []int64{10}}
	if res := h.compile(f); res.Policy == nil {
		t.Fatal("a disabled inbound runs nothing and must not hold the node's policy back")
	}
}

// Over a per-node limit the policy is never truncated: truncating a block list
// is a silent allow.
func TestCompile_OverLimitFallsBackAndNeverTruncates(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("grp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs, p.Scope, p.GroupIDs = []int64{l.ID}, domain.DestScopeGroups, []int64{5}
	})
	h.publishNow()
	h.defs.Definitions = bigGroup{Definitions: h.repos, group: 5}

	users := make([]int64, 0, protocol.MaxPolicySubjects+1)
	for i := int64(1); i <= protocol.MaxPolicySubjects+1; i++ {
		users = append(users, i)
	}
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: users}
	res := h.compile(f)
	if res.Policy != nil {
		t.Fatalf("an over-limit policy was sent: %d rules", len(res.Policy.Rules))
	}
	if len(res.Issues) != 1 || res.Issues[0].Code != IssueOverLimit || !strings.HasPrefix(res.Issues[0].Detail, "subjects over limit") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	meta, _ := h.repos.Agents.Get(h.ctx, "agt_9")
	if meta.FallbackReason != domain.DestFallbackOverLimit || !strings.Contains(meta.OverLimit, `"subjects"`) {
		t.Fatalf("state = %+v", meta)
	}

	// Back inside the limit: recovers.
	f.users = users[:100]
	if res := h.compile(f); res.Policy == nil {
		t.Fatal("did not recover")
	}
}

// bigGroup puts every user in one group without 50 000 rows in the users table.
type bigGroup struct {
	Definitions
	group int64
}

func (b bigGroup) GroupIDsByUserIDs(_ context.Context, ids []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(ids))
	for _, id := range ids {
		out[id] = b.group
	}
	return out, nil
}

func TestCompile_CacheHitReadsNothingButTheStateRow(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("grp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs, p.Scope, p.GroupIDs = []int64{l.ID}, domain.DestScopeGroups, []int64{5}
	})
	h.setGroups(map[int64]int64{10: 5})
	h.publishNow()

	first := h.compile(base1())
	h.counts.Lock()
	h.counts.reads = reads{}
	h.counts.Unlock()
	h.rec.Lock()
	h.rec.compile = nil
	h.rec.Unlock()

	second := h.compile(base1())
	if second.Digest != first.Digest {
		t.Fatal("digest moved")
	}
	h.counts.Lock()
	defer h.counts.Unlock()
	if h.counts.snapshotReads != 0 || h.counts.metaReads != 0 || h.counts.bodyReads != 0 || h.counts.patches != 0 || h.counts.defsReads != 0 {
		t.Fatalf("a cache hit touched the stores: %+v", h.counts.reads)
	}
	if h.counts.stateReads != 1 {
		t.Fatalf("state reads = %d, want the single row read", h.counts.stateReads)
	}
	if h.rec.count(ResultCacheHit) != 1 {
		t.Fatalf("results = %v", h.rec.compile)
	}
}

func TestCompile_RosterChangeIsNotDebouncedAndRecompiles(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("grp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs, p.Scope, p.GroupIDs = []int64{l.ID}, domain.DestScopeGroups, []int64{5}
	})
	h.setGroups(map[int64]int64{10: 5, 11: 5})
	h.publishNow()
	f := base1()
	f.users = []int64{10}
	a := h.compile(f)
	f.users = []int64{10, 11}
	b := h.compile(f)
	if a.Digest == b.Digest || !slices.Equal(b.Policy.Rules[0].Subjects, []protocol.SubjectKey{"usr_10", "usr_11"}) {
		t.Fatalf("a new member did not reach the next compile: %v", b.Policy.Rules[0].Subjects)
	}
}

func TestCompile_InvalidPolicyFallsBackAndIsCounted(t *testing.T) {
	h := newHarness(t)
	// Ports that do not validate: the pre-publish check would normally
	// refuse them, so make it pass by publishing a good one first and then
	// forcing a bad snapshot through the cache.
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	cb, err := h.svc.baseFor(h.ctx, h.generation())
	if err != nil {
		t.Fatal(err)
	}
	cb.base.block[0].ports = "0"
	res := h.compile(base1())
	if res.Policy != nil || h.rec.count(ResultInvalid) != 1 {
		t.Fatalf("an invalid per-node policy must not be sent: %+v results=%v", res.Policy, h.rec.compile)
	}
	meta, _ := h.repos.Agents.Get(h.ctx, "agt_9")
	if meta.FallbackReason != domain.DestFallbackRejected {
		t.Fatalf("state = %+v", meta)
	}
}

func (h *harness) generation() int64 {
	h.t.Helper()
	st, err := h.repos.State.Get(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return st.PublishedGeneration
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// groupMap answers the roster's groups from a map, so a test does not need the
// users table.
type groupMap struct {
	Definitions
	m map[int64]int64
}

func (g groupMap) GroupIDsByUserIDs(_ context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for _, id := range ids {
		if grp, ok := g.m[id]; ok {
			out[id] = grp
		}
	}
	return out, nil
}

func (h *harness) setGroups(m map[int64]int64) {
	h.defs.Definitions = groupMap{Definitions: h.repos, m: m}
}
