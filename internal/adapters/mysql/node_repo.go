package mysql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeRepo struct{ db *gorm.DB }

func (r *nodeRepo) Create(ctx context.Context, n *domain.Node) error {
	row := nodeFromDomain(n)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	n.ID = row.ID
	n.CreatedAt = row.CreatedAt
	return nil
}

func (r *nodeRepo) Update(ctx context.Context, n *domain.Node) error {
	return r.db.WithContext(ctx).Save(nodeFromDomain(n)).Error
}

// UpdateTrafficCounters writes only the lifetime + last-raw counter columns.
// The traffic poll and the health checker run on separate goroutines and both
// loaded-mutated-Saved the same node row; a full-row Save from one would
// revert the other's columns (health flapping, or counters resetting and
// producing a bogus delta next poll). Narrow writes let them coexist.
func (r *nodeRepo) UpdateTrafficCounters(ctx context.Context, n *domain.Node) error {
	if n == nil || n.ID == 0 {
		return fmt.Errorf("UpdateTrafficCounters requires a non-zero node ID; got %+v", n)
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", n.ID).
		Updates(map[string]any{
			"lifetime_up_bytes":        n.LifetimeUpBytes,
			"lifetime_down_bytes":      n.LifetimeDownBytes,
			"lifetime_total_bytes":     n.LifetimeTotalBytes,
			"last_traffic_up_bytes":    n.LastTrafficUpBytes,
			"last_traffic_down_bytes":  n.LastTrafficDownBytes,
			"last_traffic_total_bytes": n.LastTrafficTotalBytes,
		}).Error
}

// UpdateHealth writes only the health-probe columns (see UpdateTrafficCounters
// for why this is column-scoped rather than a full-row Save).
func (r *nodeRepo) UpdateHealth(ctx context.Context, n *domain.Node) error {
	if n == nil || n.ID == 0 {
		return fmt.Errorf("UpdateHealth requires a non-zero node ID; got %+v", n)
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", n.ID).
		Updates(map[string]any{
			"health_state":      string(n.HealthState),
			"health_detail":     n.HealthDetail,
			"health_checked_at": n.HealthCheckedAt,
		}).Error
}

// BatchUpdateSortOrder rewrites sort_order for the listed nodes inside one
// transaction. Used by the drag-to-reorder UI: the admin drags a row, the
// frontend re-numbers the visible list in 10-step increments, and POSTs the
// whole list back here. Doing this row-by-row outside a tx would leave the
// table in a half-reordered state if the request was interrupted.
func (r *nodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range updates {
			if err := tx.Model(&nodeRow{}).
				Where("id = ?", u.NodeID).
				Update("sort_order", u.SortOrder).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *nodeRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&nodeRow{}, id).Error
}

func (r *nodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	var row nodeRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *nodeRepo) GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	var row nodeRow
	err := r.db.WithContext(ctx).
		Where("panel_id = ? AND inbound_id = ?", panelID, inboundID).
		First(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *nodeRepo) List(ctx context.Context) ([]*domain.Node, error) {
	var rows []nodeRow
	if err := r.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *nodeRepo) ListEnabled(ctx context.Context) ([]*domain.Node, error) {
	var rows []nodeRow
	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
