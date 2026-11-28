package render

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// singBoxBodyInput is everything the sing-box document is assembled from once
// the line outbounds are built. Like mihomoBodyInput, both assemblies read it
// and neither changes it.
type singBoxBodyInput struct {
	template *domain.Template
	user     *domain.User
	// shared is the bundle's rule text; order, members and options its
	// proxy-group settings.
	shared  string
	order   []string
	members map[string][]domain.ProxyGroupMember
	options map[string]domain.ProxyGroupOptions
	items   []renderItem
	// outbounds are the line outbounds, generated ones marked with lineOrigin.
	outbounds      []map[string]any
	lineNames      *lineNameCatalog
	generatedLines map[string]bool
	lineOrigin     string
	// placeholders are resolved once per render; see mihomoBodyInput.
	placeholders map[string]string
}

// templateNames reads the outbound tags the template defines itself from a
// document without rules.
func (in *singBoxBodyInput) templateNames() (fixedNames, templateTargets map[string]bool, err error) {
	probeOutbounds, err := marshalJSONBlock(in.outbounds)
	if err != nil {
		return nil, nil, err
	}
	probeBody := substituteBlockPlaceholders(in.template.Content, map[string]string{"outbounds": probeOutbounds, "route_rules": "[]", "rule_sets": "[]"})
	probeBody = substituteInlinePlaceholders(probeBody, mergePlaceholders(in.placeholders, map[string]string{"final_outbound": `"direct"`}))
	probe, err := parseSingBoxSubscription(probeBody)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve sing-box template names: %w", err)
	}
	fixedNames, _ = in.lineNames.singBoxNames(probe)
	templateTargets = make(map[string]bool, len(fixedNames))
	for name := range fixedNames {
		templateTargets[name] = true
	}
	for name := range configuredGroupNames(in.members, in.options) {
		fixedNames[name] = true
	}
	// Built-in exits have reserved target semantics even in custom templates.
	for name := range mihomoBuiltinNames() {
		fixedNames[name] = true
	}
	return fixedNames, templateTargets, nil
}

// assemble composes the document around the given groups and route rules and
// finishes it: rule_set definitions, pruning, final line names.
func (in *singBoxBodyInput) assemble(groups singBoxGroupCompilation, rules []map[string]any, finalOutbound string) (string, error) {
	outbounds := slices.Concat(in.outbounds, groups.Outbounds)
	outboundsJSON, err := marshalJSONBlock(outbounds)
	if err != nil {
		return "", fmt.Errorf("marshal sing-box outbounds: %w", err)
	}
	rulesJSON, err := marshalJSONBlock(rules)
	if err != nil {
		return "", fmt.Errorf("marshal sing-box route rules: %w", err)
	}
	finalJSON, err := marshalJSONString(finalOutbound)
	if err != nil {
		return "", fmt.Errorf("marshal sing-box final outbound: %w", err)
	}
	body := substituteBlockPlaceholders(in.template.Content, map[string]string{
		"outbounds":   outboundsJSON,
		"route_rules": rulesJSON,
	})
	body = substituteInlinePlaceholders(body, mergePlaceholders(in.placeholders, map[string]string{
		"final_outbound": finalJSON,
	}))
	// Emit the route.rule_set definitions required by the route rules
	// (GEOSITE/GEOIP) and the template's dns.rules split. Downloads route
	// through the primary proxy group so the .srs files reach GitHub.
	body, err = assembleSingBoxRuleSets(body, singBoxAvailableRuleSetDetour(in.order, outbounds))
	if err != nil {
		return "", fmt.Errorf("assemble sing-box rule_set: %w", err)
	}
	body, err = in.lineNames.finishSingBox(body, in.generatedLines, in.lineOrigin)
	if err != nil {
		return "", fmt.Errorf("finalize sing-box line names: %w", err)
	}
	return body, nil
}

// full assembles the document from every rule. It is the reference: whatever
// it returns, bytes or error, is the render's result.
func (in *singBoxBodyInput) full() (string, error) {
	fixedNames, templateTargets, err := in.templateNames()
	if err != nil {
		return "", err
	}
	personal, err := in.lineNames.prepareRules(in.user.PersonalRules, fixedNames, in.generatedLines)
	if err != nil {
		return "", err
	}
	shared, err := in.lineNames.prepareRules(in.shared, fixedNames, in.generatedLines)
	if err != nil {
		return "", err
	}
	groups := compileSingBoxGroups(strings.Join([]string{personal, shared}, "\n"), in.items, in.order, in.members, in.options, templateTargets)
	rules, finalOutbound := buildSingBoxRouteRulesWithPassThrough(groups.PassThrough, personal, shared)
	return in.assemble(groups, rules, finalOutbound)
}

// A singBoxRuleBlock is the shared rule text compiled once for every render:
// each rule that has a sing-box form, already serialised as the route rule the
// finished document carries for it. A render then only decides which of them
// apply to this subscriber (a group that defaults to PASS drops its rules, the
// first MATCH ends the list) and joins their text.
//
// The document itself is assembled with one rule per distinct outbound and per
// rule_set it references, between two marker objects: line-name binding,
// pruning, validation and rule_set collection read a route rule only through
// those two things. The joined text then replaces that stand-in.
type singBoxRuleBlock struct {
	// targets are the distinct rule targets in first-occurrence order, as group
	// discovery and line-name binding read them.
	targets []string
	rules   []singBoxBlockRule

	compared comparedContexts
}

type singBoxBlockRule struct {
	target   string
	outbound string
	match    bool
	// text is the route rule as an element of route.rules in the finished
	// document. Empty for a MATCH, which only sets route.final.
	text string
	// stand is the same route rule as a value, set on the rules that stand in
	// for the block while the document is assembled.
	stand map[string]any
}

// The marker objects bracket the stand-in in route.rules. No pass reads a key
// it does not know, and they never reach a client: the stand-in is replaced or
// the render falls back to the full pipeline.
const singBoxBlockMarkerKey = "psp_rules_common"

var (
	singBoxBlockBegin = map[string]any{singBoxBlockMarkerKey: "begin"}
	singBoxBlockEnd   = map[string]any{singBoxBlockMarkerKey: "end"}
)

// route.rules elements sit three levels deep in the finished document, which
// finishSingBox serialises with a two-space indent.
const singBoxRouteRuleIndent = "      "

// singBoxRouteRuleText serialises one route rule as an element of route.rules
// in the finished document. A rule gets there through marshalJSONBlock into the
// composed body and a parse and re-serialisation in finishSingBox; that this
// direct form is the same text is not assumed but proven per block, in
// analyseSingBoxRuleBlock.
func singBoxRouteRuleText(rule map[string]any) (string, bool) {
	raw, err := json.MarshalIndent(rule, singBoxRouteRuleIndent, "  ")
	if err != nil {
		return "", false
	}
	return singBoxRouteRuleIndent + string(raw), true
}

// analyseSingBoxRuleBlock compiles shared rule text, or returns nil when the
// compilation cannot be shown exact for it. As in analyseRuleBlock, the real
// parser and serialisers decide: the text must survive prepareRules unchanged
// as a list of rules, and the route rules serialised one by one must add up to
// what the full pipeline's document holds for all of them.
func analyseSingBoxRuleBlock(shared, _ string) *singBoxRuleBlock {
	if strings.Contains(shared, "{{") {
		return nil // The composed body substitutes placeholders in rule values.
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(shared), &document); err != nil {
		return nil
	}
	if len(document.Content) != 1 {
		return nil
	}
	sequence := document.Content[0]
	if sequence.Kind != yaml.SequenceNode || sequence.Style != 0 || sequence.Anchor != "" || len(sequence.Content) == 0 {
		return nil
	}
	values := make([]string, len(sequence.Content))
	for index, rule := range sequence.Content {
		if rule.Kind != yaml.ScalarNode || rule.Anchor != "" {
			return nil
		}
		values[index] = rule.Value
	}
	// What prepareRules returns when it rewrites no target, read back the two
	// ways the full pipeline reads it: on its own for the route rules, and
	// after the personal rules for group discovery.
	raw, err := yaml.Marshal(&document)
	if err != nil {
		return nil
	}
	if alone, err := parseRuleSequence(string(raw)); err != nil || !slices.Equal(alone, values) {
		return nil
	}
	framed, err := parseRuleSequence("- " + ruleBlockBegin + "\n\n" + string(raw) + "\n- " + ruleBlockEnd)
	if err != nil || len(framed) != len(values)+2 || !slices.Equal(framed[1:len(framed)-1], values) {
		return nil
	}

	block := &singBoxRuleBlock{}
	seenTargets := map[string]bool{}
	stands := map[string]bool{}
	reference := []map[string]any{singBoxBlockBegin}
	beginText, _ := singBoxRouteRuleText(singBoxBlockBegin)
	endText, _ := singBoxRouteRuleText(singBoxBlockEnd)
	texts := []string{beginText}
	for _, value := range values {
		if parts := splitRuleFields(value); len(parts) >= 2 && !strings.EqualFold(parts[0], "SUB-RULE") {
			if target := ruleOutboundTarget(parts); target != "" && !seenTargets[target] {
				seenTargets[target] = true
				block.targets = append(block.targets, target)
			}
		}
		// The same reading, in the same order, as buildSingBoxRouteRules.
		kind, ruleValue, target, ok := parseClashRuleLine(value)
		if !ok {
			continue
		}
		outbound := singBoxOutboundTag(target)
		if outbound == "" {
			continue
		}
		if kind == "MATCH" {
			block.rules = append(block.rules, singBoxBlockRule{target: target, outbound: outbound, match: true})
			continue
		}
		rule := singBoxRouteRule(kind, ruleValue)
		if len(rule) == 0 {
			continue
		}
		rule["action"] = "route"
		rule["outbound"] = outbound
		text, ok := singBoxRouteRuleText(rule)
		if !ok {
			return nil
		}
		compiled := singBoxBlockRule{target: target, outbound: outbound, text: text}
		// One rule stands for its target, and one for each rule_set a target's
		// rules reference: a target's rules are kept or dropped together.
		keys := []string{"target\x00" + target}
		for _, tag := range singBoxRuleSetTags(rule) {
			keys = append(keys, "rule_set\x00"+target+"\x00"+tag)
		}
		for _, key := range keys {
			if !stands[key] {
				stands[key] = true
				compiled.stand = rule
			}
		}
		block.rules = append(block.rules, compiled)
		reference = append(reference, rule)
		texts = append(texts, text)
	}
	reference = append(reference, singBoxBlockEnd)
	texts = append(texts, endText)

	// The proof: every route rule through the full pipeline's own steps, in one
	// document, against the texts serialised one by one.
	rulesJSON, err := marshalJSONBlock(reference)
	if err != nil {
		return nil
	}
	config, err := parseSingBoxSubscription(`{"route": {"rules": ` + rulesJSON + `}}`)
	if err != nil {
		return nil
	}
	serialised, err := json.MarshalIndent(config, "", "  ")
	if err != nil || string(serialised) != "{\n  \"route\": {\n    \"rules\": [\n"+strings.Join(texts, ",\n")+"\n    ]\n  }\n}" {
		return nil
	}
	return block
}

// singBoxRuleSetTags lists the rule_set tags a route rule references.
func singBoxRuleSetTags(rule map[string]any) []string {
	tags, _ := rule["rule_set"].([]string)
	return tags
}

// reduced assembles the same document as full with the shared rules standing in
// as one route rule per outbound and rule_set, then puts every applicable route
// rule back as text. Like the Mihomo reduction it reports false instead of an
// error, and the caller then runs full.
func (in *singBoxBodyInput) reduced(block *singBoxRuleBlock) (string, bool) {
	fixedNames, templateTargets, err := in.templateNames()
	if err != nil {
		return "", false
	}
	personal, err := in.lineNames.prepareRules(in.user.PersonalRules, fixedNames, in.generatedLines)
	if err != nil {
		return "", false
	}
	// prepareRules leaves a rule alone when its target resolves to itself; see
	// the Mihomo reduction.
	for _, target := range block.targets {
		if resolved, err := in.lineNames.resolve(target, fixedNames, in.generatedLines); err != nil || resolved != target {
			return "", false
		}
	}
	// A parse failure makes the full pipeline read this text line by line, YAML
	// syntax included; that reading cannot be reproduced from targets.
	groupLines, err := parseRuleSequence(strings.Join([]string{personal, "- " + ruleBlockGroupMarker + "\n"}, "\n"))
	if err != nil {
		return "", false
	}
	groups, standIns := compileSingBoxGroupsFromLines(block.targets, groupLines, in.items, in.order, in.members, in.options, templateTargets)
	if standIns != 1 {
		return "", false
	}
	rules, finalOutbound, matched := appendSingBoxRouteRules([]map[string]any{{"action": "sniff"}}, "direct", groups.PassThrough, personal)
	if matched {
		// A personal MATCH ends the route rules before the shared ones begin.
		body, err := in.assemble(groups, rules, finalOutbound)
		return body, err == nil
	}
	// The selection below is buildSingBoxRouteRules over the compiled rules: a
	// target whose group defaults to PASS is skipped, the first MATCH sets the
	// final outbound and ends the list.
	beginText, _ := singBoxRouteRuleText(singBoxBlockBegin)
	endText, _ := singBoxRouteRuleText(singBoxBlockEnd)
	rules = append(rules, singBoxBlockBegin)
	standIn := []string{beginText}
	size := 0
	for index := range block.rules {
		rule := &block.rules[index]
		if groups.PassThrough[rule.target] {
			continue
		}
		if rule.match {
			finalOutbound = rule.outbound
			break
		}
		size += len(rule.text) + 2
		if rule.stand != nil {
			rules = append(rules, rule.stand)
			standIn = append(standIn, rule.text)
		}
	}
	rules = append(rules, singBoxBlockEnd)
	standIn = append(standIn, endText)
	body, err := in.assemble(groups, rules, finalOutbound)
	if err != nil {
		return "", false
	}
	// The stand-in must be in the document exactly once and exactly as compiled:
	// an outbound renamed to a line's final name, or a template that carries the
	// route rules at another depth, fails the match. It follows the sniff rule
	// and any personal rules, so a comma precedes it.
	expected := ",\n" + strings.Join(standIn, ",\n") + "\n"
	at := strings.Index(body, expected)
	if at < 0 || strings.Count(body, `"`+singBoxBlockMarkerKey+`"`) != 2 {
		return "", false
	}
	var out strings.Builder
	out.Grow(len(body) + size)
	out.WriteString(body[:at])
	for index := range block.rules {
		rule := &block.rules[index]
		if groups.PassThrough[rule.target] {
			continue
		}
		if rule.match {
			break
		}
		out.WriteString(",\n")
		out.WriteString(rule.text)
	}
	out.WriteString(body[at+len(expected)-1:])
	return out.String(), true
}

// singBoxBody returns the sing-box document. The reduced assembly is used under
// the same terms as for Mihomo: see (*Service).mihomoBody.
func (s *Service) singBoxBody(in *singBoxBodyInput) (string, error) {
	if strings.TrimSpace(in.shared) == "" {
		return in.full()
	}
	block := s.singBoxRuleBlocks.get(in.shared, "", analyseSingBoxRuleBlock)
	if block == nil {
		return in.full()
	}
	context := renderContext(in.template.Content, in.user.PersonalRules, ruleSetSettings(resolvedRuleBundle{ProxyGroupOrder: in.order, ProxyGroupMembers: in.members, ProxyGroupOptions: in.options}))
	proven, compared := block.compared.lookup(context)
	if compared && !proven {
		return in.full()
	}
	reduced, ok := in.reduced(block)
	if ok && proven {
		return reduced, nil
	}
	body, err := in.full()
	if ok {
		same := err == nil && body == reduced
		if !same {
			log.Error("render: reduced sing-box rule assembly differs from the full pipeline; this template keeps the full pipeline", "template", in.template.Slug, "full_error", err)
		}
		block.compared.record(context, same)
		if s.ruleBlockCompared != nil {
			s.ruleBlockCompared(same)
		}
	}
	return body, err
}
