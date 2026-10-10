package sqlstore

import (
	"cmp"
	"container/heap"
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationUsage(ctx context.Context, q domain.DestUsageQuery) (domain.DestUsagePage, error) {
	q, err := domain.NormalizeDestinationUsageQuery(q)
	if err != nil {
		return domain.DestUsagePage{}, err
	}
	out := domain.DestUsagePage{Items: []domain.DestUsageSite{}, Losses: domain.DestAuditLosses{Scope: "panel"}}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := readUsageSites(tx, q, &out); err != nil {
			return err
		}
		return readUsageLosses(tx, q, &out.Losses)
	}, options)
	if err != nil {
		return domain.DestUsagePage{}, auditStorageError(err)
	}
	return out, nil
}

func usageScope(tx *gorm.DB, q domain.DestUsageQuery) *gorm.DB {
	query := tx.Model(&destUsageHourlyRow{}).Where("user_id = ? AND hour_ms >= ? AND hour_ms < ?", q.UserID, q.Since.Truncate(time.Hour).UnixMilli(), q.Until.UnixMilli())
	if q.PanelID > 0 {
		query = query.Where("panel_id = ?", q.PanelID)
	}
	return query
}

// Stream sites in storage order, saturate counts without SQL SUM overflow,
// and retain only the requested top sites. Memory does not grow with history.
func readUsageSites(tx *gorm.DB, q domain.DestUsageQuery, out *domain.DestUsagePage) error {
	rows, err := usageScope(tx, q).Select("site", "count").Order("site").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	top := usageTop{}
	var current domain.DestUsageSite
	flush := func() {
		if current.Site == "" {
			return
		}
		out.TotalSites = auditReadAdd(out.TotalSites, 1)
		out.TotalCount = auditReadAdd(out.TotalCount, current.Count)
		if len(top) < q.Limit {
			heap.Push(&top, current)
		} else if usageBetter(current, top[0]) {
			top[0] = current
			heap.Fix(&top, 0)
		}
	}
	for rows.Next() {
		var site string
		var count int64
		if err := rows.Scan(&site, &count); err != nil {
			return err
		}
		if site == "" || count <= 0 {
			return domain.ErrUnavailable
		}
		if site != current.Site {
			flush()
			current = domain.DestUsageSite{Site: site}
		}
		current.Count = auditReadAdd(current.Count, count)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	flush()
	slices.SortFunc(top, func(a, b domain.DestUsageSite) int {
		if n := cmp.Compare(b.Count, a.Count); n != 0 {
			return n
		}
		return cmp.Compare(a.Site, b.Site)
	})
	out.Items = append(out.Items, top...)
	return nil
}

type usageTop []domain.DestUsageSite

func (h usageTop) Len() int           { return len(h) }
func (h usageTop) Less(i, j int) bool { return usageBetter(h[j], h[i]) }
func (h usageTop) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *usageTop) Push(v any)        { *h = append(*h, v.(domain.DestUsageSite)) }
func (h *usageTop) Pop() any          { old := *h; v := old[len(old)-1]; *h = old[:len(old)-1]; return v }
func usageBetter(a, b domain.DestUsageSite) bool {
	return a.Count > b.Count || a.Count == b.Count && a.Site < b.Site
}

func readUsageLosses(tx *gorm.DB, q domain.DestUsageQuery, out *domain.DestAuditLosses) error {
	query := tx.Model(&destAuditLossHourlyRow{}).Where("observed_hour_ms >= ? AND observed_hour_ms < ? AND kind = ?", q.Since.Truncate(time.Hour).UnixMilli(), q.Until.UnixMilli(), "usage")
	if q.PanelID > 0 {
		query = query.Where("panel_id = ?", q.PanelID)
	} else {
		current := tx.Model(&pspClientRow{}).Select("panel_id").Where("user_id = ?", q.UserID)
		history := usageScope(tx, q).Select("panel_id")
		query = query.Where("(panel_id IN (?) OR panel_id IN (?))", current, history)
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

var _ ports.DestUsageReadRepo = (*DestAuditRepo)(nil)
