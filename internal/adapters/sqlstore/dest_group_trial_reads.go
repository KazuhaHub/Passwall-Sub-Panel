package sqlstore

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationGroupTrial(ctx context.Context, q domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
	q, err := domain.NormalizeDestinationGroupTrialQuery(q)
	if err != nil {
		return domain.DestGroupTrialData{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.DestGroupTrialData{}, err
	}
	out := domain.DestGroupTrialData{Items: []domain.DestGroupTrialSite{}, Losses: domain.DestAuditLosses{Scope: "panel"}}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := readGroupTrialSites(tx, q, &out); err != nil {
			return err
		}
		if err := readGroupTrialLosses(tx, q, &out.Losses); err != nil {
			return err
		}
		out.DroppedInRange = out.Losses.Rows
		return nil
	}, options)
	if err := ctx.Err(); err != nil {
		return domain.DestGroupTrialData{}, err
	}
	if err != nil {
		return domain.DestGroupTrialData{}, auditStorageError(err)
	}
	return out, nil
}

func groupTrialScope(tx *gorm.DB, q domain.DestGroupTrialQuery) *gorm.DB {
	return hitTimeScope(tx, domain.DestHitQuery{Since: q.Since, Until: q.Until}).
		Where("source = ? AND action = ? AND user_id = 0 AND port = 0", "g"+strconv.FormatInt(q.GroupID, 10), "observe")
}

func readGroupTrialSites(tx *gorm.DB, q domain.DestGroupTrialQuery, out *domain.DestGroupTrialData) error {
	rows, err := groupTrialScope(tx, q).Select("dest", "count", "last_at").Order("dest").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	// The API needs every site. Stream in site order so the output itself is
	// the only collection; do not retain raw hits, accounts or another key map.
	// Supplied trial sites were validated on ingestion. Recomputing PSL here
	// could merge a different site's data after a suffix-list version change.
	for rows.Next() {
		var site string
		var count int64
		var last time.Time
		if err := rows.Scan(&site, &count, &last); err != nil {
			return err
		}
		if site == "" || count <= 0 || last.IsZero() || last.UnixMilli() <= 0 {
			return domain.ErrUnavailable
		}
		if len(out.Items) == 0 || out.Items[len(out.Items)-1].Site != site {
			out.Items = append(out.Items, domain.DestGroupTrialSite{Site: site})
		}
		item := &out.Items[len(out.Items)-1]
		item.Count = auditReadAdd(item.Count, count)
		item.LastAt = max(item.LastAt, last.UnixMilli())
		out.TotalCount = auditReadAdd(out.TotalCount, count)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	slices.SortFunc(out.Items, func(a, b domain.DestGroupTrialSite) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(b.LastAt, a.LastAt), cmp.Compare(a.Site, b.Site))
	})
	return nil
}

func readGroupTrialLosses(tx *gorm.DB, q domain.DestGroupTrialQuery, out *domain.DestAuditLosses) error {
	query := tx.Model(&destAuditLossHourlyRow{}).
		Where("observed_hour_ms >= ? AND observed_hour_ms < ? AND kind = ?", q.Since.Truncate(time.Hour).UnixMilli(), q.Until.UnixMilli(), "trial")
	// Loss counters have panel scope only. Include current member panels and
	// historical panels for this group, including after membership changes.
	query, err := hitSourceLossScope(tx, query, domain.DestHitQuery{Since: q.Since, Until: q.Until, Source: "g" + strconv.FormatInt(q.GroupID, 10)})
	if err != nil {
		return err
	}
	rows, err := query.Select("rows", "events", "unmatched").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lost, events, unmatched int64
		if err := rows.Scan(&lost, &events, &unmatched); err != nil {
			return err
		}
		if lost < 0 || events < 0 || unmatched < 0 {
			return domain.ErrUnavailable
		}
		out.Rows = auditReadAdd(out.Rows, lost)
		out.Events = auditReadAdd(out.Events, events)
		out.Unmatched = auditReadAdd(out.Unmatched, unmatched)
	}
	return rows.Err()
}

var _ ports.DestGroupTrialReadRepo = (*DestAuditRepo)(nil)
