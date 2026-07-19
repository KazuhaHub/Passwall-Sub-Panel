package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestResolveConfiguredMembersSpecificNodeBeforeDirectAndRemainingDeduplicates(t *testing.T) {
	china := &domain.Node{ID: 42, DisplayName: "🇨🇳 China SH - Aliyun", Region: "CN", Tags: []string{"premium"}}
	taiwan := &domain.Node{ID: 7, DisplayName: "🇹🇼 Taiwan", Region: "TW"}
	items := []renderItem{{name: china.DisplayName, node: china}, {name: taiwan.DisplayName, node: taiwan}}
	members := []domain.ProxyGroupMember{
		{Kind: "node", NodeID: 42},
		{Kind: "builtin", Value: "DIRECT"},
		{Kind: "proxy_group", Value: "🚀 节点选择"},
		{Kind: "node_set", Value: "remaining"},
	}
	assertMemberStrings(t, resolveConfiguredMembers(members, items), []string{china.DisplayName, "DIRECT", "🚀 节点选择", taiwan.DisplayName})
}

func TestResolveConfiguredMembersRegionTagAndMissingNode(t *testing.T) {
	a := &domain.Node{ID: 1, DisplayName: "CN premium", Region: "CN", Tags: []string{"premium"}}
	b := &domain.Node{ID: 2, DisplayName: "US premium", Region: "US", Tags: []string{"premium"}}
	c := &domain.Node{ID: 3, DisplayName: "US basic", Region: "US"}
	items := []renderItem{{name: a.DisplayName, node: a}, {name: b.DisplayName, node: b}, {name: c.DisplayName, node: c}}
	got := resolveConfiguredMembers([]domain.ProxyGroupMember{
		{Kind: "node", NodeID: 999},
		{Kind: "node_set", Value: "region:US"},
		{Kind: "node_set", Value: "tag:premium"},
		{Kind: "node_set", Value: "remaining"},
	}, items)
	assertMemberStrings(t, got, []string{"US premium", "US basic", "CN premium"})
}

func TestBuildProxyGroupsYAMLWithMembersPutsNodeBeforeDirect(t *testing.T) {
	node := &domain.Node{ID: 42, DisplayName: "🇨🇳 China SH - Aliyun", Region: "CN"}
	configs := map[string][]domain.ProxyGroupMember{
		"🇨🇳 中国大陆": {
			{Kind: "node", NodeID: 42},
			{Kind: "builtin", Value: "DIRECT"},
			{Kind: "node_set", Value: "remaining"},
		},
	}
	raw, err := buildProxyGroupsYAMLWithMembers("- MATCH,🇨🇳 中国大陆", nil, configs, nil, []renderItem{{name: node.DisplayName, node: node}})
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	var found *proxyGroup
	for i := range groups {
		if groups[i].Name == "🇨🇳 中国大陆" {
			found = &groups[i]
		}
	}
	if found == nil {
		t.Fatalf("group missing: %#v", groups)
	}
	assertMemberStrings(t, found.Proxies, []string{node.DisplayName, "DIRECT"})
}

func TestBuildProxyGroupsYAMLSanitizesAutoTypeGroups(t *testing.T) {
	nodeA := &domain.Node{ID: 42, DisplayName: "🇭🇰 HK", Region: "HK"}
	nodeB := &domain.Node{ID: 7, DisplayName: "🇯🇵 JP", Region: "JP"}
	items := []renderItem{{name: nodeA.DisplayName, node: nodeA}, {name: nodeB.DisplayName, node: nodeB}}
	members := map[string][]domain.ProxyGroupMember{
		"A": {{Kind: "node", NodeID: 42}, {Kind: "builtin", Value: "DIRECT"}, {Kind: "node_set", Value: "remaining"}},
		"B": {{Kind: "node", NodeID: 999}}, // references a node this render has no access to
	}
	options := map[string]domain.ProxyGroupOptions{
		"A": {Type: ProxyGroupTypeURLTest},
		"B": {Type: ProxyGroupTypeURLTest},
	}
	raw, err := buildProxyGroupsYAMLWithMembers("- DOMAIN,a,A\n- MATCH,B", nil, members, options, items)
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	byName := map[string]proxyGroup{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	// A: DIRECT stripped, real nodes kept, url-test preserved.
	if a := byName["A"]; a.Type != ProxyGroupTypeURLTest {
		t.Fatalf("group A type = %q, want url-test (%#v)", a.Type, groups)
	}
	assertMemberStrings(t, byName["A"].Proxies, []string{nodeA.DisplayName, nodeB.DisplayName})
	// B: nothing testable resolved for this user → degrade to a plain selector
	// over DIRECT instead of a bogus single-member url-test.
	if b := byName["B"]; b.Type != ProxyGroupTypeSelect {
		t.Fatalf("group B type = %q, want select (%#v)", b.Type, groups)
	}
	assertMemberStrings(t, byName["B"].Proxies, []string{"DIRECT"})
}

func TestSingBoxCustomMembersUseSameOrder(t *testing.T) {
	node := &domain.Node{ID: 42, DisplayName: "China", Region: "CN"}
	configs := map[string][]domain.ProxyGroupMember{
		"🇨🇳 中国大陆": {
			{Kind: "node", NodeID: 42},
			{Kind: "builtin", Value: "DIRECT"},
			{Kind: "node_set", Value: "remaining"},
		},
	}
	out := buildSingBoxSelectorOutboundsWithMembers("- MATCH,🇨🇳 中国大陆", []renderItem{{name: node.DisplayName, node: node}}, nil, configs)
	var selector map[string]any
	for _, item := range out {
		if item["tag"] == "🇨🇳 中国大陆" {
			selector = item
		}
	}
	if selector == nil {
		t.Fatalf("selector missing: %#v", out)
	}
	got, _ := selector["outbounds"].([]string)
	assertMemberStrings(t, got, []string{"China", "direct"})
	if selector["default"] != "China" {
		t.Fatalf("default = %#v", selector["default"])
	}
}

func TestInspectProxyGroupsRejectsCycleAndWarnsWithoutRemaining(t *testing.T) {
	configs := map[string][]domain.ProxyGroupMember{
		"A": {{Kind: "proxy_group", Value: "B"}},
		"B": {{Kind: "proxy_group", Value: "A"}},
	}
	inspection := InspectProxyGroups("- DOMAIN,a,A\n- MATCH,B", configs, nil, nil)
	hasCycle, hasWarning := false, false
	for _, issue := range inspection.Issues {
		if issue.Level == "error" && issue.Message == "代理组引用形成循环" {
			hasCycle = true
		}
		if issue.Level == "warning" {
			hasWarning = true
		}
	}
	if !hasCycle {
		t.Fatalf("expected cycle error: %#v", inspection.Issues)
	}
	if !hasWarning {
		t.Fatalf("expected remaining warning: %#v", inspection.Issues)
	}
}

func TestInspectProxyGroupsWarnsWhenDynamicSetHasNoCurrentMatch(t *testing.T) {
	configs := map[string][]domain.ProxyGroupMember{
		"A": {{Kind: "node_set", Value: "region:CN"}, {Kind: "node_set", Value: "remaining"}},
	}
	inspection := InspectProxyGroups("- MATCH,A", configs, nil, []*domain.Node{{ID: 1, Region: "US", Enabled: true}})
	for _, issue := range inspection.Issues {
		if issue.Code == "empty_node_set" && issue.Level == "warning" {
			return
		}
	}
	t.Fatalf("expected empty_node_set warning: %#v", inspection.Issues)
}

func TestMergeFirstProxyGroupMembersKeepsTemplateRuleSetPrecedence(t *testing.T) {
	dst := map[string][]domain.ProxyGroupMember{
		"A": {{Kind: "builtin", Value: "DIRECT"}},
	}
	src := map[string][]domain.ProxyGroupMember{
		"A": {{Kind: "builtin", Value: "REJECT"}},
		"B": {{Kind: "node_set", Value: "remaining"}},
	}
	duplicates := mergeFirstProxyGroupMembers(dst, src)
	if len(duplicates) != 1 || duplicates[0] != "A" {
		t.Fatalf("duplicates=%#v", duplicates)
	}
	if dst["A"][0].Value != "DIRECT" {
		t.Fatalf("first config was replaced: %#v", dst["A"])
	}
	if dst["B"][0].Value != "remaining" {
		t.Fatalf("new config missing: %#v", dst["B"])
	}
	// The merge deep-copies member slices so cached rule-set values cannot be
	// mutated by a later consumer.
	src["B"][0].Value = "region:CN"
	if dst["B"][0].Value != "remaining" {
		t.Fatalf("merge aliased source slice: %#v", dst["B"])
	}
}

func assertMemberStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
