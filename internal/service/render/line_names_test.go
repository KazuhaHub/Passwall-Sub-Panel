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

func collidingNameFixture(t *testing.T) (*Service, *domain.Node, *domain.User, *domain.RuleSet) {
	t.Helper()
	s, node, user, rule := referenceRenderFixture(t)
	node.Relays = []domain.RelayLine{{ID: "relay", Name: "same", OnlyLineName: true, Address: "relay.example", Enabled: true}}
	node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = "same", true
	return s, node, user, rule
}

func namedVLESSProxies(t *testing.T, output *Output, ct domain.ClientType) map[string]map[string]any {
	t.Helper()
	key := "name"
	if ct == domain.ClientSingBox {
		key = "tag"
	}
	proxies := map[string]map[string]any{}
	for _, proxy := range subscriptionProxyMaps(t, output, ct) {
		if proxy["type"] == "vless" {
			proxies[proxy[key].(string)] = proxy
		}
	}
	return proxies
}

func TestFinalLineNamesOnlyCountEmittedReferencedLines(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, scenario := range []string{"one", "both", "repeat", "disabled", "deleted", "drift", "entry_missing", "node", "literal_brackets", "fallback"} {
			t.Run(string(ct)+"/"+scenario, func(t *testing.T) {
				s, node, user, rule := collidingNameFixture(t)
				want := []string{"same"}
				switch scenario {
				case "both":
					rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "line", NodeID: node.ID, LineID: "relay"})
					want = []string{"same [中转]", "same [VLESS 路由]"}
				case "repeat":
					rule.Content = "- DOMAIN,example.com,Second\n- MATCH,国外媒体"
					rule.ProxyGroupMembers["Second"] = rule.ProxyGroupMembers["国外媒体"]
				case "disabled":
					node.Relays[0].Enabled = false
				case "deleted", "drift", "entry_missing":
					rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "line", NodeID: node.ID, LineID: "relay"}}
					if scenario == "deleted" {
						node.RouteState.Lines[0].Deleted = true
					} else if scenario == "drift" {
						node.RouteState.Status = "drift"
					} else {
						node.RouteState.Lines[0].EntryRelayID = "missing"
					}
				case "node":
					rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node", NodeID: node.ID}}
					want = []string{"A", "same [中转]", "same [VLESS 路由]"}
				case "literal_brackets":
					node.RouteState.Lines[0].Name = "user name [7:route-B]"
					want = []string{"user name [7:route-B]"}
				case "fallback":
					node.RouteState.Lines[0].Name = ""
					node.RouteState.Lines[0].OutboundTag = "same"
				}
				output, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				got := namedVLESSProxies(t, output, ct)
				if len(got) != len(want) {
					t.Fatalf("got %v want %v", got, want)
				}
				for _, name := range want {
					if got[name] == nil {
						t.Fatalf("missing %q in %v", name, got)
					}
				}
				if strings.Contains(string(output.Body), "psp-line-") || strings.Contains(string(output.Body), generatedLineOriginKey) {
					t.Fatal("internal identity leaked")
				}
			})
		}
	}
}

func TestFinalNameAllocationStableAndAvoidsSecondaryCollisions(t *testing.T) {
	c := &lineNameCatalog{lines: map[string]subscriptionLineName{
		"one":   {nodeID: 1, lineID: "direct", kind: "直连", base: "same", token: "one"},
		"two":   {nodeID: 2, lineID: "route", kind: "VLESS 路由", base: "same", token: "two"},
		"three": {nodeID: 3, lineID: "direct", kind: "直连", base: "same [直连]", token: "three"},
		"four":  {nodeID: 4, lineID: "direct", kind: "直连", base: "DIRECT", token: "four"},
	}}
	kept := map[string]bool{"one": true, "two": true, "three": true, "four": true}
	fixed := map[string]bool{"DIRECT": true, "same [直连:1]": true, "same [直连:1] [2]": true}
	want := map[string]string{"one": "same [直连:1] [3]", "two": "same [VLESS 路由]", "three": "same [直连]", "four": "DIRECT [直连]"}
	for i := 0; i < 20; i++ {
		if got := c.allocate(kept, fixed); !reflect.DeepEqual(got, want) {
			t.Fatalf("unstable collision allocation: %v want %v", got, want)
		}
	}
	delete(kept, "two")
	if got := c.allocate(kept, fixed); got["one"] != "same" {
		t.Fatalf("removed line still influenced final name: %v", got)
	}
}

func TestFinalLineNamesCompareCompleteFlagsAndIgnoreUnrenderableNodes(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, scenario := range []string{"different_flags", "same_flags", "flags_off", "unsupported", "outside_scope"} {
			t.Run(string(ct)+"/"+scenario, func(t *testing.T) {
				s, node, user, rule := referenceRenderFixture(t)
				node.RouteState = domain.NodeRouteState{}
				node.DisplayName, node.Region = "same", "US"
				other := *node
				other.ID, other.Region = 8, "JP"
				flags := scenario != "flags_off"
				if scenario == "same_flags" {
					other.Region = "US"
				}
				if scenario == "unsupported" {
					other.DesiredProtocol = "unsupported"
				}
				nodes := []*domain.Node{node, &other}
				if scenario == "outside_scope" {
					nodes = nodes[:1]
				}
				s.groupSvc = referenceGroupResolver{nodes: nodes}
				s.repos.Settings = fakeSettings{s: ports.UISettings{SubRegionFlagPrefix: flags}}
				rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "remaining"}}
				output, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				got := namedVLESSProxies(t, output, ct)
				want := []string{"🇺🇸 same", "🇯🇵 same"}
				if scenario == "same_flags" {
					want = []string{"🇺🇸 same [直连:7]", "🇺🇸 same [直连:8]"}
				} else if scenario == "flags_off" {
					want = []string{"same [直连:7]", "same [直连:8]"}
				} else if scenario == "unsupported" || scenario == "outside_scope" {
					want = []string{"🇺🇸 same"}
				}
				if len(got) != len(want) {
					t.Fatalf("got %v want %v", got, want)
				}
				for _, name := range want {
					if got[name] == nil {
						t.Fatalf("missing %q in %v", name, got)
					}
				}
			})
		}
	}
}

func TestFinalNamesBindLegacyAndUniqueHandwrittenReferences(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, scenario := range []string{"legacy_personal", "legacy_rule", "typed_rule", "unique_rule", "ambiguous_rule", "ambiguous_template", "unavailable_legacy"} {
			t.Run(string(ct)+"/"+scenario, func(t *testing.T) {
				s, node, user, rule := collidingNameFixture(t)
				switch scenario {
				case "legacy_personal":
					user.PersonalRules = "- DOMAIN,example.com,same [7:route-B],no-resolve"
				case "legacy_rule":
					rule.Content = "- MATCH,same [7:route-B]"
				case "typed_rule":
					rule.Content = "- MATCH,same [VLESS 路由]"
				case "unique_rule":
					node.RouteState.Lines[0].Name = "unique"
					rule.Content = "- MATCH,unique"
				case "ambiguous_rule":
					rule.Content = "- MATCH,same"
				case "unavailable_legacy":
					node.RouteState.Lines[0].Enabled = false
					rule.Content = "- MATCH,same [7:route-B]"
				case "ambiguous_template":
					template := referenceMihomoTemplate + "dns:\n  nameserver: ['https://1.1.1.1/dns-query#same']\n"
					if ct == domain.ClientSingBox {
						template = strings.Replace(referenceSingBoxTemplate, "{", `{"dns":{"servers":[{"tag":"d","type":"udp","server":"1.1.1.1","detour":"same"}]},`, 1)
					}
					s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: template}}
				}
				output, err := s.RenderForUser(context.Background(), user, ct)
				if strings.HasPrefix(scenario, "ambiguous") || scenario == "unavailable_legacy" {
					if err == nil || (!strings.Contains(err.Error(), "ambiguous") && !strings.Contains(err.Error(), "unavailable")) {
						t.Fatalf("unsafe handwritten reference accepted: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := "same"
				if scenario == "unique_rule" {
					want = "unique"
				}
				got := namedVLESSProxies(t, output, ct)
				if len(got) != 1 || got[want] == nil || got[want]["uuid"] != "12345678-1234-03e9-8def-123456789abc" {
					t.Fatalf("reference changed identity: %v", got)
				}
			})
		}
	}
}

func TestFixedObjectsKeepNamesWhileGeneratedLinesAvoidThem(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, scenario := range []string{"group", "builtin", "custom", "endpoint", "dns_namespace", "rule_set_namespace"} {
			if ct == domain.ClientMihomo && (scenario == "endpoint" || scenario == "dns_namespace" || scenario == "rule_set_namespace") {
				continue
			}
			t.Run(string(ct)+"/"+scenario, func(t *testing.T) {
				s, node, user, rule := collidingNameFixture(t)
				name := "same"
				template := referenceMihomoTemplate
				if ct == domain.ClientSingBox {
					template = referenceSingBoxTemplate
				}
				want := "same [VLESS 路由]"
				switch scenario {
				case "group":
					name = "国外媒体"
					want = name + " [VLESS 路由]"
				case "builtin":
					name = "DIRECT"
					if ct == domain.ClientSingBox {
						name = "direct"
					}
					want = name + " [VLESS 路由]"
				case "custom":
					if ct == domain.ClientMihomo {
						template = strings.Replace(template, "  {{ proxies }}", "  {{ proxies }}\n  - {name: same, type: direct}", 1)
					} else {
						// An endpoint shares the outbound namespace without
						// changing the generated outbounds block's JSON shape.
						template = strings.Replace(template, "{", `{"endpoints":[{"tag":"same","type":"wireguard"}],`, 1)
					}
				case "endpoint":
					template = strings.Replace(template, "{", `{"endpoints":[{"tag":"same","type":"wireguard"}],`, 1)
				case "dns_namespace":
					template = strings.Replace(template, "{", `{"dns":{"servers":[{"tag":"same","type":"local"}],"final":"same"},`, 1)
					want = "same"
				case "rule_set_namespace":
					template = strings.Replace(template, "    {{ rule_sets }}", `    [{"tag":"same","type":"inline","rules":[]}]`, 1)
					want = "same"
				}
				node.RouteState.Lines[0].Name = name
				s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: template}}
				output, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				got := namedVLESSProxies(t, output, ct)
				if len(got) != 1 || got[want] == nil {
					t.Fatalf("fixed object collision changed incorrectly: %v want %q", got, want)
				}
				if scenario == "group" && !strings.Contains(string(output.Body), "国外媒体") || rule.ProxyGroupMembers["国外媒体"][0].LineID != "route-B" {
					t.Fatal("persisted group or stable reference was changed")
				}
			})
		}
	}
}

func TestURINamingRemainsLegacyAndRenderDoesNotMutateSavedData(t *testing.T) {
	s, node, user, rule := collidingNameFixture(t)
	beforeUser, _ := json.Marshal(user)
	beforeRule, _ := json.Marshal(rule)
	beforeNode, _ := json.Marshal(node)
	ctx := context.Background()
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		first, err := s.RenderForUser(ctx, user, ct)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.RenderForUser(ctx, user, ct)
		if err != nil || string(first.Body) != string(second.Body) {
			t.Fatalf("nondeterministic render: %v", err)
		}
	}
	uri, err := s.RenderForUser(ctx, user, domain.ClientURIList)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := base64.StdEncoding.DecodeString(string(uri.Body))
	if !strings.Contains(string(plain), "same%20%5B7:relay%5D") || !strings.Contains(string(plain), "same%20%5B7:route-B%5D") {
		t.Fatalf("URI legacy labels changed: %s", plain)
	}
	afterUser, _ := json.Marshal(user)
	afterRule, _ := json.Marshal(rule)
	afterNode, _ := json.Marshal(node)
	if string(beforeUser) != string(afterUser) || string(beforeRule) != string(afterRule) || string(beforeNode) != string(afterNode) {
		t.Fatal("subscription naming mutated persisted entities")
	}
}

func TestReferenceRewriterPreservesRuleConditionsAndMetadata(t *testing.T) {
	input := `proxies:
  - {name: internal, type: vless}
proxy-groups: [{name: G, proxies: [internal]}]
rules:
  - 'AND,((DOMAIN,internal),(NETWORK,tcp)),internal,no-resolve'
  - 'SUB-RULE,(DOMAIN,internal),internal'
sub-rules: {internal: ['DOMAIN,internal,internal,src']}
proxy-providers: {p: {proxy: internal, override: {dialer-proxy: internal}}}
dns:
  nameserver: ['https://internal/dns-query#internal&h3=true&ecs=1.1.1.1/24']
metadata: {name: internal, address: internal}
`
	doc, err := parseMihomoSubscription(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := transformMihomoReferences(doc.Content[0], func(_ string, name string, _ bool) (string, error) {
		if name == "internal" {
			return "最终 名称", nil
		}
		return name, nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(doc)
	var config map[string]any
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	rules := config["rules"].([]any)
	if rules[0] != "AND,((DOMAIN,internal),(NETWORK,tcp)),最终 名称,no-resolve" || rules[1] != "SUB-RULE,(DOMAIN,internal),internal" {
		t.Fatalf("rewrote conditions or control-flow symbols: %v", rules)
	}
	metadata := config["metadata"].(map[string]any)
	if metadata["name"] != "internal" || !strings.Contains(string(raw), "https://internal/dns-query#%E6%9C%80%E7%BB%88%20%E5%90%8D%E7%A7%B0&h3=true&ecs=1.1.1.1/24") {
		t.Fatalf("metadata or DNS options corrupted:\n%s", raw)
	}
}

type lineNameSeparatorRepo struct {
	ports.SeparatorRepo
	entries []*domain.SeparatorEntry
}

func (r lineNameSeparatorRepo) ListEnabled(context.Context) ([]*domain.SeparatorEntry, error) {
	return r.entries, nil
}

func TestFinalNameSeparatorCollisionsAndImmutableDuplicates(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, _, user, rule := collidingNameFixture(t)
			s.repos.Separator = lineNameSeparatorRepo{entries: []*domain.SeparatorEntry{{ID: 1, DisplayName: "same", Enabled: true, Mode: domain.SeparatorModeGlobal}}}
			rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "node_set", Value: "remaining"})
			output, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			key := "name"
			if ct == domain.ClientSingBox {
				key = "tag"
			}
			names := emittedProxyNames(subscriptionProxyMaps(t, output, ct), key)
			if !names["same"] || !names["same [VLESS 路由]"] || !names["same [中转]"] {
				t.Fatalf("separator renamed or real lines not disambiguated: %v", names)
			}
			s.repos.Separator = lineNameSeparatorRepo{entries: []*domain.SeparatorEntry{
				{ID: 1, DisplayName: "same", Enabled: true, Mode: domain.SeparatorModeGlobal},
				{ID: 2, DisplayName: "same", Enabled: true, Mode: domain.SeparatorModeGlobal},
			}}
			if _, err := s.RenderForUser(context.Background(), user, ct); err == nil {
				t.Fatal("two immutable separators silently renamed or overwritten")
			}
		})
	}
}

func TestFixedTemplateRawReferencesDoNotCreateSpuriousGroups(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, _, user, rule := collidingNameFixture(t)
			rule.Content = "- MATCH,same"
			var template string
			if ct == domain.ClientMihomo {
				template = strings.Replace(referenceMihomoTemplate, "  {{ proxies }}", "  {{ proxies }}\n  - {name: same, type: direct}", 1)
			} else {
				template = `{"outbounds":[{"tag":"same","type":"direct"}],"route":{"final":{{ final_outbound }}}}`
			}
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: template}}
			output, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			if len(namedVLESSProxies(t, output, ct)) != 0 {
				t.Fatalf("handwritten custom proxy reference selected a same-name PSP line: %s", output.Body)
			}
		})
	}
	// No automatic groups are needed when the only rule targets a group that
	// the template itself declares. Empty generated lists must compose cleanly.
	s, _, user, rule := collidingNameFixture(t)
	rule.Content = "- MATCH,same"
	template := strings.Replace(referenceMihomoTemplate, "  {{ proxy_groups }}", "  {{ proxy_groups }}\n  - {name: same, type: select, proxies: [DIRECT]}", 1)
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: template}}
	if _, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo); err != nil {
		t.Fatalf("template-owned group collided with generated group: %v", err)
	}
}

func TestOpaqueNamesNeverLeakThroughTemplateAliases(t *testing.T) {
	s, _, user, _ := collidingNameFixture(t)
	template := strings.Replace(referenceMihomoTemplate, "proxies:", "proxies: &copied", 1) + "metadata: *copied\n"
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: template}}
	output, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output.Body), "psp-line-") || strings.Contains(string(output.Body), generatedLineOriginKey) {
		t.Fatalf("private markers leaked into aliased metadata:\n%s", output.Body)
	}
	// A shared scalar used in a reference and in plain metadata must not be
	// renamed in both places simply because it shares an anchor.
	document, err := parseMihomoSubscription("metadata: &s old\nproxy-groups: [{name: G, proxies: [*s]}]")
	if err != nil {
		t.Fatal(err)
	}
	if err := transformMihomoReferences(document.Content[0], func(_ string, name string, _ bool) (string, error) { return "new", nil }); err != nil {
		t.Fatal(err)
	}
	if got := referenceString(referenceField(document.Content[0], "metadata")); got != "old" {
		t.Fatalf("reference rename mutated plain metadata anchor: %q", got)
	}
}

func TestSingBoxSemanticReferenceSlotsAndIndependentNamespaces(t *testing.T) {
	config, err := parseSingBoxSubscription(`{
  "outbounds":[{"tag":"line","type":"vless","server":"line","detour":"line","tls":{"http_client":{"detour":"line"}}},{"tag":"G","type":"selector","outbounds":["line"],"default":"line"}],
  "endpoints":[{"tag":"ep","type":"wireguard","detour":"line"}],
  "route":{"final":"line","rules":[{"type":"logical","rules":[{"outbound":"line"}]}],"rule_set":[{"tag":"line","download_detour":"line"}],"default_domain_resolver":"line"},
  "dns":{"final":"line","servers":[{"tag":"line","detour":"line","domain_resolver":"line"}],"rules":[{"server":"line","outbound":["line"]}]},
  "ntp":{"detour":"line"}, "experimental":{"clash_api":{"external_ui_download_detour":"line"}},
  "metadata":{"outbound":"line","detour":"line","description":"line"}
}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := transformSingBoxReferences(config, func(_ string, name string, _ bool) (string, error) {
		if name == "line" {
			return "renamed", nil
		}
		return name, nil
	}); err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	if err := transformSingBoxReferences(config, func(_ string, name string, _ bool) (string, error) { refs = append(refs, name); return name, nil }); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if ref == "line" {
			t.Fatal("missed semantic reference slot")
		}
	}
	dns := config["dns"].(map[string]any)
	metadata := config["metadata"].(map[string]any)
	route := config["route"].(map[string]any)
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	if dns["final"] != "line" || metadata["outbound"] != "line" || outbound["server"] != "line" || route["default_domain_resolver"] != "line" {
		t.Fatal("rewrote a non-outbound namespace or plain text")
	}
}

func TestFinalSpecialNamesAndRenderScopeAreStable(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, name := range []string{"中文 专线", "name [existing]", "line, comma", "line & + # /", `line "quoted"`, "false", "123"} {
			t.Run(string(ct)+"/"+name, func(t *testing.T) {
				s, node, user, rule := collidingNameFixture(t)
				node.Relays[0].Name, node.RouteState.Lines[0].Name = name, name
				first, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				if got := namedVLESSProxies(t, first, ct); len(got) != 1 || got[name] == nil {
					t.Fatalf("special name changed: %v", got)
				}
				rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "line", NodeID: node.ID, LineID: "relay"})
				both, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				if got := namedVLESSProxies(t, both, ct); len(got) != 2 || got[name+" [中转]"] == nil || got[name+" [VLESS 路由]"] == nil {
					t.Fatalf("special collision names changed: %v", got)
				}
				rule.ProxyGroupMembers["国外媒体"] = rule.ProxyGroupMembers["国外媒体"][:1]
				after, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil || string(first.Body) != string(after.Body) {
					t.Fatalf("previous render's name map escaped its scope: %v", err)
				}
			})
		}
	}
}

func TestNameMapsDoNotCrossUsersOrCacheInvalidations(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, node, user, rule := collidingNameFixture(t)
			now := time.Unix(1_700_000_000, 0)
			s.now = func() time.Time { return now }
			rule.ProxyGroupMembers["国外媒体"] = append(rule.ProxyGroupMembers["国外媒体"], domain.ProxyGroupMember{Kind: "line", NodeID: node.ID, LineID: "relay"})
			first, err := s.RenderForUserCached(context.Background(), user, ct)
			if err != nil || len(namedVLESSProxies(t, first, ct)) != 2 {
				t.Fatalf("initial cache render failed: %v", err)
			}
			rule.ProxyGroupMembers["国外媒体"] = rule.ProxyGroupMembers["国外媒体"][:1]
			secondUser := *user
			secondUser.ID++
			// Remove the route (it requires an applied UUID for this user), and
			// select a relay so this second scope has one publishable same name.
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "line", NodeID: node.ID, LineID: "relay"}}
			second, err := s.RenderForUserCached(context.Background(), &secondUser, ct)
			if err != nil || namedVLESSProxies(t, second, ct)["same"] == nil {
				t.Fatalf("first user's suffix leaked to second user: %v", err)
			}
			s.InvalidateAll()
			updated, err := s.RenderForUserCached(context.Background(), user, ct)
			if err != nil || namedVLESSProxies(t, updated, ct)["same"] == nil {
				t.Fatalf("stale collision suffix survived invalidation: %v", err)
			}
		})
	}
}

func TestYAMLAndPolicyQuotingKeepTargetsAndFlagsDistinct(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, name := range []string{"no-resolve", "src", `line "quoted"`, `"quoted"`, "线路: 专线"} {
			t.Run(string(ct)+"/"+name, func(t *testing.T) {
				s, node, user, rule := referenceRenderFixture(t)
				node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = name, true
				// The legacy identity alias is unambiguous even when the public
				// name resembles a rule option or contains literal quote marks.
				raw, err := yaml.Marshal([]string{"DOMAIN,example.com," + name + " [7:route-B],no-resolve", "MATCH,DIRECT"})
				if err != nil {
					t.Fatal(err)
				}
				rule.Content = string(raw)
				output, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				if namedVLESSProxies(t, output, ct)[name] == nil {
					t.Fatalf("quoted YAML rule failed to retain target %q: %s", name, output.Body)
				}
			})
		}
	}
}

func TestMihomoDNSSpecialOptionsAreNotLineReferences(t *testing.T) {
	for _, name := range []string{"RULES", "h3=true", "ecs=1.1.1.1/24"} {
		t.Run(name, func(t *testing.T) {
			s, node, user, rule := referenceRenderFixture(t)
			node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = name, true
			rule.Content = "- MATCH,DIRECT"
			template := referenceMihomoTemplate + "dns:\n  nameserver: ['https://1.1.1.1/dns-query#" + name + "']\n"
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: template}}
			output, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo)
			if err != nil {
				t.Fatal(err)
			}
			if len(namedVLESSProxies(t, output, domain.ClientMihomo)) != 0 || !strings.Contains(string(output.Body), "#"+name) {
				t.Fatalf("DNS option was bound as a line or changed: %s", output.Body)
			}
		})
	}
}

func TestFinalNameIdentityDoesNotFollowNodeOrder(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, node, user, rule := referenceRenderFixture(t)
			node.RouteState = domain.NodeRouteState{}
			node.DisplayName = "same"
			other := *node
			other.ID, other.ServerAddress = 8, "other.example"
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "remaining"}}
			s.groupSvc = referenceGroupResolver{nodes: []*domain.Node{node, &other}}
			first, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			s.groupSvc = referenceGroupResolver{nodes: []*domain.Node{&other, node}}
			other.SortOrder = -10
			second, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			before, after := namedVLESSProxies(t, first, ct), namedVLESSProxies(t, second, ct)
			for name, proxy := range before {
				if after[name] == nil || after[name]["server"] != proxy["server"] || after[name]["uuid"] != proxy["uuid"] {
					t.Fatalf("name followed order instead of stable identity: %v -> %v", before, after)
				}
			}
		})
	}
}
