package destpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type cachedSelection struct {
	candidate ports.DestPolicyCandidate
	decision  candidateDecisionFields
}

func clonePolicy(policy *protocol.DestinationPolicy) *protocol.DestinationPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	clone.Exempt = slices.Clone(policy.Exempt)
	clone.Rules = slices.Clone(policy.Rules)
	for n := range clone.Rules {
		r := &clone.Rules[n]
		r.Subjects = slices.Clone(r.Subjects)
		r.Domains = slices.Clone(r.Domains)
		r.CIDRs = slices.Clone(r.CIDRs)
		r.Protocols = slices.Clone(r.Protocols)
	}
	return &clone
}
func cloneCandidate(candidate ports.DestPolicyCandidate) ports.DestPolicyCandidate {
	candidate.Policy = clonePolicy(candidate.Policy)
	return candidate
}
func cloneDecision(decision candidateDecisionFields) candidateDecisionFields {
	decision.Listeners = slices.Clone(decision.Listeners)
	if decision.Limit != nil {
		copy := *decision.Limit
		decision.Limit = &copy
	}
	return decision
}
func (d candidateDecisionFields) apply(state *domain.DestAgentPolicy) bool {
	if reflect.DeepEqual(candidateDecision(*state), d) {
		return false
	}
	d = cloneDecision(d)
	state.FallbackReason, state.RejectedGeneration, state.RejectedContext = d.Reason, d.RejectedGeneration, d.RejectedContext
	state.FallbackExhausted, state.OverLimit, state.PrecheckListeners = d.Exhausted, d.Limit, d.Listeners
	return true
}
func fingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func selectionKey(input string, state domain.DestAgentPolicy) string {
	if input == "" {
		return ""
	}
	source := ""
	// Minted context matters only for exhaustion or legacy rejection migration.
	// Normal source-only minting must not make identical compilation cold again.
	if state.FallbackExhausted || (state.RejectedGeneration > 0 && state.RejectedContext == "") {
		source = state.MintedContext
	}
	return fingerprint(struct {
		Input, Applied, Source string
		Decision               candidateDecisionFields
	}{input, state.AppliedSHA256, source, candidateDecision(state)})
}
func (c *Compiler) prepareCached(agentID string, panelID int64, defs domain.DestDefinitions, roster, quota RosterInput, base protocol.ConfigBody) (*PreparedCandidate, string, error) {
	if !roster.Collect.Valid() {
		return nil, "", invalid("audit_collect")
	}
	context, err := PolicyContext(defs.State.Generation, roster.Capabilities, base)
	if err != nil {
		return nil, "", err
	}
	key := ""
	if roster.MembershipTracked && quota.MembershipTracked && roster.MembershipGeneration == quota.MembershipGeneration {
		ids := slices.Clone(roster.UserIDs)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		key = fingerprint(struct {
			Agent, Context, Roster, Engine string
			Paused                         bool
			Collect                        domain.AuditCollect
			Revision                       uint64
		}{agentID, context, membershipKey(panelID, roster.MembershipGeneration, ids), base.Core.Engine, defs.State.Paused, roster.Collect, roster.CollectRevision})
		if prepared, found := c.preparedCache.Get(key); found {
			return prepared, key, nil
		}
	}
	prepared, err := c.prepare(defs, roster, quota, base)
	if err != nil {
		return nil, "", err
	}
	if key != "" {
		copy := *prepared
		copy.roster = cloneMembershipInput(prepared.roster)
		copy.roster.Capabilities = slices.Clone(prepared.roster.Capabilities)
		copy.desired = clonePolicy(prepared.desired)
		copy.base = protocol.ConfigBody{Core: prepared.base.Core, Listeners: slices.Clone(prepared.base.Listeners)}
		for n := range copy.base.Listeners {
			copy.base.Listeners[n].Config = slices.Clone(copy.base.Listeners[n].Config)
		}
		prepared = &copy
		weight := policyWeight(copy.desired) + 64*len(copy.roster.UserGroups) + 8*len(copy.roster.UserIDs) + 512
		for _, listener := range copy.base.Listeners {
			weight += len(listener.Config) + 128
		}
		for _, list := range defs.Lists {
			weight += len(list.Entries) + 256
		}
		for _, policy := range defs.Policies {
			weight += 256 + 8*(len(policy.GroupIDs)+len(policy.ListIDs))
			for _, cidr := range policy.Inline.CIDRs {
				weight += len(cidr) + 32
			}
		}
		weight += 64 * len(defs.Exemptions)
		for _, group := range defs.Groups {
			weight += 128 + 8*len(group.ListIDs)
		}
		c.preparedCache.Put(key, prepared, weight)
	}
	return prepared, key, nil
}
func policyWeight(policy *protocol.DestinationPolicy) int {
	if policy == nil {
		return 128
	}
	weight := 256 + 32*len(policy.Exempt)
	for _, rule := range policy.Rules {
		weight += 256 + len(rule.ID) + len(rule.Ports) + len(rule.Network) + 32*len(rule.Subjects)
		for _, values := range [][]string{rule.Domains, rule.CIDRs, rule.Protocols} {
			for _, value := range values {
				weight += 32 + 2*len(value)
			}
		}
	}
	return weight
}
