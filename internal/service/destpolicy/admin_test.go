package destpolicy

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakePanels struct{ panels map[int64]*domain.XUIPanel }

func (f fakePanels) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	if p, ok := f.panels[id]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

func (f fakePanels) List(context.Context) ([]*domain.XUIPanel, error) {
	var out []*domain.XUIPanel
	for _, p := range f.panels {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *domain.XUIPanel) int { return int(a.ID - b.ID) })
	return out, nil
}

type fakeNodes struct{ nodes map[int64]*domain.Node }

func (f fakeNodes) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if n, ok := f.nodes[id]; ok {
		return n, nil
	}
	return nil, domain.ErrNotFound
}

type fakeAgents struct{ agents []*domain.NodeAgent }

func (f fakeAgents) List(context.Context) ([]*domain.NodeAgent, error) { return f.agents, nil }

type fakeDesired struct{ snap *ports.NativeDesiredSnapshot }

func (f fakeDesired) Load(context.Context, int64) (*ports.NativeDesiredSnapshot, error) {
	return f.snap, nil
}

type fakeClients struct{ byUser map[int64][]*domain.PSPClient }

func (f fakeClients) ListByUser(_ context.Context, id int64) ([]*domain.PSPClient, error) {
	return f.byUser[id], nil
}

type adminHarness struct {
	*harness
	admin  *Admin
	panels fakePanels
	nodes  fakeNodes
}

func newAdminHarness(t *testing.T) *adminHarness {
	t.Helper()
	h := newHarness(t)
	ah := &adminHarness{harness: h}
	ah.panels = fakePanels{panels: map[int64]*domain.XUIPanel{
		9:  {ID: 9, Kind: domain.PanelKindPSP, Name: "native-9"},
		10: {ID: 10, Kind: domain.PanelKind3XUI, Name: "xui-10"},
	}}
	ah.nodes = fakeNodes{nodes: map[int64]*domain.Node{
		1: node(1, goodSniffing), 2: node(2, goodSniffing),
		3: {ID: 3, PanelID: 10, DisplayName: "on-xui", Enabled: true},
	}}
	agents := fakeAgents{agents: []*domain.NodeAgent{{
		AgentID: "agt_9", PanelID: 9, ObservedCapabilities: []string{policyCap}, ObservedCoreEngine: domain.NodeCoreXray,
	}}}
	now := h.clock.Now()
	agents.agents[0].LastSeen = &now
	admin, err := NewAdmin(AdminDeps{
		Service: h.svc, Policies: h.repos.Policies, Lists: h.repos.Lists, Counter: h.repos, Panels: ah.panels, Nodes: ah.nodes,
		Agents: agents, Desired: fakeDesired{snap: fleet{nodes: []*domain.Node{ah.nodes.nodes[1], ah.nodes.nodes[2]}, users: []int64{10, 11}}.snapshot()},
		Clients:     fakeClients{byUser: map[int64][]*domain.PSPClient{10: {{ID: 1, UserID: 10, PanelID: 9}}}},
		Settings:    h.settings,
		Instantiate: h.repos.InstantiateTemplate, ExceptionEditor: h.lists, Now: h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ah.admin = admin
	return ah
}

func validationCode(t *testing.T, err error) *ValidationError {
	t.Helper()
	var v *ValidationError
	if !errors.As(err, &v) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	return v
}

func TestPreparePolicy(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("l", "a.example.com")
	src, _, _ := ah.lists.CreateCustom(ctx, "src", domain.DestSideSource, []byte("SRC-GEOIP,CN"))

	good := PolicyInput{Name: "p", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll, ListIDs: []int64{l.ID, l.ID}}
	p, err := ah.admin.PreparePolicy(ctx, good, nil)
	if err != nil || !slices.Equal(p.ListIDs, []int64{l.ID}) {
		t.Fatalf("good policy = (%+v, %v)", p, err)
	}

	for _, c := range []struct {
		name string
		mut  func(*PolicyInput)
		code string
	}{
		{"blank name", func(i *PolicyInput) { i.Name = " x" }, CodePolicyInvalid},
		{"unknown action", func(i *PolicyInput) { i.Action = "observe" }, CodePolicyInvalid},
		{"report state", func(i *PolicyInput) { i.State = domain.DestStateReport }, CodeStateUnavailable},
		{"unknown state", func(i *PolicyInput) { i.State = "maybe" }, CodePolicyInvalid},
		{"no match", func(i *PolicyInput) { i.ListIDs = nil }, CodePolicyNoMatch},
		{"missing list", func(i *PolicyInput) { i.ListIDs = []int64{999} }, CodePolicyInvalid},
		{"source list", func(i *PolicyInput) { i.ListIDs = []int64{src.ID} }, CodePolicyInvalid},
		{"bad cidr", func(i *PolicyInput) { i.Inline.CIDRs = []string{"nope"} }, CodePolicyInvalid},
		{"bad ports", func(i *PolicyInput) { i.Inline.Ports = "70000" }, CodePolicyInvalid},
		{"overlapping ports", func(i *PolicyInput) { i.Inline.Ports = "80-90,85" }, CodePolicyInvalid},
		{"bad network", func(i *PolicyInput) { i.Inline.Network = "icmp" }, CodePolicyInvalid},
		{"bad protocol", func(i *PolicyInput) { i.Inline.Protocols = []string{"quic"} }, CodePolicyInvalid},
		{"allow with protocol", func(i *PolicyInput) { i.Action = domain.DestActionAllow; i.Inline.Protocols = []string{"bittorrent"} }, CodePolicyInvalid},
		{"groups without groups", func(i *PolicyInput) { i.Scope = domain.DestScopeGroups }, CodePolicyInvalid},
		{"panel that is not native", func(i *PolicyInput) { i.Scope, i.PanelIDs = domain.DestScopePanels, []int64{10} }, CodePolicyInvalid},
		{"missing panel", func(i *PolicyInput) { i.Scope, i.PanelIDs = domain.DestScopePanels, []int64{77} }, CodePolicyInvalid},
		{"template key on create", func(i *PolicyInput) { i.TemplateKey = "bittorrent" }, CodePolicyInvalid},
	} {
		in := good
		in.ListIDs = []int64{l.ID}
		c.mut(&in)
		if _, err := ah.admin.PreparePolicy(ctx, in, nil); err == nil || validationCode(t, err).Code != c.code {
			t.Errorf("%s: err = %v, want code %s", c.name, err, c.code)
		}
	}
}

func TestPreparePolicy_NormalisesTheBuiltInMatch(t *testing.T) {
	ah := newAdminHarness(t)
	p, err := ah.admin.PreparePolicy(ah.ctx, PolicyInput{
		Name: "p", Action: domain.DestActionBlock, State: domain.DestStateOff, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{
			CIDRs: []string{"10.1.2.3/8", "10.0.0.0/8", "192.168.0.0/16"}, Ports: "587, 25,465,80-80", Network: "tcp",
			Protocols: []string{"bittorrent", "bittorrent"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Inline.CIDRs, []string{"10.0.0.0/8", "192.168.0.0/16"}) || p.Inline.Ports != "25,80,465,587" || !slices.Equal(p.Inline.Protocols, []string{"bittorrent"}) {
		t.Fatalf("inline = %+v: masked, one spelling each, ports ascending (a range of one is the port)", p.Inline)
	}
}

func TestPreparePolicy_EditKeepsIdentityAndTemplateKey(t *testing.T) {
	ah := newAdminHarness(t)
	existing := ah.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	existing.TemplateKey = "smtp"
	p, err := ah.admin.PreparePolicy(ah.ctx, PolicyInput{Name: "renamed", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll, Inline: domain.DestPolicyInline{Ports: "25"}, TemplateKey: "other"}, existing)
	if err != nil || p.ID != existing.ID || p.Priority != existing.Priority || p.TemplateKey != "smtp" {
		t.Fatalf("edit = (%+v, %v)", p, err)
	}
}

// A catalog rule group keeps its mapping: name, description, state and scope
// are editable, what it matches is not.
func TestPreparePolicy_ACatalogGroupCannotChangeWhatItMatches(t *testing.T) {
	ah := newAdminHarness(t)
	made, err := ah.repos.InstantiateTemplate(ah.ctx, nil, &domain.DestPolicy{
		Name: "SMTP", Action: domain.DestActionBlock, State: domain.DestStateOff, Scope: domain.DestScopeAll, TemplateKey: "smtp",
		Inline: domain.DestPolicyInline{Ports: "25,465,587"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := PolicyInput{Name: "Mail ports", Description: "edited", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{Ports: "25,465,587"}}
	if p, err := ah.admin.PreparePolicy(ah.ctx, ok, made); err != nil || p.TemplateKey != "smtp" || p.State != domain.DestStateOn {
		t.Fatalf("a rename and a state change must be allowed: (%+v, %v)", p, err)
	}
	for name, mut := range map[string]func(*PolicyInput){
		"ports":  func(i *PolicyInput) { i.Inline.Ports = "25" },
		"action": func(i *PolicyInput) { i.Action = domain.DestActionAllow },
		"cidrs":  func(i *PolicyInput) { i.Inline.CIDRs = []string{"10.0.0.0/8"} },
	} {
		in := ok
		mut(&in)
		if _, err := ah.admin.PreparePolicy(ah.ctx, in, made); err == nil || validationCode(t, err).Field != "template_key" {
			t.Errorf("changing the %s of a catalog group was accepted: %v", name, err)
		}
	}
}

func TestBudgetAndOverLimit(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("l", "DOMAIN-SUFFIX,a.example.com\nregexp:^x[0-9]+\\.example\\.org$")
	ah.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	if err := ah.repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 5}); err != nil {
		t.Fatal(err)
	}
	b, err := ah.admin.Budget(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rules != (BudgetItem{1, 256}) || b.Domains != (BudgetItem{2, 50000}) || b.Regexps != (BudgetItem{1, 256}) || b.Subjects != (BudgetItem{1, 50000}) || b.Bytes.Used == 0 {
		t.Fatalf("budget = %+v", b)
	}

	// A hypothetical policy is measured without being saved.
	hypo := &domain.DestPolicy{Name: "h", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll, Inline: domain.DestPolicyInline{Ports: "25"}}
	hb, err := ah.admin.Budget(ctx, hypo)
	if err != nil || hb.Rules.Used != 2 {
		t.Fatalf("hypothetical budget = (%+v, %v)", hb, err)
	}
	if all, _ := ah.repos.Policies.List(ctx); len(all) != 1 {
		t.Fatal("measuring saved the hypothetical policy")
	}
	if err := ah.admin.CheckBudget(ctx, hypo); err != nil {
		t.Fatal(err)
	}

	// 256 rules exist; one more is over the limit.
	for i := 0; i < 255; i++ {
		ah.policy("q"+itoa(int64(i)), domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	}
	err = ah.admin.CheckBudget(ctx, hypo)
	v := validationCode(t, err)
	if v.Code != CodePolicyOverLimit || v.Kind != "rules" || v.Used != 257 || v.Limit != 256 {
		t.Fatalf("over-limit = %+v", v)
	}
	// Replacing an existing policy (same id) does not count twice.
	existing, _ := ah.repos.Policies.List(ctx)
	same := *existing[0]
	if err := ah.admin.CheckBudget(ctx, &same); err != nil {
		t.Fatalf("an edit of an existing policy counted as an addition: %v", err)
	}
}

func TestSourceRuleOnlyForNativeNodes(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	r, err := ah.admin.PrepareSourceRule(ctx, 1, SourceRuleInput{Countries: []string{"cn", "CN", "us"}, CIDRs: []string{"203.0.113.9/24", "203.0.113.0/24"}, State: domain.DestStateOn})
	if err != nil || !slices.Equal(r.Countries, []string{"CN", "US"}) || !slices.Equal(r.CIDRs, []string{"203.0.113.0/24"}) {
		t.Fatalf("rule = (%+v, %v)", r, err)
	}
	if _, err := ah.admin.PrepareSourceRule(ctx, 3, SourceRuleInput{State: domain.DestStateOn}); !errors.Is(err, ErrSourceRuleNotNative) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a node on a 3X-UI panel: %v", err)
	}
	if _, err := ah.admin.PrepareSourceRule(ctx, 999, SourceRuleInput{State: domain.DestStateOn}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing node: %v", err)
	}
	for _, in := range []SourceRuleInput{
		{Countries: []string{"CHN"}, State: domain.DestStateOn},
		{CIDRs: []string{"x"}, State: domain.DestStateOn},
		{State: domain.DestStateReport},
	} {
		if _, err := ah.admin.PrepareSourceRule(ctx, 1, in); err == nil || validationCode(t, err) == nil {
			t.Fatalf("%+v was accepted", in)
		}
	}
}

func TestValidateExemption(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	if err := ValidateExemption("ok", &past, now); validationCode(t, err).Code != CodeExemptionInvalid {
		t.Fatalf("past expiry: %v", err)
	}
	if err := ValidateExemption("ok", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExemption(string(make([]byte, 300)), nil, now); err == nil {
		t.Fatal("an over-long reason was accepted")
	}
}

func TestTest_TraceOfAPublishedPolicy(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	ah.mmdb(map[string]string{"1.0.0.0/24": "CN"})
	l := ah.list("sites", "DOMAIN-SUFFIX,blocked.example.com")
	pol := ah.policy("Sites", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{l.ID}
		p.Inline = domain.DestPolicyInline{CIDRs: []string{"198.51.100.0/24"}}
	})
	if err := ah.repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ah.repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 11}); err != nil {
		t.Fatal(err)
	}
	ah.publishNow()

	res, err := ah.admin.Test(ctx, TestInput{Target: "www.blocked.example.com", Port: 443, Network: "tcp", PanelID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "block" || res.TerminatingStep != "p"+itoa(pol.ID) || res.Unpublished {
		t.Fatalf("result = %+v", res)
	}
	var hit *TestStep
	for i := range res.Steps {
		if res.Steps[i].Result == "hit" {
			hit = &res.Steps[i]
		}
	}
	if hit == nil || hit.Name != "Sites" || hit.PolicyID != pol.ID || hit.Kind != "block" || hit.Entry != "domain:blocked.example.com" {
		t.Fatalf("hit = %+v (steps %+v)", hit, res.Steps)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Name != "native-9" {
		t.Fatalf("nodes = %+v", res.Nodes)
	}

	// An exempt user is let through, ahead of the blocks.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "www.blocked.example.com", UserID: 11, PanelID: 9})
	if res.Verdict != "allow" || res.TerminatingStep != "exempt" {
		t.Fatalf("exempt result = %+v", res)
	}
	// The user's own panel is found from their clients when none is given.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "www.blocked.example.com", UserID: 10})
	if res.Verdict != "block" || len(res.Nodes) != 1 || res.Nodes[0].PanelID != 9 {
		t.Fatalf("derived panel result = %+v", res)
	}

	// A hostname is never matched against the address rules: the note says so.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "unlisted.example.net", PanelID: 9})
	if res.Verdict != "default" || !slices.Contains(res.Notes, string(protocol.NoteHostnameSkipsIPRules)) {
		t.Fatalf("hostname result = %+v", res)
	}
	res, _ = ah.admin.Test(ctx, TestInput{Target: "198.51.100.7", PanelID: 9})
	if res.Verdict != "block" {
		t.Fatalf("an address in the destination range: %+v", res)
	}

	// The source rule: the inbound and the client address decide it, and a
	// step the input leaves unknown is untestable rather than a miss.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "example.net", PanelID: 9, InboundNodeID: 1, SourceIP: "1.0.0.9"})
	if res.Verdict != "block" || res.TerminatingStep != "s1" || res.Steps[0].Kind != "source" || res.Steps[0].NodeID != 1 {
		t.Fatalf("source result = %+v", res)
	}
	res, _ = ah.admin.Test(ctx, TestInput{Target: "example.net", PanelID: 9, InboundNodeID: 1, SourceIP: "9.9.9.9"})
	if res.Verdict == "block" {
		t.Fatalf("a client outside the denied country was blocked: %+v", res)
	}
	res, _ = ah.admin.Test(ctx, TestInput{Target: "www.blocked.example.com", PanelID: 9, InboundNodeID: 1})
	if !res.Conditional || res.Steps[0].Result != "untestable" {
		t.Fatalf("unknown source address must make the source step untestable: %+v", res)
	}

	// The builtin that stops a trailing-dot host from slipping past is a step.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "blocked.example.com.", PanelID: 9})
	if res.Verdict != "block" || res.Builtin != "trailing_dot" || res.TerminatingStep != "trailing_dot" {
		t.Fatalf("trailing dot result = %+v", res)
	}

	// An edit after the publish is flagged as not yet in force.
	ah.policy("later", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	if res, _ := ah.admin.Test(ctx, TestInput{Target: "x.example.net", PanelID: 9}); !res.Unpublished {
		t.Fatal("unpublished edits must be flagged")
	}

	for _, in := range []TestInput{{Target: ""}, {Target: "x", Port: 70000}, {Target: "x", Network: "icmp"}} {
		if _, err := ah.admin.Test(ctx, in); err == nil || validationCode(t, err).Code != CodeTestInvalid {
			t.Fatalf("%+v was accepted", in)
		}
	}
}

func TestTest_NothingPublishedIsTheDefault(t *testing.T) {
	ah := newAdminHarness(t)
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "example.com"})
	if err != nil || res.Verdict != "default" || len(res.Steps) != 0 {
		t.Fatalf("result = (%+v, %v)", res, err)
	}
}

func TestAddException_CreatesTheListAndPolicyThenAppends(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	other := ah.policy("other allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "80"} })

	first, err := ah.admin.AddException(ctx, "https://Sub.Example.com:8443/path?q=1", "site")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Entry != "DOMAIN-SUFFIX,sub.example.com" || first.ListID == 0 || first.PolicyID == 0 {
		t.Fatalf("first = %+v", first)
	}
	pol, _ := ah.repos.Policies.GetByID(ctx, first.PolicyID)
	if pol.Action != domain.DestActionAllow || pol.State != domain.DestStateOn || pol.TemplateKey != "global_exceptions" || pol.Priority != 1 {
		t.Fatalf("policy = %+v: an allow, on, first among the allows", pol)
	}
	if o, _ := ah.repos.Policies.GetByID(ctx, other.ID); o.Priority != 2 {
		t.Fatalf("the other allow = %+v", o)
	}

	second, err := ah.admin.AddException(ctx, "host.example.org", "host")
	if err != nil || second.Created || second.ListID != first.ListID || second.PolicyID != first.PolicyID {
		t.Fatalf("second = (%+v, %v): later ones only append", second, err)
	}
	stored, _ := ah.repos.Lists.GetByID(ctx, first.ListID)
	if string(stored.Entries) != "domain:sub.example.com\nfull:host.example.org" {
		t.Fatalf("entries = %q", stored.Entries)
	}

	for _, c := range []struct{ target, match string }{{"", "site"}, {"a b", "site"}, {"example.com", "page"}, {"http://", "site"}} {
		if _, err := ah.admin.AddException(ctx, c.target, c.match); err == nil || validationCode(t, err).Code != CodeExceptionInvalid {
			t.Fatalf("%+v was accepted: %v", c, err)
		}
	}
}

func TestStatus_StatesAndRuleDataVersion(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("l", "DOMAIN-SUFFIX,a.example.com")
	ah.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()

	get := func() NodeState {
		t.Helper()
		v, err := ah.admin.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range v.Nodes {
			if n.PanelID == 9 {
				return n
			}
		}
		t.Fatal("native node missing from the status")
		return NodeState{}
	}

	if n := get(); n.State != "none" {
		t.Fatalf("before any sync: state %q", n.State)
	}
	res := ah.compile(base1())
	if n := get(); n.State != "pending" || n.PendingSince == nil || n.MintedAt == nil {
		t.Fatalf("minted, not reported: %+v", n)
	}
	ah.observe(applied(res.Digest))
	n := get()
	if n.State != "applied" || n.AppliedRules != 1 || n.AppliedAt == nil || n.RulesDataVersion == nil {
		t.Fatalf("applied: %+v", n)
	}
	if !n.Supports.Policy || n.Supports.Hits || n.Collecting || n.Hits24h != nil {
		t.Fatalf("supports/collecting = %+v", n)
	}

	// The non-native panel is listed and says why it enforces nothing.
	v, _ := ah.admin.Status(ctx)
	foundXUI := false
	for _, x := range v.Nodes {
		if x.PanelID == 10 {
			foundXUI = x.State == "unsupported_kind"
		}
	}
	if !foundXUI || v.PublishedGeneration != v.Generation || v.NextPublishAt != nil || v.ApplyETAMS != 0 || v.Paused {
		t.Fatalf("status = %+v", v)
	}

	// A pending edit has an ETA: the rest of the window plus one sync cycle.
	ah.policy("later", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	v, _ = ah.admin.Status(ctx)
	if v.NextPublishAt == nil || v.ApplyETAMS <= 0 || v.ApplyETAMS > int64((60*time.Second+60*time.Second)/time.Millisecond) {
		t.Fatalf("eta = %d, next = %v", v.ApplyETAMS, v.NextPublishAt)
	}

	// Rejected, over limit, sniffing, paused, offline.
	ah.publishNow()
	second := ah.compile(base1())
	ah.observe(rejected(second.Digest))
	if n := get(); n.State != "rejected" || n.FallbackReason != "rejected" {
		t.Fatalf("rejected: %+v", n)
	}
	bad := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"destOverride":["http"]}`)}, users: []int64{10}}
	ah.compile(bad)
	if n := get(); n.State != "sniffing" || len(n.Sniffing) != 1 || n.Sniffing[0].NodeID != 1 {
		t.Fatalf("sniffing: %+v", n)
	}
	if err := ah.svc.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if n := get(); n.State != "paused" {
		t.Fatalf("paused: %+v", n)
	}
	if err := ah.svc.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	ah.clock.Advance(time.Hour)
	if n := get(); n.State != "offline" {
		t.Fatalf("offline: %+v", n)
	}
}
