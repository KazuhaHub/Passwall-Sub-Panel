package render

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// observeReduced watches a Service's comparisons of the reduced Mihomo assembly
// with the full pipeline. The report is "same" or "differs" for a render that
// was compared, and "" when the reduced assembly declined or never applied.
func observeReduced(s *Service) func() string {
	outcome := ""
	s.ruleBlockCompared = func(same bool) {
		outcome = "differs"
		if same {
			outcome = "same"
		}
	}
	return func() string { return outcome }
}

func init() { goldenObserve = observeReduced }

func compareReduced(t testing.TB, s *Service, u *domain.User, ct domain.ClientType) (string, *Output, error) {
	t.Helper()
	report := observeReduced(s)
	out, err := s.RenderForUser(context.Background(), u, ct)
	return report(), out, err
}

// Every golden case, rendered both ways. The golden pins the full pipeline to
// the bytes of 72a2d43f; this pins the reduced assemblies to the full pipeline.
// The cases a reduction must apply to are listed, so that a change which
// quietly makes it decline everything cannot pass as "no differences".
func TestReducedRuleAssemblyMatchesFullPipeline(t *testing.T) {
	goldenTB = t
	_, observed := goldenRenders()
	applied := map[string]bool{}
	for name, outcome := range observed {
		if outcome == "differs" {
			t.Errorf("%s: the reduced assembly produced a different document", name)
		}
		applied[name] = outcome == "same"
	}
	expect := func(ct domain.ClientType, want bool, names ...string) {
		for _, name := range names {
			if applied[name+"/"+string(ct)] != want {
				t.Errorf("%s/%s: reduced assembly applied = %v, want %v", name, ct, !want, want)
			}
		}
	}
	expect(domain.ClientMihomo, true,
		"seed", "seed-members",
		"colliding-names", "line-labels", "line-visibility", "awkward-fleet", "fleet-line-kinds",
		"quoted-targets", "rule-options", "logical-rules", "sub-rule-defined", "rematch",
		"implicit-groups-in-rules", "implicit-groups-template-defines", "implicit-groups-template-refers",
		"implicit-groups-template-refers-and-rules", "implicit-groups-escaped", "implicit-groups-escaped-personal",
		"comments-head-only", "comments-template", "comments-template-empty-personal",
		"target-unknown-name", "target-unknown-with-members", "personal-shadows", "personal-comment-only",
		"shared-yaml-specials", "shared-long-line", "shared-at-sign", "shared-control-characters",
		"shared-builtins-only", "shared-many-groups", "group-options-url-test",
		"template-own-groups", "template-swapped-blocks", "template-deep-indent", "template-inline-placeholder",
		"subscription-domain")
	// Shapes the Mihomo reduction must leave to the full pipeline.
	expect(domain.ClientMihomo, false,
		"seed-line-in-shared-rules", "target-line-names", "target-line-quoted", "flag-prefix",
		"comments-and-blanks", "crlf", "shared-two-documents", "shared-flow-sequence", "shared-plain-lines",
		"shared-indented-sequence", "shared-literal-block", "shared-multiline-plain", "shared-anchors", "shared-tagged",
		"shared-placeholder", "shared-node-ref-line", "shared-non-string-scalars", "shared-single-field",
		"template-common-twice", "template-no-common", "template-anchored-rules", "template-common-elsewhere", "template-trailer")
	// sing-box never puts the rule text itself into the document, so comments,
	// quoting and layout of the block do not matter to it.
	expect(domain.ClientSingBox, true,
		"seed", "seed-members",
		"colliding-names", "line-labels", "awkward-fleet", "fleet-line-kinds",
		"quoted-targets", "rule-options", "logical-rules", "comments-and-blanks", "crlf",
		"implicit-groups-in-rules", "implicit-groups-escaped", "personal-shadows",
		"shared-yaml-specials", "shared-long-line", "shared-indented-sequence", "shared-literal-block",
		"shared-builtins-only", "shared-many-groups", "group-options-url-test", "subscription-domain")
	expect(domain.ClientSingBox, false,
		"seed-line-in-shared-rules", "target-line-names", "target-line-quoted", "flag-prefix",
		"shared-flow-sequence", "shared-plain-lines", "shared-anchors", "shared-placeholder", "shared-empty")
	if testing.Verbose() {
		names := []string{}
		for name, ok := range applied {
			if ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		t.Logf("a reduced assembly applied to %d of %d renders: %s", len(names), len(applied), strings.Join(names, ", "))
	}
}

// Rule and template text assembled at random from the shapes rules are written
// in. Each document is rendered both ways; a seed that fails is printed.
func TestReducedRuleAssemblyMatchesFullPipelineOnRandomRules(t *testing.T) {
	targets := []string{"国外媒体", "DIRECT", "REJECT", "🚀 节点选择", "🎮 UDP控制", "🐟 漏网之鱼", "Extra", "⚡ QUIC控制", "Group: one", "it's", "#hash", "A", "A B", "Nobody Home", "PASS", "\"国外媒体\"", "'Extra'"}
	// A wild document also uses the shapes the reduction declines (a target
	// naming a subscription line, inline comments, indentation, blank lines);
	// a tame one stays within what the rule editor writes.
	rule := func(r *rand.Rand, wild, subRules bool) string {
		target := targets[r.Intn(len(targets))]
		for !wild && (target == "A" || target == "A B") {
			target = targets[r.Intn(len(targets))]
		}
		value := ""
		switch r.Intn(9) {
		case 0:
			value = fmt.Sprintf("DOMAIN-SUFFIX,site%d.example,%s", r.Intn(50), target)
		case 1:
			value = fmt.Sprintf("IP-CIDR,10.%d.0.0/16,%s,no-resolve", r.Intn(250), target)
		case 2:
			value = fmt.Sprintf("AND,((NETWORK,UDP),(DST-PORT,%d)),%s", r.Intn(65000), target)
		case 3:
			value = fmt.Sprintf("DOMAIN-KEYWORD,%s,%s", []string{"a: b", "x #y", "plain", "🎬 video", "*star", "quote\"d", "tick's", "ends ", "été", "back\\slash", "[bracket]", "{brace}", "100%", "yes", "~"}[r.Intn(15)], target)
		case 4:
			value = fmt.Sprintf("%s,%s", []string{"GEOSITE,category-ads", "GEOSITE,google@cn", "GEOIP,CN", "GEOIP,telegram", "GEOSITE,youtube", "DST-PORT,8000-9000", "NETWORK,udp", "PROCESS-NAME,curl"}[r.Intn(8)], target)
		case 5:
			value = "MATCH," + target
		case 6:
			// An undefined sub-rule fails the render either way; keep most
			// documents renderable so the comparison has something to compare.
			if name := []string{"child", "other", "missing"}[r.Intn(3)]; subRules && (wild || name != "missing") {
				value = fmt.Sprintf("SUB-RULE,(NETWORK,tcp),%s", name)
			} else {
				value = "DST-PORT,22," + target
			}
		case 7:
			value = fmt.Sprintf("DOMAIN-REGEX,^(a|b)%d\\.example$,%s", r.Intn(9), target)
		default:
			value = fmt.Sprintf("IP-CIDR6,2001:db8:%x::/48,%s,src", r.Intn(65000), target)
		}
		switch r.Intn(12) {
		case 0, 1:
			return "- \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + "\""
		case 2, 3:
			return "- '" + strings.ReplaceAll(value, "'", "''") + "'"
		case 4:
			if wild {
				return "- " + value + " # inline note"
			}
		case 5:
			if wild {
				return "  - " + value
			}
		}
		if strings.ContainsAny(value, "#:'\"*[{%~") {
			return "- '" + strings.ReplaceAll(value, "'", "''") + "'"
		}
		return "- " + value
	}
	block := func(r *rand.Rand, rules int, wild, subRules bool) string {
		lines := []string{}
		for i := 0; i < rules; i++ {
			switch r.Intn(14) {
			case 0:
				if wild || i > 0 {
					lines = append(lines, "# a comment about the next rule")
				}
			case 1:
				if wild || i > 0 {
					lines = append(lines, "# first line", "#second line")
				}
			case 2:
				if wild {
					lines = append(lines, "")
				}
			}
			lines = append(lines, rule(r, wild, subRules))
		}
		if wild && r.Intn(8) == 0 {
			lines = append(lines, "# trailing comment")
		}
		return strings.Join(lines, "\n") + strings.Repeat("\n", r.Intn(3))
	}
	singBoxTemplates := []string{referenceSingBoxTemplate, readTemplateContent(t, "../../seed/files/templates/default-sing-box.yaml"), singBoxTemplateRulesElsewhere}
	templates := []string{referenceMihomoTemplate, goldenTemplateOwnGroups, goldenTemplateRefersOnly, goldenTemplateComments, goldenTemplateSwapped, goldenTemplateDeep, goldenTemplateInline, goldenTemplateTrailer, goldenTemplateAnchored}
	subRules := []domain.MihomoSubRule{{Name: "child", Content: "- DOMAIN,sub.example,国外媒体\n- MATCH,A B"}, {Name: "other", Content: "- MATCH,Extra"}}

	iterations := 120
	if testing.Short() {
		iterations = 40
	}
	compared := map[domain.ClientType]int{}
	for seed := 0; seed < iterations; seed++ {
		r := rand.New(rand.NewSource(int64(seed)))
		withSubRules := r.Intn(2) == 0
		sc := goldenScenario{
			mihomo:   templates[r.Intn(len(templates))],
			singBox:  singBoxTemplates[r.Intn(len(singBoxTemplates))],
			shared:   block(r, 1+r.Intn(30), seed%3 == 0, withSubRules),
			personal: block(r, r.Intn(4), seed%2 == 0, withSubRules),
			members: map[string][]domain.ProxyGroupMember{
				"国外媒体":  {{Kind: "line", NodeID: 7, LineID: "relay-B"}, {Kind: "builtin", Value: "DIRECT"}},
				"Extra": {{Kind: "node_set", Value: "all"}},
			},
		}
		if withSubRules {
			sc.subRules = subRules
		}
		if r.Intn(4) == 0 {
			// A group that defaults to PASS: sing-box drops its rules.
			sc.members["Extra"] = []domain.ProxyGroupMember{{Kind: "builtin", Value: "PASS"}, {Kind: "node_set", Value: "all"}}
		}
		for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
			s, u := sc.build(t)
			outcome, reduced, reducedErr := compareReduced(t, s, u, ct)
			if testing.Verbose() && outcome == "" {
				t.Logf("seed %d %s: not compared (%v)", seed, ct, reducedErr)
			}
			switch outcome {
			case "differs":
				t.Errorf("seed %d %s: the reduced assembly produced a different document\ntemplate:\n%s\nshared:\n%s\npersonal:\n%s", seed, ct, sc.mihomo, sc.shared, sc.personal)
			case "same":
				compared[ct]++
				// The second render of a proven context returns the reduced
				// document alone; it must still be the first render's bytes.
				again, second, err := compareReduced(t, s, u, ct)
				if again != "" || err != nil || reducedErr != nil || string(second.Body) != string(reduced.Body) {
					t.Errorf("seed %d %s: a proven context rendered differently the second time (%q, %v)", seed, ct, again, err)
				}
			}
		}
	}
	for ct, count := range compared {
		if count < iterations/4 {
			t.Errorf("the %s reduced assembly applied to only %d of %d random documents", ct, count, iterations)
		}
	}
	if len(compared) != 2 {
		t.Fatalf("a reduced assembly never applied: %v", compared)
	}
}

// singBoxTemplateRulesElsewhere carries the route rules under a key no pass
// reads as route rules, one level deeper than route.rules.
const singBoxTemplateRulesElsewhere = `{
  "outbounds":
  {{ outbounds }},
  "route": {
    "final": {{ final_outbound }},
    "rule_set":
    {{ rule_sets }}
  },
  "notes": {
    "kept": {
      "rules":
      {{ route_rules }}
    }
  }
}`

// A context whose comparison fails must keep the full pipeline, and return the
// full pipeline's bytes on the very render that found the difference.
func TestReducedRuleAssemblyThatDiffersIsNeverServed(t *testing.T) {
	sc := goldenScenario{shared: "- DOMAIN,a.example,国外媒体\n- DOMAIN,b.example,国外媒体\n- MATCH,国外媒体"}
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		s, u := sc.build(t)
		want, err := s.RenderForUser(context.Background(), u, ct)
		if err != nil {
			t.Fatal(err)
		}

		// Stand in for a serialiser that would have written the block
		// differently than the analysis recorded, in a rule that is not one of
		// the representatives (a changed representative makes the reduction
		// decline before anything is compared).
		s, u = sc.build(t)
		if ct == domain.ClientMihomo {
			block := s.ruleBlocks.get(sc.shared, "  ", analyseRuleBlock)
			if block == nil {
				t.Fatal("the rule block was not analysed")
			}
			block.emitted = strings.Replace(block.emitted, "b.example", "c.example", 1)
		} else {
			block := s.singBoxRuleBlocks.get(sc.shared, "", analyseSingBoxRuleBlock)
			if block == nil || len(block.rules) != 3 || block.rules[1].stand != nil {
				t.Fatal("the rule block was not compiled as expected")
			}
			block.rules[1].text = strings.Replace(block.rules[1].text, "b.example", "c.example", 1)
		}
		for attempt, wantOutcome := range []string{"differs", ""} {
			outcome, got, err := compareReduced(t, s, u, ct)
			if err != nil || string(got.Body) != string(want.Body) {
				t.Fatalf("%s render %d served something other than the full pipeline's document (%v)", ct, attempt, err)
			}
			if outcome != wantOutcome {
				t.Fatalf("%s render %d: comparison outcome %q, want %q", ct, attempt, outcome, wantOutcome)
			}
		}
	}
}

func TestSingBoxRuleBlockAnalysis(t *testing.T) {
	block := analyseSingBoxRuleBlock("# top\n- DOMAIN,a.example,G\n- GEOSITE,youtube,G\n- GEOSITE,youtube,H # again\n- DOMAIN,b.example,G\n- DOMAIN,c.example,PASS\n- PROCESS-PATH,/x,G\n- SUB-RULE,(NETWORK,tcp),child\n- 'MATCH,\"H\"'\n- DOMAIN,after.example,Z\n", "")
	if block == nil {
		t.Fatal("a rule list was refused")
	}
	if got := strings.Join(block.targets, "|"); got != "G|H|PASS|Z" {
		t.Fatalf("targets %q", got)
	}
	// PASS, the unsupported PROCESS-PATH and the SUB-RULE have no sing-box form.
	summary := []string{}
	for _, rule := range block.rules {
		summary = append(summary, fmt.Sprintf("%s>%s match=%v stand=%v", rule.target, rule.outbound, rule.match, rule.stand != nil))
	}
	want := "G>G match=false stand=true|G>G match=false stand=true|H>H match=false stand=true|G>G match=false stand=false|H>H match=true stand=false|Z>Z match=false stand=true"
	if got := strings.Join(summary, "|"); got != want {
		t.Fatalf("compiled rules:\n%s\nwant:\n%s", got, want)
	}
	if want := "      {\n        \"action\": \"route\",\n        \"domain\": [\n          \"a.example\"\n        ],\n        \"outbound\": \"G\"\n      }"; block.rules[0].text != want {
		t.Fatalf("route rule text:\n%s", block.rules[0].text)
	}
	for name, shared := range map[string]string{
		"flow":           "[\"MATCH,G\"]",
		"plain lines":    "MATCH,G",
		"placeholder":    "- DOMAIN,{{ site_title }},G",
		"nested list":    "- [MATCH, G]",
		"anchored rule":  "- &a MATCH,G",
		"empty":          "",
		"malformed YAML": "- \"MATCH,G",
	} {
		if analyseSingBoxRuleBlock(shared, "") != nil {
			t.Errorf("%s: a block of this shape was accepted", name)
		}
	}
}

func TestRuleBlockAnalysis(t *testing.T) {
	block := analyseRuleBlock("- DOMAIN,a.example,G\n# why\n- DOMAIN,b.example,G\n- SUB-RULE,(NETWORK,tcp),child\n- SUB-RULE,(NETWORK,udp),child\n- \"MATCH,\\U0001F41F 漏网之鱼\"\n", "  ")
	if block == nil {
		t.Fatal("a plain block was refused")
	}
	if got := strings.Join(block.targets, "|"); got != "G|🐟 漏网之鱼" {
		t.Fatalf("targets %q", got)
	}
	// One rule per distinct target and per distinct sub-rule, between markers.
	wantStandIn := "- " + ruleBlockBegin + "\n- DOMAIN,a.example,G\n- SUB-RULE,(NETWORK,tcp),child\n- \"MATCH,\\U0001F41F 漏网之鱼\"\n- " + ruleBlockEnd
	if block.standIn != wantStandIn || block.standInRules != 5 {
		t.Fatalf("stand-in (%d rules):\n%s", block.standInRules, block.standIn)
	}
	wantEmitted := "    - DOMAIN,a.example,G\n    # why\n    - DOMAIN,b.example,G\n    - SUB-RULE,(NETWORK,tcp),child\n    - SUB-RULE,(NETWORK,udp),child\n    - \"MATCH,\\U0001F41F 漏网之鱼\"\n"
	if block.emitted != wantEmitted {
		t.Fatalf("emitted:\n%s", block.emitted)
	}
	for name, shared := range map[string]string{
		"comment above the first rule": "# top\n- MATCH,G",
		"comment after the last rule":  "- MATCH,G\n# end",
		"inline comment":               "- MATCH,G # why",
		"comment followed by a blank":  "- DOMAIN,a,G\n# foot\n\n- MATCH,G",
		"indented":                     "  - MATCH,G",
		"flow":                         "[\"MATCH,G\"]",
		"rule without a target field":  "- MATCH",
		"placeholder":                  "- DOMAIN,{{ site_title }},G",
		"node reference":               "- \"@all\"",
		"carriage return":              "- MATCH,G\r\n",
		"second document":              "- MATCH,G\n---\n- MATCH,H",
		"number":                       "- 42",
		"empty":                        "",
	} {
		if analyseRuleBlock(shared, "  ") != nil {
			t.Errorf("%s: a block of this shape was accepted", name)
		}
	}
}

func TestRuleBlockCacheIsBoundedAndAnalysesOnce(t *testing.T) {
	var cache ruleBlockCache[ruleBlock]
	get := func(shared, indent string) *ruleBlock { return cache.get(shared, indent, analyseRuleBlock) }
	first := get("- MATCH,G", "  ")
	if first == nil || get("- MATCH,G", "  ") != first {
		t.Fatal("the same text was analysed twice")
	}
	if get("- MATCH,G", "    ") == first {
		t.Fatal("a different indentation shared an analysis")
	}
	for i := 0; i < 3*ruleBlockCacheEntries; i++ {
		get(fmt.Sprintf("- MATCH,G%d", i), "  ")
		get("- MATCH,G", "  ") // Stays recently used throughout.
	}
	if len(cache.entries) != ruleBlockCacheEntries {
		t.Fatalf("cache holds %d entries, bound is %d", len(cache.entries), ruleBlockCacheEntries)
	}
	if get("- MATCH,G", "  ") != first {
		t.Fatal("the entry in constant use was evicted")
	}

	var wg sync.WaitGroup
	blocks := make([]*ruleBlock, 16)
	for i := range blocks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocks[i] = get("- MATCH,Concurrent", "  ")
		}()
	}
	wg.Wait()
	for _, block := range blocks {
		if block == nil || block != blocks[0] {
			t.Fatal("concurrent first uses did not share one analysis")
		}
	}
}

// Renders of one Service share the analysed block and its record of compared
// contexts; -race covers the sharing.
func TestReducedRuleAssemblyConcurrentRenders(t *testing.T) {
	s, u := goldenScenario{mihomo: goldenTemplateComments, shared: "- DOMAIN,a.example,国外媒体\n# why\n- MATCH,国外媒体"}.build(t)
	want, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user := *u
			if i%2 == 1 {
				user.PersonalRules = fmt.Sprintf("- DOMAIN,p%d.example,DIRECT", i)
			}
			got, err := s.RenderForUser(context.Background(), &user, domain.ClientMihomo)
			if err != nil || i%2 == 0 && string(got.Body) != string(want.Body) {
				t.Errorf("concurrent render %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
}
