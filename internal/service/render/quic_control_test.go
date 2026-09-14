package render

import (
	"os"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestSeedRulesSeparateQUICFromGeneralUDP(t *testing.T) {
	rules := readTemplateContent(t, "../../seed/files/rulesets/default-rules.yaml")
	private := strings.Index(rules, "IP-CIDR6,fd00::/8,🎯 全球直连")
	quic := strings.Index(rules, "AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制")
	udp := strings.Index(rules, "NETWORK,udp,🎮 UDP控制")
	if private < 0 || quic <= private || udp <= quic {
		t.Fatalf("private/QUIC/UDP rules are missing or out of order: private=%d quic=%d udp=%d", private, quic, udp)
	}
	if strings.Contains(rules, "GEOSITE,geolocation-cn,DIRECT") || strings.Contains(rules, "GEOIP,CN,DIRECT") {
		t.Fatal("QUIC policy must not hard-code a country-specific direct route")
	}

	template := readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml")
	if strings.Contains(template, "DST-PORT,443)),REJECT") {
		t.Fatal("mihomo template must not preempt the user-selectable QUIC policy")
	}
	personal := strings.Index(template, "{{ rules_personal }}")
	common := strings.Index(template, "{{ rules_common }}")
	if personal < 0 || common <= personal {
		t.Fatal("personal rules must remain ahead of the shared QUIC/UDP controls")
	}
}

func TestSeedUDPControlsDefaultToDirectAndDisplayUDPBeforeQUIC(t *testing.T) {
	body, err := os.ReadFile("../../seed/files/rulesets/default-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		Content         string   `yaml:"content"`
		ProxyGroupOrder []string `yaml:"proxy_group_order"`
	}
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	items := []renderItem{{name: "test-proxy", node: &domain.Node{ID: 1, DisplayName: "test-proxy"}}}
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{name: "seed order", order: defaults.ProxyGroupOrder},
		{name: "renderer fallback order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := buildProxyGroupsYAMLWithMembers(defaults.Content, tc.order, nil, nil, items)
			if err != nil {
				t.Fatal(err)
			}
			var groups []proxyGroup
			if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
				t.Fatal(err)
			}
			if len(groups) < 3 {
				t.Fatalf("missing control groups: %#v", groups)
			}
			assertMemberStrings(t, []string{groups[0].Name, groups[1].Name, groups[2].Name}, []string{"🚀 节点选择", "🎮 UDP控制", "⚡ QUIC控制"})
			assertMemberStrings(t, groups[1].Proxies, []string{"DIRECT", "🚀 节点选择", "REJECT"})
			assertMemberStrings(t, groups[2].Proxies, []string{"🎮 UDP控制", "🚀 节点选择", "DIRECT", "REJECT"})

			// Both formats use the same default and preserve the independent
			// selectors; do not disable UDP support on the actual proxy nodes.
			outbounds := buildSingBoxSelectorOutboundsWithMembers(defaults.Content, items, tc.order, nil)
			if len(outbounds) < 3 {
				t.Fatalf("missing sing-box control groups: %#v", outbounds)
			}
			for i, name := range []string{"🚀 节点选择", "🎮 UDP控制", "⚡ QUIC控制"} {
				if outbounds[i]["tag"] != name {
					t.Fatalf("sing-box group[%d] = %#v, want %s", i, outbounds[i], name)
				}
			}
			assertMemberStrings(t, outbounds[1]["outbounds"].([]string), []string{"direct", "🚀 节点选择", "block"})
			if outbounds[1]["default"] != "direct" || outbounds[2]["default"] != "🎮 UDP控制" {
				t.Fatalf("sing-box defaults must delegate QUIC to direct UDP: %#v", outbounds[:3])
			}
		})
	}
}

func TestUDPControlExplicitMembersAndOrderArePreserved(t *testing.T) {
	rules := "- AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制\n- NETWORK,udp,🎮 UDP控制\n- MATCH,🚀 节点选择\n"
	order := []string{"⚡ QUIC控制", "🚀 节点选择", "🎮 UDP控制"}
	items := []renderItem{{name: "test-proxy", node: &domain.Node{ID: 1, DisplayName: "test-proxy"}}}
	for _, tc := range []struct {
		name         string
		udp, quic    domain.ProxyGroupMember
		udpExpected  string
		quicExpected string
	}{
		{
			name:        "proxy UDP with rejected QUIC",
			udp:         domain.ProxyGroupMember{Kind: "proxy_group", Value: "🚀 节点选择"},
			quic:        domain.ProxyGroupMember{Kind: "builtin", Value: "REJECT"},
			udpExpected: "🚀 节点选择", quicExpected: "REJECT",
		},
		{
			name:        "rejected UDP with proxied QUIC",
			udp:         domain.ProxyGroupMember{Kind: "builtin", Value: "REJECT"},
			quic:        domain.ProxyGroupMember{Kind: "proxy_group", Value: "🚀 节点选择"},
			udpExpected: "REJECT", quicExpected: "🚀 节点选择",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := map[string][]domain.ProxyGroupMember{
				"🎮 UDP控制":  {tc.udp},
				"⚡ QUIC控制": {tc.quic},
			}
			raw, err := buildProxyGroupsYAMLWithMembers(rules, order, members, nil, items)
			if err != nil {
				t.Fatal(err)
			}
			var groups []proxyGroup
			if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
				t.Fatal(err)
			}
			if len(groups) != 3 {
				t.Fatalf("unexpected groups: %#v", groups)
			}
			assertMemberStrings(t, []string{groups[0].Name, groups[1].Name, groups[2].Name}, order)
			assertMemberStrings(t, groups[0].Proxies, []string{tc.quicExpected})
			assertMemberStrings(t, groups[2].Proxies, []string{tc.udpExpected})
			outbounds := buildSingBoxSelectorOutboundsWithMembers(rules, items, order, members)
			if len(outbounds) != 3 {
				t.Fatalf("unexpected sing-box groups: %#v", outbounds)
			}
			for i, name := range order {
				if outbounds[i]["tag"] != name {
					t.Fatalf("sing-box custom order changed: %#v", outbounds)
				}
			}
			if outbounds[0]["default"] != singBoxOutboundTag(tc.quicExpected) || outbounds[2]["default"] != singBoxOutboundTag(tc.udpExpected) {
				t.Fatalf("sing-box custom choices changed: %#v", outbounds)
			}
		})
	}
}
