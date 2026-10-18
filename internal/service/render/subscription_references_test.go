package render

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestPruneMihomoLinesUsesSemanticReferences(t *testing.T) {
	base := `# keep template comments
proxies:
  - {name: A, type: vless, server: original.example, uuid: original}
  - {name: B, type: vless, server: original.example, uuid: routed}
  - {name: C, type: vless, server: unused.example}
  - {name: Custom, type: direct}
`
	for _, tc := range []struct {
		name, config string
		want         []string
	}{
		{"exact line", "proxy-groups: [{name: G, type: select, proxies: [B]}]\nrules: ['MATCH,G']", []string{"B", "Custom"}},
		{"personal or template rule", "rules: ['DOMAIN-SUFFIX,example.com,B', 'MATCH,DIRECT']", []string{"B", "Custom"}},
		{"nested selectable groups", "proxy-groups: [{name: G, proxies: [Nested]}, {name: Nested, proxies: [B]}]", []string{"B", "Custom"}},
		{"sub-rule", "rules: ['SUB-RULE,(NETWORK,tcp),child']\nsub-rules: {child: ['MATCH,B']}", []string{"B", "Custom"}},
		{"provider detour", "proxy-providers: {remote: {type: http, proxy: B}}", []string{"B", "Custom"}},
		{"no references", "rules: ['MATCH,DIRECT']\n# B is not a reference\nmetadata: {description: B}", []string{"Custom"}},
		{"all expanded", "proxy-groups: [{name: G, proxies: [A, B, C]}]", []string{"A", "B", "C", "Custom"}},
		{"custom dialer", "proxy-groups: [{name: G, proxies: [Via]}]\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			if tc.name == "custom dialer" {
				input += "  - {name: Via, type: socks5, dialer-proxy: B}\n"
				tc.want = []string{"B", "Custom", "Via"}
			}
			raw, err := pruneMihomoLines(input+tc.config, map[string]bool{"A": true, "B": true, "C": true})
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Proxies []map[string]any `yaml:"proxies"`
			}
			if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, proxy := range config.Proxies {
				names = append(names, proxy["name"].(string))
			}
			if !reflect.DeepEqual(names, tc.want) || !strings.Contains(raw, "# keep template comments") {
				t.Fatalf("got %v want %v\n%s", names, tc.want, raw)
			}
		})
	}
}

func TestPruneMihomoRematchKeepsSubRuleDependencies(t *testing.T) {
	raw, err := pruneMihomoLines(`proxies:
  - {name: A, type: vless}
  - {name: B, type: vless}
  - {name: Mark, type: rematch, target-rematch-name: label, target-sub-rule: child}
proxy-groups: [{name: G, proxies: [Mark]}]
rules: ['REMATCH-NAME,label,G', 'MATCH,DIRECT']
sub-rules: {child: ['MATCH,B']}
`, map[string]bool{"A": true, "B": true})
	if err != nil || strings.Contains(raw, "name: A") || !strings.Contains(raw, "name: B") || !strings.Contains(raw, "name: Mark") {
		t.Fatalf("rematch dependencies: %v\n%s", err, raw)
	}
}

func TestPruneMihomoDNSAndYAMLAliases(t *testing.T) {
	body := `proxies:
  - &removed {name: A, type: vless, server: original.example}
  - {name: B, type: vless}
  - name: Custom
    type: socks5
    <<: &dialer {dialer-proxy: B}
rules: ['MATCH,DIRECT']
dns:
  nameserver: ['https://1.1.1.1/dns-query#B&h3=true&ecs=1.1.1.1/24', 'https://1.0.0.1/dns-query#en0', 'https://1.0.0.1/dns-query#RULES']
metadata: *removed
`
	raw, err := pruneMihomoLines(body, map[string]bool{"A": true, "B": true})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Proxies  []map[string]any `yaml:"proxies"`
		Metadata map[string]any   `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("filtering left an orphan alias: %v\n%s", err, raw)
	}
	if len(config.Proxies) != 2 || config.Proxies[0]["name"] != "B" || config.Metadata["name"] != "A" {
		t.Fatalf("alias kept unused proxy or lost custom values: %+v", config)
	}
	// Isolate DNS-only use so a dialer dependency cannot hide a missing DNS edge.
	raw, err = pruneMihomoLines("proxies: [{name: B, type: vless}]\ndns: {nameserver: ['https://1.1.1.1/dns-query#B&h3=true']}", map[string]bool{"B": true})
	if err != nil || !strings.Contains(raw, "name: B") {
		t.Fatalf("DNS-only line disappeared: %v\n%s", err, raw)
	}
}

func TestPruneSubscriptionsRejectBrokenReferences(t *testing.T) {
	for _, body := range []string{
		"proxies: [",
		"proxies: [{name: A}, {name: A}]",
		"proxy-groups: [{name: G, proxies: [missing]}]",
		"proxy-groups: [{name: G, proxies: [H]}, {name: H, proxies: [G]}]",
		"rules: ['SUB-RULE,(NETWORK,tcp),missing']",
		"proxies: []\nproxies: []",
	} {
		if _, err := pruneMihomoLines(body, nil); err == nil {
			t.Fatalf("accepted broken YAML: %s", body)
		}
	}
	for _, body := range []string{
		`{"outbounds":`,
		`{"outbounds":[{"tag":"A"},{"tag":"A"}]}`,
		`{"route":{"final":"missing"}}`,
		`{"outbounds":[{"tag":"G","outbounds":["H"]},{"tag":"H","outbounds":["G"]}]}`,
		`{"outbounds":{}}`,
		`{} {}`,
	} {
		if _, err := pruneSingBoxLines(body, nil); err == nil {
			t.Fatalf("accepted broken JSON: %s", body)
		}
	}
}

func TestPruneSingBoxLinesDependenciesAndCustomValues(t *testing.T) {
	for _, tc := range []struct {
		name, section string
		wantB         bool
	}{
		{"final", `"route":{"final":"B"}`, true},
		{"logical nested route", `"route":{"final":"direct","rules":[{"type":"logical","rules":[{"outbound":"B"}]}]}`, true},
		{"dns detour", `"dns":{"servers":[{"tag":"dns","detour":"B"}]}`, true},
		{"download", `"route":{"rule_set":[{"download_detour":"B"}]}`, true},
		{"not DNS tag", `"dns":{"final":"B","servers":[{"tag":"B","type":"local"}]}`, false},
		{"metadata not a reference", `"metadata":{"outbound":"B","description":"B"}`, false},
		{"endpoint detour", `"endpoints":[{"tag":"endpoint","type":"wireguard","detour":"B"}],"route":{"final":"endpoint"}`, true},
		{"ntp detour", `"ntp":{"enabled":true,"detour":"B"}`, true},
		{"UI download detour", `"experimental":{"clash_api":{"external_ui_download_detour":"B"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"outbounds":[{"tag":"direct","type":"direct"},{"tag":"A","type":"vless"},{"tag":"B","type":"vless","server":"original.example","server_port":443,"uuid":"routed"}],"custom_number":9007199254740993,` + tc.section + `}`
			raw, err := pruneSingBoxLines(body, map[string]bool{"A": true, "B": true})
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Outbounds []map[string]any `json:"outbounds"`
			}
			if err := json.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			gotB := false
			for _, outbound := range config.Outbounds {
				if outbound["tag"] == "A" {
					t.Fatal("unreferenced A was retained")
				}
				gotB = gotB || outbound["tag"] == "B"
			}
			if gotB != tc.wantB || !strings.Contains(raw, "9007199254740993") {
				t.Fatalf("B=%v want %v; lost custom value?\n%s", gotB, tc.wantB, raw)
			}
		})
	}
	// A custom outbound retains its detour chain; selector defaults and member
	// order are not rewritten by filtering.
	body := `{"outbounds":[{"tag":"direct","type":"direct"},{"tag":"A","type":"vless"},{"tag":"B","type":"vless","detour":"A"},{"tag":"Custom","type":"socks","detour":"B"},{"tag":"G","type":"selector","outbounds":["Custom","direct"],"default":"Custom"}],"route":{"final":"G"}}`
	raw, err := pruneSingBoxLines(body, map[string]bool{"A": true, "B": true})
	if err != nil || !strings.Contains(raw, `"tag": "A"`) || !strings.Contains(raw, `"tag": "B"`) {
		t.Fatalf("lost detour chain: %v\n%s", err, raw)
	}
	raw, err = pruneSingBoxLines(`{"outbounds":[{"tag":"B","type":"vless"},{"tag":"A","type":"vless"}]}`, map[string]bool{"A": true, "B": true})
	if err != nil || !strings.Contains(raw, `"tag": "B"`) || strings.Contains(raw, `"tag": "A"`) {
		t.Fatalf("lost implicit first outbound: %v\n%s", err, raw)
	}
}

type referenceGroupRepo struct{ ports.GroupRepo }

func (referenceGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) {
	return &domain.Group{ID: 1}, nil
}

type referenceGroupResolver struct{ nodes []*domain.Node }

func (r referenceGroupResolver) NodesFor(context.Context, *domain.Group) ([]*domain.Node, error) {
	return r.nodes, nil
}

type referenceTemplateRepo struct {
	ports.TemplateRepo
	content map[domain.ClientType]string
}

func (r referenceTemplateRepo) GetDefault(_ context.Context, ct domain.ClientType) (*domain.Template, error) {
	return &domain.Template{Content: r.content[ct], RuleSets: []string{"test"}}, nil
}

type referenceRuleRepo struct {
	ports.RuleSetRepo
	rule *domain.RuleSet
}

func (r referenceRuleRepo) GetBySlug(context.Context, string) (*domain.RuleSet, error) {
	return r.rule, nil
}

const referenceMihomoTemplate = "proxies:\n  {{ proxies }}\nproxy-groups:\n  {{ proxy_groups }}\nrules:\n  {{ rules_personal }}\n  {{ rules_common }}\nsub-rules:\n  {{ sub_rules }}\n"
const referenceSingBoxTemplate = `{
  "outbounds":
  {{ outbounds }},
  "route": {
    "rules":
    {{ route_rules }},
    "final": {{ final_outbound }},
    "rule_set":
    {{ rule_sets }}
  }
}`

func referenceRenderFixture(t *testing.T) (*Service, *domain.Node, *domain.User, *domain.RuleSet) {
	t.Helper()
	s, n, u, _ := routeRenderFixture(t)
	rule := &domain.RuleSet{Slug: "test", Enabled: true, Content: "- MATCH,国外媒体", ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
		"国外媒体": {{Kind: "line", NodeID: n.ID, LineID: "route-B"}},
	}}
	s.repos.Group = referenceGroupRepo{}
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: referenceMihomoTemplate, domain.ClientSingBox: referenceSingBoxTemplate}}
	s.repos.RuleSet = referenceRuleRepo{rule: rule}
	s.groupSvc = referenceGroupResolver{nodes: []*domain.Node{n}}
	s.cache = newRenderCache(subRenderCacheTTL)
	return s, n, u, rule
}

func subscriptionProxyMaps(t *testing.T, output *Output, ct domain.ClientType) []map[string]any {
	t.Helper()
	var config map[string]any
	if ct == domain.ClientMihomo {
		if err := yaml.Unmarshal(output.Body, &config); err != nil {
			t.Fatal(err)
		}
	} else if err := json.Unmarshal(output.Body, &config); err != nil {
		t.Fatal(err)
	}
	key := "proxies"
	if ct == domain.ClientSingBox {
		key = "outbounds"
	}
	out := []map[string]any{}
	for _, value := range config[key].([]any) {
		out = append(out, value.(map[string]any))
	}
	return out
}

func TestRenderReferencedRouteOnlyAndURIUnchanged(t *testing.T) {
	ctx := context.Background()
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, n, u, rule := referenceRenderFixture(t)
			// A geosite rule also exercises rule-set download detour selection:
			// the overridden group no longer needs the default node selector.
			rule.Content = "- GEOSITE,youtube,国外媒体\n- MATCH,国外媒体"
			output, err := s.RenderForUser(ctx, u, ct)
			if err != nil {
				t.Fatal(err)
			}
			key := "name"
			if ct == domain.ClientSingBox {
				key = "tag"
			}
			found := false
			for _, proxy := range subscriptionProxyMaps(t, output, ct) {
				if proxy[key] == "A" || proxy[key] == "🚀 节点选择" {
					t.Fatalf("unreferenced direct line or default dependency leaked: %v", proxy)
				}
				if proxy[key] == "A B" {
					found = true
					serverKey, portKey := "server", "port"
					if ct == domain.ClientSingBox {
						portKey = "server_port"
					}
					if proxy[serverKey] != n.ServerAddress || proxy["uuid"] != "12345678-1234-03e9-8def-123456789abc" || proxy[portKey] == nil {
						t.Fatalf("route connection values changed: %v", proxy)
					}
				}
			}
			if !found || n.EffectiveHideDirect() {
				t.Fatal("route disappeared or implementation hid its original entry")
			}
			// URI must not read a template or rule set at all.
			s.repos.Template, s.repos.RuleSet = nil, nil
			uri, err := s.RenderForUser(ctx, u, domain.ClientURIList)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := base64.StdEncoding.DecodeString(string(uri.Body))
			if err != nil || len(strings.Split(string(plain), "\n")) != 2 || !strings.Contains(string(plain), "#A%20B") {
				t.Fatalf("URI behavior changed: %v\n%s", err, plain)
			}
		})
	}
}

func TestRenderLegacyMainReferencedDefinitionsUnchanged(t *testing.T) {
	ctx := context.Background()
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, n, u, rule := referenceRenderFixture(t)
			n.RouteState = domain.NodeRouteState{} // main has no VLESS route configuration
			n.Region = "US"
			s.repos.Settings = fakeSettings{s: ports.UISettings{SubRegionFlagPrefix: true}}
			n.Relays = []domain.RelayLine{{ID: "relay", Name: "R", Address: "relay.example", Port: 8443, Enabled: true}, {ID: "off", Name: "off", Enabled: false}}
			unused := *n
			unused.ID, unused.DisplayName = 8, "Unused"
			unused.Relays = nil
			s.groupSvc = referenceGroupResolver{nodes: []*domain.Node{n, &unused}}
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node", NodeID: n.ID}}
			items := expandRelays([]renderItem{{name: "A", node: n}})
			applyRegionFlagPrefix(items)
			var baseline []map[string]any
			nameKey := "name"
			if ct == domain.ClientMihomo {
				baseline = s.buildProxies(ctx, u, items, ports.UISettings{})
			} else {
				nameKey = "tag"
				baseline = s.buildSingBoxOutbounds(ctx, u, items, nil, nil, nil, ports.UISettings{})
			}
			output, err := s.RenderForUser(ctx, u, ct)
			if err != nil {
				t.Fatal(err)
			}
			actual := subscriptionProxyMaps(t, output, ct)
			for _, expected := range baseline {
				found := false
				for _, got := range actual {
					if got[nameKey] == expected[nameKey] {
						// JSON round trip normalizes numeric Go types without
						// weakening the equality check for any connection field.
						wantJSON, _ := json.Marshal(expected)
						gotJSON, _ := json.Marshal(got)
						if string(wantJSON) != string(gotJSON) {
							t.Fatalf("legacy definition changed:\n%s\n%s", wantJSON, gotJSON)
						}
						found = true
					}
				}
				if !found {
					t.Fatalf("lost legacy referenced proxy: %v", expected)
				}
			}
			for _, proxy := range actual {
				if strings.Contains(proxy[nameKey].(string), "Unused") || strings.Contains(proxy[nameKey].(string), "off") {
					t.Fatalf("unreferenced or disabled proxy leaked: %v", proxy)
				}
			}
		})
	}
}

func TestEffectiveDependenciesMatchInspectionNormalizationAndRendering(t *testing.T) {
	content := "- MATCH,⚡ QUIC控制"
	members := map[string][]domain.ProxyGroupMember{
		"⚡ QUIC控制": {{Kind: "proxy_group", Value: "🎮 UDP控制"}},
		"🎮 UDP控制":  {{Kind: "builtin", Value: "DIRECT"}},
		// This orphan must neither become a root nor pull in the main selector.
		"Orphan": {{Kind: "proxy_group", Value: "🚀 节点选择"}},
	}
	want := []string{"🎮 UDP控制", "⚡ QUIC控制"}
	if got := proxyGroupTargets(content, members); !reflect.DeepEqual(got, want) {
		t.Fatalf("effective dependency closure = %v want %v", got, want)
	}
	normalized := NormalizeProxyGroupMetadata(content, nil, members, nil)
	if len(normalized.Members) != 2 || !reflect.DeepEqual(normalized.Removed, []string{"Orphan"}) {
		t.Fatalf("normalization disagrees: %+v", normalized)
	}
	inspection := InspectProxyGroups(content, members, nil, nil)
	if len(inspection.Groups) != 2 {
		t.Fatalf("inspection added unwanted groups: %+v", inspection.Groups)
	}
	// Non-canonical references remain missing_group warnings, not synthesized
	// defaults. Cycles remain errors at the existing save-validation boundary.
	for _, tc := range []struct {
		members map[string][]domain.ProxyGroupMember
		code    string
	}{
		{map[string][]domain.ProxyGroupMember{"G": {{Kind: "proxy_group", Value: "Missing"}}}, "missing_group"},
		{map[string][]domain.ProxyGroupMember{"G": {{Kind: "proxy_group", Value: "H"}}, "H": {{Kind: "proxy_group", Value: "G"}}}, "cycle"},
	} {
		got := InspectProxyGroups("- DOMAIN,example.com,H\n- MATCH,G", tc.members, nil, nil)
		found := false
		for _, issue := range got.Issues {
			found = found || issue.Code == tc.code
		}
		if !found {
			t.Fatalf("lost %s validation: %+v", tc.code, got.Issues)
		}
	}
}

func TestDanglingTypedMembersAreWarningsAndSkipped(t *testing.T) {
	for _, kind := range []string{"proxy_group", "rematch", "builtin"} {
		t.Run(kind, func(t *testing.T) {
			for _, client := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
				s, _, user, rule := referenceRenderFixture(t)
				missing := domain.ProxyGroupMember{Kind: kind, Value: "Deleted member"}
				rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{missing, {Kind: "node", NodeID: 7}}
				inspection := InspectProxyGroups(rule.Content, rule.ProxyGroupMembers, nil, nil)
				found := false
				for _, issue := range inspection.Issues {
					if issue.Code == "missing_group" || issue.Code == "missing_rematch" || issue.Code == "unknown_builtin" {
						found = true
						if issue.Level != "warning" {
							t.Fatalf("dangling member blocks saving: %+v", issue)
						}
					}
				}
				if !found {
					t.Fatal("missing warning")
				}
				output, err := s.RenderForUser(context.Background(), user, client)
				if err != nil {
					t.Fatalf("%s: %v", client, err)
				}
				if strings.Contains(string(output.Body), missing.Value) {
					t.Fatalf("%s emitted dangling reference", client)
				}
			}
		})
	}
}

func TestDanglingMembersDoNotChangePassOrAutoValidation(t *testing.T) {
	members := map[string][]domain.ProxyGroupMember{"G": {
		{Kind: "proxy_group", Value: "Missing"},
		{Kind: "builtin", Value: "Unknown"},
		{Kind: "builtin", Value: "PASS"},
	}}
	if !singBoxPassThroughProxyGroups(nil, members, "- MATCH,G")["G"] {
		t.Fatal("dangling members changed PASS default")
	}
	members["G"] = []domain.ProxyGroupMember{{Kind: "rematch", Value: "Missing"}}
	inspection := InspectProxyGroups("- MATCH,G", members, map[string]domain.ProxyGroupOptions{"G": {Type: ProxyGroupTypeURLTest}}, nil)
	for _, issue := range inspection.Issues {
		if issue.Code == "auto_group_builtin_member" {
			t.Fatalf("missing Rematch still blocks auto group: %+v", issue)
		}
	}
}

func TestRenderDynamicAndTemplateReferences(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, member := range []domain.ProxyGroupMember{
			{Kind: "node", NodeID: 7},
			{Kind: "node_set", Value: "remaining"},
			{Kind: "node_set", Value: "region:US"},
			{Kind: "node_set", Value: "tag:edge"},
		} {
			t.Run(string(ct)+"/"+member.Kind+member.Value, func(t *testing.T) {
				s, n, u, rule := referenceRenderFixture(t)
				n.Region, n.Tags = "US", []string{"edge"}
				n.Relays = []domain.RelayLine{{ID: "relay", Name: "R", Address: "relay.example", Enabled: true}}
				rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{member}
				output, err := s.RenderForUser(context.Background(), u, ct)
				if err != nil {
					t.Fatal(err)
				}
				key := "name"
				if ct == domain.ClientSingBox {
					key = "tag"
				}
				names := emittedProxyNames(subscriptionProxyMaps(t, output, ct), key)
				if !names["A"] || !names["A R"] || !names["A B"] {
					t.Fatalf("dynamic expansion lost lines: %v", names)
				}
			})
		}
	}
	for _, ref := range []string{"@all", "@region:US", "@tag:edge", "@region:US+tag:edge"} {
		t.Run(ref, func(t *testing.T) {
			s, n, u, _ := referenceRenderFixture(t)
			n.Region, n.Tags = "US", []string{"edge"}
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: strings.Replace(referenceMihomoTemplate, "  {{ proxy_groups }}", "  {{ proxy_groups }}\n  - name: Template\n    type: select\n    proxies:\n      - "+ref, 1)}}
			output, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
			if err != nil {
				t.Fatal(err)
			}
			names := emittedProxyNames(subscriptionProxyMaps(t, output, domain.ClientMihomo), "name")
			if !names["A"] || !names["A B"] {
				t.Fatalf("template expansion was filtered too early: %v", names)
			}
		})
	}
}

func TestRenderUnavailableReferencesNeverRestoreAllLines(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, reason := range []string{"drift", "unknown", "disabled", "deleted", "missing_entry", "outside_scope"} {
			t.Run(string(ct)+"/"+reason, func(t *testing.T) {
				s, n, u, _ := referenceRenderFixture(t)
				switch reason {
				case "drift", "unknown":
					n.RouteState.Status = reason
				case "disabled":
					n.RouteState.Lines[0].Enabled = false
				case "deleted":
					n.RouteState.Lines[0].Deleted = true
				case "missing_entry":
					n.RouteState.Lines[0].EntryRelayID = "missing"
				case "outside_scope":
					s.groupSvc = referenceGroupResolver{}
				}
				output, err := s.RenderForUser(context.Background(), u, ct)
				if err != nil {
					t.Fatal(err)
				}
				for _, proxy := range subscriptionProxyMaps(t, output, ct) {
					if proxy["type"] == "vless" {
						t.Fatalf("empty group restored unreferenced/unavailable proxies: %v", proxy)
					}
				}
			})
		}
	}
}

func TestReferencedRenderCacheRefreshesAfterInvalidationAndExpiry(t *testing.T) {
	s, n, u, rule := referenceRenderFixture(t)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	ctx := context.Background()
	first, err := s.RenderForUserCached(ctx, u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "line", NodeID: n.ID, LineID: "direct"}}
	s.InvalidateAll() // The ruleset-save hook uses this same invalidation seam.
	second, err := s.RenderForUserCached(ctx, u, domain.ClientMihomo)
	if err != nil || first == second {
		t.Fatalf("invalidation retained old subscription: %v", err)
	}
	names := emittedProxyNames(subscriptionProxyMaps(t, second, domain.ClientMihomo), "name")
	if !names["A"] || names["A B"] {
		t.Fatalf("stale referenced lines: %v", names)
	}
	// Other existing configuration paths use the bounded TTL. Template edits
	// must be reflected after that bound, without a second reference cache.
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: referenceMihomoTemplate + "# changed template\n"}}
	now = now.Add(subRenderCacheTTL + time.Second)
	third, err := s.RenderForUserCached(ctx, u, domain.ClientMihomo)
	if err != nil || !strings.Contains(string(third.Body), "# changed template") {
		t.Fatalf("template remained stale past TTL: %v", err)
	}
}

func TestSeedTemplatesOnlyEmitReferencedLines(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, _, u, rule := referenceRenderFixture(t)
			rule.Content = "- MATCH,🚀 节点选择"
			rule.ProxyGroupMembers["🚀 节点选择"] = rule.ProxyGroupMembers["国外媒体"]
			delete(rule.ProxyGroupMembers, "国外媒体")
			name := "default-mihomo.yaml"
			key := "name"
			if ct == domain.ClientSingBox {
				name, key = "default-sing-box.yaml", "tag"
			}
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: readTemplateContent(t, "../../seed/files/templates/"+name)}}
			output, err := s.RenderForUser(context.Background(), u, ct)
			if err != nil {
				t.Fatal(err)
			}
			names := emittedProxyNames(subscriptionProxyMaps(t, output, ct), key)
			if names["A"] || !names["A B"] {
				t.Fatalf("seed template leaked direct line or lost B: %v", names)
			}
		})
	}
}

func TestMihomoTemplateActualCanonicalDependencyIsRetained(t *testing.T) {
	s, _, u, _ := referenceRenderFixture(t)
	// Default template DNS explicitly uses the canonical selector. Unlike
	// overridden rule defaults, this is a real template dependency, so its
	// dynamic members (including A) must not be dropped.
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml")}}
	output, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	names := emittedProxyNames(subscriptionProxyMaps(t, output, domain.ClientMihomo), "name")
	var config struct {
		Groups []proxyGroup `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(output.Body, &config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range config.Groups {
		found = found || group.Name == "🚀 节点选择"
	}
	if !names["A"] || !names["A B"] || !found {
		t.Fatalf("real DNS dependency was lost: %v", names)
	}
}

func TestSubscriptionOnlyPrunesOwnedDefinitionsAndIsDeterministic(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, _, user, rule := referenceRenderFixture(t)
			first, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil || string(first.Body) != string(second.Body) || strings.Contains(string(first.Body), generatedLineOriginKey) {
				t.Fatalf("transient ownership marker leaked or changed output: %v", err)
			}
			// Same name as a candidate, but defined by the template rather than
			// inserted by PSP. It must survive even without an explicit reference.
			rule.Content = "- MATCH,DIRECT"
			template := "proxies: [{name: A, type: direct}]\nrules: ['MATCH,DIRECT']"
			key := "name"
			if ct == domain.ClientSingBox {
				template = `{"outbounds":[{"tag":"direct","type":"direct"},{"tag":"A","type":"direct"}],"route":{"final":"direct"}}`
				key = "tag"
			}
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: template}}
			output, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			names := emittedProxyNames(subscriptionProxyMaps(t, output, ct), key)
			if !names["A"] || strings.Contains(string(output.Body), generatedLineOriginKey) {
				t.Fatalf("custom definition was treated as owned: %v\n%s", names, output.Body)
			}
		})
	}
}

func TestSingBoxPassDoesNotRetainUnusedImplicitSelector(t *testing.T) {
	s, _, user, rule := referenceRenderFixture(t)
	rule.Content = "- NETWORK,udp,🎮 UDP控制\n- MATCH,国外媒体"
	rule.ProxyGroupMembers["🎮 UDP控制"] = []domain.ProxyGroupMember{{Kind: "builtin", Value: "PASS"}, {Kind: "proxy_group", Value: "🚀 节点选择"}}
	output, err := s.RenderForUser(context.Background(), user, domain.ClientSingBox)
	if err != nil {
		t.Fatal(err)
	}
	names := emittedProxyNames(subscriptionProxyMaps(t, output, domain.ClientSingBox), "tag")
	if names["A"] || names["🎮 UDP控制"] || names["🚀 节点选择"] || !names["A B"] {
		t.Fatalf("omitted PASS group kept unused dependencies: %v", names)
	}
}
