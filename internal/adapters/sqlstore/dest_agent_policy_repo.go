package sqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

type DestAgentPolicyRepo struct{ db *gorm.DB }

func NewDestAgentPolicyRepo(db *gorm.DB) *DestAgentPolicyRepo { return &DestAgentPolicyRepo{db: db} }

func (r *DestAgentPolicyRepo) Get(ctx context.Context, agentID string, includeBodies bool) (*domain.DestAgentPolicy, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	if agentID == "" {
		return nil, domain.ErrValidation
	}
	query := r.db.WithContext(ctx)
	if !includeBodies {
		query = query.Omit("MintedBody", "AppliedBody")
	}
	var row destAgentPolicyRow
	err := query.Where("agent_id = ?", agentID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var owner nodeAgentRow
		if err := r.db.WithContext(ctx).Select("agent_id").Where("agent_id = ?", agentID).First(&owner).Error; err != nil {
			return nil, wrapNotFound(err)
		}
		return &domain.DestAgentPolicy{AgentID: agentID}, nil
	}
	if err != nil {
		return nil, err
	}
	return runtimePolicyDomain(row)
}

func (r *DestAgentPolicyRepo) Update(ctx context.Context, agentID string, now time.Time, mutate func(*domain.DestAgentPolicy, func() error) (bool, error)) (bool, error) {
	if r == nil || r.db == nil {
		return false, domain.ErrUnavailable
	}
	if agentID == "" || mutate == nil {
		return false, domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return false, err
	}
	changed := false
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockNodeAgentByAgentID(tx, agentID); err != nil {
			return err
		}
		var old destAgentPolicyRow
		err := tx.Omit("MintedBody", "AppliedBody").Where("agent_id = ?", agentID).First(&old).Error
		exists := err == nil
		if errors.Is(err, gorm.ErrRecordNotFound) {
			old.AgentID = agentID
		} else if err != nil {
			return err
		}
		state, err := runtimePolicyDomain(old)
		if err != nil {
			return err
		}
		loaded := false
		loadBodies := func() error {
			if loaded {
				return nil
			}
			if !exists {
				return domain.ErrNotFound
			}
			var bodies destAgentPolicyRow
			if err := tx.Select("minted_body", "applied_body").Where("agent_id = ?", agentID).First(&bodies).Error; err != nil {
				return wrapNotFound(err)
			}
			old.MintedBody, old.AppliedBody = bodies.MintedBody, bodies.AppliedBody
			state.MintedBody = append([]byte(nil), bodies.MintedBody...)
			state.AppliedBody = append([]byte(nil), bodies.AppliedBody...)
			loaded = true
			return nil
		}
		requested, err := mutate(state, loadBodies)
		if err != nil {
			return err
		}
		if !requested {
			return nil
		}
		if !runtimeMintSourceEqual(old, state) || !bytes.Equal(old.MintedBody, state.MintedBody) {
			return fmt.Errorf("%w: runtime mutation changed minted source", domain.ErrValidation)
		}
		next, err := runtimePolicyRow(*state)
		if err != nil {
			return err
		}
		updates := runtimePolicyUpdates(old, next)
		if len(updates) == 0 {
			return nil
		}
		if old.AppliedSHA256 != next.AppliedSHA256 || !bytes.Equal(old.AppliedBody, next.AppliedBody) || !runtimeTimeEqual(old.AppliedAt, next.AppliedAt) || old.AppliedRuleCount != next.AppliedRuleCount || !reflect.DeepEqual(old.AppliedGroups, next.AppliedGroups) {
			if err := validateRuntimeApplied(next); err != nil {
				return err
			}
		}
		if !exists {
			next.UpdatedAt = now
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			changed = true
			return nil
		}
		updates["updated_at"] = now
		result := tx.Model(&destAgentPolicyRow{}).Where("agent_id = ?", agentID).UpdateColumns(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return domain.ErrConflict
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func runtimeMintSourceEqual(old destAgentPolicyRow, s *domain.DestAgentPolicy) bool {
	return old.AgentID == s.AgentID && old.DesiredSHA256 == s.DesiredSHA256 && old.MintedSHA256 == s.MintedSHA256 && old.MintedKind == string(s.MintedKind) && old.MintedGeneration == s.MintedGeneration && old.MintedContext == s.MintedContext && old.CollectEffective == s.CollectEffective && runtimeTimeEqual(old.MintedAt, s.MintedAt)
}
func runtimeTimeEqual(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func runtimeTimeCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	v := value.UTC()
	return &v
}

func runtimePolicyDomain(r destAgentPolicyRow) (*domain.DestAgentPolicy, error) {
	var over *domain.DestPublishError
	if r.OverLimit != nil {
		over = new(domain.DestPublishError)
		if json.Unmarshal([]byte(*r.OverLimit), over) != nil {
			return nil, fmt.Errorf("%w: corrupt destination runtime limit", domain.ErrUnavailable)
		}
	}
	return &domain.DestAgentPolicy{
		AgentID: r.AgentID, DesiredSHA256: r.DesiredSHA256, MintedSHA256: r.MintedSHA256, MintedBody: append([]byte(nil), r.MintedBody...), MintedKind: domain.DestCandidateKind(r.MintedKind), MintedGeneration: r.MintedGeneration, MintedContext: r.MintedContext, MintedAt: runtimeTimeCopy(r.MintedAt),
		FallbackReason: r.FallbackReason, RejectedGeneration: r.RejectedGeneration, FallbackExhausted: r.FallbackExhausted, OverLimit: over, PrecheckListeners: append([]string(nil), r.PrecheckListeners...),
		AppliedSHA256: r.AppliedSHA256, AppliedBody: append([]byte(nil), r.AppliedBody...), AppliedAt: runtimeTimeCopy(r.AppliedAt), AppliedRuleCount: r.AppliedRuleCount, AppliedGroups: append([]int64(nil), r.AppliedGroups...),
		CollectEffective: r.CollectEffective, ReportedSHA256: r.ReportedSHA256, ReportedState: r.ReportedState, ReportedIssue: r.ReportedIssue, ReportedListeners: append([]string(nil), r.ReportedListeners...), ReportedAt: runtimeTimeCopy(r.ReportedAt), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}
func runtimePolicyRow(s domain.DestAgentPolicy) (destAgentPolicyRow, error) {
	var over *string
	if s.OverLimit != nil {
		raw, err := json.Marshal(s.OverLimit)
		if err != nil {
			return destAgentPolicyRow{}, err
		}
		value := string(raw)
		over = &value
	}
	return destAgentPolicyRow{
		AgentID: s.AgentID, DesiredSHA256: s.DesiredSHA256, MintedSHA256: s.MintedSHA256, MintedBody: destBytes(s.MintedBody), MintedKind: string(s.MintedKind), MintedGeneration: s.MintedGeneration, MintedContext: s.MintedContext, MintedAt: s.MintedAt,
		FallbackReason: s.FallbackReason, RejectedGeneration: s.RejectedGeneration, FallbackExhausted: s.FallbackExhausted, OverLimit: over, PrecheckListeners: jsonStrings(s.PrecheckListeners),
		AppliedSHA256: s.AppliedSHA256, AppliedBody: destBytes(s.AppliedBody), AppliedAt: s.AppliedAt, AppliedRuleCount: s.AppliedRuleCount, AppliedGroups: jsonInt64s(s.AppliedGroups),
		CollectEffective: s.CollectEffective, ReportedSHA256: s.ReportedSHA256, ReportedState: s.ReportedState, ReportedIssue: s.ReportedIssue, ReportedListeners: jsonStrings(s.ReportedListeners), ReportedAt: s.ReportedAt, UpdatedAt: s.UpdatedAt,
	}, nil
}
func runtimePolicyFields(r destAgentPolicyRow) map[string]any {
	return map[string]any{
		"fallback_reason": r.FallbackReason, "rejected_generation": r.RejectedGeneration, "fallback_exhausted": r.FallbackExhausted, "over_limit": r.OverLimit, "precheck_listeners": r.PrecheckListeners,
		"applied_sha256": r.AppliedSHA256, "applied_body": r.AppliedBody, "applied_at": r.AppliedAt, "applied_rule_count": r.AppliedRuleCount, "applied_groups": r.AppliedGroups,
		"reported_sha256": r.ReportedSHA256, "reported_state": r.ReportedState, "reported_issue": r.ReportedIssue, "reported_listeners": r.ReportedListeners, "reported_at": r.ReportedAt,
	}
}
func runtimePolicyUpdates(old, next destAgentPolicyRow) map[string]any {
	before, after := runtimePolicyFields(old), runtimePolicyFields(next)
	updates := map[string]any{}
	for key, value := range after {
		if !reflect.DeepEqual(before[key], value) {
			updates[key] = value
		}
	}
	if old.AppliedSHA256 != "" && next.AppliedSHA256 == "" {
		updates["applied_body"] = destBytes(nil)
	}
	return updates
}
func validateRuntimeApplied(row destAgentPolicyRow) error {
	if row.AppliedSHA256 == "" {
		if len(row.AppliedBody) == 0 && row.AppliedRuleCount == 0 && len(row.AppliedGroups) == 0 && row.AppliedAt == nil {
			return nil
		}
		return domain.ErrValidation
	}
	var policy *protocol.DestinationPolicy
	if row.AppliedAt == nil || json.Unmarshal(row.AppliedBody, &policy) != nil || policy == nil || len(policy.Rules) == 0 || protocol.ValidateDestinationPolicy(policy) != nil || protocol.PolicyDigest(policy) != row.AppliedSHA256 || row.AppliedSHA256 != row.MintedSHA256 || !bytes.Equal(row.AppliedBody, row.MintedBody) || len(policy.Rules) != row.AppliedRuleCount {
		return fmt.Errorf("%w: invalid confirmed destination policy", domain.ErrValidation)
	}
	// Group acceptance is derived from the exact confirmed rules, so a caller
	// cannot create acceptance metadata for an absent group.
	var groups []int64
	for _, rule := range policy.Rules {
		if !strings.HasPrefix(rule.ID, "g") {
			continue
		}
		raw, _, _ := strings.Cut(strings.TrimPrefix(rule.ID, "g"), "x")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return domain.ErrValidation
		}
		groups = append(groups, id)
	}
	slices.Sort(groups)
	if !slices.Equal(slices.Compact(groups), row.AppliedGroups) {
		return fmt.Errorf("%w: confirmed destination groups mismatch", domain.ErrValidation)
	}
	return nil
}

var _ ports.DestAgentPolicyRepo = (*DestAgentPolicyRepo)(nil)
