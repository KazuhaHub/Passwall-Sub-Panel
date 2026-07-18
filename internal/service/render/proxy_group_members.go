package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const maxProxyGroupMembers = 128

var proxyGroupBuiltins = []string{"DIRECT", "REJECT", "REJECT-DROP", "REJECT-DROP-BIT", "PASS"}

// ProxyGroupIssue is returned by the draft inspector and save validator.
// Errors block persistence; warnings describe references that are valid but
// do not currently resolve (for example a disabled/deleted node).
type ProxyGroupIssue struct {
	Level   string         `json:"level"`
	Group   string         `json:"group,omitempty"`
	Code    string         `json:"code,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

type ProxyGroupInspectNode struct {
	ID            int64    `json:"id"`
	DisplayName   string   `json:"display_name"`
	ServerAddress string   `json:"server_address"`
	Region        string   `json:"region"`
	Tags          []string `json:"tags"`
	Enabled       bool     `json:"enabled"`
}

type ProxyGroupInspectGroup struct {
	Name              string                    `json:"name"`
	Configured        bool                      `json:"configured"`
	OptionsConfigured bool                      `json:"options_configured"`
	Options           domain.ProxyGroupOptions  `json:"options"`
	DefaultMembers    []domain.ProxyGroupMember `json:"default_members"`
	Members           []domain.ProxyGroupMember `json:"members"`
	Preview           []string                  `json:"preview"`
}

type ProxyGroupInspection struct {
	Groups   []ProxyGroupInspectGroup `json:"groups"`
	Builtins []string                 `json:"builtins"`
	Nodes    []ProxyGroupInspectNode  `json:"nodes"`
	Regions  []string                 `json:"regions"`
	Tags     []string                 `json:"tags"`
	Issues   []ProxyGroupIssue        `json:"issues"`
}

// InspectProxyGroups parses the same target set used by subscription rendering,
// validates a draft member map, and resolves a metadata-only preview. It is
// intentionally free of repositories so the admin handler and unit tests can
// use the exact compiler semantics without constructing a render Service.
func InspectProxyGroups(content string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, nodes []*domain.Node, previewScope ...[]*domain.Node) ProxyGroupInspection {
	targets := withRequiredProxyGroupDependencies(ruleTargetsInOrder(content))
	targets = withConfiguredProxyGroupDependencies(targets, members)
	issues := validateProxyGroupMembers(targets, members, nodes)
	issues = append(issues, validateProxyGroupOptions(targets, options)...)

	regionsSet := map[string]bool{}
	tagsSet := map[string]bool{}
	inspectNodes := make([]ProxyGroupInspectNode, 0, len(nodes))
	sortOrders := make(map[int64]int, len(nodes))
	for _, n := range nodes {
		if n == nil || n.IsSeparator() {
			continue
		}
		inspectNodes = append(inspectNodes, ProxyGroupInspectNode{
			ID: n.ID, DisplayName: n.DisplayName, ServerAddress: n.ServerAddress, Region: n.Region,
			Tags: append([]string(nil), n.Tags...), Enabled: n.Enabled,
		})
		sortOrders[n.ID] = n.SortOrder
		if n.Region != "" {
			regionsSet[n.Region] = true
		}
		for _, tag := range n.Tags {
			if tag != "" {
				tagsSet[tag] = true
			}
		}
	}
	sort.SliceStable(inspectNodes, func(i, j int) bool {
		if sortOrders[inspectNodes[i].ID] != sortOrders[inspectNodes[j].ID] {
			return sortOrders[inspectNodes[i].ID] < sortOrders[inspectNodes[j].ID]
		}
		return inspectNodes[i].ID < inspectNodes[j].ID
	})

	previewNodes := nodes
	if len(previewScope) > 0 && previewScope[0] != nil {
		previewNodes = previewScope[0]
	}
	previewItems := make([]renderItem, 0, len(previewNodes))
	for _, n := range previewNodes {
		if n != nil && n.Enabled && !n.IsSeparator() {
			previewItems = append(previewItems, renderItem{name: n.DisplayName, node: n})
		}
	}
	sort.SliceStable(previewItems, func(i, j int) bool {
		if previewItems[i].node.SortOrder != previewItems[j].node.SortOrder {
			return previewItems[i].node.SortOrder < previewItems[j].node.SortOrder
		}
		return previewItems[i].node.ID < previewItems[j].node.ID
	})

	groups := make([]ProxyGroupInspectGroup, 0, len(targets))
	for _, target := range targets {
		defaults := defaultMembersForTarget(target)
		configuredMembers, configured := members[target]
		effective := defaults
		if configured {
			effective = configuredMembers
		}
		effectiveOptions := domain.ProxyGroupOptions{Type: ProxyGroupTypeSelect}
		rawOptions, optionsConfigured := options[target]
		if optionsConfigured {
			effectiveOptions = EffectiveProxyGroupOptions(rawOptions)
		}
		preview := resolveConfiguredMembers(effective, previewItems)
		if (effectiveOptions.Type == ProxyGroupTypeURLTest || effectiveOptions.Type == ProxyGroupTypeLoadBalance) && len(preview) < 2 {
			issues = append(issues, ProxyGroupIssue{Level: "warning", Group: target, Code: "insufficient_auto_members", Message: "自动选择或负载均衡通常至少需要两个可用成员"})
		}
		groups = append(groups, ProxyGroupInspectGroup{
			Name: target, Configured: configured, OptionsConfigured: optionsConfigured,
			Options:        effectiveOptions,
			DefaultMembers: defaults,
			Members:        append([]domain.ProxyGroupMember(nil), effective...),
			Preview:        preview,
		})
	}

	return ProxyGroupInspection{
		Groups: groups, Builtins: append([]string(nil), proxyGroupBuiltins...),
		Nodes: inspectNodes, Regions: sortedKeys(regionsSet), Tags: sortedKeys(tagsSet), Issues: issues,
	}
}

func withConfiguredProxyGroupDependencies(targets []string, configs map[string][]domain.ProxyGroupMember) []string {
	hasNodeSelector := false
	needsNodeSelector := false
	for _, target := range targets {
		if target == "🚀 节点选择" {
			hasNodeSelector = true
		}
	}
	for _, members := range configs {
		for _, member := range members {
			if member.Kind == "proxy_group" && member.Value == "🚀 节点选择" {
				needsNodeSelector = true
			}
		}
	}
	if !needsNodeSelector || hasNodeSelector {
		return targets
	}
	return append([]string{"🚀 节点选择"}, targets...)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func defaultMembersForTarget(target string) []domain.ProxyGroupMember {
	raw := proxyGroupChoices(target)
	out := make([]domain.ProxyGroupMember, 0, len(raw))
	for _, choice := range raw {
		switch {
		case choice == "@all":
			out = append(out, domain.ProxyGroupMember{Kind: "node_set", Value: "remaining"})
		case builtInRuleTargets[choice]:
			out = append(out, domain.ProxyGroupMember{Kind: "builtin", Value: choice})
		default:
			out = append(out, domain.ProxyGroupMember{Kind: "proxy_group", Value: choice})
		}
	}
	return out
}

func validateProxyGroupMembers(targets []string, configs map[string][]domain.ProxyGroupMember, nodes []*domain.Node) []ProxyGroupIssue {
	issues := []ProxyGroupIssue{}
	targetSet := map[string]bool{}
	for _, t := range targets {
		targetSet[t] = true
	}
	nodeSet := map[int64]*domain.Node{}
	for _, n := range nodes {
		if n != nil {
			nodeSet[n.ID] = n
		}
	}
	graph := map[string][]string{}

	for group, list := range configs {
		if !targetSet[group] {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "unknown_group", Message: "策略组不在当前规则内容中"})
		}
		if len(list) == 0 {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "empty_members", Message: "自定义成员不能为空；如需默认行为请删除自定义配置"})
		}
		if len(list) > maxProxyGroupMembers {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "too_many_members", Params: map[string]any{"max": maxProxyGroupMembers}, Message: fmt.Sprintf("成员数量不能超过 %d", maxProxyGroupMembers)})
		}
		seen := map[string]bool{}
		hasRemaining := false
		for _, member := range list {
			key := member.Kind + ":" + member.Value + ":" + strconv.FormatInt(member.NodeID, 10)
			if seen[key] {
				label := memberLabel(member, nodeSet)
				issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "duplicate", Params: map[string]any{"member": label}, Message: "存在重复成员：" + label})
				continue
			}
			seen[key] = true
			switch member.Kind {
			case "builtin":
				if !builtInRuleTargets[member.Value] {
					issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "unknown_builtin", Params: map[string]any{"value": member.Value}, Message: "未知内置出口：" + member.Value})
				}
			case "proxy_group":
				if member.Value == group {
					issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "self_reference", Message: "代理组不能引用自身"})
				} else if !targetSet[member.Value] {
					issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "missing_group", Params: map[string]any{"value": member.Value}, Message: "引用的代理组不存在：" + member.Value})
				} else {
					graph[group] = append(graph[group], member.Value)
				}
			case "node":
				if member.NodeID <= 0 {
					issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_node_id", Message: "节点引用缺少有效 node_id"})
				} else if n := nodeSet[member.NodeID]; n == nil {
					issues = append(issues, ProxyGroupIssue{Level: "warning", Group: group, Code: "missing_node", Params: map[string]any{"id": member.NodeID}, Message: fmt.Sprintf("节点 #%d 已不存在，渲染时会跳过", member.NodeID)})
				} else if !n.Enabled {
					issues = append(issues, ProxyGroupIssue{Level: "warning", Group: group, Code: "disabled_node", Params: map[string]any{"name": n.DisplayName}, Message: n.DisplayName + " 当前已禁用，渲染时会跳过"})
				}
			case "node_set":
				valid := member.Value == "remaining" ||
					(strings.HasPrefix(member.Value, "region:") && strings.TrimPrefix(member.Value, "region:") != "") ||
					(strings.HasPrefix(member.Value, "tag:") && strings.TrimPrefix(member.Value, "tag:") != "")
				if !valid {
					issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_node_set", Params: map[string]any{"value": member.Value}, Message: "非法节点集合：" + member.Value})
				} else if member.Value != "remaining" && !nodeSetHasMatch(member.Value, nodes) {
					issues = append(issues, ProxyGroupIssue{Level: "warning", Group: group, Code: "empty_node_set", Params: map[string]any{"value": member.Value}, Message: "节点集合当前没有匹配项：" + member.Value})
				}
				if member.Value == "remaining" {
					hasRemaining = true
				}
			default:
				issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "unknown_kind", Params: map[string]any{"value": member.Kind}, Message: "未知成员类型：" + member.Kind})
			}
		}
		if len(list) > 0 && !hasRemaining {
			issues = append(issues, ProxyGroupIssue{Level: "warning", Group: group, Code: "missing_remaining", Message: "未包含“其余节点”，未来新增节点不会自动进入此代理组"})
		}
	}

	state := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		if state[n] == 1 {
			return true
		}
		if state[n] == 2 {
			return false
		}
		state[n] = 1
		for _, next := range graph[n] {
			if visit(next) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	for group := range graph {
		if visit(group) {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "cycle", Message: "代理组引用形成循环"})
			break
		}
	}
	return issues
}

func nodeSetHasMatch(selector string, nodes []*domain.Node) bool {
	for _, n := range nodes {
		if n == nil || n.IsSeparator() {
			continue
		}
		switch {
		case strings.HasPrefix(selector, "region:") && n.Region == strings.TrimPrefix(selector, "region:"):
			return true
		case strings.HasPrefix(selector, "tag:"):
			want := strings.TrimPrefix(selector, "tag:")
			for _, tag := range n.Tags {
				if tag == want {
					return true
				}
			}
		}
	}
	return false
}

func memberLabel(member domain.ProxyGroupMember, nodes map[int64]*domain.Node) string {
	if member.Kind == "node" {
		if n := nodes[member.NodeID]; n != nil {
			return n.DisplayName
		}
		return fmt.Sprintf("node #%d", member.NodeID)
	}
	return member.Value
}

func hasProxyGroupErrors(issues []ProxyGroupIssue) bool {
	for _, issue := range issues {
		if issue.Level == "error" {
			return true
		}
	}
	return false
}

// resolveConfiguredMembers expands typed member definitions against the
// already layout-sorted, user-authorized render items. It deduplicates final
// labels globally, which makes "specific node, DIRECT, remaining" do what an
// admin expects without repeating that node inside remaining.
func resolveConfiguredMembers(members []domain.ProxyGroupMember, items []renderItem) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	addMatching := func(match func(renderItem) bool) {
		for _, item := range items {
			if match(item) {
				add(item.name)
			}
		}
	}
	for _, member := range members {
		switch member.Kind {
		case "builtin", "proxy_group":
			add(member.Value)
		case "node":
			addMatching(func(item renderItem) bool { return item.node != nil && item.node.ID == member.NodeID })
		case "node_set":
			switch {
			case member.Value == "remaining":
				addMatching(func(renderItem) bool { return true })
			case strings.HasPrefix(member.Value, "region:"):
				region := strings.TrimPrefix(member.Value, "region:")
				addMatching(func(item renderItem) bool { return item.node != nil && item.node.Region == region })
			case strings.HasPrefix(member.Value, "tag:"):
				tag := strings.TrimPrefix(member.Value, "tag:")
				addMatching(func(item renderItem) bool {
					if item.node == nil {
						return false
					}
					for _, t := range item.node.Tags {
						if t == tag {
							return true
						}
					}
					return false
				})
			}
		}
	}
	return out
}
