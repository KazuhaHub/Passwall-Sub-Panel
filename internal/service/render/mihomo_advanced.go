package render

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// MihomoRuleFeatures groups the rule-set fields that are meaningful only to
// Mihomo. It is passed as an optional compiler input so legacy callers keep
// their existing shared-rule behavior.
type MihomoRuleFeatures struct {
	SubRules         []domain.MihomoSubRule
	RematchOutbounds []domain.MihomoRematchOutbound
}

// NormalizeMihomoRuleFeatures canonicalizes identifiers without rewriting raw
// rule fragments. It keeps persisted names identical to the symbols validated
// and emitted by the compiler.
func NormalizeMihomoRuleFeatures(features MihomoRuleFeatures) MihomoRuleFeatures {
	normalized := MihomoRuleFeatures{}
	normalized.SubRules = make([]domain.MihomoSubRule, len(features.SubRules))
	for index, subRule := range features.SubRules {
		subRule.Name = strings.TrimSpace(subRule.Name)
		normalized.SubRules[index] = subRule
	}
	normalized.RematchOutbounds = make([]domain.MihomoRematchOutbound, len(features.RematchOutbounds))
	for index, outbound := range features.RematchOutbounds {
		outbound.Name = strings.TrimSpace(outbound.Name)
		outbound.TargetRematchName = strings.TrimSpace(outbound.TargetRematchName)
		outbound.TargetSubRule = strings.TrimSpace(outbound.TargetSubRule)
		normalized.RematchOutbounds[index] = outbound
	}
	return normalized
}

// ValidateMihomoTemplateBundle checks invariants that only exist after several
// rule sets are bound to one Mihomo template.
func ValidateMihomoTemplateBundle(ruleSets []*domain.RuleSet) []ProxyGroupIssue {
	issues := []ProxyGroupIssue{}
	subRuleOwner := map[string]string{}
	outboundOwner := map[string]string{}
	for _, ruleSet := range ruleSets {
		if ruleSet == nil || !ruleSet.Enabled {
			continue
		}
		for _, subRule := range ruleSet.MihomoSubRules {
			name := strings.TrimSpace(subRule.Name)
			if name == "" {
				continue
			}
			if owner, exists := subRuleOwner[name]; exists {
				issues = append(issues, mihomoIssue("error", "sub_rule", name, "duplicate_bound_sub_rule", fmt.Sprintf("Mihomo 模板绑定的规则集 %s 与 %s 定义了同名子规则：%s", owner, ruleSet.Slug, name)))
				continue
			}
			subRuleOwner[name] = ruleSet.Slug
		}
		for _, outbound := range ruleSet.MihomoRematchOutbounds {
			name := strings.TrimSpace(outbound.Name)
			if name == "" {
				continue
			}
			if owner, exists := outboundOwner[name]; exists {
				issues = append(issues, mihomoIssue("error", "rematch_outbound", name, "duplicate_bound_rematch_outbound", fmt.Sprintf("Mihomo 模板绑定的规则集 %s 与 %s 定义了同名 Rematch 出站：%s", owner, ruleSet.Slug, name)))
				continue
			}
			outboundOwner[name] = ruleSet.Slug
		}
	}
	return issues
}

func (f MihomoRuleFeatures) outboundNames() map[string]bool {
	out := make(map[string]bool, len(f.RematchOutbounds))
	for _, outbound := range f.RematchOutbounds {
		if name := strings.TrimSpace(outbound.Name); name != "" {
			out[name] = true
		}
	}
	return out
}

func (f MihomoRuleFeatures) allRuleFragments(content string) []string {
	parts := []string{content}
	for _, subRule := range f.SubRules {
		parts = append(parts, subRule.Content)
	}
	return parts
}

// inspectMihomoFeatures validates references that must remain local to one
// rule set. Cross-rule-set name collisions are checked when a template bundle
// is compiled.
func inspectMihomoFeatures(shared string, features MihomoRuleFeatures, targets []string, members map[string][]domain.ProxyGroupMember, nodes []*domain.Node) []ProxyGroupIssue {
	features = NormalizeMihomoRuleFeatures(features)
	issues := []ProxyGroupIssue{}
	subRules := map[string]domain.MihomoSubRule{}
	for _, subRule := range features.SubRules {
		name := strings.TrimSpace(subRule.Name)
		if issue := validateMihomoSymbol("sub_rule", name); issue != nil {
			issues = append(issues, *issue)
			continue
		}
		if _, exists := subRules[name]; exists {
			issues = append(issues, mihomoIssue("error", "sub_rule", name, "duplicate_sub_rule", "子规则名称重复："+name))
			continue
		}
		subRules[name] = subRule
		lines, err := parseRuleSequence(subRule.Content)
		if err != nil {
			issues = append(issues, mihomoIssue("error", "sub_rule", name, "invalid_rule_yaml", "子规则不是有效的 YAML 规则列表："+err.Error()))
			continue
		}
		hasMatch := false
		for _, line := range lines {
			fields := splitRuleFields(line)
			if len(fields) == 0 {
				continue
			}
			switch strings.ToUpper(fields[0]) {
			case "MATCH":
				hasMatch = true
			case "SUB-RULE":
				issues = append(issues, mihomoIssue("error", "sub_rule", name, "nested_sub_rule", "Mihomo 子规则内不能再次跳转到 SUB-RULE"))
			}
		}
		if len(lines) > 0 && !hasMatch {
			issues = append(issues, mihomoIssue("warning", "sub_rule", name, "missing_match_fallback", "子规则没有 MATCH 兜底，未命中时将无法得到明确出口"))
		}
	}

	mainLines, err := parseRuleSequence(shared)
	if err != nil {
		issues = append(issues, mihomoIssue("error", "rules", "", "invalid_rule_yaml", "主规则不是有效的 YAML 规则列表："+err.Error()))
		mainLines = nil
	}
	for _, line := range mainLines {
		fields := splitRuleFields(line)
		if strings.EqualFold(firstField(fields), "SUB-RULE") {
			ref := subRuleReference(fields)
			if ref == "" || subRules[ref].Name == "" {
				issues = append(issues, mihomoIssue("error", "rules", ref, "missing_sub_rule", "SUB-RULE 引用的子规则不存在："+ref))
			}
		}
	}

	groupSet := map[string]bool{}
	for _, target := range targets {
		groupSet[target] = true
	}
	nodeNames := map[string]bool{}
	for _, node := range nodes {
		if node != nil {
			nodeNames[node.DisplayName] = true
		}
	}
	outbounds := map[string]domain.MihomoRematchOutbound{}
	outboundIndexes := map[string]int{}
	for outboundIndex, outbound := range features.RematchOutbounds {
		addOutboundIssue := func(issue ProxyGroupIssue) {
			if issue.Params == nil {
				issue.Params = map[string]any{}
			}
			issue.Params["index"] = outboundIndex
			issues = append(issues, issue)
		}
		name := strings.TrimSpace(outbound.Name)
		if issue := validateMihomoSymbol("rematch_outbound", name); issue != nil {
			addOutboundIssue(*issue)
			continue
		}
		if _, exists := outbounds[name]; exists {
			addOutboundIssue(mihomoIssue("error", "rematch_outbound", name, "duplicate_rematch_outbound", "Rematch 出站名称重复："+name))
			continue
		}
		outbound.Name = name
		outbound.TargetRematchName = strings.TrimSpace(outbound.TargetRematchName)
		outbound.TargetSubRule = strings.TrimSpace(outbound.TargetSubRule)
		outbounds[name] = outbound
		outboundIndexes[name] = outboundIndex
		if outbound.TargetRematchName == "" && outbound.TargetSubRule == "" {
			addOutboundIssue(mihomoIssue("error", "rematch_outbound", name, "missing_rematch_target", "Rematch 出站至少需要设置标记名或目标子规则"))
		}
		if outbound.TargetRematchName != "" {
			if issue := validateMihomoSymbol("rematch_outbound", outbound.TargetRematchName); issue != nil {
				issue.Name = name
				issue.Code = "invalid_rematch_name"
				issue.Message = "Rematch 标记名不能为空，也不能包含逗号或换行"
				addOutboundIssue(*issue)
			}
		}
		if outbound.TargetSubRule != "" {
			if _, ok := subRules[outbound.TargetSubRule]; !ok {
				addOutboundIssue(mihomoIssue("error", "rematch_outbound", name, "missing_target_sub_rule", "Rematch 目标子规则不存在："+outbound.TargetSubRule))
			}
		}
		if builtInRuleTargets[name] || groupSet[name] || nodeNames[name] {
			addOutboundIssue(mihomoIssue("error", "rematch_outbound", name, "outbound_name_collision", "Rematch 出站名称与内置出口、策略组或节点名称冲突："+name))
		}
	}

	// A main-rule rematch must be intercepted before the rule that selects the
	// rematch outbound. This pins the ordering requirement from Mihomo's own
	// documentation and prevents the common infinite-loop configuration.
	for name, outbound := range outbounds {
		if outbound.TargetSubRule != "" || outbound.TargetRematchName == "" {
			continue
		}
		handlerIndex, triggerIndex := -1, -1
		for index, line := range mainLines {
			fields := splitRuleFields(line)
			if len(fields) == 0 {
				continue
			}
			if strings.EqualFold(fields[0], "REMATCH-NAME") && len(fields) >= 3 && normalizeRulePart(fields[1]) == outbound.TargetRematchName && handlerIndex < 0 {
				handlerIndex = index
			}
			if ruleOutboundTarget(fields) == name && triggerIndex < 0 {
				triggerIndex = index
			}
		}
		if triggerIndex >= 0 && (handlerIndex < 0 || handlerIndex > triggerIndex) {
			issue := mihomoIssue("error", "rematch_outbound", name, "unsafe_rematch_order", "对应的 REMATCH-NAME 规则必须位于触发该 Rematch 出站的规则之前")
			issue.Params["index"] = outboundIndexes[name]
			issues = append(issues, issue)
		}
	}

	return issues
}

func parseRuleSequence(fragment string) ([]string, error) {
	if strings.TrimSpace(fragment) == "" {
		return nil, nil
	}
	var lines []string
	if err := yaml.Unmarshal([]byte(fragment), &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// splitRuleFields splits only top-level commas. Logical and SUB-RULE payloads
// contain nested comma-separated expressions that must remain one field.
func splitRuleFields(line string) []string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "{{") {
		return nil
	}
	fields := []string{}
	start, depth := 0, 0
	quote := rune(0)
	for index, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case r == ',' && depth == 0:
			fields = append(fields, normalizeRulePart(line[start:index]))
			start = index + 1
		}
	}
	fields = append(fields, normalizeRulePart(line[start:]))
	return fields
}

func ruleOutboundTarget(fields []string) string {
	if len(fields) < 2 || strings.EqualFold(fields[0], "SUB-RULE") {
		return ""
	}
	for index := len(fields) - 1; index > 0; index-- {
		candidate := normalizeRulePart(fields[index])
		if candidate == "" || strings.EqualFold(candidate, "no-resolve") || strings.EqualFold(candidate, "src") {
			continue
		}
		return candidate
	}
	return ""
}

func subRuleReference(fields []string) string {
	if len(fields) < 3 || !strings.EqualFold(fields[0], "SUB-RULE") {
		return ""
	}
	for index := len(fields) - 1; index >= 2; index-- {
		candidate := normalizeRulePart(fields[index])
		if candidate != "" && !strings.EqualFold(candidate, "no-resolve") {
			return candidate
		}
	}
	return ""
}

func firstField(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func validateMihomoSymbol(section, name string) *ProxyGroupIssue {
	if name == "" || strings.ContainsAny(name, ",\r\n") {
		issue := mihomoIssue("error", section, name, "invalid_name", "名称不能为空，也不能包含逗号或换行")
		return &issue
	}
	return nil
}

func mihomoIssue(level, section, name, code, message string) ProxyGroupIssue {
	return ProxyGroupIssue{Level: level, Section: section, Name: name, Code: code, Message: message, Params: map[string]any{"name": name}}
}

func marshalMihomoSubRules(subRules []domain.MihomoSubRule) (string, error) {
	if len(subRules) == 0 {
		return "{}", nil
	}
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, subRule := range subRules {
		lines, err := parseRuleSequence(subRule.Content)
		if err != nil {
			return "", fmt.Errorf("sub-rule %s: %w", subRule.Name, err)
		}
		nameNode := &yaml.Node{Kind: yaml.ScalarNode, Value: strings.TrimSpace(subRule.Name)}
		valueNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, line := range lines {
			valueNode.Content = append(valueNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: line})
		}
		root.Content = append(root.Content, nameNode, valueNode)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\n"), nil
}

func appendMihomoRematchOutbounds(proxies []map[string]any, outbounds []domain.MihomoRematchOutbound) ([]map[string]any, error) {
	seen := make(map[string]bool, len(proxies)+len(outbounds))
	for _, proxy := range proxies {
		if name, _ := proxy["name"].(string); name != "" {
			seen[name] = true
		}
	}
	out := append([]map[string]any(nil), proxies...)
	for _, outbound := range outbounds {
		name := strings.TrimSpace(outbound.Name)
		if seen[name] {
			return nil, fmt.Errorf("Mihomo rematch outbound name collides with an emitted proxy: %s", name)
		}
		seen[name] = true
		block := map[string]any{"name": name, "type": "rematch"}
		if target := strings.TrimSpace(outbound.TargetRematchName); target != "" {
			block["target-rematch-name"] = target
		}
		if target := strings.TrimSpace(outbound.TargetSubRule); target != "" {
			block["target-sub-rule"] = target
		}
		out = append(out, block)
	}
	return out, nil
}
