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
}

// assemble specialises the base for one node, in the wire's phase order:
// source denies, then global allows, then blocks. The exempt list sits between
// the allows and the blocks on the node; it is carried in the policy and not as
// a rule. Rules whose subjects came out empty are left out, and so are rules
// of a policy scoped to other panels.
func (b *base) assemble(v nodeView) *protocol.DestinationPolicy {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}}

	for _, g := range b.sources {
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
		policy.Rules = append(policy.Rules, protocol.DestinationRule{
			ID: "s" + strconv.FormatInt(first, 10), Action: protocol.RuleBlock,
			Inbounds: inbounds, SourceCIDRs: slices.Clone(g.cidrs),
		})
	}

	for _, group := range [][]baseRule{b.allow, b.block} {
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
func (b *base) needsGroups() bool {
	for _, group := range [][]baseRule{b.allow, b.block} {
		for _, r := range group {
			if r.scope == domain.DestScopeGroups {
				return true
			}
		}
	}
	return false
}
