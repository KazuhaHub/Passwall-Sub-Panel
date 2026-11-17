package destpolicy

import (
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// nodeView is what a policy is specialised for: one native node (one panel).
type nodeView struct {
	// nodeIDs are the inbounds (listeners) the node has.
	nodeIDs map[int64]bool
	panelID int64
	// users are the roster's user ids, and groupOf their groups. Both are only
	// needed for group-scoped rules; users is read whenever a policy names a
	// group.
	users   []int64
	groupOf map[int64]int64
	// bound ignores the node and the roster: every rule, no subjects, every
	// source group on every inbound it names. It is the UPPER BOUND that is
	// validated before publishing (one answer for every panel) and the
	// budget the admin page shows.
	bound bool
	// groupCap: the node declares the groups capability, so it is sent group
	// rules (the upper bound always has them).
	groupCap bool
	// tagMatched is, per allowlist group, how many members the panel's tags
	// select. Only the per-node limit reads it.
	tagMatched map[int64]int
	// egress maps each listener that leaves through a tunnel to the server it
	// leaves from. A panel-scoped policy follows the traffic's exit: on such a
	// listener it applies when the exit server is in its scope, whatever this
	// server's own scope says.
	egress map[int64]int64
	// routes maps each listener that routes by destination (transit.route.v1) to
	// the exits its traffic can leave through: the exit of each of its routes and
	// its default exit. A panel-scoped policy follows the traffic's exit
	// listener by listener, and exit by exit on a routed one (see panelCopies).
	// Empty for a node without routes, which then assembles exactly as before.
	routes map[int64][]RouteExit
}

// RouteExit is one exit a routed listener's traffic can leave through: the
// link's key on the wire (protocol.LocalExit for the server itself) and the
// server it leaves from.
type RouteExit struct {
	Key     protocol.LinkKey
	PanelID int64
}

// panelScope answers which of the node's inbounds a policy scoped to panels
// applies to. all means every inbound (the rule needs no inbound list); with
// all false, inbounds names them, and is empty when it applies to none. A node
// without tunnels gets all or nothing, as it always did.
func (v nodeView) panelScope(panels []int64) (all bool, inbounds []protocol.ListenerKey) {
	here := slices.Contains(panels, v.panelID)
	if len(v.egress) == 0 {
		return here, nil
	}
	all = true
	for id := range v.nodeIDs {
		applies := here
		if exit, bound := v.egress[id]; bound {
			applies = slices.Contains(panels, exit)
		}
		if applies {
			inbounds = append(inbounds, protocol.NewListenerKey(id))
		} else {
			all = false
		}
	}
	if all {
		return true, nil
	}
	slices.Sort(inbounds)
	return false, inbounds
}

// ruleScope is where one copy of a panel-scoped rule applies. all means every
// inbound; with all false, inbounds names them. exits, when set, confines the
// copy to traffic that leaves through one of them (DestinationRule.Exits).
type ruleScope struct {
	all      bool
	inbounds []protocol.ListenerKey
	exits    []protocol.LinkKey
}

// panelCopies answers where a policy scoped to panels applies, as the copies of
// the rule it takes. A node without routes gets what panelScope says: one copy,
// or none. On a routed listener the policy applies exit by exit: the traffic
// that leaves through a server in the policy's scope is covered and the rest is
// not, so a listener whose exits are only partly in scope takes a copy
// confined to those exits. Listeners that cover the same exits share a copy, and
// a listener whose every exit is in scope is in the unconfined one.
func (v nodeView) panelCopies(panels []int64) []ruleScope {
	if len(v.routes) == 0 {
		all, inbounds := v.panelScope(panels)
		if !all && len(inbounds) == 0 {
			return nil
		}
		return []ruleScope{{all: all, inbounds: inbounds}}
	}
	here := slices.Contains(panels, v.panelID)
	var open []protocol.ListenerKey
	confined := map[string][]protocol.ListenerKey{}
	exitsOf := map[string][]protocol.LinkKey{}
	ids := make([]int64, 0, len(v.nodeIDs))
	for id := range v.nodeIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		key := protocol.NewListenerKey(id)
		exits, routed := v.routes[id]
		if !routed {
			applies := here
			if exit, bound := v.egress[id]; bound {
				applies = slices.Contains(panels, exit)
			}
			if applies {
				open = append(open, key)
			}
			continue
		}
		var allowed []protocol.LinkKey
		for _, exit := range exits {
			if slices.Contains(panels, exit.PanelID) {
				allowed = append(allowed, exit.Key)
			}
		}
		switch {
		case len(allowed) == 0:
		case len(allowed) == len(exits):
			open = append(open, key)
		default:
			slices.Sort(allowed)
			name := strings.Join(linkKeyStrings(allowed), ",")
			confined[name] = append(confined[name], key)
			exitsOf[name] = allowed
		}
	}
	var out []ruleScope
	if len(open) > 0 {
		out = append(out, ruleScope{all: len(confined) == 0 && len(open) == len(ids), inbounds: open})
		if out[0].all {
			out[0].inbounds = nil
		}
	}
	names := make([]string, 0, len(confined))
	for name := range confined {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		slices.Sort(confined[name])
		out = append(out, ruleScope{inbounds: confined[name], exits: exitsOf[name]})
	}
	return out
}

// covered narrows a rule of the last policy a node applied, scoped to panels, to
// the listeners whose traffic that scope still covers through every exit the
// rule takes: the rule's own Exits when it has them, else every exit of the
// listener. A listener it no longer fully covers is dropped from the rule, which
// can only loosen it, and a listener the rule's exits never reach is dropped
// too.
func (v nodeView) covered(panels []int64, r protocol.DestinationRule) []protocol.ListenerKey {
	here := slices.Contains(panels, v.panelID)
	var kept []protocol.ListenerKey
	for id := range v.nodeIDs {
		key := protocol.NewListenerKey(id)
		if len(r.Inbounds) > 0 && !slices.Contains(r.Inbounds, key) {
			continue
		}
		exits, routed := v.routes[id]
		if !routed {
			if len(r.Exits) > 0 {
				// A copy confined to exits belongs to a routed listener.
				continue
			}
			applies := here
			if exit, bound := v.egress[id]; bound {
				applies = slices.Contains(panels, exit)
			}
			if applies {
				kept = append(kept, key)
			}
			continue
		}
		reached := 0
		covered := true
		for _, exit := range exits {
			if len(r.Exits) > 0 && !slices.Contains(r.Exits, exit.Key) {
				continue
			}
			reached++
			if !slices.Contains(panels, exit.PanelID) {
				covered = false
			}
		}
		if reached > 0 && covered {
			kept = append(kept, key)
		}
	}
	slices.Sort(kept)
	return kept
}

func linkKeyStrings(keys []protocol.LinkKey) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = string(key)
	}
	return out
}

// syntheticSubject stands for a group's members in the upper bound, which has
// no roster: group rules need subjects, and no real account has this id.
func syntheticSubject(gid int64) protocol.SubjectKey {
	return protocol.NewSubjectKey(1<<62 + gid)
}

// assemble specialises the base for one node, in the wire's phase order:
// source denies, then global allows, then blocks, and last the report-only
// rules (phase 5: source rules, then policies). The exempt list sits between
// the allows and the blocks on the node; it is carried in the policy and not as
// a rule. Rules whose subjects came out empty are left out, and so are rules
// of a policy scoped to other panels.
func (b *base) assemble(v nodeView) *protocol.DestinationPolicy {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}}

	// sourceRules emits the source groups of one state. Report-only ones wait for
	// the last phase.
	sourceRules := func(report bool) {
		for _, g := range b.sources {
			if g.report != report {
				continue
			}
			var inbounds []protocol.ListenerKey
			for _, n := range g.nodes {
				if v.bound || v.nodeIDs[n] {
					inbounds = append(inbounds, protocol.NewListenerKey(n))
				}
			}
			if len(inbounds) == 0 {
				continue
			}
			slices.Sort(inbounds)
			first := g.nodes[0]
			if !v.bound {
				for _, n := range g.nodes {
					if v.nodeIDs[n] {
						first = n
						break
					}
				}
			}
			action := protocol.RuleBlock
			if report {
				action = protocol.RuleObserve
			}
			policy.Rules = append(policy.Rules, protocol.DestinationRule{
				ID: "s" + strconv.FormatInt(first, 10), Action: action,
				Inbounds: inbounds, SourceCIDRs: slices.Clone(g.cidrs),
			})
		}
	}
	var used map[string]bool
	policyRules := func(group []baseRule) {
		for _, r := range group {
			rule := protocol.DestinationRule{
				ID: r.id, Action: r.action, Domains: r.domains, CIDRs: r.cidrs, Ports: r.ports,
				Network: r.network, Protocols: r.protocols, Private: r.private,
			}
			var extras []protocol.DestinationRule
			if !v.bound {
				switch r.scope {
				case domain.DestScopePanels:
					copies := v.panelCopies(r.panels)
					if len(copies) == 0 {
						continue
					}
					rule.Inbounds, rule.Exits = copies[0].inbounds, copies[0].exits
					// The other copies are the same policy for other exits: each
					// takes the next free x-suffix of its id.
					for _, more := range copies[1:] {
						if used == nil {
							used = usedRuleIDs(b)
						}
						extra := rule
						extra.ID = freeSuffix(used, r.id)
						extra.Inbounds, extra.Exits = more.inbounds, more.exits
						extras = append(extras, extra)
					}
				case domain.DestScopeGroups:
					var subjects []protocol.SubjectKey
					for _, u := range v.users {
						if slices.Contains(r.groups, v.groupOf[u]) {
							subjects = append(subjects, protocol.NewSubjectKey(u))
						}
					}
					if len(subjects) == 0 {
						continue
					}
					sortKeys(subjects)
					rule.Subjects = subjects
				}
			}
			policy.Rules = append(policy.Rules, rule)
			policy.Rules = append(policy.Rules, extras...)
		}
	}
	// groupRules emits the allowlist groups: per group (by id) the allows of its
	// lists and of its base and extra lists, then the catch-all.
	groupRules := func() {
		for _, g := range b.groups {
			var subjects []protocol.SubjectKey
			switch {
			case v.bound:
				subjects = []protocol.SubjectKey{syntheticSubject(g.id)}
			case !v.groupCap:
				continue
			default:
				for _, u := range v.users {
					if v.groupOf[u] == g.id {
						subjects = append(subjects, protocol.NewSubjectKey(u))
					}
				}
				if len(subjects) == 0 {
					continue
				}
				sortKeys(subjects)
			}
			gid := strconv.FormatInt(g.id, 10)
			for i, a := range []groupAllow{g.x1, g.x2} {
				if a.empty() {
					continue
				}
				policy.Rules = append(policy.Rules, protocol.DestinationRule{
					ID: "g" + gid + "x" + strconv.Itoa(i+1), Action: protocol.RuleAllow,
					Domains: a.domains, CIDRs: a.cidrs, Subjects: subjects,
				})
			}
			action := protocol.RuleBlock
			if g.report {
				action = protocol.RuleObserve
			}
			policy.Rules = append(policy.Rules, protocol.DestinationRule{ID: "g" + gid, Action: action, Subjects: subjects, CatchAll: true})
		}
	}
	sourceRules(false)
	policyRules(b.allow)
	policyRules(b.block)
	// PHASE 4. Behind the blocks, ahead of the report-only rules.
	groupRules()
	// PHASE 5. Behind every enforcing rule: the first matching rule terminates,
	// so a report rule anywhere earlier would shadow an enforced one after it.
	sourceRules(true)
	policyRules(b.watch)

	if len(policy.Rules) == 0 {
		return nil
	}
	if !v.bound {
		roster := make(map[int64]bool, len(v.users))
		for _, u := range v.users {
			roster[u] = true
		}
		for _, u := range b.exempt {
			if roster[u] {
				policy.Exempt = append(policy.Exempt, protocol.NewSubjectKey(u))
			}
		}
		sortKeys(policy.Exempt)
	}
	return policy
}

// usedRuleIDs are the ids of every policy rule of the base, for the copies a
// routed listener's exits make.
func usedRuleIDs(b *base) map[string]bool {
	used := map[string]bool{}
	for _, group := range [][]baseRule{b.allow, b.block, b.watch} {
		for _, r := range group {
			used[r.id] = true
		}
	}
	return used
}

// sortKeys puts subject keys in the wire's bytewise order ("usr_10" before
// "usr_9"), which is not numeric order.
func sortKeys[K ~string](keys []K) {
	slices.Sort(keys)
}

// needsGroups reports whether any rule is scoped to groups, which is when the
// roster's group ids have to be read.
func (b *base) needsGroups(groupCap bool) bool {
	if groupCap && len(b.groups) > 0 {
		return true
	}
	for _, group := range [][]baseRule{b.allow, b.block, b.watch} {
		for _, r := range group {
			if r.scope == domain.DestScopeGroups {
				return true
			}
		}
	}
	return false
}
