package sqlstore

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (r *xuiPanelRepo) GetAuditSettings(ctx context.Context, panelID int64) (ports.PanelAuditSettings, error) {
	if panelID <= 0 {
		return ports.PanelAuditSettings{}, domain.ErrValidation
	}
	if r == nil || r.db == nil {
		return ports.PanelAuditSettings{}, domain.ErrUnavailable
	}
	var row xuiPanelRow
	if err := r.db.WithContext(ctx).Select("id", "kind", "audit_collect", "audit_collect_revision").First(&row, panelID).Error; err != nil {
		return ports.PanelAuditSettings{}, wrapNotFound(err)
	}
	collect := domain.AuditCollect(row.AuditCollect)
	if domain.NormalizePanelKind(domain.PanelKind(row.Kind)) != domain.PanelKindPSP || !collect.Valid() || row.AuditCollectRevision < 1 {
		return ports.PanelAuditSettings{}, fmt.Errorf("%w: invalid native audit control", domain.ErrUnavailable)
	}
	return ports.PanelAuditSettings{Collect: collect, Revision: uint64(row.AuditCollectRevision)}, nil
}

var _ ports.PanelAuditSettingsRepo = (*xuiPanelRepo)(nil)
