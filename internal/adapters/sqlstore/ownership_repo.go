package sqlstore

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

// DistinctUserIDs returns every user_id that still has at least one legacy
// ownership row — i.e. the set of users not yet migrated to the shared-client
// model. The V3 migration sweep enqueues a user_migrate task per returned id;
// an empty result means the migration is complete. V3-transitional.
func (r *ownershipRepo) DistinctUserIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	if err := r.db.WithContext(ctx).Model(&ownershipRow{}).
		Distinct().Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
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

// ListByUsers fetches every ownership row whose user_id is in the input
// list, in ONE SQL roundtrip, and buckets by user_id. PollOnce uses it
// once per cycle instead of N per-user SELECTs.
//
// Absolute win is modest on a localhost SQLite / MySQL / Postgres (where
// per-query overhead is sub-ms anyway), more visible on remote DBs where
// each round-trip carries 5–30ms of network latency. Same cross-dialect
// shape as the other v3.5.0-beta.9/15 batch reads — GORM's `IN ?` clause
// expands portably across all three backends.
//
// Users with no ownership rows are absent from the returned map (not
// nil-valued, not zero-length); an empty input returns an empty non-nil
// map so callers don't need a guard.
func (r *ownershipRepo) ListByUsers(ctx context.Context, userIDs []int64) (map[int64][]*domain.XUIClientEntry, error) {
	if len(userIDs) == 0 {
		return map[int64][]*domain.XUIClientEntry{}, nil
	}
	var rows []ownershipRow
	if err := r.db.WithContext(ctx).
		Where("user_id IN ?", userIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64][]*domain.XUIClientEntry, len(userIDs))
	for i := range rows {
		d := rows[i].toDomain()
		out[d.UserID] = append(out[d.UserID], d)
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

// ownershipCounterColumns is the explicit column set the traffic poll
// rewrites per client per cycle: lifetime + last-raw, plus the per-client
// period baselines. Listing columns explicitly (rather than a full-row Save)
// keeps the write tight so the poll's N updates don't touch identity columns.
//
// The period baselines are only mutated by the poll at a user's period
// rollover; on every other cycle the value here equals what was loaded, so
// including them is an idempotent rewrite (no behaviour change for the common
// path) while letting the rollover's new baseline land on the same batched
// write the lifetime delta already rides.
func ownershipCounterColumns(e *domain.XUIClientEntry) map[string]any {
	return map[string]any{
		"lifetime_up_bytes":    e.LifetimeUpBytes,
		"lifetime_down_bytes":  e.LifetimeDownBytes,
		"lifetime_total_bytes": e.LifetimeTotalBytes,
		"last_raw_up_bytes":    e.LastRawUpBytes,
		"last_raw_down_bytes":  e.LastRawDownBytes,
		"last_raw_total_bytes": e.LastRawTotalBytes,

		"period_baseline_up_bytes":    e.PeriodBaselineUpBytes,
		"period_baseline_down_bytes":  e.PeriodBaselineDownBytes,
		"period_baseline_total_bytes": e.PeriodBaselineTotalBytes,
	}
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
		Updates(ownershipCounterColumns(e)).Error
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
				Updates(ownershipCounterColumns(e)).Error
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
