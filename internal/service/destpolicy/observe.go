package destpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ApplyPolicyStatus is the status transition used inside the future durable
// observer transaction. It changes no published definitions and never infers
// a candidate from the current generation: the exact minted source is authority.
func ApplyPolicyStatus(state *domain.DestAgentPolicy, status *protocol.PolicyStatus, capabilities []string, now time.Time) (bool, error) {
	if status == nil || !slices.Contains(capabilities, protocol.CapabilityDestinationPolicy) || protocol.ValidatePolicyStatus(*status) != nil {
		return false, nil
	}
	if state == nil {
		return false, domain.ErrUnavailable
	}
	if now.UnixMilli() <= 0 || now.Year() > 9999 {
		return false, invalid("observation.time")
	}
	now = time.UnixMilli(now.UnixMilli()).UTC()
	next := *state
	listeners := make([]string, len(status.Listeners))
	for i, id := range status.Listeners {
		listeners[i] = string(id)
	}
	changed := next.ReportedSHA256 != status.Digest || next.ReportedState != status.State || next.ReportedIssue != status.IssueCode || !slices.Equal(next.ReportedListeners, listeners)
	if changed {
		next.ReportedSHA256, next.ReportedState, next.ReportedIssue = status.Digest, status.State, status.IssueCode
		next.ReportedListeners = listeners
		next.ReportedAt = &now
	}
	matching := next.MintedAt != nil && status.Digest == next.MintedSHA256
	if matching {
		// Repeated observations are metadata-only. A same-digest source change
		// that has a pending desired transition must still clear its fallback.
		settled := false
		switch next.MintedKind {
		case domain.DestCandidateDesired:
			settled = status.State == "applied" && next.AppliedSHA256 == next.MintedSHA256 && next.AppliedAt != nil && next.FallbackReason == "" && !next.FallbackExhausted && next.RejectedGeneration == 0 && next.OverLimit == nil && len(next.PrecheckListeners) == 0 || status.State == "rejected" && next.FallbackReason == "rejected" && next.RejectedGeneration == next.MintedGeneration
		case domain.DestCandidateFallback:
			settled = status.State == "applied" && next.AppliedSHA256 == next.MintedSHA256 && next.AppliedAt != nil || status.State == "rejected" && next.FallbackExhausted && next.AppliedSHA256 == "" && len(next.AppliedBody) == 0
		case domain.DestCandidateEmpty, domain.DestCandidatePaused:
			settled = true
		default:
			return false, fmt.Errorf("%w: unknown minted policy source", domain.ErrUnavailable)
		}
		if !settled {
			var policy *protocol.DestinationPolicy
			if json.Unmarshal(next.MintedBody, &policy) != nil || protocol.ValidateDestinationPolicy(policy) != nil || protocol.PolicyDigest(policy) != next.MintedSHA256 {
				return false, fmt.Errorf("%w: corrupt minted policy", domain.ErrUnavailable)
			}
			canonical, err := json.Marshal(policy)
			if err != nil || !bytes.Equal(canonical, next.MintedBody) {
				return false, fmt.Errorf("%w: noncanonical minted policy", domain.ErrUnavailable)
			}
			if status.State == "applied" && policy != nil && len(policy.Rules) > 0 {
				if next.AppliedSHA256 != next.MintedSHA256 || !bytes.Equal(next.AppliedBody, next.MintedBody) || next.AppliedAt == nil {
					next.AppliedSHA256 = next.MintedSHA256
					next.AppliedBody = append([]byte(nil), next.MintedBody...)
					next.AppliedAt = &now
					next.AppliedRuleCount = len(policy.Rules)
					next.AppliedGroups = appliedPolicyGroups(policy)
					changed = true
				}
				if next.MintedKind == domain.DestCandidateDesired && (next.FallbackReason != "" || next.RejectedGeneration != 0 || next.FallbackExhausted || next.OverLimit != nil || len(next.PrecheckListeners) > 0) {
					next.FallbackReason = ""
					next.RejectedGeneration = 0
					next.FallbackExhausted = false
					next.OverLimit = nil
					next.PrecheckListeners = nil
					changed = true
				}
			} else if status.State == "rejected" {
				switch next.MintedKind {
				case domain.DestCandidateDesired:
					if next.FallbackReason != "rejected" || next.RejectedGeneration != next.MintedGeneration {
						next.FallbackReason = "rejected"
						next.RejectedGeneration = next.MintedGeneration
						changed = true
					}
				case domain.DestCandidateFallback:
					next.AppliedSHA256 = ""
					next.AppliedBody = nil
					next.AppliedAt = nil
					next.AppliedRuleCount = 0
					next.AppliedGroups = nil
					next.FallbackExhausted = true
					changed = true
				}
			}
		}
	}
	if changed {
		next.UpdatedAt = now
		*state = next
	}
	return changed, nil
}

func appliedPolicyGroups(policy *protocol.DestinationPolicy) []int64 {
	var result []int64
	for _, rule := range policy.Rules {
		if !strings.HasPrefix(rule.ID, "g") {
			continue
		}
		raw, _, _ := strings.Cut(strings.TrimPrefix(rule.ID, "g"), "x")
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			result = append(result, id)
		}
	}
	return uniqueIDs(result)
}
