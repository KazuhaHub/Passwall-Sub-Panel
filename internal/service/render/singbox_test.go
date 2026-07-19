package render

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	yaml "gopkg.in/yaml.v3"
)

// TestBuildSingBoxHysteria2Outbound checks the sing-box outbound JSON
// shape per https://sing-box.sagernet.org/configuration/outbound/hysteria2/.
// Mandatory: type, server, server_port, password. Optional: obfs object,
// tls{enabled, server_name, alpn, insecure}.
func TestBuildSingBoxHysteria2Outbound(t *testing.T) {
	got := buildSingBoxHysteria2Outbound("hy2-us-1", "node.example.com", 8443, "secret-pwd", hysteria2Opts{
		SNI:          "node.example.com",
		ObfsType:     "salamander",
		ObfsPassword: "obfs-secret",
		ALPN:         []string{"h3"},
		Insecure:     false,
	})
	want := map[string]any{
		"tag":         "hy2-us-1",
		"type":        "hysteria2",
		"server":      "node.example.com",
		"server_port": 8443,
		"password":    "secret-pwd",
		"obfs": map[string]any{
			"type":     "salamander",
			"password": "obfs-secret",
		},
		"tls": map[string]any{
			"enabled":     true,
			"server_name": "node.example.com",
			"alpn":        []string{"h3"},
			"insecure":    false,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestBuildSingBoxHysteria2Outbound_NoObfs: when obfs isn't configured,
// the "obfs" key MUST be omitted (sing-box treats it as enabled-with-
// empty-password otherwise).
func TestBuildSingBoxHysteria2Outbound_NoObfs(t *testing.T) {
	got := buildSingBoxHysteria2Outbound("x", "1.2.3.4", 443, "p", hysteria2Opts{Insecure: true})
	if _, ok := got["obfs"]; ok {
		t.Fatalf("obfs key should be absent: %#v", got)
	}
	tls := got["tls"].(map[string]any)
	if tls["insecure"] != true {
		t.Fatalf("tls.insecure = %v, want true", tls["insecure"])
	}
}

func TestEmitSingBoxSUIModernOutbounds(t *testing.T) {
	node := &domain.Node{ID: 1, DisplayName: "edge", ServerAddress: "edge.example.com"}
	user := &domain.User{UUID: "uuid-1"}
	stream := `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"sni.example.com","alpn":["h3"],"settings":{"fingerprint":"chrome"}}}`

	tests := []struct {
		protocol string
		settings string
		email    string
		check    func(*testing.T, map[string]any)
	}{
		{"anytls", `{}`, "u1@psp.local", func(t *testing.T, got map[string]any) {
			if got["type"] != "anytls" || got["password"] != "uuid-1" {
				t.Fatalf("AnyTLS outbound = %#v", got)
			}
		}},
		{"tuic", `{"congestion_control":"bbr","zero_rtt_handshake":true,"heartbeat":"9s"}`, "u1@psp.local", func(t *testing.T, got map[string]any) {
			if got["type"] != "tuic" || got["uuid"] != "uuid-1" || got["congestion_control"] != "bbr" || got["zero_rtt_handshake"] != true {
				t.Fatalf("TUIC outbound = %#v", got)
			}
		}},
		{"naive", `{"quic_congestion_control":"bbr2_variant"}`, "actual@psp.local", func(t *testing.T, got map[string]any) {
			if got["type"] != "naive" || got["username"] != "actual@psp.local" || got["quic"] != true || got["quic_congestion_control"] != "bbr2" {
				t.Fatalf("Naive outbound = %#v", got)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.protocol, func(t *testing.T) {
			got, err := emitSingBoxOutbound("edge", node, user, &ports.Inbound{
				Port: 443, Protocol: tc.protocol, Settings: tc.settings, StreamSettings: stream,
			}, tc.email, nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, got)
			tls, ok := got["tls"].(map[string]any)
			if !ok || tls["server_name"] != "sni.example.com" || tls["utls"] == nil {
				t.Fatalf("TLS = %#v", got["tls"])
			}
		})
	}
}

// TestBuildSingBoxRouteRules_NetworkUDP pins the NETWORK,udp translation: it
// becomes a sing-box route rule matching network=udp that routes to the named
// selector (the 🎮 UDP控制 catch-all). Without the NETWORK case the rule would
// be silently dropped for sing-box.
func TestBuildSingBoxRouteRules_NetworkUDP(t *testing.T) {
	rules, _ := buildSingBoxRouteRules("- NETWORK,udp,🎮 UDP控制\n- MATCH,DIRECT\n")
	// rules[0] sniff, rules[1] the built-in web-QUIC reject (higher priority);
	// the NETWORK,udp catch-all route follows.
	if len(rules) < 3 {
		t.Fatalf("want sniff + quic-reject + udp rule, got %#v", rules)
	}
	if rules[1]["action"] != "reject" {
		t.Fatalf("rules[1] should be the quic reject (higher priority than UDP控制): %#v", rules[1])
	}
	var udp map[string]any
	for _, r := range rules {
		if r["action"] == "route" && r["outbound"] == "🎮 UDP控制" {
			udp = r
		}
	}
	if udp == nil {
		t.Fatalf("no route rule to 🎮 UDP控制: %#v", rules)
	}
	if udp["network"] != "udp" {
		t.Fatalf("udp rule network = %v, want udp: %#v", udp["network"], udp)
	}
}

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
	// rules[0] is the sniff action prepended for the sing-box >= 1.11
	// inbound-field migration. After that: 4 parsed entries — GEOIP,CN is
	// now mapped to a rule_set reference (geoip-cn) instead of being
	// dropped, so the CN routing it expresses survives on sing-box 1.12+.
	if len(rules) != 6 {
		t.Fatalf("rules len = %d, want 6 (sniff + quic-reject + 4): %#v", len(rules), rules)
	}
	if got := rules[0]["action"]; got != "sniff" {
		t.Fatalf("rules[0] action = %q, want sniff", got)
	}
	// rules[1] is the built-in web-QUIC (UDP 443) reject, highest priority.
	if rules[1]["action"] != "reject" || rules[1]["network"] != "udp" {
		t.Fatalf("rules[1] should be the udp/443 quic reject: %#v", rules[1])
	}
	if got := rules[2]["outbound"]; got != "🚀 节点选择" {
		t.Fatalf("domain suffix outbound = %q", got)
	}
	if got := rules[3]["outbound"]; got != "block" {
		t.Fatalf("reject outbound = %q", got)
	}
	if _, ok := rules[4]["ip_cidr"]; !ok {
		t.Fatalf("ip-cidr rule missing ip_cidr: %#v", rules[4])
	}
	rs, ok := rules[5]["rule_set"].([]string)
	if !ok || len(rs) != 1 || rs[0] != "geoip-cn" {
		t.Fatalf("GEOIP,CN should map to rule_set [geoip-cn]: %#v", rules[5])
	}
	if got := rules[5]["outbound"]; got != "🇨🇳 中国大陆" {
		t.Fatalf("geoip rule_set outbound = %q, want 🇨🇳 中国大陆", got)
	}
	// Routing rules (after sniff + the quic-reject) carry the explicit
	// "action":"route" — the canonical sing-box form.
	for i := 2; i < len(rules); i++ {
		if rules[i]["action"] != "route" {
			t.Fatalf("rules[%d] must carry action:route (canonical), got %#v", i, rules[i])
		}
	}
}

// GEOSITE maps to a geosite-<category> rule_set; attribute-filtered categories
// (microsoft@cn) have no standalone .srs and must still be dropped to keep the
// rendered config downloadable.
func TestBuildSingBoxRouteRulesGeositeMapsToRuleSet(t *testing.T) {
	rules, _ := buildSingBoxRouteRules(`
- GEOSITE,geolocation-cn,🇨🇳 中国大陆
- GEOSITE,microsoft@cn,🇨🇳 中国大陆
- MATCH,🐟 漏网之鱼
`)
	// sniff + quic-reject + geolocation-cn only; the @cn attribute rule is dropped.
	if len(rules) != 3 {
		t.Fatalf("rules len = %d, want 3 (sniff + quic-reject + geolocation-cn; @cn dropped): %#v", len(rules), rules)
	}
	rs, ok := rules[2]["rule_set"].([]string)
	if !ok || len(rs) != 1 || rs[0] != "geosite-geolocation-cn" {
		t.Fatalf("GEOSITE,geolocation-cn should map to rule_set [geosite-geolocation-cn]: %#v", rules[2])
	}
}

func TestBuildSingBoxRuleSetDefs(t *testing.T) {
	defs := buildSingBoxRuleSetDefs([]string{"geoip-cn", "geosite-geolocation-cn", "geosite-geolocation-!cn"}, "🚀 节点选择")
	if len(defs) != 3 {
		t.Fatalf("defs len = %d, want 3: %#v", len(defs), defs)
	}
	byTag := map[string]map[string]any{}
	for _, d := range defs {
		byTag[d["tag"].(string)] = d
	}
	cases := map[string]string{
		"geoip-cn":                "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs",
		"geosite-geolocation-cn":  "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-cn.srs",
		"geosite-geolocation-!cn": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs",
	}
	for tag, url := range cases {
		d, ok := byTag[tag]
		if !ok {
			t.Fatalf("missing def for %q: %#v", tag, defs)
		}
		if d["type"] != "remote" || d["format"] != "binary" {
			t.Fatalf("def %q type/format = %v/%v, want remote/binary", tag, d["type"], d["format"])
		}
		if d["url"] != url {
			t.Fatalf("def %q url = %q, want %q", tag, d["url"], url)
		}
		// Downloads route through the proxy via download_detour — the field
		// the current STABLE line (1.13.x) understands. http_client is
		// 1.14-alpha-only, and sing-box's strict DisallowUnknownFields parser
		// makes an unknown http_client key a FATAL load error on stable.
		if d["download_detour"] != "🚀 节点选择" {
			t.Fatalf("def %q download_detour = %q, want 🚀 节点选择", tag, d["download_detour"])
		}
		if _, fut := d["http_client"]; fut {
			t.Fatalf("def %q must not carry http_client (1.14-alpha only; breaks stable 1.13.x)", tag)
		}
	}
}

func TestCollectSingBoxRuleSetRefs(t *testing.T) {
	body := `{
	  "dns": {"rules": [
	    {"rule_set": ["geosite-geolocation-cn"], "server": "alidns"},
	    {"rule_set": ["geosite-geolocation-!cn"], "server": "cf"}
	  ]},
	  "route": {
	    "rule_set": [],
	    "rules": [
	      {"rule_set": ["geoip-cn"], "outbound": "x"},
	      {"rule_set": ["geosite-geolocation-cn"], "outbound": "x"}
	    ]
	  }
	}`
	tags, err := collectSingBoxRuleSetRefs(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"geoip-cn", "geosite-geolocation-!cn", "geosite-geolocation-cn"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags = %#v, want %#v (sorted, deduped)", tags, want)
	}
}

// DST-PORT ranges must be emitted with sing-box's colon syntax ("8000:9000"),
// not Clash's hyphen ("8000-9000") which sing-box rejects as an invalid
// port_range. Single ports still go to `port`.
func TestBuildSingBoxRouteRules_DSTPortRangeUsesColon(t *testing.T) {
	rules, _ := buildSingBoxRouteRules(`
- DST-PORT,8000-9000,🚀 节点选择
- DST-PORT,443,🎯 全球直连
`)
	var rangeRule, singleRule map[string]any
	for _, r := range rules {
		if _, ok := r["port_range"]; ok {
			rangeRule = r
		}
		if _, ok := r["port"]; ok {
			singleRule = r
		}
	}
	if rangeRule == nil {
		t.Fatalf("no port_range rule emitted: %#v", rules)
	}
	pr, ok := rangeRule["port_range"].([]string)
	if !ok || len(pr) != 1 || pr[0] != "8000:9000" {
		t.Fatalf("port_range = %#v, want [8000:9000] (colon, not hyphen)", rangeRule["port_range"])
	}
	if singleRule == nil {
		t.Fatalf("single DST-PORT must use `port`: %#v", rules)
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
	// rules[0] sniff, rules[1] the built-in web-QUIC reject; the personal rule
	// follows them before MATCH terminates the loop.
	if len(rules) != 3 {
		t.Fatalf("rules len = %d, want 3 (sniff + quic-reject + personal): %#v", len(rules), rules)
	}
	if got := rules[0]["action"]; got != "sniff" {
		t.Fatalf("rules[0] action = %q, want sniff", got)
	}
	if rules[1]["action"] != "reject" {
		t.Fatalf("rules[1] should be the quic reject: %#v", rules[1])
	}
	if got := rules[2]["outbound"]; got != "💬 Ai平台" {
		t.Fatalf("personal rule outbound = %q", got)
	}
}

func TestBuildSingBoxSelectorOutbounds(t *testing.T) {
	raw := `
- DOMAIN-SUFFIX,example.com,💬 Ai平台
- MATCH,🐟 漏网之鱼
`
	selectors := buildSingBoxSelectorOutbounds(raw, []string{"node-a", "node-b"}, nil)
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

func TestBuildSingBoxSelectorOutboundsUsesManualDisplayOrder(t *testing.T) {
	raw := `
- DOMAIN-SUFFIX,direct.example,🎯 全球直连
- DOMAIN-SUFFIX,ads.example,🛑 广告拦截
- DOMAIN-SUFFIX,ai.example,💬 Ai平台
- DOMAIN-SUFFIX,game.example,🎮 游戏平台
- MATCH,🐟 漏网之鱼
`
	selectors := buildSingBoxSelectorOutbounds(raw, []string{"node-a"}, []string{"🚀 节点选择", "💬 Ai平台", "🎮 游戏平台"})
	got := make([]string, len(selectors))
	for i, selector := range selectors {
		got[i] = selector["tag"].(string)
	}
	want := []string{"🚀 节点选择", "💬 Ai平台", "🎮 游戏平台", "🎯 全球直连", "🛑 广告拦截", "🐟 漏网之鱼"}
	if len(got) != len(want) {
		t.Fatalf("selectors len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selector[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
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

// TestSeedSingBoxRuleSetReferentialIntegrity renders the shipped sing-box seed
// template end-to-end (outbounds + route rules + dynamic rule_set assembly) and
// asserts the invariant sing-box itself enforces: EVERY rule_set referenced in
// dns.rules or route.rules must have a matching definition in route.rule_set.
// This is the drift guard tying the template's DNS split rules and singbox.go's
// route-rule mapping to the dynamic rule_set emission — rename or add a rule_set
// in either place without wiring its definition and this goes red, not the user.
func TestSeedSingBoxRuleSetReferentialIntegrity(t *testing.T) {
	raw, err := os.ReadFile("../../seed/files/templates/default-sing-box.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Content string `yaml:"content"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("seed doc not valid YAML: %v", err)
	}

	rules, final := buildSingBoxRouteRules(`
- DOMAIN-SUFFIX,example.com,🚀 节点选择
- GEOSITE,geolocation-cn,🇨🇳 中国大陆
- GEOIP,CN,🇨🇳 中国大陆
- MATCH,🐟 漏网之鱼
`)
	outbounds := []map[string]any{
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
		{"type": "vless", "tag": "🇯🇵 JP-01", "server": "a.example.com", "server_port": 443, "uuid": "x"},
		{"type": "selector", "tag": "🚀 节点选择", "outbounds": []string{"🇯🇵 JP-01", "direct"}},
		{"type": "selector", "tag": "🇨🇳 中国大陆", "outbounds": []string{"direct"}},
		{"type": "selector", "tag": "🐟 漏网之鱼", "outbounds": []string{"🚀 节点选择", "direct"}},
	}
	outboundsJSON, _ := marshalJSONBlock(outbounds)
	rulesJSON, _ := marshalJSONBlock(rules)
	finalJSON, _ := marshalJSONString(final)

	body := substituteBlockPlaceholders(doc.Content, map[string]string{
		"outbounds":   outboundsJSON,
		"route_rules": rulesJSON,
	})
	body = substituteInlinePlaceholders(body, map[string]string{"final_outbound": finalJSON})
	body, err = assembleSingBoxRuleSets(body, "🚀 节点选择")
	if err != nil {
		t.Fatalf("assembleSingBoxRuleSets: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("rendered sing-box config not valid JSON: %v\n---\n%s", err, body)
	}

	// Every referenced rule_set tag must be defined in route.rule_set.
	refs, err := collectSingBoxRuleSetRefs(body)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := cfg["route"].(map[string]any)
	defArr, _ := route["rule_set"].([]any)
	defined := map[string]bool{}
	detours := map[string]bool{}
	for _, d := range defArr {
		dm, _ := d.(map[string]any)
		if tag, ok := dm["tag"].(string); ok {
			defined[tag] = true
		}
		if dt, ok := dm["download_detour"].(string); ok {
			detours[dt] = true
		}
	}
	for _, ref := range refs {
		if !defined[ref] {
			t.Fatalf("rule_set %q referenced but not defined in route.rule_set (defined=%v)", ref, defined)
		}
	}
	// The CN-routing + split-DNS rule_sets must all be present.
	for _, want := range []string{"geoip-cn", "geosite-geolocation-cn", "geosite-geolocation-!cn"} {
		found := false
		for _, ref := range refs {
			if ref == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected rule_set %q to be referenced (refs=%v)", want, refs)
		}
	}
	// download_detour on every definition must name a real outbound.
	outTags := map[string]bool{}
	for _, o := range outbounds {
		outTags[o["tag"].(string)] = true
	}
	for dt := range detours {
		if !outTags[dt] {
			t.Fatalf("download_detour %q is not a defined outbound", dt)
		}
	}

	// Canonical-form guard: every dns.rules and route.rules entry must carry an
	// explicit "action" (no bare server/outbound shorthand, which deprecation-
	// warns on current sing-box). Drift here means a rule slipped back to the
	// legacy form in the template or the generator.
	assertAllRulesHaveAction := func(where string, arr []any) {
		for i, r := range arr {
			rm, _ := r.(map[string]any)
			if _, ok := rm["action"].(string); !ok {
				t.Fatalf("%s[%d] missing explicit action (legacy shorthand): %#v", where, i, rm)
			}
		}
	}
	dnsBlock, _ := cfg["dns"].(map[string]any)
	dnsRules, _ := dnsBlock["rules"].([]any)
	assertAllRulesHaveAction("dns.rules", dnsRules)
	routeRules, _ := route["rules"].([]any)
	assertAllRulesHaveAction("route.rules", routeRules)

	// DNS-server referential integrity: every dns.rules[].server, dns.final,
	// and per-server domain_resolver must name a DEFINED dns.servers tag. A
	// dangling reference (typo, renamed/removed server) is a load error in
	// sing-box, so guard it here.
	dnsServers, _ := dnsBlock["servers"].([]any)
	serverTags := map[string]bool{}
	for _, s := range dnsServers {
		if sm, ok := s.(map[string]any); ok {
			if tag, ok := sm["tag"].(string); ok {
				serverTags[tag] = true
			}
		}
	}
	assertServerRef := func(what, tag string) {
		if tag != "" && !serverTags[tag] {
			t.Fatalf("%s references undefined dns server %q (defined=%v)", what, tag, serverTags)
		}
	}
	for _, r := range dnsRules {
		if rm, ok := r.(map[string]any); ok {
			if srv, ok := rm["server"].(string); ok {
				assertServerRef("a dns.rule", srv)
			}
		}
	}
	if f, ok := dnsBlock["final"].(string); ok {
		assertServerRef("dns.final", f)
	}
	for _, s := range dnsServers {
		if sm, ok := s.(map[string]any); ok {
			if dr, ok := sm["domain_resolver"].(string); ok {
				assertServerRef("server "+sm["tag"].(string)+".domain_resolver", dr)
			}
		}
	}
}
