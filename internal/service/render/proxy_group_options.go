package render

import (
	"net/url"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const (
	ProxyGroupTypeSelect      = "select"
	ProxyGroupTypeURLTest     = "url-test"
	ProxyGroupTypeFallback    = "fallback"
	ProxyGroupTypeLoadBalance = "load-balance"

	DefaultProxyGroupTestURL   = "https://www.gstatic.com/generate_204"
	DefaultProxyGroupInterval  = 300
	DefaultProxyGroupTimeout   = 5000
	DefaultProxyGroupTolerance = 50
	DefaultProxyGroupStrategy  = "consistent-hashing"
)

var validProxyGroupTypes = map[string]bool{
	ProxyGroupTypeSelect: true, ProxyGroupTypeURLTest: true,
	ProxyGroupTypeFallback: true, ProxyGroupTypeLoadBalance: true,
}

var validLoadBalanceStrategies = map[string]bool{
	"round-robin": true, "consistent-hashing": true, "sticky-sessions": true,
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

// EffectiveProxyGroupOptions returns safe, fully populated Mihomo settings.
// Invalid hand-written YAML falls back to select (invalid type) or the field
// defaults, while the inspector reports the corresponding issue to the admin.
func EffectiveProxyGroupOptions(raw domain.ProxyGroupOptions) domain.ProxyGroupOptions {
	typeName := strings.TrimSpace(raw.Type)
	if !validProxyGroupTypes[typeName] {
		typeName = ProxyGroupTypeSelect
	}
	if typeName == ProxyGroupTypeSelect {
		return domain.ProxyGroupOptions{Type: ProxyGroupTypeSelect}
	}

	testURL := strings.TrimSpace(raw.URL)
	if !validProxyGroupTestURL(testURL) {
		testURL = DefaultProxyGroupTestURL
	}
	interval := DefaultProxyGroupInterval
	if raw.Interval != nil && *raw.Interval >= 0 {
		interval = *raw.Interval
	}
	lazy := true
	if raw.Lazy != nil {
		lazy = *raw.Lazy
	}
	timeout := DefaultProxyGroupTimeout
	if raw.Timeout != nil && *raw.Timeout > 0 {
		timeout = *raw.Timeout
	}

	out := domain.ProxyGroupOptions{
		Type: typeName, URL: testURL, Interval: intPointer(interval),
		Lazy: boolPointer(lazy), Timeout: intPointer(timeout),
	}
	if typeName == ProxyGroupTypeURLTest {
		tolerance := DefaultProxyGroupTolerance
		if raw.Tolerance != nil && *raw.Tolerance >= 0 {
			tolerance = *raw.Tolerance
		}
		out.Tolerance = intPointer(tolerance)
	}
	if typeName == ProxyGroupTypeLoadBalance {
		strategy := strings.TrimSpace(raw.Strategy)
		if !validLoadBalanceStrategies[strategy] {
			strategy = DefaultProxyGroupStrategy
		}
		out.Strategy = strategy
	}
	return out
}

// NormalizeProxyGroupOptionsMap canonicalizes persisted options. select is the
// implicit default and is deliberately omitted to keep legacy YAML compact.
func NormalizeProxyGroupOptionsMap(raw map[string]domain.ProxyGroupOptions) map[string]domain.ProxyGroupOptions {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]domain.ProxyGroupOptions, len(raw))
	for group, options := range raw {
		effective := EffectiveProxyGroupOptions(options)
		if effective.Type != ProxyGroupTypeSelect {
			out[group] = effective
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateProxyGroupOptions(targets []string, configs map[string]domain.ProxyGroupOptions) []ProxyGroupIssue {
	issues := []ProxyGroupIssue{}
	targetSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		targetSet[target] = true
	}
	for group, options := range configs {
		if !targetSet[group] {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "unknown_group_options", Message: "代理组类型配置不在当前规则内容中"})
		}
		typeName := strings.TrimSpace(options.Type)
		if !validProxyGroupTypes[typeName] {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_group_type", Params: map[string]any{"value": options.Type}, Message: "未知代理组类型：" + options.Type})
			continue
		}
		if typeName == ProxyGroupTypeSelect {
			continue
		}
		if options.URL != "" && !validProxyGroupTestURL(strings.TrimSpace(options.URL)) {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_test_url", Message: "测试地址必须是有效的 HTTP/HTTPS URL"})
		}
		if options.Interval != nil && *options.Interval < 0 {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_interval", Message: "测试间隔不能小于 0"})
		}
		if options.Timeout != nil && *options.Timeout <= 0 {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_timeout", Message: "超时时间必须大于 0"})
		}
		if typeName == ProxyGroupTypeURLTest && options.Tolerance != nil && *options.Tolerance < 0 {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_tolerance", Message: "节点切换容差不能小于 0"})
		}
		if typeName == ProxyGroupTypeLoadBalance && options.Strategy != "" && !validLoadBalanceStrategies[strings.TrimSpace(options.Strategy)] {
			issues = append(issues, ProxyGroupIssue{Level: "error", Group: group, Code: "invalid_strategy", Params: map[string]any{"value": options.Strategy}, Message: "未知负载均衡策略：" + options.Strategy})
		}
	}
	return issues
}

func validProxyGroupTestURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
