package destpolicy

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// SEND EACH NODE WHATEVER IT CAN ENFORCE.
//
// A rule with domains or protocols needs the core's sniffer, and an inbound's
// sniffing can be configured so it cannot supply that (metadataOnly, a short
// destOverride, routeOnly, exclusions). The node checks this PER LISTENER, on
// the rules that apply to the listener, so a rule restricted by Inbounds to
// listeners that can sniff passes the check even when another listener cannot.
// restrictForSniffing builds exactly that policy: every rule that needs the
// sniffer runs only where it can be supplied, and every other rule (ports,
// address ranges, source rules, network, private) is untouched and keeps
// applying everywhere.
//
// This is not a fallback: nothing is looked up, the result is the desired
// policy for this node, and it is recorded as the last known good like any
// other when the node applies it.

// sniffRestriction says what restrictForSniffing did.
type sniffRestriction struct {
	// changed: at least one rule was restricted, split or dropped.
	changed bool
	// observe: one of them was a report-only rule.
	observe bool
}

// ruleSniffNeed is what one rule needs the sniffer to supply.
func ruleSniffNeed(r protocol.DestinationRule) protocol.SniffNeed {
	var need protocol.SniffNeed
	if len(r.Domains) > 0 {
		need |= protocol.SniffForDomains
	}
	if len(r.Protocols) > 0 {
		need |= protocol.SniffForProtocols
	}
	return need
}

// sniffAdequate reports whether an inbound's sniffing supplies need. An
// inbound with sniffing off counts: the node turns it on. One whose sniffing
// does not parse never does.
func sniffAdequate(n *domain.Node, need protocol.SniffNeed) bool {
	verdict, _, err := protocol.CheckXraySniffing([]byte(n.Sniffing), need)
	return err == nil && verdict != protocol.SniffInsufficient
}

// restrictForSniffing returns p as the node can run it: p itself (same pointer,
// same bytes) when nothing has to change, else a new policy; nil when nothing
// is left. Xray only: sing-box takes its sniff rule from the route, which the
// node writes itself.
//
//   - A rule with domains or protocols runs on (its own Inbounds, or every
//     enabled inbound) intersected with the inbounds whose sniffing supplies
//     what THAT rule needs; with none left it is dropped.
//   - A rule whose domains are one alternative of several (a list of domains
//     and addresses) is split: the address half is unchanged, the domain half
//     is restricted under the next free x-suffix of the same policy.
//   - Group rules cannot carry inbounds: when one that needs the sniffer has
//     an inbound that cannot supply it, every rule of that group is dropped.
func restrictForSniffing(p *protocol.DestinationPolicy, nodes []*domain.Node, engine string) (*protocol.DestinationPolicy, sniffRestriction) {
	var info sniffRestriction
	if p == nil || engine != xrayEngine {
		return p, info
	}
	var all []protocol.ListenerKey
	for _, n := range nodes {
		if n != nil && n.Enabled {
			all = append(all, protocol.NewListenerKey(n.ID))
		}
	}
	slices.Sort(all)
	if len(all) == 0 {
		return p, info
	}
	adequateFor := map[protocol.SniffNeed][]protocol.ListenerKey{}
	adequate := func(need protocol.SniffNeed) []protocol.ListenerKey {
		if got, ok := adequateFor[need]; ok {
			return got
		}
		var keys []protocol.ListenerKey
		for _, n := range nodes {
			if n != nil && n.Enabled && sniffAdequate(n, need) {
				keys = append(keys, protocol.NewListenerKey(n.ID))
			}
		}
		slices.Sort(keys)
		adequateFor[need] = keys
		return keys
	}
	used := make(map[string]bool, len(p.Rules))
	for _, r := range p.Rules {
		used[r.ID] = true
	}

	// Group rules are an allowlist (members allowed what it names, everything
	// else blocked), which only means something as a whole: one of them losing
	// the inbounds it cannot run on would turn the rest into a block-all. They
	// cannot carry inbounds either, so when any rule of a group needs a sniffer
	// some inbound cannot supply, that whole group goes (every rule with its
	// group id): its allowlist is not enforced on this node, which only relaxes.
	// Other groups are untouched.
	dropGroups := map[int64]bool{}
	for _, r := range p.Rules {
		if need := ruleSniffNeed(r); need != 0 && strings.HasPrefix(r.ID, "g") && len(adequate(need)) != len(all) {
			if _, gid, ok := ruleOrigin(r.ID); ok {
				dropGroups[gid] = true
			}
		}
	}

	out := make([]protocol.DestinationRule, 0, len(p.Rules))
	for _, r := range p.Rules {
		if strings.HasPrefix(r.ID, "g") {
			if _, gid, ok := ruleOrigin(r.ID); ok && dropGroups[gid] {
				info.changed = true
				if r.Action == protocol.RuleObserve {
					info.observe = true
				}
				continue
			}
		}
		need := ruleSniffNeed(r)
		if need == 0 {
			out = append(out, r)
			continue
		}
		ok := adequate(need)
		if len(ok) == len(all) {
			out = append(out, r)
			continue
		}
		info.changed = true
		if r.Action == protocol.RuleObserve {
			info.observe = true
		}
		target := slices.Clone(ok)
		if len(r.Inbounds) > 0 {
			target = slices.DeleteFunc(target, func(k protocol.ListenerKey) bool { return !slices.Contains(r.Inbounds, k) })
		}
		if len(r.Domains) > 0 && len(r.Protocols) == 0 && (len(r.CIDRs) > 0 || r.Private) {
			addr := r
			addr.Domains = nil
			out = append(out, addr)
			if len(target) > 0 {
				dom := r
				dom.CIDRs, dom.Private = nil, false
				dom.ID = freeSuffix(used, r.ID)
				dom.Inbounds = target
				out = append(out, dom)
			}
			continue
		}
		if len(target) == 0 {
			continue
		}
		r.Inbounds = target
		out = append(out, r)
	}
	if !info.changed {
		return p, info
	}
	if len(out) == 0 {
		return nil, info
	}
	res := &protocol.DestinationPolicy{Rules: out, Exempt: slices.Clone(p.Exempt)}
	// A report-only rule is one of the things that records, so the level is
	// derived again from what is left.
	withCollect(res, p.Collect)
	withResolve(res, p.Resolve)
	return res, info
}

// freeSuffix is a rule id of the same policy as id, with an x-suffix no rule in
// used has, and marks it used.
func freeSuffix(used map[string]bool, id string) string {
	base, _, _ := strings.Cut(id, "x")
	for k := 1; k < 10000; k++ {
		if cand := base + "x" + strconv.Itoa(k); !used[cand] {
			used[cand] = true
			return cand
		}
	}
	return id
}

// perListenerSniffFindings is the node's own check, per listener: for each
// enabled inbound, the need of the rules that apply to it against its sniffing,
// with the reason CheckXraySniffing gives (an unparseable object is "malformed").
// On the unrestricted policy it names the inbounds that cannot run what the
// policy asks; on a restricted one it must come out empty, as an assertion.
func perListenerSniffFindings(p *protocol.DestinationPolicy, nodes []*domain.Node) []sniffFinding {
	if p == nil {
		return nil
	}
	var out []sniffFinding
	for _, n := range nodes {
		if n == nil || !n.Enabled {
			continue
		}
		key := protocol.NewListenerKey(n.ID)
		var need protocol.SniffNeed
		for _, r := range p.Rules {
			if len(r.Inbounds) == 0 || slices.Contains(r.Inbounds, key) {
				need |= ruleSniffNeed(r)
			}
		}
		if need == 0 {
			continue
		}
		verdict, reason, err := protocol.CheckXraySniffing([]byte(n.Sniffing), need)
		switch {
		case err != nil:
			out = append(out, sniffFinding{Listener: string(key), NodeID: n.ID, Label: n.DisplayName, Reason: sniffMalformed})
		case verdict == protocol.SniffInsufficient:
			out = append(out, sniffFinding{Listener: string(key), NodeID: n.ID, Label: n.DisplayName, Reason: reason})
		}
	}
	slices.SortFunc(out, func(a, b sniffFinding) int { return strings.Compare(a.Listener, b.Listener) })
	return out
}

// engineOf is the core engine the panel's node is configured for, which is what
// Compile judges by (nodesync builds the config from the agent's desired core);
// Xray when it has none.
func (a *Admin) engineOf(ctx context.Context, panelID int64) string {
	if a.Agents != nil {
		if agents, err := a.Agents.List(ctx); err == nil {
			for _, ag := range agents {
				if ag.PanelID == panelID {
					return strings.ToLower(string(domain.NormalizeNodeCoreEngine(ag.DesiredCoreEngine)))
				}
			}
		}
	}
	return xrayEngine
}

// stepInfo is what the trace names a rule by. A rule the compiler split off a
// policy's rule (its domain half, restricted to some inbounds) has an id the
// base does not know; it is named after the policy it came from.
func stepInfo(names map[string]TestStep, id string) TestStep {
	if info, ok := names[id]; ok {
		return info
	}
	if kind, n, ok := ruleOrigin(id); ok && kind == 'p' {
		prefix := "p" + strconv.FormatInt(n, 10)
		for known, info := range names {
			if known == prefix || strings.HasPrefix(known, prefix+"x") {
				return info
			}
		}
	}
	return TestStep{}
}
