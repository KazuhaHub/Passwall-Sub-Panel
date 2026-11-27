package render

import (
	"context"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/rulematch"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type explainTemplates struct {
	ports.TemplateRepo
	bySlug map[string]*domain.Template
}

func (r explainTemplates) GetBySlug(_ context.Context, slug string) (*domain.Template, error) {
	if t := r.bySlug[slug]; t != nil {
		return t, nil
	}
	return nil, domain.ErrNotFound
}

func (r explainTemplates) GetDefault(_ context.Context, ct domain.ClientType) (*domain.Template, error) {
	if ct == domain.ClientMihomo {
		return r.bySlug["mihomo"], nil
	}
	return r.bySlug["singbox"], nil
}

type explainRuleSets struct {
	ports.RuleSetRepo
	bySlug map[string]*domain.RuleSet
}

func (r explainRuleSets) GetBySlug(_ context.Context, slug string) (*domain.RuleSet, error) {
	if rs := r.bySlug[slug]; rs != nil {
		return rs, nil
	}
	return nil, domain.ErrNotFound
}

type explainUsers struct {
	ports.UserRepo
	users map[int64]*domain.User
}

func (r explainUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if u := r.users[id]; u != nil {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

type explainGroups struct{ referenceGroupRepo }

func (explainGroups) List(context.Context) ([]*domain.Group, error) {
	return []*domain.Group{{ID: 1}}, nil
}

func direct(name string) map[string][]domain.ProxyGroupMember {
	return map[string][]domain.ProxyGroupMember{name: {{Kind: "builtin", Value: "DIRECT"}}}
}

// explainFixture serves a Mihomo template bound to the given rule sets (in
// order) and one user, on top of the reference node fixture.
func explainFixture(t *testing.T, sets ...*domain.RuleSet) (*Service, *domain.User) {
	t.Helper()
	s, _, u, _ := referenceRenderFixture(t)
	u.GroupID = 1
	bySlug := map[string]*domain.RuleSet{}
	slugs := make([]string, 0, len(sets))
	for _, rs := range sets {
		bySlug[rs.Slug] = rs
		slugs = append(slugs, rs.Slug)
	}
	s.repos.Template = explainTemplates{bySlug: map[string]*domain.Template{
		"mihomo":  {Slug: "mihomo", ClientType: domain.ClientMihomo, Content: referenceMihomoTemplate, RuleSets: slugs},
		"singbox": {Slug: "singbox", ClientType: domain.ClientSingBox, Content: referenceSingBoxTemplate, RuleSets: slugs},
	}}
	s.repos.RuleSet = explainRuleSets{bySlug: bySlug}
	s.repos.User = explainUsers{users: map[int64]*domain.User{u.ID: u}}
	s.repos.Group = explainGroups{}
	return s, u
}

func ruleSet(slug, content string, members map[string][]domain.ProxyGroupMember) *domain.RuleSet {
	return &domain.RuleSet{Slug: slug, Enabled: true, Content: content, ProxyGroupMembers: members}
}

func explain(t *testing.T, s *Service, req ExplainRequest) *ExplainResponse {
	t.Helper()
	if req.TemplateSlug == "" {
		req.TemplateSlug = "mihomo"
	}
	if req.Probe.Host == "" {
		req.Probe = rulematch.ProbeInput{Host: "claude.ai", Port: 443}
	}
	resp, err := s.ExplainRoute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestExplainPersonalRulesPrecedeRuleSets(t *testing.T) {
	rs := ruleSet("common", "# comment\n- DOMAIN-SUFFIX,claude.ai,G\n- MATCH,G", direct("G"))
	s, u := explainFixture(t, rs)
	u.PersonalRules = "- DOMAIN,claude.ai,DIRECT"

	got := explain(t, s, ExplainRequest{UserID: u.ID})
	r := got.Result
	if r.State != ExplainMatched || r.Source != "personal" || r.Line != 1 || r.Target != "DIRECT" || got.PersonalRules != "included" {
		t.Fatalf("with the user: %+v %+v", r, got)
	}
	// The same connection without a user has no personal rules, and says so.
	got = explain(t, s, ExplainRequest{})
	r = got.Result
	if r.State != ExplainMatched || r.Source != "rule_set" || r.RuleSet != "common" || r.Line != 2 || got.PersonalRules != "no_user" {
		t.Fatalf("without a user: %+v %+v", r, got)
	}
	if r.GroupType != "select" || r.GroupDefault != "DIRECT" {
		t.Fatalf("group info: %+v", r)
	}
}

func TestExplainSubscriptionDomainRuleComesFirst(t *testing.T) {
	rs := ruleSet("common", "- MATCH,G", direct("G"))
	rs.DirectSubscriptionDomain = true
	s, _ := explainFixture(t, rs)
	s.repos.Settings = fakeSettings{s: ports.UISettings{SubBaseURL: "https://sub.example.net"}}

	got := explain(t, s, ExplainRequest{Probe: rulematch.ProbeInput{Host: "api.sub.example.net", Port: 443}})
	r := got.Result
	if r.Source != "subscription_domain" || r.Target != "DIRECT" || r.State != ExplainMatched {
		t.Fatalf("%+v", r)
	}
	// Anything else falls to the rule set's MATCH, and the generated rule is not
	// counted in its line numbers.
	got = explain(t, s, ExplainRequest{})
	if got.Result.Source != "rule_set" || got.Result.Line != 1 {
		t.Fatalf("%+v", got.Result)
	}
}

func TestExplainLaterRuleSetAfterMatchIsUnreachable(t *testing.T) {
	first := ruleSet("first", "- MATCH,G1", direct("G1"))
	second := ruleSet("second", "- DOMAIN,claude.ai,G2", direct("G2"))
	s, _ := explainFixture(t, first, second)

	got := explain(t, s, ExplainRequest{})
	if got.Result.State != ExplainMatched || got.Result.RuleSet != "first" {
		t.Fatalf("%+v", got.Result)
	}
	if got.AlsoTotal != 1 || got.AlsoMatching[0].RuleSet != "second" || got.AlsoMatching[0].Status != "unreachable" {
		t.Fatalf("also matching: %+v", got.AlsoMatching)
	}
}

func TestExplainDraftReplacesStoredRuleSet(t *testing.T) {
	stored := ruleSet("common", "- DOMAIN,claude.ai,Old", direct("Old"))
	s, _ := explainFixture(t, stored)
	draft := ruleSet("common", "- DOMAIN,claude.ai,New", direct("New"))

	got := explain(t, s, ExplainRequest{Draft: draft})
	if got.Result.Target != "New" || !got.DraftApplied || len(got.Warnings) != 0 {
		t.Fatalf("%+v", got)
	}
	if stored.Content != "- DOMAIN,claude.ai,Old" {
		t.Fatal("the stored rule set was changed")
	}
	// A draft whose slug the template does not use changes nothing, and says so.
	other := ruleSet("elsewhere", "- DOMAIN,claude.ai,Else", direct("Else"))
	got = explain(t, s, ExplainRequest{Draft: other})
	if got.Result.Target != "Old" || got.DraftApplied || len(got.Warnings) != 1 || got.Warnings[0] != "draft_not_in_template" {
		t.Fatalf("%+v", got)
	}
}

func TestExplainLineNameTargetResolvesForTheUser(t *testing.T) {
	rs := ruleSet("common", "- DOMAIN,claude.ai,A B\n- MATCH,DIRECT", nil)
	s, u := explainFixture(t, rs)

	got := explain(t, s, ExplainRequest{UserID: u.ID})
	r := got.Result
	if r.State != ExplainMatched || r.Target != "A B" || r.GroupType != "" || r.Line != 1 {
		t.Fatalf("%+v", r)
	}
	// Over UDP a proxy that cannot carry UDP is skipped by Mihomo, so a rule that
	// names a line directly may be passed over.
	got = explain(t, s, ExplainRequest{UserID: u.ID, Probe: rulematch.ProbeInput{Host: "claude.ai", Port: 443, Network: "udp"}})
	if got.Result.State != ExplainContinues || got.Result.ContinuesVia != "udp_unsupported_proxy" {
		t.Fatalf("%+v", got.Result)
	}
}

func TestExplainPassCapableGroupContinues(t *testing.T) {
	members := map[string][]domain.ProxyGroupMember{
		"Quic": {{Kind: "builtin", Value: "PASS"}, {Kind: "builtin", Value: "REJECT"}},
		"Main": {{Kind: "builtin", Value: "DIRECT"}},
	}
	rs := ruleSet("common", "- AND,((NETWORK,UDP),(DST-PORT,443)),Quic\n- DOMAIN-SUFFIX,claude.ai,Main", members)
	s, _ := explainFixture(t, rs)

	got := explain(t, s, ExplainRequest{Probe: rulematch.ProbeInput{Host: "claude.ai", Port: 443, Network: "udp"}})
	r := got.Result
	if r.State != ExplainContinues || r.Target != "Quic" || r.ContinuesVia != "pass" || r.Line != 1 {
		t.Fatalf("%+v", r)
	}
	if got.AlsoTotal != 1 || got.AlsoMatching[0].Target != "Main" || got.AlsoMatching[0].Status != "possible" {
		t.Fatalf("later rules may apply: %+v", got.AlsoMatching)
	}
	// Over TCP the first rule does not match at all.
	got = explain(t, s, ExplainRequest{})
	if got.Result.State != ExplainMatched || got.Result.Target != "Main" || got.Result.Line != 2 {
		t.Fatalf("%+v", got.Result)
	}
}

func TestExplainUndecidedLineMakesLaterCandidatesUncertain(t *testing.T) {
	rs := ruleSet("common", "- GEOSITE,cn,CN\n- DOMAIN,claude.ai,G\n- MATCH,G", map[string][]domain.ProxyGroupMember{
		"CN": {{Kind: "builtin", Value: "DIRECT"}}, "G": {{Kind: "builtin", Value: "DIRECT"}}})
	s, _ := explainFixture(t, rs)

	got := explain(t, s, ExplainRequest{})
	if got.Result.State != ExplainUndecided || got.Result.Certain || got.Result.Line != 2 {
		t.Fatalf("%+v", got.Result)
	}
	if got.BranchesTotal != 1 || got.Branches[0].Line != 1 || got.Branches[0].Reason != "client_data" {
		t.Fatalf("branches: %+v", got.Branches)
	}
	if len(got.AlsoMatching) != 1 || got.AlsoMatching[0].Status != "possible" {
		t.Fatalf("later candidates must not be certain: %+v", got.AlsoMatching)
	}
}

func TestExplainNoMatchGoesDirect(t *testing.T) {
	rs := ruleSet("common", "- DOMAIN,other.example,G", direct("G"))
	s, _ := explainFixture(t, rs)
	got := explain(t, s, ExplainRequest{})
	if got.Result.State != ExplainNoMatch || got.DefaultOutbound != "DIRECT" || !got.Result.Certain || got.Result.ExplainRuleRef != nil {
		t.Fatalf("%+v", got)
	}
	if got.TotalRules != 1 {
		t.Fatalf("markers must not be counted: %d", got.TotalRules)
	}
}

func TestExplainRejectsUnsupportedAndInvalidInput(t *testing.T) {
	s, _ := explainFixture(t, ruleSet("common", "- MATCH,DIRECT", nil))
	ctx := context.Background()
	if _, err := s.ExplainRoute(ctx, ExplainRequest{TemplateSlug: "singbox", Probe: rulematch.ProbeInput{Host: "a.b"}}); !errors.Is(err, ErrExplainUnsupportedClient) {
		t.Fatalf("sing-box: %v", err)
	}
	if _, err := s.ExplainRoute(ctx, ExplainRequest{TemplateSlug: "missing", Probe: rulematch.ProbeInput{Host: "a.b"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("template: %v", err)
	}
	if _, err := s.ExplainRoute(ctx, ExplainRequest{TemplateSlug: "mihomo", UserID: 999, Probe: rulematch.ProbeInput{Host: "a.b"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("user: %v", err)
	}
	var coded *domain.CodedValidationError
	if _, err := s.ExplainRoute(ctx, ExplainRequest{TemplateSlug: "mihomo", Probe: rulematch.ProbeInput{Host: "https://x"}}); !errors.As(err, &coded) || coded.Code != "explain_address_invalid" {
		t.Fatalf("probe: %v", err)
	}
	if _, err := s.ExplainRoute(ctx, ExplainRequest{Probe: rulematch.ProbeInput{Host: "a.b"}}); !errors.As(err, &coded) || coded.Code != "explain_template_required" {
		t.Fatalf("slug: %v", err)
	}
}

func TestExplainTemplateWithoutPersonalPlaceholder(t *testing.T) {
	rs := ruleSet("common", "- MATCH,DIRECT", nil)
	s, u := explainFixture(t, rs)
	s.repos.Template = explainTemplates{bySlug: map[string]*domain.Template{"mihomo": {Slug: "mihomo", ClientType: domain.ClientMihomo, RuleSets: []string{"common"},
		Content: "proxies:\n  {{ proxies }}\nproxy-groups:\n  {{ proxy_groups }}\nrules:\n  - DOMAIN,claude.ai,REJECT\n  {{ rules_common }}\n"}}}
	u.PersonalRules = "- DOMAIN,claude.ai,DIRECT"
	got := explain(t, s, ExplainRequest{UserID: u.ID})
	if got.PersonalRules != "not_in_template" || got.Result.Source != "template" || got.Result.Target != "REJECT" || got.Result.Line != 6 {
		t.Fatalf("%+v %+v", got, got.Result)
	}
}

// The explanation walks the same rules the real subscription carries, in the
// same order: the marker rules it adds are gone again, and nothing else differs.
func TestExplainWalksExactlyTheRealSubscriptionRules(t *testing.T) {
	rs := ruleSet("common", "# c\n- DOMAIN,a.example,G\n\n- DOMAIN,b.example,G\n- MATCH,G", direct("G"))
	rs.DirectSubscriptionDomain = true
	s, u := explainFixture(t, rs)
	s.repos.Settings = fakeSettings{s: ports.UISettings{SubBaseURL: "https://sub.example.net"}}
	u.PersonalRules = "- DOMAIN,p.example,DIRECT"

	real, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	var doc explainDocument
	if err := yaml.Unmarshal(real.Body, &doc); err != nil {
		t.Fatal(err)
	}
	got := explain(t, s, ExplainRequest{UserID: u.ID})
	if got.TotalRules != len(doc.Rules) || got.TotalRules != 5 {
		t.Fatalf("explain walked %d rules, the subscription has %d", got.TotalRules, len(doc.Rules))
	}
}
