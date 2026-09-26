package render

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestSubscriptionDirectRule(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "hostname", base: "https://Sub.Example.test:8443/sub", want: "- DOMAIN-SUFFIX,sub.example.test,DIRECT"},
		{name: "ipv4", base: "http://192.0.2.10:8788", want: "- IP-CIDR,192.0.2.10/32,DIRECT,no-resolve"},
		{name: "ipv6", base: "https://[2001:db8::10]/", want: "- IP-CIDR6,2001:db8::10/128,DIRECT,no-resolve"},
		{name: "relative URL", base: "/sub", want: ""},
		{name: "invalid scheme", base: "ftp://sub.example.test", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subscriptionDirectRule(tt.base); got != tt.want {
				t.Fatalf("subscriptionDirectRule(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

type renderRuleSetRepo struct {
	items map[string]*domain.RuleSet
}

func (r renderRuleSetRepo) List(context.Context) ([]*domain.RuleSet, error) { return nil, nil }

func (r renderRuleSetRepo) ListPaged(context.Context, ports.Pagination) ([]*domain.RuleSet, int64, error) {
	return nil, 0, nil
}

func (r renderRuleSetRepo) GetBySlug(_ context.Context, slug string) (*domain.RuleSet, error) {
	return r.items[slug], nil
}

func (r renderRuleSetRepo) Save(context.Context, *domain.RuleSet) error { return nil }
func (r renderRuleSetRepo) Delete(context.Context, string) error        { return nil }

func TestResolveRulesCommonPrependsDirectSubscriptionRule(t *testing.T) {
	s := &Service{repos: ports.Repos{RuleSet: renderRuleSetRepo{items: map[string]*domain.RuleSet{
		"default": {
			Slug:                     "default",
			Enabled:                  true,
			DirectSubscriptionDomain: true,
			Content:                  "- MATCH,🚀 节点选择",
		},
	}}}}

	got, _, _, _, err := s.resolveRulesCommon(context.Background(), &domain.Template{
		Slug:     "default-mihomo",
		RuleSets: []string{"default"},
	}, ports.UISettings{SubBaseURL: "https://sub.example.test/panel"})
	if err != nil {
		t.Fatal(err)
	}
	want := "- DOMAIN-SUFFIX,sub.example.test,DIRECT\n- MATCH,🚀 节点选择"
	if got != want {
		t.Fatalf("resolved rules = %q, want %q", got, want)
	}
}

func TestResolveRulesCommonDoesNotInjectWithoutEnabledOptionOrHostname(t *testing.T) {
	tests := []struct {
		name string
		set  domain.RuleSet
		base string
		want string
	}{
		{name: "option disabled", set: domain.RuleSet{Enabled: true, Content: "- MATCH,DIRECT"}, base: "https://sub.example.test", want: "- MATCH,DIRECT"},
		{name: "ruleset disabled", set: domain.RuleSet{Enabled: false, DirectSubscriptionDomain: true, Content: "- MATCH,DIRECT"}, base: "https://sub.example.test", want: ""},
		{name: "relative subscription URL", set: domain.RuleSet{Enabled: true, DirectSubscriptionDomain: true, Content: "- MATCH,DIRECT"}, base: "/sub", want: "- MATCH,DIRECT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{repos: ports.Repos{RuleSet: renderRuleSetRepo{items: map[string]*domain.RuleSet{"rules": &tt.set}}}}
			got, _, _, _, err := s.resolveRulesCommon(context.Background(), &domain.Template{RuleSets: []string{"rules"}}, ports.UISettings{SubBaseURL: tt.base})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("resolved rules = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRuleBundleSeparatesMihomoAndSharedRules(t *testing.T) {
	s := &Service{repos: ports.Repos{RuleSet: renderRuleSetRepo{items: map[string]*domain.RuleSet{
		"advanced": {
			Slug: "advanced", Enabled: true,
			Content:                "- REMATCH-NAME,marked,Mihomo\n- MATCH,Shared",
			MihomoSubRules:         []domain.MihomoSubRule{{Name: "sub", Content: "- MATCH,Mihomo"}},
			MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump", TargetSubRule: "sub"}},
		},
	}}}}
	bundle, err := s.resolveRuleBundle(context.Background(), &domain.Template{RuleSets: []string{"advanced"}}, ports.UISettings{}, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SharedRules != "- REMATCH-NAME,marked,Mihomo\n- MATCH,Shared" {
		t.Fatalf("shared rules = %q", bundle.SharedRules)
	}
	if len(bundle.SubRules) != 1 || len(bundle.RematchOutbounds) != 1 {
		t.Fatalf("advanced bundle missing: %#v", bundle)
	}
}

func TestResolveRuleBundleRejectsDuplicateAdvancedNames(t *testing.T) {
	s := &Service{repos: ports.Repos{RuleSet: renderRuleSetRepo{items: map[string]*domain.RuleSet{
		"one": {Slug: "one", Enabled: true, Content: "- MATCH,DIRECT", MihomoSubRules: []domain.MihomoSubRule{{Name: "duplicate", Content: "- MATCH,DIRECT"}}},
		"two": {Slug: "two", Enabled: true, Content: "- MATCH,DIRECT", MihomoSubRules: []domain.MihomoSubRule{{Name: "duplicate", Content: "- MATCH,DIRECT"}}},
	}}}}
	if _, err := s.resolveRuleBundle(context.Background(), &domain.Template{RuleSets: []string{"one", "two"}}, ports.UISettings{}, domain.ClientMihomo); err == nil {
		t.Fatal("expected duplicate sub-rule error")
	}
}

func TestResolveRuleBundleIgnoresMihomoNamesForSingBox(t *testing.T) {
	s := &Service{repos: ports.Repos{RuleSet: renderRuleSetRepo{items: map[string]*domain.RuleSet{
		"one": {Slug: "one", Enabled: true, Content: "- DOMAIN,one.example,DIRECT", MihomoSubRules: []domain.MihomoSubRule{{Name: "duplicate", Content: "- MATCH,DIRECT"}}, MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump", TargetSubRule: "duplicate"}}},
		"two": {Slug: "two", Enabled: true, Content: "- MATCH,DIRECT", MihomoSubRules: []domain.MihomoSubRule{{Name: "duplicate", Content: "- MATCH,DIRECT"}}, MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump", TargetSubRule: "duplicate"}}},
	}}}}
	bundle, err := s.resolveRuleBundle(context.Background(), &domain.Template{RuleSets: []string{"one", "two"}}, ports.UISettings{}, domain.ClientSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SharedRules != "- DOMAIN,one.example,DIRECT\n- MATCH,DIRECT" || len(bundle.SubRules) != 0 || len(bundle.RematchOutbounds) != 0 {
		t.Fatalf("sing-box bundle includes Mihomo-only features or loses shared rules: %#v", bundle)
	}
}
