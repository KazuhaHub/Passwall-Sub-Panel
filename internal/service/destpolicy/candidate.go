package destpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"reflect"
)

type PreparedCandidate struct {
	defs                domain.DestDefinitions
	roster              RosterInput
	base                protocol.ConfigBody
	desired             *protocol.DestinationPolicy
	context, desiredSHA string
	limit               *domain.DestPublishError
	precheck            []string
}

// PrepareCandidate performs definition/roster work before taking the runtime
// state transaction. Quota membership includes tag-matched ineligible members.
func PrepareCandidate(defs domain.DestDefinitions, roster, quota RosterInput, base protocol.ConfigBody) (*PreparedCandidate, error) {
	if defs.State.Generation < 0 || defs.State.PublishedGeneration != defs.State.Generation {
		return nil, fmt.Errorf("%w: candidate requires published definitions", domain.ErrUnavailable)
	}
	roster.Engine = base.Core.Engine
	if !roster.Collect.Valid() {
		return nil, invalid("audit_collect")
	}
	if EffectiveCollect(roster.Collect, roster.Capabilities, roster.Engine) != "" && roster.CollectRevision == 0 {
		return nil, invalid("audit_collect_revision")
	}
	quota.Capabilities, quota.Engine, quota.Collect, quota.CollectRevision = roster.Capabilities, roster.Engine, roster.Collect, roster.CollectRevision
	context, err := PolicyContext(defs.State.Generation, roster.Capabilities, base)
	if err != nil {
		return nil, err
	}
	p := &PreparedCandidate{defs: defs, roster: roster, base: base, context: context}
	p.desired, err = buildPolicy(defs, roster, false)
	if err != nil {
		return nil, err
	}
	p.desiredSHA = protocol.PolicyDigest(p.desired)
	if p.desired != nil && len(p.desired.Rules) > 0 && defs.State.Generation == 0 {
		return nil, fmt.Errorf("%w: executable policy has no publication", domain.ErrUnavailable)
	}
	if defs.State.Paused {
		return p, nil
	}
	quotaPolicy, err := buildPolicy(defs, quota, false)
	if err != nil {
		return nil, err
	}
	p.limit = nodePolicyLimit(quotaPolicy)
	if p.limit == nil {
		p.limit = nodePolicyLimit(p.desired)
	}
	for _, key := range PreflightPolicy(quotaPolicy, roster.Engine, base.Listeners) {
		p.precheck = append(p.precheck, string(key))
	}
	return p, nil
}

// SelectCandidate is called against runtime state in its owner transaction.
// It requests lazy bodies only when an actual LKG must be pruned.
func SelectCandidate(prepared *PreparedCandidate, state *domain.DestAgentPolicy) (ports.DestPolicyCandidate, bool, error) {
	if prepared == nil || state == nil {
		return ports.DestPolicyCandidate{}, false, domain.ErrUnavailable
	}
	p := prepared
	result := ports.DestPolicyCandidate{Mint: domain.DestPolicyMint{Kind: domain.DestCandidateEmpty, Generation: p.defs.State.Generation, Context: p.context, DesiredSHA256: p.desiredSHA}}
	if p.defs.State.Paused {
		result.Policy, result.Mint.Kind = p.desired, domain.DestCandidatePaused
		setCandidateCollect(&result)
		return result, false, nil
	}
	emptyPolicy, err := emptyCandidatePolicy(p.roster)
	if err != nil {
		return ports.DestPolicyCandidate{}, false, err
	}
	next := *state
	if next.FallbackExhausted && next.MintedContext == p.context {
		result.Policy = emptyPolicy
		setCandidateCollect(&result)
		return result, false, nil
	}
	if next.MintedContext != p.context {
		next.FallbackExhausted = false
	}
	// Old rows can retain their historical rejection without guessing it from
	// a later mint. New observations always write this separate source field.
	if next.RejectedGeneration > 0 && next.RejectedContext == "" {
		next.RejectedContext = next.MintedContext
	}
	rejected := next.RejectedGeneration > 0 && next.RejectedGeneration == p.defs.State.Generation && next.RejectedContext == p.context
	fallback := false
	if p.limit != nil {
		next.FallbackReason, next.OverLimit, next.PrecheckListeners = "over_limit", p.limit, nil
		fallback = true
	} else if len(p.precheck) > 0 {
		next.FallbackReason, next.OverLimit, next.PrecheckListeners = "sniffing", nil, append([]string(nil), p.precheck...)
		fallback = true
	} else if rejected {
		next.FallbackReason, next.OverLimit, next.PrecheckListeners = "rejected", nil, nil
		fallback = true
	} else {
		if next.FallbackReason == "over_limit" || next.FallbackReason == "sniffing" {
			next.FallbackReason = ""
		}
		next.OverLimit, next.PrecheckListeners = nil, nil
	}
	if !fallback {
		result.Policy = p.desired
		if result.Policy != nil && len(result.Policy.Rules) > 0 {
			result.Mint.Kind = domain.DestCandidateDesired
		}
	} else if next.AppliedSHA256 != "" {
		if len(next.AppliedBody) == 0 {
			return ports.DestPolicyCandidate{}, false, errPolicyBodiesRequired
		}
		var lkg *protocol.DestinationPolicy
		if json.Unmarshal(next.AppliedBody, &lkg) != nil || lkg == nil || protocol.ValidateDestinationPolicy(lkg) != nil || protocol.PolicyDigest(lkg) != next.AppliedSHA256 {
			return ports.DestPolicyCandidate{}, false, fmt.Errorf("%w: corrupt confirmed destination body", domain.ErrUnavailable)
		}
		canonical, err := json.Marshal(lkg)
		if err != nil || !bytes.Equal(canonical, next.AppliedBody) {
			return ports.DestPolicyCandidate{}, false, fmt.Errorf("%w: noncanonical confirmed destination body", domain.ErrUnavailable)
		}
		result.Policy, err = PruneFallback(lkg, p.defs, p.roster)
		if err != nil && !errors.Is(err, domain.ErrValidation) {
			return ports.DestPolicyCandidate{}, false, err
		}
		if err != nil || nodePolicyLimit(result.Policy) != nil || len(PreflightPolicy(result.Policy, p.roster.Engine, p.base.Listeners)) > 0 {
			next.FallbackExhausted = true
			result.Policy = emptyPolicy
		} else if result.Policy != nil && len(result.Policy.Rules) > 0 {
			result.Mint.Kind = domain.DestCandidateFallback
		}
	} else {
		result.Policy = emptyPolicy
	}
	setCandidateCollect(&result)
	changed := !reflect.DeepEqual(candidateDecision(*state), candidateDecision(next))
	if changed {
		*state = next
	}
	return result, changed, nil
}

func emptyCandidatePolicy(input RosterInput) (*protocol.DestinationPolicy, error) {
	return buildPolicy(domain.DestDefinitions{State: domain.DestPolicyState{Paused: true}}, input, true)
}
func setCandidateCollect(result *ports.DestPolicyCandidate) {
	if result.Policy != nil {
		result.Mint.CollectEffective = string(result.Policy.Collect)
	}
}

type candidateDecisionFields struct {
	Reason             string
	RejectedGeneration int64
	RejectedContext    string
	Exhausted          bool
	Limit              *domain.DestPublishError
	Listeners          []string
}

func candidateDecision(s domain.DestAgentPolicy) candidateDecisionFields {
	return candidateDecisionFields{s.FallbackReason, s.RejectedGeneration, s.RejectedContext, s.FallbackExhausted, s.OverLimit, s.PrecheckListeners}
}
func nodePolicyLimit(p *protocol.DestinationPolicy) *domain.DestPublishError {
	if p == nil {
		return nil
	}
	used := int64(len(p.Exempt))
	for _, rule := range p.Rules {
		used += int64(len(rule.Subjects))
	}
	if used > protocol.MaxDestinationSubjects {
		return &domain.DestPublishError{Kind: "subjects", Used: used, Limit: protocol.MaxDestinationSubjects}
	}
	encoded, err := json.Marshal(p)
	if err == nil && len(encoded) > protocol.MaxDestinationPolicyBytes {
		return &domain.DestPublishError{Kind: "bytes", Used: int64(len(encoded)), Limit: protocol.MaxDestinationPolicyBytes}
	}
	if err != nil || protocol.ValidateDestinationPolicy(p) != nil {
		return &domain.DestPublishError{Kind: "invalid", Field: "policy"}
	}
	return nil
}
