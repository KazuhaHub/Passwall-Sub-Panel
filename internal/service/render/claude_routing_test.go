package render

import (
	"net/netip"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Exercise the shipped rules through both subscription formats. The inputs
// must match before external Geo rules, so no downloaded Geo database is needed.
func TestSeedClaudeRoutingRespectsGlobalQUICAndKeepsAITCPExit(t *testing.T) {
	common := readTemplateContent(t, "../../seed/files/rulesets/default-rules.yaml")
	for _, personal := range []string{"", "- DOMAIN,clau.de,DIRECT"} {
		body := substituteBlockPlaceholders(readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml"), map[string]string{
			"proxies": "[]", "proxy_groups": "[]", "rules_personal": personal,
			"rules_common": common, "sub_rules": "{}",
		})
		var mihomo struct {
			Rules []string `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(body), &mihomo); err != nil {
			t.Fatal(err)
		}
		singbox, _ := buildSingBoxRouteRules(personal, common)
		for _, network := range []string{"tcp", "udp"} {
			for _, tc := range []struct{ host, ip, want string }{
				{"clau.de", "203.0.113.10", "💬 Ai平台"},
				{"links.clau.de", "203.0.113.10", "💬 Ai平台"},
				{"claude.ai", "203.0.113.10", "💬 Ai平台"},
				{"claude.com", "203.0.113.10", "💬 Ai平台"},
				{"api.anthropic.com", "203.0.113.10", "💬 Ai平台"},
				{"files.claudeusercontent.com", "203.0.113.10", "💬 Ai平台"},
				{"", "160.79.104.0", "💬 Ai平台"},
				{"", "160.79.104.10", "💬 Ai平台"},
				{"", "160.79.105.255", "💬 Ai平台"},
				{"", "2607:6bc0::10", "💬 Ai平台"},
				{"", "2607:6bc0:0:ffff:ffff:ffff:ffff:ffff", "💬 Ai平台"},
				{"", "192.168.1.1", "🎯 全球直连"},
			} {
				t.Run(network+"/"+tc.host+tc.ip+"/personal="+personal, func(t *testing.T) {
					want := tc.want
					if network == "udp" && want == "💬 Ai平台" {
						want = "⚡ QUIC控制"
					}
					if personal != "" && tc.host == "clau.de" {
						want = "DIRECT"
					}
					assertClaudeFirstRoute(t, mihomo.Rules, singbox, tc.host, tc.ip, network, want)
				})
			}
		}
		// The outbound /21 and unrelated domains must retain the general QUIC policy.
		for _, tc := range []struct{ host, ip string }{
			{"notclau.de", "203.0.113.10"}, {"clau.de.example.com", "203.0.113.10"},
			{"", "160.79.103.255"}, {"", "160.79.106.0"}, {"", "2607:6bc0:1::1"},
		} {
			assertClaudeFirstRoute(t, mihomo.Rules, singbox, tc.host, tc.ip, "udp", "⚡ QUIC控制")
		}
	}
}

func assertClaudeFirstRoute(t *testing.T, mihomo []string, singbox []map[string]any, host, ip, network, want string) {
	t.Helper()
	address := netip.MustParseAddr(ip)
	matches := func(kind, value string) bool {
		switch kind {
		case "DOMAIN":
			return host == value
		case "DOMAIN-SUFFIX":
			return host == value || strings.HasSuffix(host, "."+value)
		case "DOMAIN-KEYWORD":
			return strings.Contains(host, value)
		case "IP-CIDR", "IP-CIDR6":
			return netip.MustParsePrefix(value).Contains(address)
		case "NETWORK":
			return strings.EqualFold(network, value)
		case "AND":
			return value == "((NETWORK,UDP),(DST-PORT,443))" && network == "udp"
		}
		return false
	}
	gotMihomo, gotSingbox := "", ""
	for _, line := range mihomo {
		kind, value, target, ok := parseClashRuleLine(line)
		if ok && matches(kind, value) {
			gotMihomo = target
			break
		}
	}
	for _, rule := range singbox {
		matched := false
		for _, field := range []struct{ key, kind string }{
			{"domain", "DOMAIN"}, {"domain_suffix", "DOMAIN-SUFFIX"},
			{"domain_keyword", "DOMAIN-KEYWORD"}, {"ip_cidr", "IP-CIDR"},
		} {
			if values, ok := rule[field.key].([]string); ok {
				for _, value := range values {
					matched = matched || matches(field.kind, value)
				}
			}
		}
		if net, ok := rule["network"].(string); ok {
			matched = network == net
		}
		if ports, ok := rule["port"].([]int); ok {
			portMatch := false
			for _, port := range ports {
				portMatch = portMatch || port == 443
			}
			matched = matched && portMatch
		}
		if matched {
			gotSingbox, _ = rule["outbound"].(string)
			break
		}
	}
	if gotMihomo != want || gotSingbox != singBoxOutboundTag(want) {
		t.Fatalf("%s %s %s: mihomo=%s sing-box=%s, want %s", network, host, ip, gotMihomo, gotSingbox, want)
	}
}
