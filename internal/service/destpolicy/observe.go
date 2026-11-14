package destpolicy

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ObserveStatus takes what a node says about the policy it runs. It is called
// BEFORE the node's config is built, and that placement is the requirement: the
// status is a control input. A refusal has to change this very round's answer
// (fall back), and the last-known-good record has to be current when Compile
// reads it; were the status handled after the response, "refused, so fall
// back" would arrive a sync late.
//
// status has already been decoded and validated on its own by the handler (a
// malformed one never reaches here); a node that did not advertise the
// capability is ignored.
//
// It writes only what changed. The returned issues are one-time events to
// record (the ongoing ones come from Compile).
func (s *Service) ObserveStatus(ctx context.Context, agentID string, caps []string, status *protocol.PolicyStatus) ([]domain.NodeAgentIssue, error) {
	if status == nil || !policyHasCapability(caps) {
		return nil, nil
	}
	meta, err := s.metaFor(ctx, agentID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var patch domain.DestAgentPolicyPatch

	// What the node reports, kept as reported. The listeners are stored as a
	// JSON list of keys.
	listeners := ""
	if len(status.Listeners) > 0 {
		b, _ := json.Marshal(status.Listeners)
		listeners = string(b)
	}
	if status.Digest != meta.ReportedSHA256 || status.State != meta.ReportedState ||
		status.IssueCode != meta.ReportedIssue || listeners != meta.ReportedListeners {
		d, st, ic := status.Digest, status.State, status.IssueCode
		patch.ReportedSHA256, patch.ReportedState, patch.ReportedIssue, patch.ReportedListeners = &d, &st, &ic, &listeners
		patch.ReportedAt = &now
	}

	var issues []domain.NodeAgentIssue
	switch {
	case status.State == protocol.PolicyStateApplied && status.Digest != "":
		if status.Digest == meta.MintedSHA256 && status.Digest != meta.AppliedSHA256 {
			// The node runs exactly what PSP minted: that is the new last
			// known good. While paused the record is left alone, so what a
			// pause is lifted back to is what applied before it.
			if paused, err := s.paused(ctx); err != nil {
				return nil, err
			} else if !paused {
				if pol := s.mintedPolicy(agentID, status.Digest); pol != nil {
					body, err := json.Marshal(pol)
					if err != nil {
						return nil, err
					}
					count, groups := len(pol.Rules), appliedGroupIDs(pol)
					patch.AppliedSHA256, patch.AppliedBody, patch.AppliedAt = &status.Digest, &body, &now
					patch.AppliedRuleCount, patch.AppliedGroups = &count, &groups
					level := string(pol.Collect)
					patch.AppliedCollect = &level
				}
			}
		}
		if meta.RejectedSHA256 != "" && status.Digest == meta.DesiredSHA256 {
			none, noDigest := domain.DestFallbackNone, ""
			patch.RejectedSHA256 = &noDigest
			if meta.FallbackReason == domain.DestFallbackRejected {
				patch.FallbackReason = &none
			}
		}
	case status.State == protocol.PolicyStateRejected && status.Digest != "" && status.Digest == meta.MintedSHA256:
		// The refused policy may be the fallback rather than the desired one:
		// the recorded LKG, or the trimmed LKG minted in its place (its digest
		// differs from the record's, and minted differs from desired exactly
		// when a fallback was minted — for sniffing, over a limit, or a refusal).
		// A refused fallback is only that: dropping it makes the fallback "no
		// policy", which is always deployable, and minting it again would make
		// the whole config undeployable (roster frozen, disabled users still
		// connecting). It must NOT hold back the desired policy, which was
		// never what the node refused.
		if status.Digest == meta.AppliedSHA256 || meta.MintedSHA256 != meta.DesiredSHA256 {
			empty, none := "", []byte{}
			zero, noGroups := 0, []int64{}
			patch.AppliedSHA256, patch.AppliedBody, patch.AppliedRuleCount, patch.AppliedGroups = &empty, &none, &zero, &noGroups
			patch.AppliedCollect = &empty
			short := status.Digest
			if len(short) > 12 {
				short = short[:12]
			}
			issues = append(issues, domain.NodeAgentIssue{Code: IssueLKGRejected, Detail: "digest " + short})
		}
		// A refusal is held (and the new policy not retried until a new
		// generation, a capability change, or the admin) only when what was
		// refused IS the desired policy, against the generation it was minted at.
		if status.Digest == meta.DesiredSHA256 {
			reason, caps := domain.DestFallbackRejected, capsToken(caps)
			gen, digest := meta.MintedGeneration, status.Digest
			patch.FallbackReason, patch.RejectedSHA256, patch.RejectedGeneration, patch.RejectedCaps = &reason, &digest, &gen, &caps
			// The context the policy was minted in, which is the one it was
			// refused in.
			rctx := meta.MintedCtx
			patch.RejectedCtx = &rctx
		}
	}
	if err := s.patchMeta(ctx, agentID, patch); err != nil {
		return nil, err
	}
	return issues, nil
}

// appliedGroupIDs are the groups whose catch-all is in the policy, ascending.
func appliedGroupIDs(p *protocol.DestinationPolicy) []int64 {
	ids := []int64{}
	for _, r := range p.Rules {
		if kind, n, ok := ruleOrigin(r.ID); ok && kind == 'g' && r.CatchAll {
			ids = append(ids, n)
		}
	}
	slices.Sort(ids)
	return ids
}

func (s *Service) paused(ctx context.Context) (bool, error) {
	st, err := s.state.Get(ctx)
	if err != nil {
		return false, err
	}
	return st.Paused, nil
}

// mintedPolicy is the policy last minted for agentID when its digest is d: the
// body of what the node reports it runs, which the status itself does not
// carry. Not there (a restart since the mint) means the record waits for the
// next report, by which time the compile has run again.
func (s *Service) mintedPolicy(agentID, d string) *protocol.DestinationPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.compiled[agentID]; e != nil && e.res.Digest == d && e.res.Policy != nil {
		return e.res.Policy
	}
	return nil
}

// Retry clears a refusal so the next sync tries the current policy again (the
// admin's "send again"). It reports whether the agent was in a refused state.
func (s *Service) Retry(ctx context.Context, agentID string) (bool, error) {
	meta, err := s.metaFor(ctx, agentID)
	if err != nil {
		return false, err
	}
	if meta.RejectedSHA256 == "" {
		return false, nil
	}
	noDigest := ""
	patch := domain.DestAgentPolicyPatch{RejectedSHA256: &noDigest}
	if meta.FallbackReason == domain.DestFallbackRejected {
		none := domain.DestFallbackNone
		patch.FallbackReason = &none
	}
	return true, s.patchMeta(ctx, agentID, patch)
}
