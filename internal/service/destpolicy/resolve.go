package destpolicy

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// IssueResolveNotDeployed says a panel set to on_demand was NOT delivered it:
// the node is held at as_is. Its detail is why; today only "route_only" (an
// inbound's sniffing would substitute the name the router resolves). A node too
// old to know the mode raises no issue: that is the status page's chip, derived
// from the capability, not a fault.
const IssueResolveNotDeployed = "destination_policy_resolve_not_deployed"

// resolveRouteOnly is the reason in IssueResolveNotDeployed, and the value of
// the status page's resolve_unavailable.
const resolveRouteOnly = "route_only"

// resolveNoIPRules is resolve_unavailable when on_demand is asked for and
// delivered everywhere it could be, but nothing in the policy has an IP rule, so
// no mode is written. Not a fault: the status page says so.
const resolveNoIPRules = "no_ip_rules"

// capabilityDestinationResolve is the capability a node declares when it
// compiles DestinationPolicy.Resolve on every engine it runs.
const capabilityDestinationResolve = protocol.CapabilityDestinationResolve

// EffectiveResolve is the mode a node is told to run: on_demand only when the
// panel's admin chose it AND the node declares the capability. Sending Resolve
// to a node that does not know it is not a refusal but a silent downgrade (an
// agent ignores a field it does not know and runs AsIs), so the gate is
// mandatory. Anything the panel holds that is not on_demand, including the mode
// that is not offered (if_non_match), is as_is.
func EffectiveResolve(panel domain.DestResolve, caps []string) protocol.ResolveMode {
	if domain.NormalizeDestResolve(panel) == domain.DestResolveOnDemand && slices.Contains(caps, capabilityDestinationResolve) {
		return protocol.ResolveOnDemand
	}
	return protocol.ResolveAsIs
}

// resolveUnavailableVersion reports whether a panel set to on_demand is held at
// as_is because the node does not declare the capability: the status page's
// resolve_unavailable "version".
func resolveUnavailableVersion(panel domain.DestResolve, caps []string) bool {
	return domain.NormalizeDestResolve(panel) == domain.DestResolveOnDemand && !slices.Contains(caps, capabilityDestinationResolve)
}

// ruleHasIPRules is whether a rule asks for addresses: CIDRs or Private.
// Source ranges are matched on the client's own address and never resolve a
// name.
func ruleHasIPRules(r protocol.DestinationRule) bool { return len(r.CIDRs) > 0 || r.Private }

// hasIPRules is whether any rule of p asks for addresses. Only then does
// resolving at routing time change an answer.
func hasIPRules(p *protocol.DestinationPolicy) bool {
	return p != nil && slices.ContainsFunc(p.Rules, ruleHasIPRules)
}

// withResolve sets the policy's mode where it is needed and leaves it empty
// where it is not: emitting it for a policy with no IP rule, or for a panel
// that is as_is, would change the digest and restart the core for no benefit.
// It is derived from the rules left, so a rule set that shrinks (report-only
// shedding, restriction) re-derives it.
func withResolve(p *protocol.DestinationPolicy, level protocol.ResolveMode) {
	if p == nil {
		return
	}
	p.Resolve = protocol.ResolveAsIs
	if level == protocol.ResolveOnDemand && hasIPRules(p) {
		p.Resolve = level
	}
}

// lowerResolve is the lower of two modes (as_is < on_demand). The fallback to the
// last known good policy uses it: it may only loosen, and raising the mode would
// mint bytes the node never applied.
func lowerResolve(a, b protocol.ResolveMode) protocol.ResolveMode {
	if a == protocol.ResolveOnDemand && b == protocol.ResolveOnDemand {
		return protocol.ResolveOnDemand
	}
	return protocol.ResolveAsIs
}

// resolveFindings are the inbounds that cannot run on_demand: the node checks,
// per listener, that where an IP rule applies, no sniffed name stands in for the
// real target while the connection goes elsewhere (sniffing routeOnly:true).
// Xray's domainStrategy is router-wide, so ONE such inbound holds the whole node
// at as_is. A sniffing object that cannot be read is a finding too (reason
// "malformed", as for the other needs): the node would refuse it.
//
// This is deliberately stricter than the node's own planner in two ways: it
// looks at every rule that applies to the inbound, where the node filters rules
// whose subjects match nobody on its roster, and at every enabled inbound, where
// the node checks only the inbounds it emits. PSP may hold a node at as_is that
// the node would have run on_demand; it never lets the node refuse.
func resolveFindings(p *protocol.DestinationPolicy, nodes []*domain.Node) []sniffFinding {
	if p == nil {
		return nil
	}
	var out []sniffFinding
	for _, n := range nodes {
		if n == nil || !n.Enabled {
			continue
		}
		key := protocol.NewListenerKey(n.ID)
		if !slices.ContainsFunc(p.Rules, func(r protocol.DestinationRule) bool {
			return ruleHasIPRules(r) && (len(r.Inbounds) == 0 || slices.Contains(r.Inbounds, key))
		}) {
			continue
		}
		verdict, reason, err := protocol.CheckXraySniffing([]byte(n.Sniffing), protocol.SniffForResolvedIPs)
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

// settleResolve makes the policy something the node can run. It returns the
// policy to send, the inbounds that held the WHOLE node at as_is (nil when none
// did) and whether report-only rules were taken off some inbounds. Xray only:
// sing-box takes no sniffed name for routing.
//
//   - The decision to hold the node at as_is is made on the ENFORCED rules alone:
//     report-only must not disturb enforcement, and an enforced CIDR block elsewhere
//     must not stop judging hostnames because a report-only rule meets a routeOnly
//     inbound.
//   - When only report-only IP rules meet such an inbound, they are taken off it
//     (their inbounds narrowed, or the rule dropped when none is left), the mode
//     stays on_demand and the node is told report-only rules were not deployed.
func settleResolve(p *protocol.DestinationPolicy, nodes []*domain.Node, engine string) (*protocol.DestinationPolicy, []sniffFinding, bool) {
	if p == nil || p.Resolve != protocol.ResolveOnDemand || engine != xrayEngine {
		return p, nil, false
	}
	if held := resolveFindings(enforcedOnly(p), nodes); len(held) > 0 {
		withResolve(p, protocol.ResolveAsIs)
		return p, held, false
	}
	found := resolveFindings(p, nodes)
	if len(found) == 0 {
		return p, nil, false
	}
	bad := map[protocol.ListenerKey]bool{}
	for _, f := range found {
		bad[protocol.ListenerKey(f.Listener)] = true
	}
	var all []protocol.ListenerKey
	for _, n := range nodes {
		if n != nil && n.Enabled {
			all = append(all, protocol.NewListenerKey(n.ID))
		}
	}
	slices.Sort(all)
	out := make([]protocol.DestinationRule, 0, len(p.Rules))
	for _, r := range p.Rules {
		if sheddable(r) && ruleHasIPRules(r) {
			targets := r.Inbounds
			if len(targets) == 0 {
				targets = all
			}
			kept := slices.DeleteFunc(slices.Clone(targets), func(k protocol.ListenerKey) bool { return bad[k] })
			if len(kept) != len(targets) {
				if len(kept) == 0 {
					continue
				}
				r.Inbounds = kept
			}
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, nil, true
	}
	res := &protocol.DestinationPolicy{Rules: out, Exempt: slices.Clone(p.Exempt)}
	withCollect(res, p.Collect)
	withResolve(res, p.Resolve)
	return res, nil, true
}

// resolveReason is what an inbound that held the node at as_is is called in the
// node issue and in the status page's resolve_unavailable.
func resolveReason(f sniffFinding) string {
	if f.Reason == sniffMalformed {
		return "sniffing"
	}
	return resolveRouteOnly
}

// resolveIssues are the node issues of the inbounds that held the node at as_is,
// one each, named by listener like the sniffing issues.
func resolveIssues(found []sniffFinding) []domain.NodeAgentIssue {
	out := make([]domain.NodeAgentIssue, 0, len(found))
	for _, f := range found {
		out = append(out, domain.NodeAgentIssue{Code: IssueResolveNotDeployed, Key: f.Listener, Detail: resolveReason(f)})
	}
	return out
}

// validateResolvedIPs is the check on a test's resolved_ips: at most
// maxResolvedIPs entries, each an address literal exactly (no name, no range, no
// port, no zone, no padding).
func validateResolvedIPs(ips []string) error {
	if len(ips) > maxResolvedIPs {
		return &ValidationError{Code: CodeTestInvalid, Field: "resolved_ips", Msg: "at most 16 addresses"}
	}
	for _, text := range ips {
		if a, err := netip.ParseAddr(text); err != nil || a.Zone() != "" {
			return &ValidationError{Code: CodeTestInvalid, Field: "resolved_ips", Msg: "resolved_ips are IP address literals"}
		}
	}
	return nil
}

// applyResolve sets on p the mode the panel's node would run, for the test tool:
// the panel's choice through EffectiveResolve and the node's capabilities, held
// at as_is by an inbound that cannot run it, exactly as Compile decides. A
// panel that is not a Passwall Node, or has no agent, is as_is.
func (a *Admin) applyResolve(ctx context.Context, p *protocol.DestinationPolicy, panelID int64, nodes []*domain.Node) {
	if p == nil || a.Agents == nil {
		return
	}
	panel, err := a.Panels.GetByID(ctx, panelID)
	if err != nil || domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return
	}
	agents, err := a.Agents.List(ctx)
	if err != nil {
		return
	}
	for _, ag := range agents {
		if ag.PanelID == panelID {
			withResolve(p, EffectiveResolve(panel.DestResolve, ag.ObservedCapabilities))
			settleResolve(p, nodes, a.engineOf(ctx, panelID))
			return
		}
	}
}
