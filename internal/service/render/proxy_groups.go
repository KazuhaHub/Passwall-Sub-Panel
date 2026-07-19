package render

import (
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

var builtInRuleTargets = map[string]bool{
	"DIRECT":          true,
	"REJECT":          true,
	"REJECT-DROP":     true,
	"REJECT-DROP-BIT": true,
	"PASS":            true,
}

// defaultProxyGroupOrder preserves the original project ordering when a rule
// set does not declare a custom proxy_group_order. Groups that are not listed
// here are prepended in their first-occurrence order from the rule content.
var defaultProxyGroupOrder = []string{
	"🚀 节点选择",
	"🎮 UDP控制",
	"🇨🇳 中国大陆",
	"💬 Ai平台",
	"📹 油管视频",
	"🎥 奈飞视频",
	"📺 巴哈姆特",
	"🌍 国外媒体",
	"🎮 游戏平台",
	"📲 电报消息",
	"Ⓜ️ 微软Bing",
	"📢 谷歌FCM",
	"🌏 国内媒体",
	"📺 哔哩哔哩",
	"Ⓜ️ 微软云盘",
	"Ⓜ️ 微软服务",
	"🍎 苹果服务",
	"🎶 网易音乐",
	"🎯 全球直连",
	"🛑 广告拦截",
	"🍃 应用净化",
	"🐟 漏网之鱼",
}

type proxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  *int     `yaml:"interval,omitempty"`
	Lazy      *bool    `yaml:"lazy,omitempty"`
	Timeout   *int     `yaml:"timeout,omitempty"`
	Tolerance *int     `yaml:"tolerance,omitempty"`
	Strategy  string   `yaml:"strategy,omitempty"`
}

func buildProxyGroupsYAML(rules string, preferredOrder []string) (string, error) {
	return buildProxyGroupsYAMLInternal(rules, preferredOrder, nil, nil, nil, false)
}

func buildProxyGroupsYAMLWithMembers(rules string, preferredOrder []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, items []renderItem) (string, error) {
	return buildProxyGroupsYAMLInternal(rules, preferredOrder, members, options, items, true)
}

func buildProxyGroupsYAMLInternal(rules string, preferredOrder []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, items []renderItem, resolve bool) (string, error) {
	targets := ruleTargetsInOrder(rules)
	targets = withRequiredProxyGroupDependencies(targets)
	targets = withConfiguredProxyGroupDependencies(targets, members)
	targets = applyProxyGroupOrder(targets, preferredOrder)
	if len(targets) == 0 {
		return "[]", nil
	}

	lines := make([]string, 0, len(targets)*6)
	for _, target := range targets {
		groupOptions := domain.ProxyGroupOptions{Type: ProxyGroupTypeSelect}
		if configured, ok := options[target]; ok {
			groupOptions = EffectiveProxyGroupOptions(configured)
		}
		choices := proxyGroupChoices(target)
		if resolve {
			effectiveMembers := defaultMembersForTarget(target)
			if configured, ok := members[target]; ok {
				effectiveMembers = configured
			}
			choices = resolveConfiguredMembers(effectiveMembers, items)
		}
		// Auto types (url-test/fallback/load-balance) health-check their members
		// and pick a winner, so a DIRECT/REJECT sitting among real nodes would
		// short-circuit the whole group — DIRECT answers in ~0ms and always wins,
		// silently routing everything direct. Strip the built-in exits, and when
		// nothing testable is left (e.g. the configured members don't intersect
		// THIS user's authorized nodes) degrade to a plain manual selector rather
		// than emit a bogus single-member url-test.
		if groupOptions.Type != ProxyGroupTypeSelect {
			choices = withoutBuiltinExits(choices)
			if len(choices) == 0 {
				groupOptions = domain.ProxyGroupOptions{Type: ProxyGroupTypeSelect}
			}
		}
		if len(choices) == 0 {
			choices = []string{"DIRECT"}
		}
		lines = append(lines,
			"- name: "+yamlScalar(target),
			"  type: "+groupOptions.Type,
			"  proxies:",
		)
		for _, proxy := range choices {
			lines = append(lines, "  - "+yamlScalar(proxy))
		}
		if groupOptions.Type != ProxyGroupTypeSelect {
			lines = append(lines,
				"  url: "+yamlScalar(groupOptions.URL),
				"  interval: "+strconv.Itoa(*groupOptions.Interval),
				"  lazy: "+strconv.FormatBool(*groupOptions.Lazy),
				"  timeout: "+strconv.Itoa(*groupOptions.Timeout),
			)
			if groupOptions.Type == ProxyGroupTypeURLTest {
				lines = append(lines, "  tolerance: "+strconv.Itoa(*groupOptions.Tolerance))
			}
			if groupOptions.Type == ProxyGroupTypeLoadBalance {
				lines = append(lines, "  strategy: "+groupOptions.Strategy)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// applyProxyGroupOrder emits the explicitly ordered groups first, in the
// configured order, then appends any group the order does not mention at the
// END, preserving its first-occurrence order from the rule content. Keeping the
// unlisted groups last is what existing subscriptions already render, so a
// partial custom order never reshuffles the groups an admin did not name.
func applyProxyGroupOrder(targets, preferredOrder []string) []string {
	if len(preferredOrder) == 0 {
		preferredOrder = defaultProxyGroupOrder
	}
	remaining := make(map[string]bool, len(targets))
	for _, target := range targets {
		remaining[target] = true
	}
	out := make([]string, 0, len(targets))
	for _, preferred := range preferredOrder {
		preferred = strings.TrimSpace(preferred)
		if preferred == "" || !remaining[preferred] {
			continue
		}
		out = append(out, preferred)
		delete(remaining, preferred)
	}
	for _, target := range targets {
		if remaining[target] {
			out = append(out, target)
			delete(remaining, target)
		}
	}
	return out
}

// withoutBuiltinExits drops DIRECT/REJECT-family built-in outbounds from a
// resolved proxy list. Auto group types (url-test/fallback/load-balance) must
// only health-check real endpoints, never a built-in exit that would win or
// dilute the selection.
func withoutBuiltinExits(choices []string) []string {
	out := make([]string, 0, len(choices))
	for _, choice := range choices {
		if builtInRuleTargets[choice] {
			continue
		}
		out = append(out, choice)
	}
	return out
}

func withRequiredProxyGroupDependencies(targets []string) []string {
	hasNodeSelector := false
	needsNodeSelector := false
	for _, target := range targets {
		// Exact match, not substring: a custom group merely CONTAINING the phrase
		// (e.g. "美国节点选择") must not suppress the canonical "🚀 节点选择"
		// selector other emitted groups still reference, which would leave a
		// dangling reference Clash-family clients reject.
		if target == "🚀 节点选择" {
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
	case strings.Contains(name, "UDP控制"):
		// UDP catch-all selector: pick where ALL (non-local) UDP goes at
		// runtime — through the node (default), straight DIRECT (bypass proxy,
		// e.g. when the node's UDP is poor), or REJECT (drop UDP → QUIC falls
		// back to TCP). 🚀 节点选择 first so the default preserves today's
		// behaviour (UDP proxied through the chosen node).
		return []string{"🚀 节点选择", "DIRECT", "REJECT"}
	case strings.Contains(name, "全球直连"):
		return []string{"DIRECT"}
	case strings.Contains(name, "广告拦截") || strings.Contains(name, "应用净化"):
		return []string{"REJECT", "DIRECT"}
	case strings.Contains(name, "节点选择"):
		return []string{"DIRECT", "@all"}
	case strings.Contains(name, "中国大陆") ||
		strings.Contains(name, "国内媒体") ||
		strings.Contains(name, "哔哩哔哩") ||
		strings.Contains(name, "微软云盘") ||
		strings.Contains(name, "微软服务") ||
		strings.Contains(name, "苹果服务") ||
		strings.Contains(name, "网易音乐"):
		return []string{"DIRECT", "🚀 节点选择", "@all"}
	case strings.Contains(name, "国外媒体") ||
		strings.Contains(name, "奈飞视频") ||
		strings.Contains(name, "油管视频") ||
		strings.Contains(name, "巴哈姆特") ||
		strings.Contains(name, "游戏平台") ||
		strings.Contains(name, "Ai平台") ||
		strings.Contains(name, "电报消息") ||
		strings.Contains(name, "微软Bing") ||
		strings.Contains(name, "谷歌FCM"):
		return []string{"🚀 节点选择", "@all", "DIRECT"}
	case strings.Contains(name, "漏网之鱼"):
		return []string{"🚀 节点选择", "DIRECT", "@all"}
	default:
		// Conservative default for user-defined groups that don't match
		// any predefined case: DIRECT first, so an unrecognized group
		// (e.g. "Home" / "Company VPN") doesn't silently route traffic
		// through proxies. Users can switch to 🚀 节点选择 or a specific
		// node from the Clash UI when they actually want to proxy.
		return []string{"DIRECT", "🚀 节点选择", "@all"}
	}
}
