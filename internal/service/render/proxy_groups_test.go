package render

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRuleTargetsInOrder(t *testing.T) {
	rules := `
- DOMAIN-SUFFIX,example.com,💬 Ai平台
- DOMAIN-SUFFIX,example.org,DIRECT
- IP-CIDR,1.1.1.1/32,🎯 全球直连,no-resolve
- MATCH,🐟 漏网之鱼
- DOMAIN-SUFFIX,duplicate.example,💬 Ai平台
`
	got := ruleTargetsInOrder(rules)
	want := []string{"💬 Ai平台", "🎯 全球直连", "🐟 漏网之鱼"}
	if len(got) != len(want) {
		t.Fatalf("targets len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("target[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildProxyGroupsYAML(t *testing.T) {
	raw, err := buildProxyGroupsYAML("- DOMAIN-SUFFIX,example.com,💬 Ai平台\n- MATCH,🐟 漏网之鱼", nil)
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `\U0001F`) {
		t.Fatalf("proxy groups should keep emoji readable, got:\n%s", raw)
	}
	if len(groups) != 3 {
		t.Fatalf("groups len = %d, want 3: %#v", len(groups), groups)
	}
	if groups[0].Name != "🚀 节点选择" {
		t.Fatalf("first group = %q", groups[0].Name)
	}
	if groups[1].Name != "💬 Ai平台" {
		t.Fatalf("second group = %q", groups[1].Name)
	}
	if got := groups[1].Proxies; len(got) != 3 || got[0] != "🚀 节点选择" || got[1] != "@all" || got[2] != "DIRECT" {
		t.Fatalf("ai group proxies = %#v", got)
	}
	if groups[2].Name != "🐟 漏网之鱼" {
		t.Fatalf("third group = %q", groups[2].Name)
	}
}

func TestBuildProxyGroupsYAMLCustomGroupDefaultsToDirect(t *testing.T) {
	raw, err := buildProxyGroupsYAML(`
- IP-CIDR,fd00::/8,🏠 家里,no-resolve
- MATCH,🐟 漏网之鱼
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	var custom *proxyGroup
	for i := range groups {
		if groups[i].Name == "🏠 家里" {
			custom = &groups[i]
			break
		}
	}
	if custom == nil {
		t.Fatalf("custom group not present in output: %#v", groups)
	}
	want := []string{"DIRECT", "🚀 节点选择", "@all"}
	if len(custom.Proxies) != len(want) {
		t.Fatalf("custom group proxies = %#v, want %#v", custom.Proxies, want)
	}
	for i := range want {
		if custom.Proxies[i] != want[i] {
			t.Fatalf("custom group proxies[%d] = %q, want %q (full=%#v)", i, custom.Proxies[i], want[i], custom.Proxies)
		}
	}
}

func TestBuildProxyGroupsYAMLDomesticServiceGroupHasNodeSelector(t *testing.T) {
	raw, err := buildProxyGroupsYAML(`
- DOMAIN-SUFFIX,bing.com,Ⓜ️ 微软Bing
- DOMAIN-SUFFIX,apple.com,🍎 苹果服务
- MATCH,🐟 漏网之鱼
`, nil)
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
	for _, name := range []string{"Ⓜ️ 微软Bing", "🍎 苹果服务"} {
		g, ok := byName[name]
		if !ok {
			t.Fatalf("expected group %q in output, got %#v", name, byName)
		}
		want := []string{"DIRECT", "🚀 节点选择", "@all"}
		if len(g.Proxies) != len(want) {
			t.Fatalf("%s proxies = %#v, want %#v", name, g.Proxies, want)
		}
		for i := range want {
			if g.Proxies[i] != want[i] {
				t.Fatalf("%s proxies[%d] = %q, want %q (full=%#v)", name, i, g.Proxies[i], want[i], g.Proxies)
			}
		}
	}
}

func TestBuildProxyGroupsYAMLUsesManualDisplayOrder(t *testing.T) {
	raw, err := buildProxyGroupsYAML(`
- DOMAIN-SUFFIX,direct.example,🎯 全球直连
- DOMAIN-SUFFIX,ads.example,🛑 广告拦截
- DOMAIN-SUFFIX,ai.example,💬 Ai平台
- DOMAIN-SUFFIX,game.example,🎮 游戏平台
- MATCH,🐟 漏网之鱼
`, []string{"🚀 节点选择", "💬 Ai平台", "🎮 游戏平台"})
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(groups))
	for i, group := range groups {
		got[i] = group.Name
	}
	want := []string{"🚀 节点选择", "💬 Ai平台", "🎮 游戏平台", "🎯 全球直连", "🛑 广告拦截", "🐟 漏网之鱼"}
	if len(got) != len(want) {
		t.Fatalf("groups len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("group[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}
