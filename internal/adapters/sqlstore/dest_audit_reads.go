package sqlstore

import (
	"context"
	"database/sql"
	"math"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationAuditPanelStats(ctx context.Context, since, until time.Time, panelIDs []int64) (map[int64]domain.DestAuditPanelStats, error) {
	if since.IsZero() || since.UnixMilli() <= 0 || !until.After(since) || until.Sub(since) > 31*24*time.Hour {
		return nil, domain.ErrValidation
	}
	for _, id := range panelIDs {
		if id <= 0 {
			return nil, domain.ErrValidation
		}
	}
	ids := slices.Clone(panelIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make(map[int64]domain.DestAuditPanelStats, len(ids))
	for _, id := range ids {
		out[id] = domain.DestAuditPanelStats{Losses: domain.DestAuditLosses{Scope: "panel"}}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	// Both tables and every panel chunk belong to one read snapshot. Read
	// only counters: no host, subject or rule is loaded. A count histogram
	// reduces transfer without an overflowing or floating-point SQL SUM.
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(ids); start += 512 {
			chunk := ids[start:min(start+512, len(ids))]
			if err := readAuditPanelHits(tx, since, until, chunk, out); err != nil {
				return err
			}
			if err := readAuditPanelLosses(tx, since, until, chunk, out); err != nil {
				return err
			}
		}
		return nil
	}, options)
	if err != nil {
		return nil, auditStorageError(err)
	}
	return out, nil
}

func readAuditPanelHits(tx *gorm.DB, since, until time.Time, ids []int64, out map[int64]domain.DestAuditPanelStats) error {
	rows, err := tx.Model(&destHitRow{}).Select("panel_id, count, COUNT(*) AS key_count").
		Where("panel_id IN ? AND hour_ms >= ? AND hour_ms < ?", ids, since.UTC().Truncate(time.Hour).UnixMilli(), until.UnixMilli()).Group("panel_id, count").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, count, keys int64
		if err := rows.Scan(&id, &count, &keys); err != nil {
			return err
		}
		if count < 0 || keys < 1 {
			return domain.ErrUnavailable
		}
		v := out[id]
		if count > 0 && keys > math.MaxInt64/count {
			v.Hits = math.MaxInt64
		} else {
			v.Hits = auditReadAdd(v.Hits, count*keys)
		}
		out[id] = v
	}
	return rows.Err()
}

func readAuditPanelLosses(tx *gorm.DB, since, until time.Time, ids []int64, out map[int64]domain.DestAuditPanelStats) error {
	rows, err := tx.Model(&destAuditLossHourlyRow{}).Select("panel_id", "rows", "events", "unmatched").
		Where("panel_id IN ? AND observed_hour_ms >= ? AND observed_hour_ms < ? AND kind IN ?", ids, since.UTC().Truncate(time.Hour).UnixMilli(), until.UnixMilli(), []string{"block", "observe", "trial", "usage"}).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, lost, events, unmatched int64
		if err := rows.Scan(&id, &lost, &events, &unmatched); err != nil {
			return err
		}
		if lost < 0 || events < 0 || unmatched < 0 {
			return domain.ErrUnavailable
		}
		v := out[id]
		v.Losses.Rows = auditReadAdd(v.Losses.Rows, lost)
		v.Losses.Events = auditReadAdd(v.Losses.Events, events)
		v.Losses.Unmatched = auditReadAdd(v.Losses.Unmatched, unmatched)
		out[id] = v
	}
	return rows.Err()
}

func auditReadAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

var _ ports.DestAuditReadRepo = (*DestAuditRepo)(nil)
