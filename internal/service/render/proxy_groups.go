package render

import (
	"encoding/csv"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

var builtInRuleTargets = map[string]bool{
	"DIRECT":      true,
	"REJECT":      true,
	"REJECT-DROP": true,
	"PASS":        true,
}

// defaultProxyGroupOrder preserves the original project ordering when a rule
// set does not declare a custom proxy_group_order. Groups that are not listed
// here are prepended in their first-occurrence order from the main rules.
var defaultProxyGroupOrder = []string{
	"🚀 节点选择",
	"🎮 UDP控制",
	"⚡ QUIC控制",
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
	return buildProxyGroupsYAMLWithFeatures(rules, preferredOrder, members, options, items, MihomoRuleFeatures{})
}

func buildProxyGroupsYAMLWithFeatures(rules string, preferredOrder []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, items []renderItem, advanced MihomoRuleFeatures, templateTargets ...map[string]bool) (string, error) {
	templateNames := map[string]bool{}
	if len(templateTargets) > 0 {
		templateNames = templateTargets[0]
	}
	return buildProxyGroupsYAMLInternal(strings.Join(advanced.allRuleFragments(rules), "\n"), preferredOrder, members, options, items, true, advanced.outboundNames(), templateNames)
}

func buildProxyGroupsYAMLInternal(rules string, preferredOrder []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, items []renderItem, resolve bool, excluded ...map[string]bool) (string, error) {
	var excludedTargets map[string]bool
	if len(excluded) > 0 {
		excludedTargets = excluded[0]
	}
	targetExclusions := excludedTargets
	if resolve {
		targetExclusions = proxyTargetExclusions(items, excludedTargets)
	}
	if len(excluded) > 1 {
		for name := range excluded[1] {
			targetExclusions[name] = true
		}
	}
	targets := ruleTargetsInOrderExcluding(rules, targetExclusions, true)
	targets = withEffectiveProxyGroupDependencies(targets, members)
	if len(excluded) > 1 {
		kept := []string{}
		for _, name := range targets {
			if !excluded[1][name] {
				kept = append(kept, name)
			}
		}
		targets = kept
	}
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
			choices = resolveConfiguredMembersWithOutbounds(effectiveMembers, items)
		}
		// Auto types (url-test/fallback/load-balance) health-check their members
		// and pick a winner, so a DIRECT/REJECT or control-flow-only Rematch
		// outbound sitting among real nodes would
		// short-circuit the whole group — DIRECT answers in ~0ms and always wins,
		// silently routing everything direct. Strip the built-in exits, and when
		// nothing testable is left (e.g. the configured members don't intersect
		// THIS user's authorized nodes) degrade to a plain manual selector rather
		// than emit a bogus single-member url-test.
		if groupOptions.Type != ProxyGroupTypeSelect {
			choices = withoutBuiltinExits(choices, excludedTargets)
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

// Explicit rule targets can name a line directly. Such names are not groups;
// materializing default groups with the same names would create collisions.
func proxyTargetExclusions(items []renderItem, additional map[string]bool) map[string]bool {
	excluded := make(map[string]bool, len(items)+len(additional))
	for name, value := range additional {
		excluded[name] = value
	}
	for _, item := range items {
		excluded[item.name] = true
	}
	return excluded
}

// applyProxyGroupOrder emits the explicitly ordered groups first, in the
// configured order, then appends any group the order does not mention at the
// END, preserving its first-occurrence order from the main rules. Keeping the
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

// withoutBuiltinExits drops DIRECT/REJECT-family built-ins and optional
// control-flow-only outbounds from a resolved proxy list. Auto group types
// must health-check real endpoints only.
func withoutBuiltinExits(choices []string, nonTestable ...map[string]bool) []string {
	extra := map[string]bool{}
	if len(nonTestable) > 0 {
		extra = nonTestable[0]
	}
	out := make([]string, 0, len(choices))
	for _, choice := range choices {
		if builtInRuleTargets[choice] || extra[choice] {
			continue
		}
		out = append(out, choice)
	}
	return out
}

// withEffectiveProxyGroupDependencies closes the two implicit built-in group
// dependencies using effective members, never defaults that a user overrode.
// Other group references must still be declared by rules: synthesizing them
// here would hide missing_group errors and resurrect orphaned configuration.
func withEffectiveProxyGroupDependencies(targets []string, configs map[string][]domain.ProxyGroupMember) []string {
	seen := make(map[string]bool, len(targets))
	queue := append([]string(nil), targets...)
	for _, target := range targets {
		seen[target] = true
	}
	for index := 0; index < len(queue); index++ {
		target := queue[index]
		members, configured := configs[target]
		if !configured {
			members = defaultMembersForTarget(target)
		}
		for _, member := range members {
			if member.Kind != "proxy_group" || (member.Value != "🚀 节点选择" && member.Value != "🎮 UDP控制") || seen[member.Value] {
				continue
			}
			seen[member.Value] = true
			queue = append(queue, member.Value)
		}
	}
	// Preserve legacy dependency order when no explicit order is configured.
	out := make([]string, 0, len(queue))
	for _, dependency := range []string{"🚀 节点选择", "🎮 UDP控制"} {
		for _, added := range queue[len(targets):] {
			if added == dependency {
				out = append(out, added)
			}
		}
	}
	return append(out, targets...)
}

func ruleTargetsInOrder(rules string) []string {
	return ruleTargetsInOrderExcluding(rules, nil)
}

func ruleTargetsInOrderExcluding(rules string, excluded map[string]bool, rawPolicy ...bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, rawLine := range subscriptionRuleLines(rules) {
		parts := splitRuleFields(rawLine)
		if len(parts) < 2 || strings.EqualFold(parts[0], "SUB-RULE") {
			continue
		}
		target := ruleOutboundTarget(parts)
		if len(rawPolicy) > 0 && rawPolicy[0] {
			target = mihomoRuleTarget(rawLine)
		}
		if target == "" || builtInRuleTargets[target] || excluded[target] || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

// Saved rule fragments may quote an entire YAML rule scalar, independently of
// CSV quoting inside that scalar. Group discovery and route compilation must
// inspect decoded values, not confuse those two layers of quoting.
func subscriptionRuleLines(content string) []string {
	if lines, err := parseRuleSequence(content); err == nil {
		return lines
	}
	return strings.Split(content, "\n")
}

func normalizeRulePart(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == raw[len(raw)-1] {
		if raw[0] == '"' {
			reader := csv.NewReader(strings.NewReader(raw))
			if fields, err := reader.Read(); err == nil && len(fields) == 1 {
				return fields[0]
			}
		} else if raw[0] == '\'' {
			return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
		}
	}
	return raw
}

func proxyGroupChoices(name string) []string {
	switch {
	case strings.Contains(name, "QUIC控制"):
		// Disable Internet UDP/443 by default so browsers fall back to TCP.
		// Users can explicitly allow QUIC directly, via the node, or through
		// the general UDP selector. PASS has no equivalent sing-box outbound.
		return []string{"REJECT", "🎮 UDP控制", "🚀 节点选择", "DIRECT"}
	case strings.Contains(name, "UDP控制"):
		// General non-local UDP defaults to the local DIRECT exit, independently
		// of the main node selection. This allows UDP; it neither blocks it nor
		// depends on the selected node's UDP support, and it does not promise a
		// proxied source IP. QUIC is rejected by default; either selector can
		// still be overridden by subscribers.
		//
		// PASS hands UDP to the service rules below (YouTube to its group,
		// mainland IPs to 🇨🇳 中国大陆, the rest to the final MATCH), which the
		// node selector cannot express: that one forces all UDP through the node.
		// Both stay. PASS is second, never first, because sing-box has no PASS
		// outbound: a non-default PASS is simply dropped from its selector, while
		// a PASS default would remove the whole UDP selector and its route there.
		return []string{"DIRECT", "PASS", "🚀 节点选择", "REJECT"}
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
