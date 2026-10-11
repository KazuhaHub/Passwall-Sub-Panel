package destpolicy

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ReadAppliedGroupStages verifies the current candidate, not the historical
// AppliedGroups summary. Callers must also match the returned stage against the
// group's current definition and verify their definition/status snapshots agree.
// No policy bytes or subjects are retained or returned, and collection may be off.
func ReadAppliedGroupStages(state domain.DestPolicyState, panel domain.DestTestPanel, poll time.Duration, now time.Time, load func() ([]byte, error)) (map[int64]string, error) {
	if poll <= 0 || poll > time.Duration(math.MaxInt64/3) || now.UnixMilli() <= 0 || state.Generation < 0 || state.PublishedGeneration < 0 || panel.ID <= 0 {
		return nil, domain.ErrUnavailable
	}
	out := map[int64]string{}
	if state.Paused || state.PublishedGeneration == 0 || state.Generation != state.PublishedGeneration || panel.Kind != domain.PanelKindPSP {
		return out, nil
	}
	a, r := panel.Agent, panel.Runtime
	if a == nil || r == nil || a.AgentID == "" || r.AgentID != a.AgentID || !a.ObservedCoreEngine.Valid() || domain.NormalizeNodeCoreEngine(a.DesiredCoreEngine) != a.ObservedCoreEngine || !slices.Contains(a.ObservedCapabilities, protocol.CapabilityDestinationPolicy) || a.LastSeen == nil || a.LastSeen.After(now) || now.Sub(*a.LastSeen) > 3*poll {
		return out, nil
	}
	if r.MintedKind != domain.DestCandidateDesired || r.MintedGeneration != state.PublishedGeneration || r.MintedSHA256 == "" || r.DesiredSHA256 != r.MintedSHA256 || r.ReportedSHA256 != r.MintedSHA256 || r.AppliedSHA256 != r.MintedSHA256 || r.ReportedState != "applied" || r.FallbackReason != "" || r.FallbackExhausted || r.OverLimit != nil || r.RejectedGeneration != 0 || r.RejectedContext != "" || len(r.PrecheckListeners) != 0 || len(r.ReportedListeners) != 0 || r.ReportedIssue != "" {
		return out, nil
	}
	for _, at := range []*time.Time{r.MintedAt, r.AppliedAt} {
		if at == nil || at.UnixMilli() <= 0 || at.After(now) {
			return out, nil
		}
	}
	if load == nil {
		return nil, domain.ErrUnavailable
	}
	body, err := load()
	if err != nil {
		return nil, err
	}
	var policy *protocol.DestinationPolicy
	if json.Unmarshal(body, &policy) != nil || protocol.ValidateDestinationPolicy(policy) != nil || protocol.PolicyDigest(policy) != r.MintedSHA256 {
		return nil, domain.ErrUnavailable
	}
	canonical, err := json.Marshal(policy)
	if err != nil || !bytes.Equal(canonical, body) {
		return nil, domain.ErrUnavailable
	}
	for _, rule := range policy.Rules {
		if !rule.CatchAll || len(rule.Subjects) == 0 || len(rule.ID) < 2 || rule.ID[0] != 'g' {
			continue
		}
		id, err := strconv.ParseInt(rule.ID[1:], 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rule.ID[1:] {
			continue
		}
		stage := ""
		switch rule.Action {
		case protocol.RuleObserve:
			stage = "trial"
		case protocol.RuleBlock:
			stage = "enforce"
		default:
			continue
		}
		if _, exists := out[id]; exists {
			return nil, domain.ErrUnavailable
		}
		out[id] = stage
	}
	return out, nil
}
