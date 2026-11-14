package destpolicy

import (
	"slices"
	"strconv"

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
	policyRules := func(group []baseRule) {
		for _, r := range group {
			rule := protocol.DestinationRule{
				ID: r.id, Action: r.action, Domains: r.domains, CIDRs: r.cidrs, Ports: r.ports,
				Network: r.network, Protocols: r.protocols, Private: r.private,
			}
			if !v.bound {
				switch r.scope {
				case domain.DestScopePanels:
					if !slices.Contains(r.panels, v.panelID) {
						continue
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
