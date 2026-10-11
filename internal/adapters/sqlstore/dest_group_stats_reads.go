package sqlstore

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationGroupStats(ctx context.Context, q domain.DestGroupStatsQuery) (map[int64]domain.DestGroupStats, error) {
	q, err := domain.NormalizeDestinationGroupStatsQuery(q)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[int64]domain.DestGroupStats, len(q.GroupIDs))
	for _, id := range q.GroupIDs {
		out[id] = domain.DestGroupStats{}
	}
	if len(q.GroupIDs) == 0 {
		return out, nil
	}
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(q.GroupIDs); start += 512 {
			sources := make([]string, 0, min(512, len(q.GroupIDs)-start))
			for _, id := range q.GroupIDs[start:min(start+512, len(q.GroupIDs))] {
				sources = append(sources, "g"+strconv.FormatInt(id, 10))
			}
			if err := readGroupTrialCounters(tx, q, sources, out); err != nil {
				return err
			}
			if err := readGroupDeniedCounters(tx, q, sources, out); err != nil {
				return err
			}
		}
		return nil
	}, options)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, auditStorageError(err)
	}
	return out, nil
}

func readGroupTrialCounters(tx *gorm.DB, q domain.DestGroupStatsQuery, sources []string, out map[int64]domain.DestGroupStats) error {
	query := hitTimeScope(tx, domain.DestHitQuery{Since: q.TrialSince, Until: q.Until}).
		Where("source IN ? AND action = ? AND user_id = 0 AND port = 0", sources, "observe")
	order := "source COLLATE BINARY, dest COLLATE BINARY"
	switch tx.Dialector.Name() {
	case "mysql":
		order = "BINARY source, BINARY dest"
	case "postgres":
		order = `source COLLATE "C", dest COLLATE "C"`
	}
	rows, err := query.Select("source", "dest", "count").Order(order).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	var lastSource, lastSite string
	for rows.Next() {
		var source, site string
		var count int64
		if err := rows.Scan(&source, &site, &count); err != nil {
			return err
		}
		kind, id, ok := hitSourceID(source)
		value, requested := out[id]
		if !ok || kind != 'g' || !requested || site == "" || count <= 0 {
			return domain.ErrUnavailable
		}
		// Storage order makes each supplied site contiguous across all hours
		// and panels. Keep only the previous key, not raw hits or a site map.
		if source != lastSource || site != lastSite {
			value.TrialSites = auditReadAdd(value.TrialSites, 1)
		}
		value.TrialCount = auditReadAdd(value.TrialCount, count)
		out[id] = value
		lastSource, lastSite = source, site
	}
	return rows.Err()
}

func readGroupDeniedCounters(tx *gorm.DB, q domain.DestGroupStatsQuery, sources []string, out map[int64]domain.DestGroupStats) error {
	query := hitTimeScope(tx, domain.DestHitQuery{Since: q.DeniedSince, Until: q.Until}).Where("source IN ? AND action = ?", sources, "block")
	rows, err := query.Select("source", "count").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			return err
		}
		kind, id, ok := hitSourceID(source)
		value, requested := out[id]
		if !ok || kind != 'g' || !requested || count <= 0 {
			return domain.ErrUnavailable
		}
		value.DeniedCount = auditReadAdd(value.DeniedCount, count)
		out[id] = value
	}
	return rows.Err()
}

var _ ports.DestGroupStatsReadRepo = (*DestAuditRepo)(nil)
