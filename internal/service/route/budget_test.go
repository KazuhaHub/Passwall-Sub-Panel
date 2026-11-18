package route

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func presentOf(keys ...protocol.ListenerKey) map[protocol.ListenerKey]bool {
	out := map[protocol.ListenerKey]bool{}
	for _, key := range keys {
		out[key] = true
	}
	return out
}

func oneGroup(rules ...protocol.RouteRule) []routeGroup {
	return []routeGroup{{Listeners: []protocol.ListenerKey{"lst_1"}, Default: protocol.LocalExit, Rules: rules}}
}

// The figures are what the node's own RouteBudget counts for this config, worked
// out by hand from its xray compiler: the estimate (core.EstimateRoutedEntries)
// is 16, the compile counts 11 entries in 4 rules: the policy's trailing-dot
// guard is one more rule in the default segment, since a block rule names domains.
func TestAnalyseCountsWhatTheNodeCountsOnXray(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:a.com", "domain:b.com", "domain:c.com"}},
	}}
	groups := oneGroup(protocol.RouteRule{ID: "r1", Domains: []string{"domain:a.com", "domain:x.org"}, Exit: "lnk_1"})
	in := analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"), Groups: groups}

	planned, exempt := planRules(in)
	if got := estimate(planned, exempt, groups[0], false, nil); got != 16 {
		t.Fatalf("estimate = %d, want 16", got)
	}
	result := analyse(in)
	if result.Over != nil || result.Warn != nil {
		t.Fatalf("a tiny compile passes a limit: %+v %+v", result.Over, result.Warn)
	}
	if result.Entries != 11 || result.Rules != 4 {
		t.Fatalf("entries %d rules %d, want 11 and 4", result.Entries, result.Rules)
	}
	if len(result.Approximate) != 0 {
		t.Fatalf("an exact intersection is reported approximate: %+v", result.Approximate)
	}
}

// A rule scoped to exits is counted only in the segments of those exits, and the
// users a rule names are carried by every copy.
func TestAnalyseFollowsExitsAndUsers(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:a.com"}, Exits: []protocol.LinkKey{"lnk_1"}},
		{ID: "p2", Action: protocol.RuleBlock, Domains: []string{"domain:z.com"}, Subjects: []protocol.SubjectKey{"usr_1"}},
		{ID: "p3", Action: protocol.RuleBlock, Domains: []string{"domain:gone.com"}, Subjects: []protocol.SubjectKey{"usr_404"}},
	}}
	groups := oneGroup(protocol.RouteRule{ID: "r1", Ports: "443", Exit: "lnk_1"})
	in := analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"), Groups: groups,
		Clients: map[protocol.SubjectKey]int{"usr_1": 3}}
	result := analyse(in)
	// r1 segment: p1 (1 domain + 1 inbound) and p2 (1 domain + 1 inbound + 3 users), then the
	// catch-all (1 inbound); default segment: p2 only, p1 being scoped to lnk_1. p3 names a
	// subject with no client and is dropped. The trailing-dot guard is one rule of
	// an inbound and the dotted-name regexp in each segment (the route names no
	// domains, so it guards the whole route).
	if want := (1 + 1) + (1 + 1 + 3) + 1 + (1 + 1 + 3) + 2 + 2; result.Entries != want {
		t.Fatalf("entries = %d, want %d", result.Entries, want)
	}
	if result.Rules != 6 {
		t.Fatalf("rules = %d, want 6", result.Rules)
	}
}

// Past the node's limit the compile is over, and the listener named is the first
// of the group that tipped it.
func TestAnalyseSaysWhereTheLimitIsPassed(t *testing.T) {
	var rules []protocol.DestinationRule
	for i := 0; i < 70; i++ {
		rules = append(rules, protocol.DestinationRule{ID: "p" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Action: protocol.RuleBlock, Domains: generated(string(rune('a'+i%26))+string(rune('a'+i/26)), 15000)})
	}
	policy := &protocol.DestinationPolicy{Rules: rules}
	in := analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1", "lst_2"),
		Groups: []routeGroup{
			{Listeners: []protocol.ListenerKey{"lst_1"}, Default: protocol.LocalExit, Rules: []protocol.RouteRule{{ID: "r1", Domains: []string{"domain:q.example"}, Exit: "lnk_1"}}},
			{Listeners: []protocol.ListenerKey{"lst_2"}, Default: protocol.LocalExit, Rules: []protocol.RouteRule{{ID: "r1", Domains: []string{"domain:w.example"}, Exit: "lnk_1"}}},
		}}
	result := analyse(in)
	if result.Over == nil || result.Over.Limit != "entries" || result.Over.Max != MaxRoutedEntries || result.Over.Listener != "lst_1" {
		t.Fatalf("over = %+v, want entries at lst_1", result.Over)
	}
}

// From half of a limit the editor warns; the compile is still accepted. The
// node's estimate counts a rule once for the route's segment and once for the
// default segment even when the intersection with the route is empty, so 20 rules
// of 15,000 domains are about 600,000.
func TestAnalyseWarnsNearTheLimit(t *testing.T) {
	var rules []protocol.DestinationRule
	for i := 0; i < 20; i++ {
		rules = append(rules, protocol.DestinationRule{ID: "p" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Action: protocol.RuleBlock, Domains: generated(string(rune('a'+i%26))+string(rune('a'+i/26)), 15000)})
	}
	in := analysisInput{Policy: &protocol.DestinationPolicy{Rules: rules}, Engine: "xray", Present: presentOf("lst_1"),
		Groups: oneGroup(protocol.RouteRule{ID: "r1", Domains: []string{"domain:q.example"}, Exit: "lnk_1"})}
	result := analyse(in)
	if result.Over != nil {
		t.Fatalf("over = %+v", result.Over)
	}
	if result.Warn == nil || result.Warn.Limit != "entries" || result.Warn.Listener != "lst_1" || result.Warn.Used < MaxRoutedEntries/2 {
		t.Fatalf("warn = %+v", result.Warn)
	}
}

// sing-box writes each policy matcher once and refers to it, so the same policy
// costs far less there than the copies Xray makes of it.
func TestAnalyseSharesMatchersOnSingBox(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleBlock, Domains: generated("a", 2000)},
	}}
	// Each route is a keyword every one of those names contains, so Xray copies the
	// whole matcher into each route's segment.
	var routes []protocol.RouteRule
	for i := 0; i < 10; i++ {
		routes = append(routes, protocol.RouteRule{ID: "r" + string(rune('1'+i)), Domains: []string{"keyword:example"}, Exit: "lnk_1"})
	}
	groups := oneGroup(routes...)
	xray := analyse(analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"), Groups: groups})
	box := analyse(analysisInput{Policy: policy, Engine: "sing-box", Present: presentOf("lst_1"), Groups: groups})
	if xray.Entries < 11*2000 {
		t.Fatalf("xray entries = %d, want a copy of the matcher per segment", xray.Entries)
	}
	if box.Entries >= xray.Entries/4 || box.Entries < 2000 {
		t.Fatalf("sing-box %d entries, xray %d: the matcher should count once on sing-box", box.Entries, xray.Entries)
	}
	// Each segment holds one reference to the shared matcher; the policy needs a
	// sniff (it names domains), so there are two passes over the 11 segments.
	if box.Rules == 0 || box.Rules > 2*11*3 {
		t.Fatalf("sing-box rules = %d", box.Rules)
	}
}

// A regexp or keyword rule cannot be intersected exactly with a route on Xray:
// it is reported, and an allow copy is left out while a block copy is widened.
func TestAnalyseReportsWhatXrayCannotIntersect(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleAllow, Domains: []string{"regexp:^cdn[0-9]+\\.example\\.com$"}},
		{ID: "p2", Action: protocol.RuleBlock, Domains: []string{"regexp:^ads[0-9]+\\."}},
		{ID: "p3", Action: protocol.RuleBlock, Domains: []string{"domain:ok.example"}},
	}}
	groups := oneGroup(protocol.RouteRule{ID: "r1", Domains: []string{"domain:example.com"}, Exit: "lnk_1"})
	result := analyse(analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"), Groups: groups})
	got := map[string]protocol.RuleAction{}
	for _, item := range result.Approximate {
		if item.Route != "r1" || item.Listener != "lst_1" {
			t.Fatalf("approximation = %+v", item)
		}
		got[item.Rule] = item.Action
	}
	if len(got) != 2 || got["p1"] != protocol.RuleAllow || got["p2"] != protocol.RuleBlock {
		t.Fatalf("approximations = %+v, want p1 (allow) and p2 (block)", got)
	}
	// sing-box intersects exactly with a logical AND, so it reports none.
	if box := analyse(analysisInput{Policy: policy, Engine: "sing-box", Present: presentOf("lst_1"), Groups: groups}); len(box.Approximate) != 0 {
		t.Fatalf("sing-box approximations = %+v", box.Approximate)
	}
}

func TestGroupRoutesJoinsListenersThatRouteAlike(t *testing.T) {
	rules := map[protocol.ListenerKey][]protocol.RouteRule{
		"lst_1": {{ID: "r1", Domains: []string{"domain:a.com"}, Exit: "lnk_1"}},
		"lst_2": {{ID: "r1", Domains: []string{"domain:a.com"}, Exit: "lnk_1"}},
		"lst_3": {{ID: "r1", Domains: []string{"domain:a.com"}, Exit: "lnk_1"}},
	}
	defaults := map[protocol.ListenerKey]protocol.LinkKey{"lst_3": "lnk_2"}
	groups := groupRoutes([]protocol.ListenerKey{"lst_1", "lst_2", "lst_3", "lst_4"}, rules, defaults)
	if len(groups) != 2 || len(groups[0].Listeners) != 2 || groups[1].Default != "lnk_2" {
		t.Fatalf("groups = %+v", groups)
	}
}

// The encoded size is a warning, never a refusal: the panel cannot see the node's
// bytes, so it estimates the whole server (roster, policy, routed rules), warns
// from three quarters of the limit, and leaves the rejection to the node.
func TestAnalyseWarnsAboutTheServersConfigSizeButNeverRefusesOnIt(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Domains: generated("a", 20)}}}
	in := analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"),
		Clients: map[protocol.SubjectKey]int{"usr_1": 70000}, // a roster this large is 28 MB of clients
		Groups:  oneGroup(protocol.RouteRule{ID: "r1", Domains: []string{"domain:a0.example.com"}, Exit: "lnk_1"})}
	result := analyse(in)
	if result.Over != nil {
		t.Fatalf("a size estimate refused: %+v", result.Over)
	}
	if result.ServerBytes < int64(bytesWarnAt*MaxRoutedConfigBytes) {
		t.Fatalf("server bytes = %d, want over %d", result.ServerBytes, int64(bytesWarnAt*MaxRoutedConfigBytes))
	}
	small := analyse(analysisInput{Policy: policy, Engine: "xray", Present: presentOf("lst_1"), Groups: in.Groups})
	if small.ServerBytes >= int64(bytesWarnAt*MaxRoutedConfigBytes) || small.ServerBytes == 0 {
		t.Fatalf("a small server estimates %d bytes", small.ServerBytes)
	}
}
