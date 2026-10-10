package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

// DestinationEligibilityRepo keeps panel credentials, definitions and policy
// bodies outside the selection read path.
type DestinationEligibilityRepo struct{ db *gorm.DB }

func NewDestinationEligibilityRepo(db *gorm.DB) *DestinationEligibilityRepo {
	return &DestinationEligibilityRepo{db: db}
}
func (r *DestinationEligibilityRepo) GroupEligibilityMode(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", domain.ErrValidation
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r == nil || r.db == nil {
		return "", domain.ErrUnavailable
	}
	var row destGroupModeRow
	err := r.db.WithContext(ctx).Select("group_id", "mode", "stage").Where("group_id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "open", nil
	}
	if err != nil {
		return "", err
	}
	if row.Stage != "" && row.Stage != "trial" && row.Stage != "enforce" {
		return "", domain.ErrUnavailable
	}
	if row.Mode == "open" {
		return "open", nil
	}
	if row.Mode == "allowlist" && (row.Stage == "trial" || row.Stage == "enforce") {
		return "allowlist", nil
	}
	return "", domain.ErrUnavailable
}
func (r *DestinationEligibilityRepo) PanelDestinationEligibility(ctx context.Context, id int64) (ports.DestinationPanelEligibility, error) {
	var result ports.DestinationPanelEligibility
	if id <= 0 {
		return result, domain.ErrValidation
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if r == nil || r.db == nil {
		return result, domain.ErrUnavailable
	}
	var row struct {
		Kind                   string
		AgentID, PolicyAgentID sql.NullString
		ObservedCapabilities   jsonStrings
		FallbackReason         sql.NullString
		FallbackExhausted      sql.NullBool
	}
	// One current statement reads all enforcement facts together, without
	// panel credentials, policy bodies or unrelated agent metadata.
	err := r.db.WithContext(ctx).Table("xui_panels AS panels").
		Select("panels.kind, agents.agent_id, agents.observed_capabilities, policies.agent_id AS policy_agent_id, policies.fallback_reason, policies.fallback_exhausted").
		Joins("LEFT JOIN node_agents AS agents ON agents.panel_id = panels.id").
		Joins("LEFT JOIN dest_agent_policy AS policies ON policies.agent_id = agents.agent_id").
		Where("panels.id = ?", id).Take(&row).Error
	if err != nil {
		return result, wrapNotFound(err)
	}
	switch domain.NormalizePanelKind(domain.PanelKind(row.Kind)) {
	case domain.PanelKind3XUI, domain.PanelKindSUI:
		return result, nil
	case domain.PanelKindPSP:
	default:
		return result, domain.ErrUnavailable
	}
	if !row.AgentID.Valid || row.AgentID.String == "" {
		return result, domain.ErrNotFound
	}
	if row.PolicyAgentID.Valid {
		if !row.FallbackReason.Valid || !row.FallbackExhausted.Valid {
			return result, domain.ErrUnavailable
		}
		switch row.FallbackReason.String {
		case "", "rejected", "sniffing", "over_limit":
		default:
			return result, domain.ErrUnavailable
		}
	}
	result.Native = true
	result.PolicyCapable = slices.Contains(row.ObservedCapabilities, protocol.CapabilityDestinationPolicy)
	result.FallbackBlocked = row.FallbackReason.String != "" || row.FallbackExhausted.Bool
	return result, nil
}

var _ ports.DestinationEligibilityRepo = (*DestinationEligibilityRepo)(nil)
