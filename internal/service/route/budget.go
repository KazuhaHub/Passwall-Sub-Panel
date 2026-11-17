package route

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// The limits Passwall Node holds a routed compile to (core.MaxRoutedEntries,
// core.MaxRoutedRules and core.MaxRoutedConfigBytes in the node). A listener
// whose compile passes them is rejected as itself, so the panel computes the
// same figures when routes are saved and refuses what the node would refuse.
const (
	MaxRoutedEntries     = 1_000_000
	MaxRoutedRules       = 20000
	MaxRoutedConfigBytes = 32 << 20
)

// warnAt is the share of a limit from which the editor warns: the node still
// accepts the listener, but a bigger blocklist or one more route may not.
const warnAt = 0.5

// bytesWarnAt is the share of MaxRoutedConfigBytes from which the estimate of the
// whole server's config warns.
const bytesWarnAt = 0.75

// What the roster and the listeners add to a server's encoded config, as a
// deliberately high estimate: a client entry with its credentials, and a
// listener with its settings.
const (
	clientConfigBytes   = 400
	listenerConfigBytes = 4096
)

// perEntryBytes is what one destination entry adds to the encoded config
// besides its own text: quotes, comma and indentation.
const perEntryBytes = 6

// routeGroup is a set of routed listeners that route identically: the same rules
// and the same default exit. The node writes each segment once per group, so a
// group is what is counted once (core.RouteGroup).
type routeGroup struct {
	Listeners []protocol.ListenerKey
	Default   protocol.LinkKey
	Rules     []protocol.RouteRule
}

// groupRoutes groups routed listeners the way the node does, in the order the
// first listener of each group appears in order.
func groupRoutes(order []protocol.ListenerKey, rules map[protocol.ListenerKey][]protocol.RouteRule, defaults map[protocol.ListenerKey]protocol.LinkKey) []routeGroup {
	var groups []routeGroup
	index := map[string]int{}
	for _, key := range order {
		list, routed := rules[key]
		if !routed {
			continue
		}
		exit := protocol.LocalExit
		if d, ok := defaults[key]; ok && d != "" {
			exit = d
		}
		encoded, _ := json.Marshal(struct {
			Default protocol.LinkKey
			Rules   []protocol.RouteRule
		}{exit, list})
		at, seen := index[string(encoded)]
		if !seen {
			at = len(groups)
			index[string(encoded)] = at
			groups = append(groups, routeGroup{Default: exit, Rules: list})
		}
		groups[at].Listeners = append(groups[at].Listeners, key)
	}
	return groups
}

// plannedRule is one policy rule as a compile sees it: what it names resolved
// against the roster and the listeners the node serves (core.PlannedRule).
type plannedRule struct {
	rule     protocol.DestinationRule
	users    int
	inbounds []protocol.ListenerKey
}

// analysisInput is what the route budget is computed from.
type analysisInput struct {
	Policy  *protocol.DestinationPolicy
	Resolve protocol.ResolveMode
	Engine  string
	// Clients counts the roster's clients per subject.
	Clients map[protocol.SubjectKey]int
	// Present are the listeners the server is sent.
	Present map[protocol.ListenerKey]bool
	Groups  []routeGroup
}

// Approximation is a policy rule a route cannot be intersected with exactly on
// Xray: its copy in the route's segment is left out when the rule allows or
// only observes, and kept as the superset it is when it blocks.
type Approximation struct {
	Listener protocol.ListenerKey
	Route    string
	Rule     string
	Action   protocol.RuleAction
}

// GroupUsage is what one route group adds to the compile.
type GroupUsage struct {
	Listeners []protocol.ListenerKey
	Entries   int
	Rules     int
	Bytes     int64
}

// Overrun says which limit a compile passes and where.
type Overrun struct {
	Listener protocol.ListenerKey
	// Limit is "entries", "rules" or "bytes".
	Limit string
	Used  int64
	Max   int64
}

// Analysis is the figures of a routed compile.
type Analysis struct {
	Groups []GroupUsage
	// Entries, Rules and Bytes are the totals the node's budget would read.
	Entries int
	Rules   int
	Bytes   int64
	// ServerBytes is a conservative estimate of the whole encoded config of the
	// server: roster, destination policy and routed rules. The node rejects a
	// routed listener past MaxRoutedConfigBytes, which the panel cannot read off
	// its own count, so it warns from bytesWarnAt of the limit and leaves the
	// refusal to the node (whose issue stays the backstop).
	ServerBytes int64
	// Over is the first limit passed, nil when the compile fits. Warn names the
	// first listener at or past warnAt of a limit while the compile still fits.
	Over *Overrun
	Warn *Overrun
	// Approximate lists the policy rules a route cannot be intersected with
	// exactly (Xray only).
	Approximate []Approximation
}

func ruleEntries(rule protocol.DestinationRule) int {
	n := len(rule.Domains) + len(rule.CIDRs)
	if rule.Private {
		n += len(protocol.PrivateDestinationCIDRs())
	}
	return n
}

func routeEntries(route protocol.RouteRule) int { return len(route.Domains) + len(route.CIDRs) }

func exitsAllow(exits []protocol.LinkKey, exit protocol.LinkKey) bool {
	return len(exits) == 0 || slices.Contains(exits, exit)
}

// planRules resolves the policy against the roster as the node does: a rule
// whose subjects expand to no client, or whose named inbounds are all absent,
// is dropped.
func planRules(in analysisInput) (planned []plannedRule, exemptUsers int) {
	if in.Policy == nil {
		return nil, 0
	}
	for _, rule := range in.Policy.Rules {
		item := plannedRule{rule: rule}
		if len(rule.Subjects) > 0 {
			for _, subject := range rule.Subjects {
				item.users += in.Clients[subject]
			}
			if item.users == 0 {
				continue
			}
		}
		if len(rule.Inbounds) > 0 {
			for _, key := range rule.Inbounds {
				if in.Present[key] {
					item.inbounds = append(item.inbounds, key)
				}
			}
			if len(item.inbounds) == 0 {
				continue
			}
		}
		planned = append(planned, item)
	}
	for _, subject := range in.Policy.Exempt {
		exemptUsers += in.Clients[subject]
	}
	return planned, exemptUsers
}

// forSegment is the planned rules that apply to traffic leaving through exit,
// confined to the group's listeners (core.PolicyPlan.ForSegment).
func forSegment(planned []plannedRule, listeners []protocol.ListenerKey, exit protocol.LinkKey) []plannedRule {
	var out []plannedRule
	for _, item := range planned {
		candidates := item.inbounds
		if candidates == nil {
			candidates = listeners
		}
		var kept []protocol.ListenerKey
		for _, key := range candidates {
			if slices.Contains(listeners, key) && exitsAllow(item.rule.Exits, exit) {
				kept = append(kept, key)
			}
		}
		if len(kept) == 0 {
			continue
		}
		item.inbounds = kept
		out = append(out, item)
	}
	return out
}

// estimate is the node's cheap up-front count of one group's entries
// (core.EstimateRoutedEntries): it may undercount, and the node refuses a
// group whose running total passes the limit before it builds anything. With
// shared (sing-box) a policy rule's matcher counts once, in seen.
func estimate(planned []plannedRule, exemptUsers int, group routeGroup, shared bool, seen map[string]bool) int {
	total := 0
	segment := func(exit protocol.LinkKey, route *protocol.RouteRule) {
		if route != nil {
			total += routeEntries(*route) + 1
		}
		rules := forSegment(planned, group.Listeners, exit)
		total += exemptUsers
		for _, item := range rules {
			rule := item.rule
			if shared {
				total += 1 + item.users
				if !seen[rule.ID] {
					seen[rule.ID] = true
					total += ruleEntries(rule) + len(rule.SourceCIDRs)
				}
				continue
			}
			extra := len(rule.SourceCIDRs) + item.users + len(item.inbounds)
			cost := ruleEntries(rule) + 1 + extra
			if route != nil {
				domains, addresses := len(rule.Domains), len(rule.CIDRs)
				if rule.Private {
					addresses += len(protocol.PrivateDestinationCIDRs())
				}
				cost = max(domains*max(len(route.Domains), 1), domains) + max(addresses*max(len(route.CIDRs), 1), addresses) + 1 + extra
			}
			total += cost
		}
	}
	for position := range group.Rules {
		segment(group.Rules[position].Exit, &group.Rules[position])
	}
	segment(group.Default, nil)
	return total
}

// intersectionBound is the most entries intersecting a rule's matcher with a
// route's can produce (the node's intersectionBound).
func intersectionBound(rule protocol.DestinationRule, route protocol.RouteRule) int {
	addresses := len(rule.CIDRs)
	if rule.Private {
		addresses += len(protocol.PrivateDestinationCIDRs())
	}
	return len(rule.Domains)*max(len(route.Domains), 1) + addresses*max(len(route.CIDRs), 1) + len(route.Domains) + len(route.CIDRs) + 1
}

// crossParts is the exact conjunction IntersectDestinations cannot write: a
// hostname of the rule that resolves into the route's addresses (the node's
// hostnameThroughAddresses).
type crossParts struct {
	domains   protocol.DestinationMatcher
	addresses int
	ips       int
}

func hostnameThroughAddresses(rule protocol.DestinationRule, route protocol.RouteRule, resolve protocol.ResolveMode) (crossParts, bool) {
	if resolve != protocol.ResolveOnDemand || len(rule.Domains) == 0 || len(rule.CIDRs) > 0 || rule.Private || len(route.CIDRs) == 0 {
		return crossParts{}, false
	}
	shared, _ := protocol.IntersectDestinations(
		protocol.DestinationMatcher{Ports: rule.Ports, Network: rule.Network},
		protocol.DestinationMatcher{Ports: route.Ports, Network: route.Network})
	if shared.Never {
		return crossParts{domains: protocol.DestinationMatcher{Never: true}}, true
	}
	parts := crossParts{domains: protocol.DestinationMatcher{Never: true}}
	if len(route.Domains) > 0 {
		both, exact := protocol.IntersectDestinations(
			protocol.DestinationMatcher{Domains: rule.Domains, Ports: rule.Ports, Network: rule.Network},
			protocol.DestinationMatcher{Domains: route.Domains, Ports: route.Ports, Network: route.Network})
		if !exact {
			return crossParts{}, false
		}
		parts.domains = both
	}
	parts.addresses, parts.ips = len(rule.Domains), len(route.CIDRs)
	return parts, true
}

// matcherCost is the entries and rules Xray writes for one destination matcher:
// a matcher with both hostnames and addresses is two rules that each carry the
// fields listed in common (core xray matcherRules). A matcher that matches
// nothing writes no rule.
func matcherCost(matcher protocol.DestinationMatcher, common int) (entries, rules int) {
	if matcher.Never {
		return 0, 0
	}
	addresses := len(matcher.CIDRs)
	if matcher.Private {
		addresses += len(protocol.PrivateDestinationCIDRs())
	}
	switch {
	case len(matcher.Domains) > 0 && addresses > 0:
		return len(matcher.Domains) + addresses + 2*common, 2
	case len(matcher.Domains) > 0:
		return len(matcher.Domains) + common, 1
	case addresses > 0:
		return addresses + common, 1
	}
	return common, 1
}

// meter counts as the node's RouteBudget does: every rule and entry as it is
// appended.
type meter struct {
	entries, rules int
	bytes          int64
}

func (m *meter) over() string {
	switch {
	case m.entries > MaxRoutedEntries:
		return "entries"
	case m.rules > MaxRoutedRules:
		return "rules"
	}
	// The encoded size is not a limit the panel can read off its own count: it
	// is a warning (see Analysis.ServerBytes), and the node's issue is the backstop.
	return ""
}

func (m *meter) overrun(listener protocol.ListenerKey, limit string) *Overrun {
	switch limit {
	case "entries":
		return &Overrun{Listener: listener, Limit: limit, Used: int64(m.entries), Max: MaxRoutedEntries}
	case "rules":
		return &Overrun{Listener: listener, Limit: limit, Used: int64(m.rules), Max: MaxRoutedRules}
	}
	return &Overrun{Listener: listener, Limit: limit, Used: m.bytes, Max: MaxRoutedConfigBytes}
}

func averageEntryBytes(planned []plannedRule, routes []protocol.RouteRule) int64 {
	var total, count int64
	add := func(items []string) {
		for _, item := range items {
			total += int64(len(item))
			count++
		}
	}
	for _, item := range planned {
		add(item.rule.Domains)
		add(item.rule.CIDRs)
	}
	for _, route := range routes {
		add(route.Domains)
		add(route.CIDRs)
	}
	if count == 0 {
		return 16
	}
	return total / count
}

// analyse computes what the node's routed compile would count for the groups,
// and where it passes a limit. It follows the node's own order of work: for each
// group the up-front estimate (which refuses before anything is built), then
// every segment (each route, then the default) built rule by rule, each rule
// charged as it is appended, exactly as core.RouteBudget is.
//
// Xray copies each policy rule's matcher into every segment, intersected with
// the route rule, so the product of the two lists is what is counted; sing-box
// writes a matcher of sharedMatcherMinimum entries or more once as a rule set
// that every segment refers to, and runs the segments twice when the policy
// needs a sniff. Both models are ports of the node's compilers, checked against
// numbers the node itself produced (testdata/routebudget.json).
func analyse(in analysisInput) Analysis {
	planned, exemptUsers := planRules(in)
	var result Analysis
	xray := in.Engine != "sing-box"
	st := &budgetState{in: in, planned: planned, exemptUsers: exemptUsers, result: &result, ruleSets: map[string]bool{}}
	if in.Policy != nil {
		for _, rule := range in.Policy.Rules {
			if rule.Action == protocol.RuleBlock && len(rule.Domains) > 0 {
				st.trailingDot = true
			}
		}
		st.usage = in.Policy.Collect == protocol.CollectHitsAndUsage
	}
	for _, item := range planned {
		need := ruleNeedsSniff(item.rule)
		st.sniff = st.sniff || need
	}
	est := 0
	seen := map[string]bool{}
	for _, group := range in.Groups {
		usage := GroupUsage{Listeners: group.Listeners}
		before := st.m
		est += estimate(planned, exemptUsers, group, !xray, seen)
		first := group.Listeners[0]
		if result.Over == nil && est > MaxRoutedEntries {
			result.Over = &Overrun{Listener: first, Limit: "entries", Used: int64(est), Max: MaxRoutedEntries}
		}
		if xray {
			average := averageEntryBytes(planned, group.Rules)
			for position := 0; position <= len(group.Rules); position++ {
				var route *protocol.RouteRule
				exit := group.Default
				if position < len(group.Rules) {
					route = &group.Rules[position]
					exit = route.Exit
				}
				if limit := st.xraySegment(group, exit, route, average); limit != "" {
					break
				}
			}
		} else {
			st.singboxGroup(group)
		}
		usage.Entries, usage.Rules, usage.Bytes = st.m.entries-before.entries, st.m.rules-before.rules, st.m.bytes-before.bytes
		if limit := st.m.over(); limit != "" && result.Over == nil {
			result.Over = st.m.overrun(first, limit)
		}
		result.Groups = append(result.Groups, usage)
		if result.Warn == nil && result.Over == nil {
			switch {
			// The node refuses on its estimate as well as on the count, and the
			// estimate is the larger of the two when routes share little with the
			// policy, so the warning reads whichever is closer to the limit.
			case float64(max(st.m.entries, est)) >= warnAt*MaxRoutedEntries:
				result.Warn = &Overrun{Listener: first, Limit: "entries", Used: int64(max(st.m.entries, est)), Max: MaxRoutedEntries}
			case float64(st.m.rules) >= warnAt*MaxRoutedRules:
				result.Warn = &Overrun{Listener: first, Limit: "rules", Used: int64(st.m.rules), Max: MaxRoutedRules}
			}
		}
		if result.Over != nil {
			break
		}
	}
	result.Entries, result.Rules, result.Bytes = st.m.entries, st.m.rules, st.m.bytes
	average := averageEntryBytes(planned, nil)
	if !xray {
		result.Bytes = int64(st.m.entries) * (average + perEntryBytes)
	}
	policyEntries := 0
	if in.Policy != nil {
		for _, rule := range in.Policy.Rules {
			policyEntries += ruleEntries(rule) + len(rule.SourceCIDRs) + len(rule.Subjects) + len(rule.Inbounds) + len(rule.Protocols) + 1
		}
		policyEntries += len(in.Policy.Exempt)
	}
	clients := 0
	for _, n := range in.Clients {
		clients += n
	}
	result.ServerBytes = result.Bytes + int64(policyEntries)*(average+perEntryBytes) +
		int64(clients)*clientConfigBytes + int64(len(in.Present))*listenerConfigBytes
	return result
}

// ruleNeedsSniff reports whether a policy rule needs the sniffed name or
// protocol (the node's SniffNeed for domains and protocols; sing-box has no
// resolved-address need).
func ruleNeedsSniff(rule protocol.DestinationRule) bool {
	return len(rule.Domains) > 0 || len(rule.Protocols) > 0
}

// budgetState is one analysis in progress.
type budgetState struct {
	in          analysisInput
	planned     []plannedRule
	exemptUsers int
	result      *Analysis
	m           meter
	// trailingDot is the node's guard against hostnames written with a trailing
	// dot; usage is the usage collection (psp-usage rules); sniff, that the
	// policy needs a sniff (sing-box).
	trailingDot, usage, sniff bool
	// ruleSets are the policy rules whose matcher has been written once as a
	// rule set (sing-box), across every group.
	ruleSets map[string]bool
}

func (st *budgetState) add(entries, rules int, average int64) string {
	st.m.entries += entries
	st.m.rules += rules
	st.m.bytes += int64(entries) * (average + perEntryBytes)
	return st.m.over()
}

// dotGuard counts the rules of the trailing-dot guard of one Xray segment (the
// node's dotGuard): the plain guard in the default segment, and in a route's
// segment only the dotted hostnames that route takes.
func (st *budgetState) dotGuard(tags int, route *protocol.RouteRule) (entries, rules int) {
	if route == nil {
		return tags + 1, 1
	}
	if len(route.Domains) == 0 && len(route.CIDRs) == 0 {
		return tags + 1, 1
	}
	keywords := 0
	for _, entry := range route.Domains {
		if strings.HasPrefix(entry, "keyword:") {
			keywords++
		}
	}
	if keywords > 0 {
		entries += tags + keywords
		rules++
	}
	if st.in.Resolve == protocol.ResolveOnDemand && len(route.CIDRs) > 0 {
		entries += tags + 1 + len(route.CIDRs)
		rules++
	}
	return entries, rules
}

// xraySegment counts one segment of an Xray group the way the node builds it
// (xray segmentRules): the exempt step and the trailing-dot guard, each policy
// rule of the segment ANDed with the route, the usage rule, and the route's
// catch-all. It returns the limit passed, "" when none. A policy rule that
// cannot be intersected exactly with the route is recorded in the result.
func (st *budgetState) xraySegment(group routeGroup, exit protocol.LinkKey, route *protocol.RouteRule, average int64) string {
	tags := len(group.Listeners)
	var routeMatcher protocol.DestinationMatcher
	if route != nil {
		routeMatcher = route.Matcher(st.in.Resolve)
	}
	rules := forSegment(st.planned, group.Listeners, exit)
	if st.exemptUsers > 0 {
		entries, count := matcherCost(routeMatcher, st.exemptUsers+tags)
		if limit := st.add(entries, count, average); limit != "" {
			return limit
		}
	}
	if st.trailingDot {
		entries, count := st.dotGuard(tags, route)
		if limit := st.add(entries, count, average); limit != "" {
			return limit
		}
	}
	for _, item := range rules {
		rule := item.rule
		matcher := protocol.DestinationMatcherOf(rule, st.in.Resolve)
		common := len(item.inbounds) + item.users + len(rule.SourceCIDRs) + len(rule.Protocols)
		extra, extraEntries := 0, 0
		if route != nil {
			// Refuse before the intersection allocates its product of the two lists.
			if bound := intersectionBound(rule, *route); st.m.entries+bound > MaxRoutedEntries {
				st.m.entries += bound
				return "entries"
			}
			intersection, exact := protocol.IntersectDestinations(matcher, routeMatcher)
			if intersection.Never {
				continue
			}
			if !exact {
				if parts, ok := hostnameThroughAddresses(rule, *route, st.in.Resolve); ok {
					intersection = parts.domains
					if parts.addresses > 0 {
						// One rule with both a domain and an ip field.
						extra = 1
						extraEntries = parts.addresses + parts.ips + common
					}
				} else {
					st.result.Approximate = append(st.result.Approximate, Approximation{Listener: group.Listeners[0], Route: route.ID, Rule: rule.ID, Action: rule.Action})
					if rule.Action != protocol.RuleBlock {
						// A note is a list element like any other: it is charged, and the copy is left out.
						if limit := st.add(1, 0, average); limit != "" {
							return limit
						}
						continue
					}
				}
			}
			matcher = intersection
		}
		entries, count := matcherCost(matcher, common)
		if limit := st.add(entries+extraEntries, count+extra, average); limit != "" {
			return limit
		}
	}
	if st.usage {
		entries, count := matcherCost(routeMatcher, tags)
		if limit := st.add(entries, count, average); limit != "" {
			return limit
		}
	}
	if route != nil {
		entries, count := matcherCost(routeMatcher, tags)
		return st.add(entries, count, average)
	}
	if exit != protocol.LocalExit {
		return st.add(tags, 1, average)
	}
	return ""
}

// sharedMatcherMinimum is the number of entries from which sing-box writes a
// policy rule's matcher once, as a rule set (the node's sharedMatcherMinimum).
const sharedMatcherMinimum = 16

// portItems is how many entries sing-box's port and port_range lists hold for a
// port spec: one per comma-separated item.
func portItems(spec string) int {
	if spec == "" {
		return 0
	}
	return strings.Count(spec, ",") + 1
}

// singboxRule is the entries of one policy rule written as a sing-box rule
// (the node's policyRule), split into what stays on the rule and the
// destination entries that can move to a shared rule set.
func singboxRule(item plannedRule) (own, destination int) {
	rule := item.rule
	own = item.users + len(item.inbounds) + portItems(rule.Ports) + len(rule.Protocols)
	if rule.Network != "" {
		own++
	}
	destination = len(rule.SourceCIDRs) + len(rule.Domains) + len(rule.CIDRs)
	if rule.Private {
		destination += len(protocol.PrivateDestinationCIDRs())
	}
	return own, destination
}

// singboxGroup counts one group's segments the way the node's sing-box compiler
// builds them (singbox applyRoutes): with a sniff, a first pass for connections
// that asked for a domain, decided entirely there, and a second for those that
// asked for an address; otherwise one pass. Each rule is charged as it is
// appended, a route's destination and each shared matcher once as rule sets.
func (st *budgetState) singboxGroup(group routeGroup) {
	tags := len(group.Listeners)
	// Each route's destination, written once per group as an inline rule set,
	// and (with a sniff) its address part.
	refs := make([]bool, len(group.Rules))
	for position, route := range group.Rules {
		st.m.entries += len(route.Domains) + len(route.CIDRs) + 1
		refs[position] = true
		if st.sniff && len(route.CIDRs) > 0 {
			st.m.entries += len(route.CIDRs) + 1
		}
	}
	average := averageEntryBytes(st.planned, group.Rules)
	build := func(forRequested bool) {
		for position := range group.Rules {
			route := group.Rules[position]
			if st.sniff && !forRequested && len(route.CIDRs) == 0 {
				continue
			}
			if st.sbSegment(group, route.Exit, true, forRequested, average) {
				return
			}
			// The route's catch-all: the group's inbounds and the route's reference
			// (and, in the first pass, the requested-domain gate).
			entries := tags + 1
			if forRequested {
				entries++
			}
			if st.add(entries, 1, average) != "" {
				return
			}
		}
		if st.sbSegment(group, group.Default, false, forRequested, average) {
			return
		}
		switch {
		case forRequested:
			st.add(tags+1, 1, average)
		case group.Default != protocol.LocalExit:
			st.add(tags, 1, average)
		}
	}
	if st.sniff {
		build(true)
		if st.m.over() == "" {
			build(false)
		}
	} else {
		build(false)
	}
}

// sbSegment counts the rules of one sing-box segment; it reports true when a
// limit was passed. confined says the segment has a route's reference to AND.
func (st *budgetState) sbSegment(group routeGroup, exit protocol.LinkKey, confined, forRequested bool, average int64) bool {
	tags := len(group.Listeners)
	extras := 0
	if confined {
		extras++
	}
	if forRequested {
		extras++
	}
	// apply: a rule that is not the node's own is ANDed with the extras.
	exempt := func(list int) bool {
		// builtin: confined to the group's inbounds, plus the extras.
		entries := list + tags + extras
		return st.add(entries, 1, average) != ""
	}
	atExempt := func() bool {
		if st.exemptUsers > 0 && exempt(st.exemptUsers) {
			return true
		}
		return st.trailingDot && exempt(1)
	}
	segment := forSegment(st.planned, group.Listeners, exit)
	exemptAt := len(segment)
	for index, item := range segment {
		if rulePhase(item.rule) >= 3 {
			exemptAt = index
			break
		}
	}
	for index, item := range segment {
		if index == exemptAt && atExempt() {
			return true
		}
		own, destination := singboxRule(item)
		entries := own + destination
		if destination >= sharedMatcherMinimum {
			if !st.ruleSets[item.rule.ID] {
				st.ruleSets[item.rule.ID] = true
				st.m.entries += destination
				if st.m.over() != "" {
					return true
				}
			}
			entries = own + 1
		}
		if st.add(entries+extras, 1, average) != "" {
			return true
		}
	}
	if exemptAt == len(segment) && atExempt() {
		return true
	}
	return false
}

// rulePhase mirrors the protocol's phase table: 0 source deny, 1 allow, 3
// block, 4 group, 5 observe (the node's rulePhase).
func rulePhase(rule protocol.DestinationRule) int {
	switch {
	case rule.ID[0] == 'g':
		return 4
	case rule.Action == protocol.RuleObserve:
		return 5
	case rule.ID[0] == 's':
		return 0
	case rule.Action == protocol.RuleAllow:
		return 1
	default:
		return 3
	}
}
