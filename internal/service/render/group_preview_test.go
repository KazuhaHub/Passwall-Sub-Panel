package render

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gopkg.in/yaml.v3"
)

func TestGroupPreviewUsesSharedNamesWithinRuleSetScope(t *testing.T) {
	for _, scenario := range []string{"single", "both", "repeat", "node", "hidden_direct", "disabled_relay", "drift", "literal", "template_collision"} {
		t.Run(scenario, func(t *testing.T) {
			s, node, user, rule := collidingNameFixture(t)
			user.GroupID = 1
			switch scenario {
			case "both":
				rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "line", NodeID: node.ID, LineID: "relay"})
			case "repeat":
				rule.Content = "- DOMAIN,example.com,Second\n- MATCH,国外媒体"
				rule.ProxyGroupMembers["Second"] = rule.ProxyGroupMembers["国外媒体"]
			case "node":
				rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node", NodeID: node.ID}}
			case "hidden_direct":
				node.HideDirect = true
			case "disabled_relay":
				node.Relays[0].Enabled = false
			case "drift":
				node.RouteState.Status = "drift"
			case "literal":
				node.RouteState.Lines[0].Name = "日本 [literal]"
			case "template_collision":
				s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: strings.Replace(referenceMihomoTemplate, "  {{ proxies }}", "  {{ proxies }}\n  - {name: same, type: direct}", 1)}}
			}
			before, _ := json.Marshal([]any{node, rule, user})
			preview, err := s.PreviewMihomoGroup(context.Background(), user.GroupID, rule)
			if err != nil {
				t.Fatal(err)
			}
			output, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Groups []struct {
					Name    string   `yaml:"name"`
					Members []string `yaml:"proxies"`
				} `yaml:"proxy-groups"`
			}
			if err := yaml.Unmarshal(output.Body, &config); err != nil {
				t.Fatal(err)
			}
			if scenario == "template_collision" {
				if !reflect.DeepEqual(preview.Groups[0].Members, []string{"same"}) || !reflect.DeepEqual(config.Groups[0].Members, []string{"same [VLESS 路由]"}) {
					t.Fatalf("preview must ignore external names; subscription must avoid them: %+v vs %+v", preview.Groups, config.Groups)
				}
			} else if len(preview.Groups) != len(config.Groups) {
				t.Fatalf("%+v vs %+v", preview.Groups, config.Groups)
			} else {
				for i, group := range config.Groups {
					if preview.Groups[i].Name != group.Name || !reflect.DeepEqual(preview.Groups[i].Members, group.Members) {
						t.Fatalf("preview differs: %+v vs %+v", preview.Groups, config.Groups)
					}
				}
			}
			if (scenario == "single" || scenario == "hidden_direct") && !reflect.DeepEqual(preview.Groups[0].Members, []string{"same"}) {
				t.Fatalf("unexpected suffix: %+v", preview)
			}
			after, _ := json.Marshal([]any{node, rule, user})
			if string(before) != string(after) {
				t.Fatal("preview mutated shared input")
			}
			raw, _ := json.Marshal(preview)
			if strings.Contains(string(raw), "uuid") || strings.Contains(string(raw), user.UUID) || strings.Contains(string(raw), "psp-line-") {
				t.Fatal("preview leaked private data")
			}
		})
	}
}

type previewRules struct {
	ports.RuleSetRepo
	rules map[string]*domain.RuleSet
}

func (r previewRules) GetBySlug(_ context.Context, slug string) (*domain.RuleSet, error) {
	if rule := r.rules[slug]; rule != nil {
		return rule, nil
	}
	return nil, domain.ErrNotFound
}

type previewTemplate struct {
	ports.TemplateRepo
	tpl *domain.Template
}

func (r previewTemplate) GetDefault(context.Context, domain.ClientType) (*domain.Template, error) {
	return r.tpl, nil
}

func TestGroupPreviewUsesOnlyDraftRegardlessOfTemplateBindingsOrPrecedence(t *testing.T) {
	for _, scenario := range []string{"draft", "not_referenced", "disabled", "overridden", "options_only"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, u, saved := collidingNameFixture(t)
			u.GroupID = 1
			draft := *saved
			draft.ProxyGroupMembers = map[string][]domain.ProxyGroupMember{"国外媒体": {{Kind: "builtin", Value: "REJECT"}}}
			tpl := &domain.Template{Slug: "mihomo", Content: referenceMihomoTemplate, RuleSets: []string{"test"}}
			rules := map[string]*domain.RuleSet{"test": saved}
			status := "ready"
			switch scenario {
			case "not_referenced":
				tpl.RuleSets = nil
			case "disabled":
				draft.Enabled = false
				status = "disabled"
			case "overridden", "options_only":
				earlier := *saved
				earlier.Slug = "earlier"
				rules["earlier"] = &earlier
				tpl.RuleSets = []string{"earlier", "test"}
				if scenario == "options_only" {
					earlier.ProxyGroupMembers = nil
					earlier.ProxyGroupOptions = map[string]domain.ProxyGroupOptions{"国外媒体": {Type: "select"}}
				}
			}
			s.repos.Template = previewTemplate{tpl: tpl}
			s.repos.RuleSet = previewRules{rules: rules}
			got, err := s.PreviewMihomoGroup(context.Background(), u.GroupID, &draft)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != status {
				t.Fatalf("%+v", got)
			}
			if scenario != "disabled" {
				if !reflect.DeepEqual(got.Groups[0].Members, []string{"REJECT"}) {
					t.Fatalf("draft not applied: %+v", got)
				}
			}
			if saved.ProxyGroupMembers["国外媒体"][0].Kind != "line" {
				t.Fatal("mutated saved rule")
			}
		})
	}
}

type previewSettings struct {
	ports.ScopedSettings
	requested []int64
}

func (p *previewSettings) LoadForGroup(_ context.Context, id int64, defaults ports.UISettings) (ports.UISettings, error) {
	p.requested = append(p.requested, id)
	defaults.SubRegionFlagPrefix = id == 1
	return defaults, nil
}
func (p *previewSettings) LoadForUser(ctx context.Context, u *domain.User, d ports.UISettings) (ports.UISettings, error) {
	return p.LoadForGroup(ctx, u.GroupID, d)
}

type previewLayoutGroups struct{ ports.GroupRepo }

func (previewLayoutGroups) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	if id != 1 && id != 2 {
		return nil, domain.ErrNotFound
	}
	return &domain.Group{ID: id, Layout: domain.Layout{Sort: []domain.SortEntry{{NodeID: 8, Weight: 0}, {NodeID: 7, Weight: 1}}, Separators: []domain.Separator{{Position: 0, Name: "Divider"}}}}, nil
}

type noPreviewCredentials struct{ ports.PSPClientRepo }

func (noPreviewCredentials) ListByUser(context.Context, int64) ([]*domain.PSPClient, error) {
	panic("group preview must not read personal credentials")
}

func TestGroupPreviewUsesScopedFlagsLayoutAndNoUserCredentialReads(t *testing.T) {
	s, node, u, rule := collidingNameFixture(t)
	node.HideDirect = false
	node.Relays = nil
	node.RouteState.Lines = nil
	node.DisplayName = "A"
	node.Region = "JP"
	other := *node
	other.ID = 8
	other.DisplayName = "B"
	rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "remaining"}}
	s.groupSvc = referenceGroupResolver{nodes: []*domain.Node{node, &other}}
	s.repos.Group = previewLayoutGroups{}
	settings := &previewSettings{}
	s.repos.ScopedSettings = settings
	for _, id := range []int64{1, 2} {
		u.GroupID = id
		preview, err := s.PreviewMihomoGroup(context.Background(), id, rule)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"Divider", "B", "A"}
		if id == 1 {
			want = []string{"Divider", "🇯🇵 B", "🇯🇵 A"}
		}
		if !reflect.DeepEqual(preview.Groups[0].Members, want) {
			t.Fatalf("group %d: %+v", id, preview.Groups)
		}
	}
	s.repos.PSPClient = noPreviewCredentials{}
	node.RouteState = domain.NodeRouteState{Status: "applied", Binding: domain.RouteBinding("https://panel.example", node), Lines: []domain.VlessRouteLine{{ID: "route-B", Name: "route", Route: 1001, Enabled: true, OutboundTag: "direct"}}}
	if _, err := s.PreviewMihomoGroup(context.Background(), 1, rule); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings.requested, []int64{1, 2, 1}) {
		t.Fatalf("unexpected setting loads: %v", settings.requested)
	}
	if _, err := s.PreviewMihomoGroup(context.Background(), 999, rule); err == nil {
		t.Fatal("invalid group silently accepted")
	}
}

func TestGroupPreviewSimulationDoesNotWeakenUserPublicationGates(t *testing.T) {
	s, _, u, rule := collidingNameFixture(t)
	u.GroupID = 1
	s.repos.PSPClient = nil
	preview, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Groups[0].Members, []string{"same"}) {
		t.Fatalf("simulation omitted route: %+v", preview)
	}
	output, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if len(namedVLESSProxies(t, output, domain.ClientMihomo)) != 0 {
		t.Fatal("user route bypassed applied credential gate")
	}
}

func TestGroupPreviewKeepsNestedGroupsButIgnoresExternalProviderGroups(t *testing.T) {
	s, _, _, rule := collidingNameFixture(t)
	rule.Content = "- DOMAIN,nested.example,Nested\n- MATCH,Outer"
	rule.ProxyGroupMembers = map[string][]domain.ProxyGroupMember{"Outer": {{Kind: "proxy_group", Value: "Nested"}}, "Nested": {{Kind: "line", NodeID: 7, LineID: "route-B"}}}
	tpl := strings.Replace(referenceMihomoTemplate, "  {{ proxy_groups }}", "  {{ proxy_groups }}\n  - name: ProviderOnly\n    type: select\n    use: [remote]", 1) + "\nproxy-providers:\n  remote: {type: file, path: remote.yaml}\n"
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: tpl}}
	got, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range got.Groups {
		if group.Name == "Outer" && !reflect.DeepEqual(group.Members, []string{"Nested"}) {
			t.Fatalf("nested group flattened: %+v", group)
		}
		if group.Name == "ProviderOnly" {
			t.Fatalf("external provider group entered draft preview: %+v", group)
		}
	}
}

func TestGroupPreviewNeedsNoExternalTemplateOrRuleRepository(t *testing.T) {
	s, _, _, rule := collidingNameFixture(t)
	s.repos.Template, s.repos.RuleSet = nil, nil
	got, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuleSet != rule.Slug || len(got.Groups) != 1 || !reflect.DeepEqual(got.Groups[0].Members, []string{"same"}) {
		t.Fatalf("external dependencies influenced standalone preview: %+v", got)
	}
}

func TestGroupPreviewIgnoresLinesReferencedOnlyByOtherRuleSets(t *testing.T) {
	s, node, user, rule := collidingNameFixture(t)
	user.GroupID = 1
	other := &domain.RuleSet{Slug: "other", Enabled: true, Content: "- DOMAIN,other.example,Other", ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
		"Other": {{Kind: "line", NodeID: node.ID, LineID: "relay"}},
	}}
	s.repos.Template = previewTemplate{tpl: &domain.Template{Slug: "mihomo", Content: referenceMihomoTemplate, RuleSets: []string{"other", rule.Slug}}}
	s.repos.RuleSet = previewRules{rules: map[string]*domain.RuleSet{"other": other, rule.Slug: rule}}
	got, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || got.Groups[0].Name != "国外媒体" || !reflect.DeepEqual(got.Groups[0].Members, []string{"same"}) {
		t.Fatalf("other rule-set lines influenced the preview: %+v", got)
	}
	output, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	names := namedVLESSProxies(t, output, domain.ClientMihomo)
	if len(names) != 2 || names["same [中转]"] == nil || names["same [VLESS 路由]"] == nil {
		t.Fatalf("subscription lost other rule-set dependencies: %+v", names)
	}
}

func TestGroupPreviewIncludesCurrentSubRuleAndRematchDependencies(t *testing.T) {
	s, _, _, rule := collidingNameFixture(t)
	rule.Content = "- DOMAIN,example.com,Mark\n- MATCH,国外媒体"
	rule.MihomoSubRules = []domain.MihomoSubRule{{Name: "child", Content: "- MATCH,Child"}}
	rule.MihomoRematchOutbounds = []domain.MihomoRematchOutbound{{Name: "Mark", TargetSubRule: "child"}}
	rule.ProxyGroupMembers["Child"] = []domain.ProxyGroupMember{{Kind: "builtin", Value: "DIRECT"}}
	rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "rematch", Value: "Mark"})
	got, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	members := map[string][]string{}
	for _, group := range got.Groups {
		members[group.Name] = group.Members
	}
	if !reflect.DeepEqual(members["Child"], []string{"DIRECT"}) || !reflect.DeepEqual(members["国外媒体"], []string{"same", "Mark"}) {
		t.Fatalf("current rule-set dependencies were omitted: %+v", got)
	}
}

func TestGroupPreviewDoesNotChangeCachedSubscriptions(t *testing.T) {
	s, _, u, rule := collidingNameFixture(t)
	s.cache, s.now = newRenderCache(subRenderCacheTTL), time.Now
	u.GroupID = 1
	before, err := s.RenderForUserCached(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	draft := *rule
	draft.ProxyGroupMembers = map[string][]domain.ProxyGroupMember{"国外媒体": {{Kind: "builtin", Value: "REJECT"}}}
	if _, err := s.PreviewMihomoGroup(context.Background(), 1, &draft); err != nil {
		t.Fatal(err)
	}
	after, err := s.RenderForUserCached(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if string(before.Body) != string(after.Body) {
		t.Fatal("draft entered formal subscription cache")
	}
}

func TestGroupPreviewDeliberatelyExcludesPersonalRules(t *testing.T) {
	s, _, u, rule := collidingNameFixture(t)
	u.GroupID = 1
	u.PersonalRules = "- DOMAIN,personal.example,same [7:relay]"
	preview, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Groups[0].Members, []string{"same"}) {
		t.Fatalf("personal rules leaked into group simulation: %+v", preview.Groups)
	}
	out, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if len(namedVLESSProxies(t, out, domain.ClientMihomo)) != 2 {
		t.Fatal("formal subscription lost personal-rule dependency")
	}
}
