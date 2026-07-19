package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestBuildProxyGroupsYAMLEmitsMihomoTypesAndDefaults(t *testing.T) {
	zero, disabled, customTimeout := 0, false, 2000
	options := map[string]domain.ProxyGroupOptions{
		"Manual": {Type: ProxyGroupTypeSelect},
		"Auto":   {Type: ProxyGroupTypeURLTest},
		"Failover": {
			Type: ProxyGroupTypeFallback, URL: "https://example.com/check",
			Interval: &zero, Lazy: &disabled, Timeout: &customTimeout,
		},
		"Balanced": {Type: ProxyGroupTypeLoadBalance},
	}
	rules := "- DOMAIN,a,Manual\n- DOMAIN,b,Auto\n- DOMAIN,c,Failover\n- MATCH,Balanced"
	raw, err := buildProxyGroupsYAMLWithMembers(rules, nil, nil, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	var groups []proxyGroup
	if err := yaml.Unmarshal([]byte(raw), &groups); err != nil {
		t.Fatal(err)
	}
	byName := map[string]proxyGroup{}
	for _, group := range groups {
		byName[group.Name] = group
	}

	manual := byName["Manual"]
	if manual.Type != ProxyGroupTypeSelect || manual.URL != "" || manual.Interval != nil {
		t.Fatalf("manual = %#v", manual)
	}
	auto := byName["Auto"]
	if auto.Type != ProxyGroupTypeURLTest || auto.URL != DefaultProxyGroupTestURL || valueOfInt(auto.Interval) != DefaultProxyGroupInterval || valueOfInt(auto.Timeout) != DefaultProxyGroupTimeout || valueOfInt(auto.Tolerance) != DefaultProxyGroupTolerance || !valueOfBool(auto.Lazy) {
		t.Fatalf("auto = %#v", auto)
	}
	fallback := byName["Failover"]
	if fallback.Type != ProxyGroupTypeFallback || fallback.URL != "https://example.com/check" || valueOfInt(fallback.Interval) != 0 || valueOfInt(fallback.Timeout) != 2000 || valueOfBool(fallback.Lazy) || fallback.Tolerance != nil || fallback.Strategy != "" {
		t.Fatalf("fallback = %#v", fallback)
	}
	balanced := byName["Balanced"]
	if balanced.Type != ProxyGroupTypeLoadBalance || balanced.Strategy != DefaultProxyGroupStrategy || balanced.Tolerance != nil {
		t.Fatalf("balanced = %#v", balanced)
	}
}

func TestInspectProxyGroupOptionsReportsInvalidValuesAndWarnsForOneMember(t *testing.T) {
	negative, zero := -1, 0
	options := map[string]domain.ProxyGroupOptions{
		"Auto": {
			Type: ProxyGroupTypeURLTest, URL: "ftp://example.com/check",
			Interval: &negative, Timeout: &zero, Tolerance: &negative,
		},
		"Missing": {Type: ProxyGroupTypeLoadBalance, Strategy: "random"},
	}
	inspection := InspectProxyGroups("- MATCH,Auto", map[string][]domain.ProxyGroupMember{
		"Auto": {{Kind: "builtin", Value: "DIRECT"}},
	}, options, nil)
	codes := map[string]bool{}
	for _, issue := range inspection.Issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"invalid_test_url", "invalid_interval", "invalid_timeout", "invalid_tolerance", "unknown_group_options", "invalid_strategy", "insufficient_auto_members", "auto_group_builtin_member"} {
		if !codes[code] {
			t.Fatalf("missing issue %q: %#v", code, inspection.Issues)
		}
	}
}

func TestEffectiveProxyGroupOptionsSafelyFallsBackForInvalidYAML(t *testing.T) {
	invalid := EffectiveProxyGroupOptions(domain.ProxyGroupOptions{Type: "random", URL: "bad"})
	if invalid.Type != ProxyGroupTypeSelect {
		t.Fatalf("invalid type did not fall back to select: %#v", invalid)
	}
	loadBalance := EffectiveProxyGroupOptions(domain.ProxyGroupOptions{Type: ProxyGroupTypeLoadBalance, Strategy: "random"})
	if loadBalance.Strategy != DefaultProxyGroupStrategy || loadBalance.URL != DefaultProxyGroupTestURL {
		t.Fatalf("invalid load-balance options did not use defaults: %#v", loadBalance)
	}
}

func TestMergeFirstProxyGroupOptionsKeepsPrecedenceAndDeepCopiesPointers(t *testing.T) {
	firstInterval, secondInterval := 30, 60
	dst := map[string]domain.ProxyGroupOptions{"A": {Type: ProxyGroupTypeURLTest, Interval: &firstInterval}}
	src := map[string]domain.ProxyGroupOptions{
		"A": {Type: ProxyGroupTypeFallback, Interval: &secondInterval},
		"B": {Type: ProxyGroupTypeLoadBalance, Interval: &secondInterval},
	}
	duplicates := mergeFirstProxyGroupOptions(dst, src)
	if len(duplicates) != 1 || duplicates[0] != "A" || dst["A"].Type != ProxyGroupTypeURLTest {
		t.Fatalf("precedence failed: duplicates=%#v dst=%#v", duplicates, dst)
	}
	secondInterval = 90
	if valueOfInt(dst["B"].Interval) != 60 {
		t.Fatalf("merged options aliased source pointer: %#v", dst["B"])
	}
}

func valueOfInt(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func valueOfBool(value *bool) bool {
	return value != nil && *value
}
