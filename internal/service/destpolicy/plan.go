package destpolicy

import (
	"context"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// Plan is the destination policy as it would be sent to one node, before the
// node's delivery state (a refusal it is holding, an over-limit fallback) is
// weighed: what routes by destination are budgeted against. Policy is nil when
// no policy is in force for the node.
type Plan struct {
	Policy *protocol.DestinationPolicy
	// Resolve is the mode the node would run under (as_is when there is no
	// policy).
	Resolve protocol.ResolveMode
	// Engine is the engine the compile was made for.
	Engine string
	// Clients counts the roster's clients per subject, which is how many user
	// names a rule that names the subject carries.
	Clients map[protocol.SubjectKey]int
}

// PlanPolicy is the policy a node would be sent for in, without touching the
// node's stored delivery state. It is read-only: the route editor asks it what
// a routed listener would cost and which rules a route would approximate, and
// the answer must not move a fallback or mint a body.
func (s *Service) PlanPolicy(ctx context.Context, in CompileInput) (Plan, error) {
	plan := Plan{Resolve: protocol.ResolveAsIs}
	engine := strings.ToLower(strings.TrimSpace(in.Config.Core.Engine))
	if engine == "" {
		engine = xrayEngine
	}
	plan.Engine = engine
	if in.Snapshot != nil {
		plan.Clients = map[protocol.SubjectKey]int{}
		for _, c := range in.Snapshot.Clients {
			if c.Client != nil {
				plan.Clients[protocol.NewSubjectKey(c.Client.UserID)]++
			}
		}
	}
	if !policyHasCapability(in.Capabilities) {
		return plan, nil
	}
	st, err := s.ensurePublished(ctx)
	if err != nil {
		return plan, err
	}
	if st.Paused || st.PublishedGeneration == 0 {
		return plan, nil
	}
	cb, err := s.baseFor(ctx, st.PublishedGeneration)
	if err != nil {
		return plan, err
	}
	if cb.doc.empty() {
		return plan, nil
	}
	view, nodes, err := s.viewFor(ctx, in, cb)
	if err != nil {
		return plan, err
	}
	desired := cb.base.assemble(view)
	withCollect(desired, effectiveCollectFor(in))
	resolve := EffectiveResolve(in.DestResolve, in.Capabilities)
	withResolve(desired, resolve)
	withAuditEgress(desired, auditEgressFor(in, engine))
	desired, _ = restrictForSniffing(desired, nodes, engine)
	desired, _, _ = settleResolve(desired, nodes, engine)
	if desired == nil {
		return plan, nil
	}
	plan.Policy = desired
	plan.Resolve = desired.Resolve
	return plan, nil
}

// PolicyRulesFor are the rules of a plan that apply to traffic leaving through
// exit on the given listeners, in policy order; an empty Exits allows every exit.
func PolicyRulesFor(policy *protocol.DestinationPolicy, listeners []protocol.ListenerKey, exit protocol.LinkKey) []protocol.DestinationRule {
	if policy == nil {
		return nil
	}
	var out []protocol.DestinationRule
	for _, rule := range policy.Rules {
		if len(rule.Exits) > 0 && !slices.Contains(rule.Exits, exit) {
			continue
		}
		if len(rule.Inbounds) > 0 {
			applies := false
			for _, key := range listeners {
				if slices.Contains(rule.Inbounds, key) {
					applies = true
					break
				}
			}
			if !applies {
				continue
			}
		}
		out = append(out, rule)
	}
	return out
}
