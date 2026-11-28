package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// mihomoBodyInput is everything the Mihomo document is assembled from once the
// proxies are built. Both ways of assembling it read the same input and neither
// changes it, so one render can run both and compare them.
type mihomoBodyInput struct {
	template *domain.Template
	user     *domain.User
	bundle   resolvedRuleBundle
	items    []renderItem
	// proxiesYAML is the serialised `proxies` block, generated lines marked with
	// lineOrigin.
	proxiesYAML    string
	lineNames      *lineNameCatalog
	generatedLines map[string]bool
	lineOrigin     string
	// placeholders are resolved once per render: one of them is the current
	// time, and two assemblies of one render must see the same value.
	placeholders map[string]string
}

func (in *mihomoBodyInput) compose(groups, common, personal, subRules string) string {
	if groups == "[]" {
		groups = "" // A template may append its own group list entries.
	}
	if subRules == "{}" {
		subRules = "" // Likewise, allow template-owned sub-rule mappings.
	}
	body := substituteBlockPlaceholders(in.template.Content, map[string]string{
		"proxies":        strings.TrimRight(in.proxiesYAML, "\n"),
		"proxy_groups":   groups,
		"rules_common":   strings.TrimRight(common, "\n"),
		"rules_personal": strings.TrimRight(personal, "\n"),
		"sub_rules":      subRules,
	})
	body = substituteInlinePlaceholders(body, in.placeholders)
	return expandNodeRefs(body, in.items)
}

// Use a harmless list entry instead of [] so templates may append custom
// groups after the generated block. Only template-owned names are fixed.
func (in *mihomoBodyInput) probeGroup() string {
	return "- name: " + yamlScalar(in.lineNames.probe) + "\n  type: select\n  proxies: [DIRECT]"
}

// prepareSubRules returns the bundle's sub-rules with their targets bound, in a
// slice of its own.
func (in *mihomoBodyInput) prepareSubRules(fixedNames map[string]bool) ([]domain.MihomoSubRule, error) {
	subRules := append([]domain.MihomoSubRule(nil), in.bundle.SubRules...)
	for index := range subRules {
		content, err := in.lineNames.prepareRules(subRules[index].Content, fixedNames, in.generatedLines, true)
		if err != nil {
			return nil, err
		}
		subRules[index].Content = content
	}
	return subRules, nil
}

// full assembles the document with every rule in it at every step. It is the
// reference: whatever it returns, bytes or error, is the render's result.
func (in *mihomoBodyInput) full() (string, error) {
	bundle, lineNames := in.bundle, in.lineNames
	subRulesYAML, err := marshalMihomoSubRules(bundle.SubRules)
	if err != nil {
		return "", fmt.Errorf("marshal Mihomo sub-rules: %w", err)
	}
	// The probe carries the rule text although only the names under `proxies`
	// and `proxy-groups` are read from it: a rule block that does not fit the
	// template as YAML must fail HERE, with this error. Leaving the rules out
	// let a rule set with a second YAML document render, and moved the error of
	// a malformed block to a later step with a different text.
	probe, err := parseMihomoSubscription(in.compose(in.probeGroup(), bundle.SharedRules, in.user.PersonalRules, subRulesYAML))
	if err != nil {
		return "", fmt.Errorf("resolve template names: %w", err)
	}
	fixedNames, _, err := lineNames.mihomoNames(probe)
	if err != nil {
		return "", err
	}
	templateTargets := make(map[string]bool, len(fixedNames))
	for name := range fixedNames {
		templateTargets[name] = true
	}
	for name := range configuredGroupNames(bundle.ProxyGroupMembers, bundle.ProxyGroupOptions) {
		fixedNames[name] = true
	}
	personal, err := lineNames.prepareRules(in.user.PersonalRules, fixedNames, in.generatedLines, true)
	if err != nil {
		return "", err
	}
	shared, err := lineNames.prepareRules(bundle.SharedRules, fixedNames, in.generatedLines, true)
	if err != nil {
		return "", err
	}
	subRules, err := in.prepareSubRules(fixedNames)
	if err != nil {
		return "", err
	}
	subRulesYAML, err = marshalMihomoSubRules(subRules)
	if err != nil {
		return "", err
	}
	advanced := MihomoRuleFeatures{SubRules: subRules, RematchOutbounds: bundle.RematchOutbounds}
	groupRules := strings.Join([]string{personal, shared}, "\n")
	proxyGroupsYAML, err := buildProxyGroupsYAMLWithFeatures(groupRules, bundle.ProxyGroupOrder, bundle.ProxyGroupMembers, bundle.ProxyGroupOptions, in.items, advanced, templateTargets)
	if err != nil {
		return "", fmt.Errorf("build proxy groups: %w", err)
	}
	body := in.compose(proxyGroupsYAML, shared, personal, subRulesYAML)
	missingGroups, err := mihomoMissingTemplateGroups(body)
	if err != nil {
		return "", fmt.Errorf("resolve template dependencies: %w", err)
	}
	if len(missingGroups) > 0 {
		for _, group := range missingGroups {
			// These synthetic targets drive group generation only, never rules.
			groupRules += "\n- MATCH," + group
		}
		proxyGroupsYAML, err = buildProxyGroupsYAMLWithFeatures(groupRules, bundle.ProxyGroupOrder, bundle.ProxyGroupMembers, bundle.ProxyGroupOptions, in.items, advanced, templateTargets)
		if err != nil {
			return "", fmt.Errorf("build template dependency groups: %w", err)
		}
		body = in.compose(proxyGroupsYAML, shared, personal, subRulesYAML)
	}
	body, err = lineNames.finishMihomo(body, in.generatedLines, in.lineOrigin)
	if err != nil {
		return "", fmt.Errorf("finalize subscription line names: %w", err)
	}
	return body, nil
}

// reduced assembles the same document with the shared rules standing in as one
// rule per distinct target, then puts the whole block back as text. It follows
// full step for step; only the shared rule block differs at each one.
//
// It reports false instead of an error. Wherever it cannot be sure of producing
// what full produces (any failure, a rule target that names a subscription
// line, a template that does not carry the block as a plain list under `rules`)
// the caller runs full, so every error a subscriber or administrator sees is
// still worded by the reference.
func (in *mihomoBodyInput) reduced(block *ruleBlock) (string, bool) {
	bundle, lineNames := in.bundle, in.lineNames
	subRulesYAML, err := marshalMihomoSubRules(bundle.SubRules)
	if err != nil {
		return "", false
	}
	probe, err := parseMihomoSubscription(in.compose(in.probeGroup(), block.standIn, in.user.PersonalRules, subRulesYAML))
	if err != nil || !block.standsInRules(probe) {
		return "", false
	}
	fixedNames, _, err := lineNames.mihomoNames(probe)
	if err != nil {
		return "", false
	}
	templateTargets := make(map[string]bool, len(fixedNames))
	for name := range fixedNames {
		templateTargets[name] = true
	}
	for name := range configuredGroupNames(bundle.ProxyGroupMembers, bundle.ProxyGroupOptions) {
		fixedNames[name] = true
	}
	personal, err := lineNames.prepareRules(in.user.PersonalRules, fixedNames, in.generatedLines, true)
	if err != nil {
		return "", false
	}
	// prepareRules leaves a rule alone when its target resolves to itself. If
	// that holds for every distinct target, the prepared block is the analysed
	// one. A target that names a subscription line would be rewritten, per
	// subscriber, in every rule carrying it.
	for _, target := range block.targets {
		if resolved, err := lineNames.resolve(target, fixedNames, in.generatedLines); err != nil || resolved != target || strings.ContainsAny(resolved, ",\r\n") {
			return "", false
		}
	}
	subRules, err := in.prepareSubRules(fixedNames)
	if err != nil {
		return "", false
	}
	subRulesYAML, err = marshalMihomoSubRules(subRules)
	if err != nil {
		return "", false
	}
	advanced := MihomoRuleFeatures{SubRules: subRules, RematchOutbounds: bundle.RematchOutbounds}
	// Group discovery reads targets in rule order; the marker rule stands where
	// the block's rules would be and yields the block's targets in their order.
	groupRules := strings.Join([]string{personal, "- " + ruleBlockGroupMarker + "\n"}, "\n")
	proxyGroupsYAML, ok := buildProxyGroupsYAMLForBlock(block, groupRules, bundle.ProxyGroupOrder, bundle.ProxyGroupMembers, bundle.ProxyGroupOptions, in.items, advanced, templateTargets)
	if !ok {
		return "", false
	}
	body := in.compose(proxyGroupsYAML, block.standIn, personal, subRulesYAML)
	missingGroups, err := mihomoMissingTemplateGroups(body)
	if err != nil {
		return "", false
	}
	if len(missingGroups) > 0 {
		for _, group := range missingGroups {
			groupRules += "\n- MATCH," + group
		}
		proxyGroupsYAML, ok = buildProxyGroupsYAMLForBlock(block, groupRules, bundle.ProxyGroupOrder, bundle.ProxyGroupMembers, bundle.ProxyGroupOptions, in.items, advanced, templateTargets)
		if !ok {
			return "", false
		}
		body = in.compose(proxyGroupsYAML, block.standIn, personal, subRulesYAML)
	}
	body, err = lineNames.finishMihomo(body, in.generatedLines, in.lineOrigin)
	if err != nil {
		return "", false
	}
	return block.splice(body)
}

// mihomoBody returns the Mihomo document.
//
// The reduced assembly is exact by argument: the block's own serialisation is
// proven when it is analysed, and what surrounds the block (the template text
// around the placeholder, the subscriber's personal rules) is handled by the
// same parser and serialiser in both assemblies. That argument leans on how a
// YAML library attaches comments and chooses styles, so it is also checked: the
// first render of each context (the template, the personal rules and the rule
// sets' group and sub-rule settings) runs both assemblies and compares them. Only a context that compared equal uses the
// reduced assembly alone afterwards; one that did not is logged and keeps the
// full pipeline. Either way the bytes returned from a compared render are the
// reference's.
func (s *Service) mihomoBody(in *mihomoBodyInput) (string, error) {
	if s.fullPipelineOnly {
		return in.full()
	}
	block := s.sharedRuleBlock(in)
	if block == nil {
		return in.full()
	}
	context := renderContext(in.template.Content, in.user.PersonalRules, ruleSetSettings(in.bundle))
	proven, compared, recheck := block.compared.lookup(context)
	if compared && !proven {
		return in.full()
	}
	reduced, ok := in.reduced(block)
	if ok && proven && !recheck {
		return reduced, nil
	}
	// Not compared yet, due for its periodic comparison (reducedRecheckEvery),
	// or the reduced assembly declined this render (which says nothing about
	// the context: the next subscriber's lines may let it through).
	body, err := in.full()
	if ok {
		same := err == nil && body == reduced
		if !same {
			log.Error("render: reduced Mihomo rule assembly differs from the full pipeline; this template keeps the full pipeline", "template", in.template.Slug, "full_error", err)
		}
		block.compared.record(context, same)
		if s.ruleBlockCompared != nil {
			s.ruleBlockCompared(same)
		}
	}
	return body, err
}

// ruleSetSettings is everything a rule bundle carries besides its shared rule
// text, in a stable form: part of the context a comparison is valid for, so an
// administrator's edit of a group or a sub-rule is compared afresh.
func ruleSetSettings(bundle resolvedRuleBundle) string {
	raw, err := json.Marshal([]any{bundle.ProxyGroupOrder, bundle.ProxyGroupMembers, bundle.ProxyGroupOptions, bundle.SubRules, bundle.RematchOutbounds})
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

// sharedRuleBlock returns the analysed shared rule block of this render, or nil
// when the render has to take the full pipeline: no shared rules, or a template
// that does not place them on exactly one line of its own, indented by spaces.
func (s *Service) sharedRuleBlock(in *mihomoBodyInput) *ruleBlock {
	if strings.TrimSpace(in.bundle.SharedRules) == "" {
		return nil
	}
	indent, found := "", false
	for _, line := range strings.Split(in.template.Content, "\n") {
		if !strings.Contains(line, "rules_common") {
			continue
		}
		match := linePlaceholderRE.FindStringSubmatch(line)
		if match == nil || match[2] != "rules_common" {
			// Named in a comment or in an inline value: not the block placeholder.
			continue
		}
		// A second placeholder, a trailer after it or an indentation that is not
		// plain spaces changes how the block's lines sit in the document.
		if found || match[3] != "" || match[1] == "" || strings.Trim(match[1], " ") != "" {
			return nil
		}
		indent, found = match[1], true
	}
	if !found {
		return nil
	}
	return s.ruleBlocks.get(in.bundle.SharedRules, indent, analyseRuleBlock)
}
