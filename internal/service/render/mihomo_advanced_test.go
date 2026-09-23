package render

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestMihomoAdvancedInspectionSeparatesGroupsOutboundsAndSubRules(t *testing.T) {
	features := MihomoRuleFeatures{
		Rules: `
- REMATCH-NAME,streaming,Streaming
- DOMAIN-SUFFIX,netflix.com,mark-streaming
- SUB-RULE,(NETWORK,tcp),ai-rules
`,
		SubRules:         []domain.MihomoSubRule{{Name: "ai-rules", Content: "- DOMAIN-SUFFIX,openai.com,AI\n- MATCH,DIRECT"}},
		RematchOutbounds: []domain.MihomoRematchOutbound{{Name: "mark-streaming", TargetRematchName: "streaming"}},
	}
	inspection := InspectProxyGroupsWithMihomo("- MATCH,Final", nil, nil, nil, features)
	for _, issue := range inspection.Issues {
		if issue.Level == "error" {
			t.Fatalf("unexpected error: %#v", issue)
		}
	}
	want := map[string]bool{"Streaming": true, "AI": true, "Final": true}
	for _, group := range inspection.Groups {
		delete(want, group.Name)
		if group.Name == "mark-streaming" || group.Name == "ai-rules" {
			t.Fatalf("control-flow symbol became a proxy group: %q", group.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing groups: %#v", want)
	}
}

func TestMihomoAdvancedInspectionRejectsUnsafeRematchOrder(t *testing.T) {
	features := MihomoRuleFeatures{
		Rules:            "- DOMAIN-SUFFIX,netflix.com,mark-streaming\n- REMATCH-NAME,streaming,Streaming",
		RematchOutbounds: []domain.MihomoRematchOutbound{{Name: "mark-streaming", TargetRematchName: "streaming"}},
	}
	inspection := InspectProxyGroupsWithMihomo("", nil, nil, nil, features)
	for _, issue := range inspection.Issues {
		if issue.Code == "unsafe_rematch_order" && issue.Level == "error" {
			return
		}
	}
	t.Fatalf("unsafe order was not rejected: %#v", inspection.Issues)
}

func TestMihomoAutoGroupDoesNotRenderRematchMember(t *testing.T) {
	node := &domain.Node{ID: 1, DisplayName: "Node"}
	features := MihomoRuleFeatures{RematchOutbounds: []domain.MihomoRematchOutbound{{Name: "AI Rematch", TargetSubRule: "ai-rules"}}}
	raw, err := buildProxyGroupsYAMLWithFeatures(
		"- MATCH,Auto", nil,
		map[string][]domain.ProxyGroupMember{"Auto": {{Kind: "outbound", Value: "AI Rematch"}, {Kind: "node", NodeID: 1}}},
		map[string]domain.ProxyGroupOptions{"Auto": {Type: ProxyGroupTypeURLTest}},
		[]renderItem{{name: "Node", node: node}}, features,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "AI Rematch") || !strings.Contains(raw, "Node") {
		t.Fatalf("auto group retained non-testable Rematch member:\n%s", raw)
	}
}

func TestMihomoAdvancedInspectionValidatesSubRuleAndOutboundMembers(t *testing.T) {
	features := MihomoRuleFeatures{
		Rules:            "- SUB-RULE,(NETWORK,tcp),missing\n- MATCH,Manual",
		RematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump"}},
	}
	members := map[string][]domain.ProxyGroupMember{"Manual": {{Kind: "outbound", Value: "missing"}}}
	inspection := InspectProxyGroupsWithMihomo("", members, nil, nil, features)
	want := map[string]bool{"missing_sub_rule": true, "missing_rematch_target": true, "missing_outbound": true}
	for _, issue := range inspection.Issues {
		delete(want, issue.Code)
	}
	if len(want) != 0 {
		t.Fatalf("missing validation issues: %#v; got %#v", want, inspection.Issues)
	}
}

func TestMarshalMihomoSubRulesAndAppendRematchOutbounds(t *testing.T) {
	raw, err := marshalMihomoSubRules([]domain.MihomoSubRule{{Name: "ai-rules", Content: "- DOMAIN,openai.com,AI\n- MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]map[string][]string
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	if got := document["sub-rules"]["ai-rules"]; len(got) != 2 || got[1] != "MATCH,DIRECT" {
		t.Fatalf("unexpected sub-rules block: %#v\n%s", document, raw)
	}

	proxies, err := appendMihomoRematchOutbounds([]map[string]any{{"name": "node", "type": "ss"}}, []domain.MihomoRematchOutbound{{
		Name: "jump", TargetRematchName: "marked", TargetSubRule: "ai-rules",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || proxies[1]["type"] != "rematch" || proxies[1]["target-rematch-name"] != "marked" || proxies[1]["target-sub-rule"] != "ai-rules" {
		t.Fatalf("unexpected rematch proxies: %#v", proxies)
	}
	if _, err := appendMihomoRematchOutbounds(proxies, []domain.MihomoRematchOutbound{{Name: "node", TargetRematchName: "x"}}); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected duplicate proxy-name error, got %v", err)
	}
}

func TestMihomoSubRulesPlaceholderProducesValidRootYAML(t *testing.T) {
	subRules, err := marshalMihomoSubRules([]domain.MihomoSubRule{{Name: "ai-rules", Content: "- MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	body := substituteBlockPlaceholders("proxies:\n  {{ proxies }}\n{{ mihomo_sub_rules }}\nrules:\n  {{ rules_common }}\n", map[string]string{
		"proxies":          "- name: node\n  type: ss",
		"mihomo_sub_rules": subRules,
		"rules_common":     "- SUB-RULE,(NETWORK,tcp),ai-rules",
	})
	var document map[string]any
	if err := yaml.Unmarshal([]byte(body), &document); err != nil {
		t.Fatalf("rendered YAML is invalid: %v\n%s", err, body)
	}
	if _, ok := document["sub-rules"]; !ok {
		t.Fatalf("sub-rules missing from rendered root: %#v\n%s", document, body)
	}
}

func TestSplitRuleFieldsKeepsLogicalPayloadTogether(t *testing.T) {
	fields := splitRuleFields("AND,((NETWORK,UDP),(DST-PORT,443)),QUIC")
	if len(fields) != 3 || fields[1] != "((NETWORK,UDP),(DST-PORT,443))" || fields[2] != "QUIC" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestValidateMihomoTemplateBundle(t *testing.T) {
	ruleSets := []*domain.RuleSet{
		{Slug: "one", Enabled: true, MihomoSubRules: []domain.MihomoSubRule{{Name: "shared"}}, MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump"}}},
		{Slug: "two", Enabled: true, MihomoSubRules: []domain.MihomoSubRule{{Name: "shared"}}, MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "jump"}}},
	}
	issues := ValidateMihomoTemplateBundle(ruleSets, "rules:\n  {{ rules_common }}")
	want := map[string]bool{"duplicate_bound_sub_rule": true, "duplicate_bound_rematch_outbound": true, "missing_sub_rules_placeholder": true}
	for _, issue := range issues {
		delete(want, issue.Code)
	}
	if len(want) != 0 {
		t.Fatalf("missing bundle issues: %#v; got %#v", want, issues)
	}
	if issues := ValidateMihomoTemplateBundle(ruleSets[:1], "{{ mihomo_sub_rules }}\nrules:\n  {{ rules_common }}"); len(issues) != 0 {
		t.Fatalf("valid bundle issues: %#v", issues)
	}
	if issues := ValidateMihomoTemplateBundle(ruleSets[:1], "{{mihomo_sub_rules}}\nrules:\n  {{ rules_common }}"); len(issues) != 0 {
		t.Fatalf("compact valid placeholder issues: %#v", issues)
	}
	if issues := ValidateMihomoTemplateBundle(ruleSets[:1], "root:\n  {{ mihomo_sub_rules }}"); len(issues) != 1 || issues[0].Code != "missing_sub_rules_placeholder" {
		t.Fatalf("indented placeholder must not satisfy root contract: %#v", issues)
	}
}
