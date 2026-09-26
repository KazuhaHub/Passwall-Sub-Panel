package render

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestProxyGroupBuiltinsMatchMihomoSupportedTargets(t *testing.T) {
	want := []string{"DIRECT", "REJECT", "REJECT-DROP", "PASS"}
	if !reflect.DeepEqual(proxyGroupBuiltins, want) {
		t.Fatalf("proxyGroupBuiltins = %#v, want %#v", proxyGroupBuiltins, want)
	}
}

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

func TestResolveConfiguredMembersKeepsRematchMihomoOnly(t *testing.T) {
	members := []domain.ProxyGroupMember{{Kind: "rematch", Value: "AI Rematch"}}
	if got := resolveConfiguredMembers(members, nil); len(got) != 0 {
		t.Fatalf("sing-box member resolution leaked Rematch outbound: %#v", got)
	}
	assertMemberStrings(t, resolveConfiguredMembersWithOutbounds(members, nil), []string{"AI Rematch"})
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

// A group left on its defaults still references other groups, and those edges
// are rendered just like configured ones. ⚡ QUIC控制 delegates to 🎮 UDP控制 by
// default, so pointing 🎮 UDP控制 back at ⚡ QUIC控制 forms a loop that both
// Mihomo and sing-box refuse to load; the validator has to see it.
func TestInspectProxyGroupsRejectsCycleThroughDefaultMembers(t *testing.T) {
	rules := "- AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制\n- NETWORK,udp,🎮 UDP控制\n- MATCH,🚀 节点选择\n"
	configs := map[string][]domain.ProxyGroupMember{
		"🎮 UDP控制": {{Kind: "proxy_group", Value: "⚡ QUIC控制"}, {Kind: "builtin", Value: "DIRECT"}},
	}
	inspection := InspectProxyGroups(rules, configs, nil, nil)
	hasCycle := false
	for _, issue := range inspection.Issues {
		if issue.Level == "error" && issue.Code == "cycle" {
			hasCycle = true
		}
	}
	if !hasCycle {
		t.Fatalf("expected cycle error through the QUIC default: %#v", inspection.Issues)
	}

	// The shipped defaults alone must stay loop-free, or every unconfigured
	// rule set would now fail validation.
	for _, content := range []string{rules, readTemplateContent(t, "../../seed/files/rulesets/default-rules.yaml")} {
		for _, issue := range InspectProxyGroups(content, nil, nil, nil).Issues {
			if issue.Code == "cycle" {
				t.Fatalf("defaults must not form a cycle: %#v", issue)
			}
		}
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

func TestNormalizeProxyGroupMetadataRemovesOrphansWithoutMutatingInput(t *testing.T) {
	interval := 60
	order := []string{"Keep", "📣 谷歌FCM", "🚀 节点选择"}
	members := map[string][]domain.ProxyGroupMember{
		"Keep":    {{Kind: "proxy_group", Value: "🚀 节点选择"}},
		"📣 谷歌FCM": {{Kind: "builtin", Value: "DIRECT"}},
	}
	options := map[string]domain.ProxyGroupOptions{
		"Keep":    {Type: ProxyGroupTypeURLTest, Interval: &interval},
		"📣 谷歌FCM": {Type: ProxyGroupTypeFallback},
	}

	got := NormalizeProxyGroupMetadata("- MATCH,Keep", order, members, options)
	if !reflect.DeepEqual(got.Removed, []string{"📣 谷歌FCM"}) {
		t.Fatalf("removed=%#v", got.Removed)
	}
	if !reflect.DeepEqual(got.Order, []string{"Keep", "🚀 节点选择"}) {
		t.Fatalf("order=%#v", got.Order)
	}
	if _, ok := got.Members["📣 谷歌FCM"]; ok {
		t.Fatalf("orphan members survived: %#v", got.Members)
	}
	if _, ok := got.Options["📣 谷歌FCM"]; ok {
		t.Fatalf("orphan options survived: %#v", got.Options)
	}
	if got.Members["Keep"][0].Value != "🚀 节点选择" || got.Options["Keep"].Interval == nil || *got.Options["Keep"].Interval != 60 {
		t.Fatalf("valid metadata changed: %#v %#v", got.Members, got.Options)
	}

	got.Members["Keep"][0].Value = "changed"
	*got.Options["Keep"].Interval = 30
	if members["Keep"][0].Value != "🚀 节点选择" || *options["Keep"].Interval != 60 {
		t.Fatalf("normalization mutated or aliased input: %#v %#v", members, options)
	}
}

func TestNormalizeProxyGroupMetadataDoesNotKeepDependencyFromOrphanOwner(t *testing.T) {
	members := map[string][]domain.ProxyGroupMember{
		"Removed": {{Kind: "proxy_group", Value: "🚀 节点选择"}},
		"🚀 节点选择":  {{Kind: "builtin", Value: "DIRECT"}},
	}
	got := NormalizeProxyGroupMetadata("- MATCH,DIRECT", []string{"Removed", "🚀 节点选择"}, members, nil)
	if len(got.Order) != 0 || len(got.Members) != 0 {
		t.Fatalf("orphan dependency survived: %#v", got)
	}
	if !reflect.DeepEqual(got.Removed, []string{"Removed", "🚀 节点选择"}) {
		t.Fatalf("removed=%#v", got.Removed)
	}
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
