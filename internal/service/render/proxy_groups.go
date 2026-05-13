package render

import (
	"strings"
)

var builtInRuleTargets = map[string]bool{
	"DIRECT":          true,
	"REJECT":          true,
	"REJECT-DROP":     true,
	"REJECT-DROP-BIT": true,
	"PASS":            true,
}

type proxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

func buildProxyGroupsYAML(rules string) (string, error) {
	targets := ruleTargetsInOrder(rules)
	targets = withRequiredProxyGroupDependencies(targets)
	if len(targets) == 0 {
		return "[]", nil
	}

	lines := make([]string, 0, len(targets)*6)
	for _, target := range targets {
		lines = append(lines,
			"- name: "+yamlScalar(target),
			"  type: select",
			"  proxies:",
		)
		for _, proxy := range proxyGroupChoices(target) {
			lines = append(lines, "  - "+yamlScalar(proxy))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func withRequiredProxyGroupDependencies(targets []string) []string {
	hasNodeSelector := false
	needsNodeSelector := false
	for _, target := range targets {
		if strings.Contains(target, "节点选择") {
			hasNodeSelector = true
			continue
		}
		for _, choice := range proxyGroupChoices(target) {
			if choice == "🚀 节点选择" {
				needsNodeSelector = true
				break
			}
		}
	}
	if !needsNodeSelector || hasNodeSelector {
		return targets
	}
	out := make([]string, 0, len(targets)+1)
	out = append(out, "🚀 节点选择")
	out = append(out, targets...)
	return out
}

func ruleTargetsInOrder(rules string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, rawLine := range strings.Split(rules, "\n") {
		line := strings.TrimSpace(rawLine)
		line = strings.TrimPrefix(line, "- ")
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "{{") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		useful := make([]string, 0, len(parts))
		for _, part := range parts {
			part = normalizeRulePart(part)
			if part == "" || part == "no-resolve" {
				continue
			}
			useful = append(useful, part)
		}
		if len(useful) < 2 {
			continue
		}
		target := useful[len(useful)-1]
		if builtInRuleTargets[target] || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

func normalizeRulePart(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), `"'`)
}

func proxyGroupChoices(name string) []string {
	switch {
	case strings.Contains(name, "全球直连"):
		return []string{"DIRECT"}
	case strings.Contains(name, "广告拦截") || strings.Contains(name, "应用净化"):
		return []string{"REJECT", "DIRECT"}
	case strings.Contains(name, "节点选择"):
		return []string{"DIRECT", "@all"}
	case strings.Contains(name, "中国大陆") ||
		strings.Contains(name, "国内媒体") ||
		strings.Contains(name, "哔哩哔哩") ||
		strings.Contains(name, "谷歌FCM") ||
		strings.Contains(name, "微软云盘") ||
		strings.Contains(name, "微软服务") ||
		strings.Contains(name, "苹果服务") ||
		strings.Contains(name, "网易音乐"):
		return []string{"DIRECT", "@all"}
	case strings.Contains(name, "漏网之鱼"):
		return []string{"🚀 节点选择", "DIRECT", "@all"}
	default:
		return []string{"🚀 节点选择", "@all", "DIRECT"}
	}
}
