package render

import (
	"encoding/json"
	"testing"
)

func TestBuildSingBoxRouteRules(t *testing.T) {
	rules, final := buildSingBoxRouteRules(`
- DOMAIN-SUFFIX,example.com,🚀 节点选择
- DOMAIN,ads.example,REJECT
- IP-CIDR,1.1.1.1/32,🎯 全球直连,no-resolve
- GEOIP,CN,🇨🇳 中国大陆
- MATCH,🐟 漏网之鱼
`)
	if final != "🐟 漏网之鱼" {
		t.Fatalf("final = %q, want 漏网之鱼", final)
	}
	if len(rules) != 4 {
		t.Fatalf("rules len = %d, want 4: %#v", len(rules), rules)
	}
	if got := rules[0]["outbound"]; got != "🚀 节点选择" {
		t.Fatalf("domain suffix outbound = %q", got)
	}
	if got := rules[1]["outbound"]; got != "block" {
		t.Fatalf("reject outbound = %q", got)
	}
	if _, ok := rules[2]["ip_cidr"]; !ok {
		t.Fatalf("ip-cidr rule missing ip_cidr: %#v", rules[2])
	}
	if got := rules[3]["geoip"].([]string)[0]; got != "cn" {
		t.Fatalf("geoip = %q, want cn", got)
	}
}

func TestBuildSingBoxRouteRulesPersonalRulesFirst(t *testing.T) {
	personal := `
- DOMAIN-SUFFIX,example.com,💬 Ai平台
- MATCH,🎯 全球直连
`
	common := `
- DOMAIN-SUFFIX,example.com,🚀 节点选择
- MATCH,🐟 漏网之鱼
`
	rules, final := buildSingBoxRouteRules(personal, common)
	if final != "🎯 全球直连" {
		t.Fatalf("final = %q, want personal MATCH target", final)
	}
	if len(rules) != 1 {
		t.Fatalf("rules len = %d, want only personal rule before MATCH: %#v", len(rules), rules)
	}
	if got := rules[0]["outbound"]; got != "💬 Ai平台" {
		t.Fatalf("personal rule outbound = %q", got)
	}
}

func TestBuildSingBoxSelectorOutbounds(t *testing.T) {
	raw := `
- DOMAIN-SUFFIX,example.com,💬 Ai平台
- MATCH,🐟 漏网之鱼
`
	selectors := buildSingBoxSelectorOutbounds(raw, []string{"node-a", "node-b"})
	if len(selectors) != 3 {
		t.Fatalf("selectors len = %d, want 3: %#v", len(selectors), selectors)
	}
	if selectors[0]["tag"] != "🚀 节点选择" {
		t.Fatalf("first selector = %q", selectors[0]["tag"])
	}
	choices := selectors[1]["outbounds"].([]string)
	if len(choices) != 4 || choices[0] != "🚀 节点选择" || choices[1] != "node-a" || choices[3] != "direct" {
		t.Fatalf("ai selector choices = %#v", choices)
	}
}

func TestMarshalJSONBlockProducesValidReadableJSON(t *testing.T) {
	raw, err := marshalJSONBlock([]map[string]any{
		{"type": "selector", "tag": "🚀 节点选择", "outbounds": []string{"direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0]["tag"] != "🚀 节点选择" {
		t.Fatalf("decoded tag = %q", decoded[0]["tag"])
	}
}
