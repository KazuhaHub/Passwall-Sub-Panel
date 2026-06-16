package sqlstore

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeRepo struct{ db *gorm.DB }

func (r *nodeRepo) Create(ctx context.Context, n *domain.Node) error {
	row, err := nodeFromDomain(n)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	n.ID = row.ID
	n.CreatedAt = row.CreatedAt
	return nil
}

func (r *nodeRepo) Update(ctx context.Context, n *domain.Node) error {
	row, err := nodeFromDomain(n)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(row).Error
}

// UpdateMetadata writes ONLY the admin-editable identity columns, leaving the
// poll-owned columns untouched: traffic counters (poll), health (health probe),
// and the inbound-config snapshot + sync-state (reconcile/write-through). A
// full-row Save here would roll those back to whatever stale values the edit
// dialog loaded — double-counted/lost traffic, flapping health. Same
// column-scoped rationale as UpdateTrafficCounters / UpdateHealth.
func (r *nodeRepo) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	if n == nil || n.ID == 0 {
		return fmt.Errorf("UpdateMetadata requires a non-zero node ID; got %+v", n)
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", n.ID).
		Updates(map[string]any{
			"display_name":   n.DisplayName,
			"server_address": n.ServerAddress,
			"flow":           n.Flow,
			"region":         n.Region,
			"tags":           jsonStrings(n.Tags),
			"sort_order":     n.SortOrder,
			"relays":         jsonRelays(n.Relays),
			"hide_direct":    n.HideDirect,
		}).Error
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

// BatchUpdateTrafficCounters applies UpdateTrafficCounters to every node in one
// transaction — the end-of-cycle flush for the traffic poll's per-node lifetime
// accounting, collapsing what used to be one inline UPDATE per inbound into a
// single batched round-trip (see ports.NodeRepo). Column-scoped, so it never
// clobbers the concurrent health pass.
func (r *nodeRepo) BatchUpdateTrafficCounters(ctx context.Context, nodes []*domain.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, n := range nodes {
			if n == nil || n.ID == 0 {
				return fmt.Errorf("BatchUpdateTrafficCounters requires a non-zero node ID; got %+v", n)
			}
			if err := tx.Model(&nodeRow{}).
				Where("id = ?", n.ID).
				Updates(map[string]any{
					"lifetime_up_bytes":        n.LifetimeUpBytes,
					"lifetime_down_bytes":      n.LifetimeDownBytes,
					"lifetime_total_bytes":     n.LifetimeTotalBytes,
					"last_traffic_up_bytes":    n.LastTrafficUpBytes,
					"last_traffic_down_bytes":  n.LastTrafficDownBytes,
					"last_traffic_total_bytes": n.LastTrafficTotalBytes,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
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
			// The health pass also refreshes the cached probe target learned
			// from the inbound, so a port/protocol change propagates without a
			// separate write path.
			"port":     n.Port,
			"protocol": n.Protocol,
		}).Error
}

// UpdateInboundConfig writes only the v3.5 inbound-config snapshot columns
// (plus port/protocol which the snapshot also owns). Same column-scoping
// rationale as UpdateHealth / UpdateTrafficCounters: snapshot writers (admin
// create/update, reconcile backfill, post-push capture) run concurrently
// with the health pass and the traffic poll, so a full-row Save would
// clobber their writes.
func (r *nodeRepo) UpdateInboundConfig(ctx context.Context, n *domain.Node) error {
	if n == nil || n.ID == 0 {
		return fmt.Errorf("UpdateInboundConfig requires a non-zero node ID; got %+v", n)
	}
	inboundSettings, err := encryptSecret(n.InboundSettings)
	if err != nil {
		return fmt.Errorf("encrypt inbound_settings: %w", err)
	}
	streamSettings, err := encryptSecret(n.StreamSettings)
	if err != nil {
		return fmt.Errorf("encrypt stream_settings: %w", err)
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", n.ID).
		Updates(map[string]any{
			"inbound_listen":      n.InboundListen,
			"inbound_remark":      n.InboundRemark,
			"inbound_settings":    inboundSettings,
			"stream_settings":     streamSettings,
			"sniffing":            n.Sniffing,
			"allocate":            n.Allocate,
			"inbound_expiry_time": n.InboundExpiryTime,
			"config_synced_at":    n.ConfigSyncedAt,
			"config_sync_state":   n.ConfigSyncState,
			"port":                n.Port,
			"protocol":            n.Protocol,
		}).Error
}

// UpdateEnabled writes only the `enabled` column (see UpdateHealth for the
// column-scoping rationale). SetEnabled, DeleteAndSync, and reconcile's
// disappeared-inbound branch flip enabled on a node snapshot read at cycle
// start; a full-row Save there would revert health/traffic/config columns the
// concurrent loops write.
func (r *nodeRepo) UpdateEnabled(ctx context.Context, id int64, enabled bool) error {
	if id == 0 {
		return fmt.Errorf("UpdateEnabled requires a non-zero node ID")
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", id).
		Update("enabled", enabled).Error
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
	return row.toDomain()
}

func (r *nodeRepo) GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	var row nodeRow
	err := r.db.WithContext(ctx).
		Where("panel_id = ? AND inbound_id = ?", panelID, inboundID).
		First(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *nodeRepo) UpdateCertBinding(ctx context.Context, nodeID int64, source domain.CertSource, certID int64) error {
	if nodeID == 0 {
		return fmt.Errorf("UpdateCertBinding requires a non-zero node ID")
	}
	return r.db.WithContext(ctx).
		Model(&nodeRow{}).
		Where("id = ?", nodeID).
		Updates(map[string]any{
			"cert_source": string(source),
			"cert_id":     certID,
		}).Error
}

func (r *nodeRepo) ListByCertID(ctx context.Context, certID int64) ([]*domain.Node, error) {
	var rows []nodeRow
	if err := r.db.WithContext(ctx).
		Where("cert_id = ? AND cert_source = ?", certID, string(domain.CertSourceManaged)).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		n, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

func (r *nodeRepo) List(ctx context.Context) ([]*domain.Node, error) {
	var rows []nodeRow
	if err := r.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		n, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

var nodeSortAllowlist = map[string]string{
	"id":           "id",
	"display_name": "display_name",
	"sort_order":   "sort_order",
	"created_at":   "created_at",
	"panel_id":     "panel_id",
	"region":       "region",
}

func (r *nodeRepo) ListPaged(ctx context.Context, p ports.Pagination) ([]*domain.Node, int64, error) {
	q := r.db.WithContext(ctx).Model(&nodeRow{})
	if like := keywordLike(p.Keyword); like != "" {
		// tags is a JSON-encoded text column; the LIKE on it gives "any
		// tag substring matches" which is what admin expects.
		q = q.Where(
			likeCols("display_name", "server_address", "region", "tags"),
			like, like, like, like,
		)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []nodeRow
	if err := applyPagination(q, p, nodeSortAllowlist, "sort_order").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		n, err := rows[i].toDomain()
		if err != nil {
			return nil, 0, err
		}
		out[i] = n
	}
	return out, total, nil
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
		n, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}
