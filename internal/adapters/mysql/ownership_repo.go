package mysql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type ownershipRepo struct{ db *gorm.DB }

func (r *ownershipRepo) Add(ctx context.Context, e *domain.XUIClientEntry) error {
	row := ownershipFromDomain(e)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	e.ID = row.ID
	e.CreatedAt = row.CreatedAt
	return nil
}

func (r *ownershipRepo) Remove(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&ownershipRow{}, id).Error
}

func (r *ownershipRepo) RemoveByMatch(ctx context.Context, panelID int64, inboundID int, email string) error {
	return r.db.WithContext(ctx).
		Where("panel_id = ? AND inbound_id = ? AND client_email = ?", panelID, inboundID, email).
		Delete(&ownershipRow{}).Error
}

func (r *ownershipRepo) GetByMatch(ctx context.Context, panelID int64, inboundID int, email string) (*domain.XUIClientEntry, error) {
	var row ownershipRow
	err := r.db.WithContext(ctx).
		Where("panel_id = ? AND inbound_id = ? AND client_email = ?", panelID, inboundID, email).
		First(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *ownershipRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error) {
	var rows []ownershipRow
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.XUIClientEntry, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *ownershipRepo) ListByInbound(ctx context.Context, panelID int64, inboundID int) ([]*domain.XUIClientEntry, error) {
	var rows []ownershipRow
	err := r.db.WithContext(ctx).
		Where("panel_id = ? AND inbound_id = ?", panelID, inboundID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.XUIClientEntry, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *ownershipRepo) UpdateUUID(ctx context.Context, panelID int64, inboundID int, email, newUUID string) error {
	return r.db.WithContext(ctx).Model(&ownershipRow{}).
		Where("panel_id = ? AND inbound_id = ? AND client_email = ?", panelID, inboundID, email).
		Update("client_uuid", newUUID).Error
}

// UpdateCounters narrow-updates the lifetime + last-raw fields for one
// ownership row. Driven by the traffic poll once per cycle per client; using
// Updates(...) with an explicit column list keeps the write tight so the
// poll loop's N client updates don't rewrite untouched columns.
//
// Refuses to run with a zero ID so a caller that forgot to load the row
// from the repo first doesn't get a silent no-op (Where("id = 0") matches
// nothing, returns no error, counters quietly evaporate).
func (r *ownershipRepo) UpdateCounters(ctx context.Context, e *domain.XUIClientEntry) error {
	if e == nil || e.ID == 0 {
		return fmt.Errorf("ownership UpdateCounters requires a non-zero ID; got %+v", e)
	}
	return r.db.WithContext(ctx).
		Model(&ownershipRow{}).
		Where("id = ?", e.ID).
		Updates(map[string]any{
			"lifetime_up_bytes":    e.LifetimeUpBytes,
			"lifetime_down_bytes":  e.LifetimeDownBytes,
			"lifetime_total_bytes": e.LifetimeTotalBytes,
			"last_raw_up_bytes":    e.LastRawUpBytes,
			"last_raw_down_bytes":  e.LastRawDownBytes,
			"last_raw_total_bytes": e.LastRawTotalBytes,
		}).Error
}

// BatchUpdateCounters is the per-poll batched form of UpdateCounters: one
// transaction wraps N per-row UPDATEs so SQLite collapses N WAL commits
// (~5–10ms each) into one. Same per-row column scope and zero-ID guard as
// the single-row UpdateCounters — see that method's doc.
//
// Empty input is a no-op so callers don't need to gate the no-clients path.
func (r *ownershipRepo) BatchUpdateCounters(ctx context.Context, items []*domain.XUIClientEntry) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, e := range items {
			if e == nil || e.ID == 0 {
				return fmt.Errorf("ownership BatchUpdateCounters requires a non-zero ID; got %+v", e)
			}
			err := tx.Model(&ownershipRow{}).
				Where("id = ?", e.ID).
				Updates(map[string]any{
					"lifetime_up_bytes":    e.LifetimeUpBytes,
					"lifetime_down_bytes":  e.LifetimeDownBytes,
					"lifetime_total_bytes": e.LifetimeTotalBytes,
					"last_raw_up_bytes":    e.LastRawUpBytes,
					"last_raw_down_bytes":  e.LastRawDownBytes,
					"last_raw_total_bytes": e.LastRawTotalBytes,
				}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ownershipRepo) Exists(ctx context.Context, panelID int64, inboundID int, email string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&ownershipRow{}).
		Where("panel_id = ? AND inbound_id = ? AND client_email = ?", panelID, inboundID, email).
		Count(&n).Error
	return n > 0, err
}
