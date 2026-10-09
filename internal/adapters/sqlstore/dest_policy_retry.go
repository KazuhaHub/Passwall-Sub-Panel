package sqlstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAgentPolicyRepo) RetryDestinationPolicy(ctx context.Context, agentID string, now time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, domain.ErrUnavailable
	}
	if agentID == "" || len(agentID) > 64 || strings.TrimSpace(agentID) != agentID {
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
		var row destAgentPolicyRow
		err := tx.Select("agent_id", "fallback_reason", "rejected_generation", "rejected_context", "fallback_exhausted").Where("agent_id = ?", agentID).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		switch row.FallbackReason {
		case "", "rejected", "sniffing", "over_limit":
		default:
			return domain.ErrUnavailable
		}
		if row.RejectedGeneration == 0 && row.RejectedContext == "" && !row.FallbackExhausted && row.FallbackReason != "rejected" {
			return nil
		}
		updates := map[string]any{"rejected_generation": int64(0), "rejected_context": "", "fallback_exhausted": false, "minted_at": nil, "updated_at": now}
		if row.FallbackReason == "rejected" {
			updates["fallback_reason"] = ""
		}
		// Retiring the receipt marker prevents the next sync's old rejected
		// observation from undoing retry before its new candidate can be minted.
		// Exact bytes/source and confirmed state remain intact. The sole config
		// minter rearms the marker even when candidate bytes are unchanged.
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

var _ ports.DestPolicyRetryRepo = (*DestAgentPolicyRepo)(nil)
