package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Write-path sizes. A transaction holds at most destHitTxRows rows and a
// statement at most destHitStmtRows: a worst-case batch (4096 rows) is a few
// short transactions, so one slow chunk cannot hold the write lock (SQLite has
// one writer) for the whole batch, and a statement stays far under every
// dialect's bound-parameter limit.
const (
	destHitTxRows   = 1000
	destHitStmtRows = 200
)

// DestHitRepo is the dest_hits / dest_audit_batches / dest_audit_drops store.
type DestHitRepo struct{ db *gorm.DB }

// destHitConflict is the upsert's conflict action: add the count, keep the
// earliest first and the latest last.
//
// THE FIVE-ARGUMENT SPELLING DIFFERS BY DIALECT. PostgreSQL has LEAST and
// GREATEST; MySQL has them too but reads the incoming value as VALUES(col)
// rather than EXCLUDED.col; SQLite has neither and spells the two-argument
// scalar min/max as MIN(a, b) and MAX(a, b). The shape follows
// service/rollup's onConflictClause.
func destHitConflict(dialect string) clause.OnConflict {
	cols := []clause.Column{
		{Name: "hour_ms"}, {Name: "panel_id"}, {Name: "user_id"}, {Name: "source"},
		{Name: "action"}, {Name: "dest"}, {Name: "port"},
	}
	col := func(name string) clause.Column { return clause.Column{Name: name} }
	switch dialect {
	case "mysql":
		return clause.OnConflict{Columns: cols, DoUpdates: clause.Set{
			{Column: col("count"), Value: clause.Expr{SQL: "? + VALUES(?)", Vars: []any{col("count"), col("count")}}},
			{Column: col("first_ms"), Value: clause.Expr{SQL: "LEAST(?, VALUES(?))", Vars: []any{col("first_ms"), col("first_ms")}}},
			{Column: col("last_ms"), Value: clause.Expr{SQL: "GREATEST(?, VALUES(?))", Vars: []any{col("last_ms"), col("last_ms")}}},
		}}
	case "sqlite":
		return clause.OnConflict{Columns: cols, DoUpdates: clause.Set{
			{Column: col("count"), Value: clause.Expr{SQL: "? + ?", Vars: []any{clause.Column{Table: "dest_hits", Name: "count"}, clause.Column{Table: "excluded", Name: "count"}}}},
			{Column: col("first_ms"), Value: clause.Expr{SQL: "MIN(?, ?)", Vars: []any{clause.Column{Table: "dest_hits", Name: "first_ms"}, clause.Column{Table: "excluded", Name: "first_ms"}}}},
			{Column: col("last_ms"), Value: clause.Expr{SQL: "MAX(?, ?)", Vars: []any{clause.Column{Table: "dest_hits", Name: "last_ms"}, clause.Column{Table: "excluded", Name: "last_ms"}}}},
		}}
	}
	// PostgreSQL.
	return clause.OnConflict{Columns: cols, DoUpdates: clause.Set{
		{Column: col("count"), Value: clause.Expr{SQL: "? + ?", Vars: []any{clause.Column{Table: "dest_hits", Name: "count"}, clause.Column{Table: "excluded", Name: "count"}}}},
		{Column: col("first_ms"), Value: clause.Expr{SQL: "LEAST(?, ?)", Vars: []any{clause.Column{Table: "dest_hits", Name: "first_ms"}, clause.Column{Table: "excluded", Name: "first_ms"}}}},
		{Column: col("last_ms"), Value: clause.Expr{SQL: "GREATEST(?, ?)", Vars: []any{clause.Column{Table: "dest_hits", Name: "last_ms"}, clause.Column{Table: "excluded", Name: "last_ms"}}}},
	}}
}

func destHitRowOf(r domain.DestHit) destHitRow {
	return destHitRow{
		HourMS: r.HourMS, PanelID: r.PanelID, UserID: r.UserID, Source: r.Source, Action: r.Action,
		Dest: r.Dest, Port: r.Port, Count: r.Count, FirstMS: r.FirstMS, LastMS: r.LastMS,
	}
}

// StoreBatch stores a batch of hits (no usage): see StoreBatchWithUsage.
func (r *DestHitRepo) StoreBatch(ctx context.Context, agentID, batchID string, receivedAt time.Time, rows []domain.DestHit) (domain.DestBatchResult, error) {
	return r.StoreBatchWithUsage(ctx, agentID, batchID, receivedAt, rows, nil)
}

// destUsageConflict adds the incoming count to the stored one. The incoming
// value is spelled EXCLUDED.col on PostgreSQL and SQLite and VALUES(col) on MySQL.
func destUsageConflict(dialect string) clause.OnConflict {
	cols := []clause.Column{{Name: "hour_ms"}, {Name: "panel_id"}, {Name: "user_id"}, {Name: "site"}}
	col := clause.Column{Name: "count"}
	var add clause.Expr
	if dialect == "mysql" {
		add = clause.Expr{SQL: "? + VALUES(?)", Vars: []any{col, col}}
	} else {
		add = clause.Expr{SQL: "? + ?", Vars: []any{clause.Column{Table: "dest_usage_hourly", Name: "count"}, clause.Column{Table: "excluded", Name: "count"}}}
	}
	return clause.OnConflict{Columns: cols, DoUpdates: clause.Set{{Column: col, Value: add}}}
}

// StoreBatchWithUsage stores one reported batch: the batch marker, the hit rows
// and the per-site usage rows, all under the one marker.
//
// The FIRST transaction inserts the marker with ON CONFLICT DO NOTHING and
// reads RowsAffected: zero means the batch was stored before, and nothing else
// is written (neither the hits nor the usage: the marker is the unit of
// de-duplication for both). It deliberately does not rely on catching a
// duplicate-key error, which on PostgreSQL aborts the whole transaction. The
// marker and the first chunk of hits and of usage share that transaction, so a
// failure there leaves no marker behind. A LATER chunk that fails leaves the
// earlier ones committed and returns the error with Stored/UsageStored showing
// how far it got: a retry of the batch would be refused as a duplicate, and
// that loss is accepted (the node keeps no durable copy either).
//
// Rows are merged by aggregation key first (domain.MergeDestHits,
// domain.MergeDestUsage), then written 1000 of each to a transaction and 200 to
// a statement; chunk i of the hits and chunk i of the usage share a transaction.
func (r *DestHitRepo) StoreBatchWithUsage(ctx context.Context, agentID, batchID string, receivedAt time.Time, rows []domain.DestHit, usage []domain.DestUsage) (domain.DestBatchResult, error) {
	if agentID == "" || len(agentID) > destAgentIDWidth || batchID == "" || len(batchID) > destBatchIDWidth {
		return domain.DestBatchResult{}, fmt.Errorf("%w: a hit batch needs an agent id and a batch id", domain.ErrValidation)
	}
	for _, h := range rows {
		if len(h.Source) > destHitSourceWidth || len(h.Action) > destHitActionWidth || len(h.Dest) > destHitDestWidth {
			return domain.DestBatchResult{}, fmt.Errorf("%w: a hit row is wider than its column", domain.ErrValidation)
		}
	}
	for _, u := range usage {
		if u.Site == "" || len(u.Site) > destUsageSiteWidth {
			return domain.DestBatchResult{}, fmt.Errorf("%w: a usage row's site is 1..%d bytes", domain.ErrValidation, destUsageSiteWidth)
		}
	}
	rows = domain.MergeDestHits(rows)
	usage = domain.MergeDestUsage(usage)
	dialect := r.db.Dialector.Name()
	chunks := max((len(rows)+destHitTxRows-1)/destHitTxRows, (len(usage)+destHitTxRows-1)/destHitTxRows, 1)
	slice := func(n, i int) (int, int) { return min(i*destHitTxRows, n), min((i+1)*destHitTxRows, n) }
	var res domain.DestBatchResult
	for i := 0; i < chunks; i++ {
		hs, he := slice(len(rows), i)
		us, ue := slice(len(usage), i)
		chunk, uchunk := rows[hs:he], usage[us:ue]
		duplicate := false
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if i == 0 {
				marker := destAuditBatchRow{AgentID: agentID, BatchID: batchID, ReceivedAt: receivedAt.UTC()}
				ins := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
				if ins.Error != nil {
					return ins.Error
				}
				if ins.RowsAffected == 0 {
					duplicate = true
					return nil
				}
			}
			if len(chunk) > 0 {
				batch := make([]destHitRow, len(chunk))
				for k, h := range chunk {
					batch[k] = destHitRowOf(h)
				}
				// One statement per destHitStmtRows rows, written as plain Creates in
				// this transaction: CreateInBatches would wrap them in savepoints,
				// which buy nothing here (the transaction is the unit of failure)
				// and which a wrapped dialector may not provide.
				for k := 0; k < len(batch); k += destHitStmtRows {
					j := min(k+destHitStmtRows, len(batch))
					if err := tx.Clauses(destHitConflict(dialect)).Create(batch[k:j]).Error; err != nil {
						return err
					}
				}
			}
			if len(uchunk) > 0 {
				batch := make([]destUsageRow, len(uchunk))
				for k, u := range uchunk {
					batch[k] = destUsageRow{HourMS: u.HourMS, PanelID: u.PanelID, UserID: u.UserID, Site: u.Site, Count: u.Count}
				}
				for k := 0; k < len(batch); k += destHitStmtRows {
					j := min(k+destHitStmtRows, len(batch))
					if err := tx.Clauses(destUsageConflict(dialect)).Create(batch[k:j]).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return res, fmt.Errorf("store dest hits: %w", err)
		}
		if duplicate {
			res.Duplicate = true
			return res, nil
		}
		res.Stored += len(chunk)
		res.UsageStored += len(uchunk)
	}
	return res, nil
}

// AddDrops adds n to the count of reported rows that were not stored, for one
// hour and panel. A zero or negative n is a no-op.
func (r *DestHitRepo) AddDrops(ctx context.Context, hourMS, panelID int64, kind string, n int64) error {
	if n <= 0 {
		return nil
	}
	if kind == "" || len(kind) > destDropKindWidth {
		return fmt.Errorf("%w: a drop kind is 1..%d bytes", domain.ErrValidation, destDropKindWidth)
	}
	add := clause.Expr{SQL: "? + ?", Vars: []any{clause.Column{Table: "dest_audit_drops", Name: "count"}, clause.Column{Table: "excluded", Name: "count"}}}
	if r.db.Dialector.Name() == "mysql" {
		add = clause.Expr{SQL: "? + VALUES(?)", Vars: []any{clause.Column{Name: "count"}, clause.Column{Name: "count"}}}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "hour_ms"}, {Name: "panel_id"}, {Name: "kind"}},
		DoUpdates: clause.Set{{Column: clause.Column{Name: "count"}, Value: add}},
	}).Create(&destAuditDropRow{HourMS: hourMS, PanelID: panelID, Kind: kind, Count: n}).Error
}

// DroppedInRange is how many reported rows are missing from [sinceMS, untilMS):
// the node's own overflow of block-hit rows, the panel's budget and queue
// drops and failed writes. The allowlist trial overflow is NOT included: it is
// expected on a busy node and is not a gap in the record of what was blocked,
// and neither are the usage kinds (see UsageDropped).
func (r *DestHitRepo) DroppedInRange(ctx context.Context, sinceMS, untilMS int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&destAuditDropRow{}).Where("kind NOT IN ?", append([]string{domain.DestDropNodeTrial}, domain.DestUsageDropKinds()...))
	if sinceMS > 0 {
		q = q.Where("hour_ms >= ?", sinceMS)
	}
	if untilMS > 0 {
		q = q.Where("hour_ms < ?", untilMS)
	}
	var total *int64
	if err := q.Select("SUM(count)").Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum dest drops: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// destHitScope applies the filter's selection columns (not its paging).
func destHitScope(q *gorm.DB, f domain.DestHitFilter, withSourceAction bool) *gorm.DB {
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.PanelID > 0 {
		q = q.Where("panel_id = ?", f.PanelID)
	}
	if f.SinceMS > 0 {
		q = q.Where("hour_ms >= ?", f.SinceMS)
	}
	if f.UntilMS > 0 {
		q = q.Where("hour_ms < ?", f.UntilMS)
	}
	if withSourceAction {
		if f.Source != "" {
			q = q.Where("source = ?", f.Source)
		}
		if f.Action != "" {
			q = q.Where("action = ?", f.Action)
		}
		switch f.SourceKind {
		case "policy":
			q = q.Where("source LIKE 'p%'")
		case "source":
			q = q.Where("source LIKE 's%'")
		case "group":
			q = q.Where("source LIKE 'g%'")
		}
		if !f.IncludeTrial && f.Source == "" && f.SourceKind != "group" {
			q = q.Where("NOT (source LIKE 'g%' AND action = 'observe')")
		}
	}
	if like := keywordLike(f.Keyword); like != "" && withSourceAction {
		q = q.Where(likeCols("dest"), like)
	}
	return q
}

// List pages the hit rows a filter selects, newest hour first, or grouped.
// Group "site" folds hours, users and ports away (one row per destination),
// "user" one row per user and "policy" one per source. A listing never holds an
// address of a client: there is none to hold.
func (r *DestHitRepo) List(ctx context.Context, f domain.DestHitFilter) ([]domain.DestHitRow, int64, error) {
	base := destHitScope(r.db.WithContext(ctx).Model(&destHitRow{}), f, true)
	var (
		sel     string
		groupBy string
		order   string
	)
	switch f.GroupBy {
	case "site":
		sel, groupBy, order = "dest, action, SUM(count) AS count, MIN(first_ms) AS first_ms, MAX(last_ms) AS last_ms", "dest, action", "count DESC, dest ASC, action ASC"
	case "user":
		sel, groupBy, order = "user_id, action, SUM(count) AS count, MIN(first_ms) AS first_ms, MAX(last_ms) AS last_ms", "user_id, action", "count DESC, user_id ASC, action ASC"
	case "policy":
		sel, groupBy, order = "source, action, SUM(count) AS count, MIN(first_ms) AS first_ms, MAX(last_ms) AS last_ms", "source, action", "count DESC, source ASC, action ASC"
	case "", "none":
		sel, order = "hour_ms, panel_id, user_id, source, action, dest, port, count, first_ms, last_ms", "hour_ms DESC, last_ms DESC, source ASC, user_id ASC, dest ASC, port ASC"
	default:
		return nil, 0, fmt.Errorf("%w: group_by is none, site, user or policy", domain.ErrValidation)
	}
	var total int64
	if groupBy == "" {
		if err := base.Count(&total).Error; err != nil {
			return nil, 0, fmt.Errorf("count dest hits: %w", err)
		}
	} else {
		var n *int64
		sub := base.Session(&gorm.Session{}).Select("1").Group(groupBy)
		if err := r.db.WithContext(ctx).Table("(?) AS g", sub).Select("COUNT(*)").Scan(&n).Error; err != nil {
			return nil, 0, fmt.Errorf("count grouped dest hits: %w", err)
		}
		if n != nil {
			total = *n
		}
	}
	size := f.PageSize
	if size <= 0 || size > 200 {
		size = 50
	}
	page := max(f.Page, 1)
	q := base.Session(&gorm.Session{}).Select(sel)
	if groupBy != "" {
		q = q.Group(groupBy)
	}
	var rows []destHitRow
	if err := q.Order(order).Limit(size).Offset((page - 1) * size).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list dest hits: %w", err)
	}
	out := make([]domain.DestHitRow, len(rows))
	for i, h := range rows {
		out[i] = domain.DestHitRow{HourMS: h.HourMS, PanelID: h.PanelID, UserID: h.UserID, Source: h.Source, Action: h.Action,
			Dest: h.Dest, Port: h.Port, Count: h.Count, FirstMS: h.FirstMS, LastMS: h.LastMS}
	}
	return out, total, nil
}

// destHitSummaryScope is the selection a summary counts: time, account, node
// and the exact source, but not action or source kind.
func destHitSummaryScope(q *gorm.DB, f domain.DestHitFilter) *gorm.DB {
	q = destHitScope(q, f, false)
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	return q
}

// Summary counts a selection by what happened. It ignores the filter's action
// and source kind (the cards above a listing count the whole selection, as the
// risk center's metric cards count the whole queue) but HONOURS its exact
// source: P7 lists only action and source_kind among what it ignores, and the
// convert-to-block dialog (S4) reads "accounts involved" from summary.users of a
// request made with source=p<id>, so a source-scoped request must give a
// source-scoped summary. Allowlist trial rows are separate from both counts: a
// group's observe row is a would-deny of a whole group, not an account's
// would-block.
func (r *DestHitRepo) Summary(ctx context.Context, f domain.DestHitFilter) (domain.DestHitSummary, error) {
	type agg struct {
		Block   *int64
		Deny    *int64
		Observe *int64
	}
	var a agg
	q := destHitSummaryScope(r.db.WithContext(ctx).Model(&destHitRow{}), f)
	if err := q.Select(
		"SUM(CASE WHEN action = 'block' AND source NOT LIKE 'g%' THEN count ELSE 0 END) AS block, " +
			"SUM(CASE WHEN action = 'block' AND source LIKE 'g%' THEN count ELSE 0 END) AS deny, " +
			"SUM(CASE WHEN action = 'observe' AND source NOT LIKE 'g%' THEN count ELSE 0 END) AS observe").Scan(&a).Error; err != nil {
		return domain.DestHitSummary{}, fmt.Errorf("summarise dest hits: %w", err)
	}
	var users int64
	if err := destHitSummaryScope(r.db.WithContext(ctx).Model(&destHitRow{}), f).
		Where("user_id > 0").Distinct("user_id").Count(&users).Error; err != nil {
		return domain.DestHitSummary{}, fmt.Errorf("count dest hit users: %w", err)
	}
	out := domain.DestHitSummary{Users: users}
	if a.Block != nil {
		out.Block = *a.Block
	}
	if a.Deny != nil {
		out.Deny = *a.Deny
	}
	if a.Observe != nil {
		out.Observe = *a.Observe
	}
	return out, nil
}

// Sources lists the sources that have rows in a time range, sorted.
func (r *DestHitRepo) Sources(ctx context.Context, sinceMS, untilMS int64) ([]string, error) {
	var out []string
	q := r.db.WithContext(ctx).Model(&destHitRow{})
	if sinceMS > 0 {
		q = q.Where("hour_ms >= ?", sinceMS)
	}
	if untilMS > 0 {
		q = q.Where("hour_ms < ?", untilMS)
	}
	if err := q.Distinct().Order("source").Pluck("source", &out).Error; err != nil {
		return nil, fmt.Errorf("list dest hit sources: %w", err)
	}
	return out, nil
}

// SourceCounts totals the hits per (source, action) from sinceMS on, with the
// latest hit's instant. It is what a policy's "hits recently" and "would have
// blocked" come from.
func (r *DestHitRepo) SourceCounts(ctx context.Context, sinceMS int64) ([]domain.DestSourceCount, error) {
	var rows []struct {
		Source string
		Action string
		N      int64
		Last   int64
	}
	if err := r.db.WithContext(ctx).Model(&destHitRow{}).Where("hour_ms >= ?", sinceMS).
		Select("source, action, SUM(count) AS n, MAX(last_ms) AS last").Group("source, action").Order("source, action").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count dest hits by source: %w", err)
	}
	out := make([]domain.DestSourceCount, len(rows))
	for i, r := range rows {
		out[i] = domain.DestSourceCount{Source: r.Source, Action: r.Action, Count: r.N, LastMS: r.Last}
	}
	return out, nil
}

// LastHits is the latest hit's instant, per listed source, from sinceMS on. It
// is bounded on both axes so it never scans the table: the sources are the ones
// the caller is about to show, and the window is the retained one, which is
// what idx_dest_hit_source (source, hour_ms) serves. A source with no row in
// the window is absent from the result.
func (r *DestHitRepo) LastHits(ctx context.Context, sinceMS int64, sources []string) (map[string]int64, error) {
	out := make(map[string]int64, len(sources))
	// Chunked so the statement stays far under any dialect's bind-variable cap.
	const chunk = 500
	for lo := 0; lo < len(sources); lo += chunk {
		part := sources[lo:min(lo+chunk, len(sources))]
		var rows []struct {
			Source string
			Last   int64
		}
		if err := r.db.WithContext(ctx).Model(&destHitRow{}).Where("source IN ? AND hour_ms >= ?", part, sinceMS).
			Select("source, MAX(last_ms) AS last").Group("source").Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("last dest hit by source: %w", err)
		}
		for _, row := range rows {
			out[row.Source] = row.Last
		}
	}
	return out, nil
}

// PanelCounts is the number of hits per panel from sinceMS on, block and
// observe together, allowlist trial rows left out.
func (r *DestHitRepo) PanelCounts(ctx context.Context, sinceMS int64) (map[int64]int64, error) {
	var rows []struct {
		PanelID int64
		N       int64
	}
	if err := r.db.WithContext(ctx).Model(&destHitRow{}).Where("hour_ms >= ?", sinceMS).
		Where("NOT (source LIKE 'g%' AND action = 'observe')").
		Select("panel_id, SUM(count) AS n").Group("panel_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count dest hits by panel: %w", err)
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.PanelID] = r.N
	}
	return out, nil
}

// UserRecent is one user's hits since sinceMS grouped by (source, action):
// the count, the latest instant, and up to three of the destinations and
// panels, most-hit first, for the account drawer.
func (r *DestHitRepo) UserRecent(ctx context.Context, userID, sinceMS int64) ([]domain.DestUserSourceHits, error) {
	var rows []destHitRow
	if err := r.db.WithContext(ctx).Where("user_id = ? AND hour_ms >= ?", userID, sinceMS).
		Order("count DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("recent dest hits of a user: %w", err)
	}
	type key struct{ source, action string }
	idx := map[key]int{}
	var out []domain.DestUserSourceHits
	for _, h := range rows {
		k := key{h.Source, h.Action}
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, domain.DestUserSourceHits{Source: h.Source, Action: h.Action, PanelID: h.PanelID, Dest: h.Dest})
			i = len(out) - 1
		}
		out[i].Count += h.Count
		out[i].LastMS = max(out[i].LastMS, h.LastMS)
	}
	slices.SortFunc(out, func(a, b domain.DestUserSourceHits) int {
		if a.Count != b.Count {
			return int(b.Count - a.Count)
		}
		return strings.Compare(a.Source, b.Source)
	})
	return out, nil
}

// UserDests returns one user's most-hit destinations and panels for a source
// and action since sinceMS, at most limit of each, most-hit first.
func (r *DestHitRepo) UserDests(ctx context.Context, userID, sinceMS int64, source, action string, limit int) (dests []string, panels []int64, err error) {
	var rows []destHitRow
	if err = r.db.WithContext(ctx).Where("user_id = ? AND hour_ms >= ? AND source = ? AND action = ?", userID, sinceMS, source, action).
		Order("count DESC, dest ASC").Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("dest hits of a user's source: %w", err)
	}
	seenDest, seenPanel := map[string]bool{}, map[int64]bool{}
	for _, h := range rows {
		if !seenDest[h.Dest] && len(dests) < limit {
			seenDest[h.Dest] = true
			dests = append(dests, h.Dest)
		}
		if !seenPanel[h.PanelID] {
			seenPanel[h.PanelID] = true
			panels = append(panels, h.PanelID)
		}
	}
	return dests, panels, nil
}

// BlockCounts is how many BLOCK hits one user has per source since sinceMS.
// Report-only (observe) rows are never in it: a connection that was allowed is
// not a blocked one, and the risk signal that reads this counts blocks only.
func (r *DestHitRepo) BlockCounts(ctx context.Context, userID, sinceMS int64) (map[string]int64, error) {
	var rows []struct {
		Source string
		N      int64
	}
	if err := r.db.WithContext(ctx).Model(&destHitRow{}).
		Where("user_id = ? AND hour_ms >= ? AND action = ?", userID, sinceMS, domain.DestHitBlock).
		Select("source, SUM(count) AS n").Group("source").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count dest block hits: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Source] = r.N
	}
	return out, nil
}

// ExistingUserIDs answers which of ids are accounts that exist. The ingest
// worker asks once per batch, so a hit of a deleted account is dropped instead
// of re-creating a row for somebody who is gone.
func (r *DestHitRepo) ExistingUserIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		var found []int64
		if err := r.db.WithContext(ctx).Table("users").Where("id IN ?", chunk).Pluck("id", &found).Error; err != nil {
			return nil, fmt.Errorf("look up hit users: %w", err)
		}
		for _, id := range found {
			out[id] = true
		}
	}
	return out, nil
}

// BlockCountsSince is every account's BLOCK hits since sinceMS, per source.
// Report-only rows and allowlist trial rows (no account) are never in it.
func (r *DestHitRepo) BlockCountsSince(ctx context.Context, sinceMS int64) (map[int64]map[string]int64, error) {
	var rows []struct {
		UserID int64
		Source string
		N      int64
	}
	if err := r.db.WithContext(ctx).Model(&destHitRow{}).
		Where("user_id > 0 AND hour_ms >= ? AND action = ?", sinceMS, domain.DestHitBlock).
		Select("user_id, source, SUM(count) AS n").Group("user_id, source").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count dest block hits: %w", err)
	}
	out := map[int64]map[string]int64{}
	for _, row := range rows {
		if out[row.UserID] == nil {
			out[row.UserID] = map[string]int64{}
		}
		out[row.UserID][row.Source] = row.N
	}
	return out, nil
}

// ---- retention ----

// PruneHits deletes rows whose hour is before cutoffMS. The caller aligns the
// cutoff to a whole UTC hour, as the traffic rollup's prune does.
func (r *DestHitRepo) PruneHits(ctx context.Context, cutoffMS int64) (int64, error) {
	res := r.db.WithContext(ctx).Where("hour_ms < ?", cutoffMS).Delete(&destHitRow{})
	return res.RowsAffected, res.Error
}

// PruneUsage deletes per-site usage rows whose hour is before cutoffMS (aligned
// to a whole UTC hour by the caller).
func (r *DestHitRepo) PruneUsage(ctx context.Context, cutoffMS int64) (int64, error) {
	res := r.db.WithContext(ctx).Where("hour_ms < ?", cutoffMS).Delete(&destUsageRow{})
	return res.RowsAffected, res.Error
}

// PruneTrial deletes allowlist trial rows (a group's observe) before
// cutoffMS: they have their own, shorter, retention.
func (r *DestHitRepo) PruneTrial(ctx context.Context, cutoffMS int64) (int64, error) {
	res := r.db.WithContext(ctx).Where("hour_ms < ? AND source LIKE 'g%' AND action = ?", cutoffMS, domain.DestHitObserve).Delete(&destHitRow{})
	return res.RowsAffected, res.Error
}

// PruneDrops deletes the drop counters of hours before cutoffMS (they live as
// long as the hits they qualify).
func (r *DestHitRepo) PruneDrops(ctx context.Context, cutoffMS int64) (int64, error) {
	res := r.db.WithContext(ctx).Where("hour_ms < ?", cutoffMS).Delete(&destAuditDropRow{})
	return res.RowsAffected, res.Error
}

// PruneBatches deletes batch markers received before the given instant.
func (r *DestHitRepo) PruneBatches(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("received_at < ?", before.UTC()).Delete(&destAuditBatchRow{})
	return res.RowsAffected, res.Error
}

// PruneOrphans deletes what nothing points at any more: hits of a user that no
// longer exists (user 0, the allowlist trial rows, is not an orphan), hits and
// drop counters of a panel that no longer exists, and the delivery state of an
// agent that no longer exists. Returns the total deleted.
func (r *DestHitRepo) PruneOrphans(ctx context.Context) (int64, error) {
	var total int64
	for _, stmt := range []struct {
		model any
		where string
	}{
		{&destHitRow{}, "user_id > 0 AND user_id NOT IN (SELECT id FROM users)"},
		{&destHitRow{}, "panel_id NOT IN (SELECT id FROM xui_panels)"},
		{&destAuditDropRow{}, "panel_id NOT IN (SELECT id FROM xui_panels)"},
		{&destUsageRow{}, "user_id NOT IN (SELECT id FROM users)"},
		{&destUsageRow{}, "panel_id NOT IN (SELECT id FROM xui_panels)"},
		{&destAgentPolicyRow{}, "agent_id NOT IN (SELECT agent_id FROM node_agents)"},
	} {
		res := r.db.WithContext(ctx).Where(stmt.where).Delete(stmt.model)
		if res.Error != nil {
			return total, fmt.Errorf("prune dest orphans: %w", res.Error)
		}
		total += res.RowsAffected
	}
	return total, nil
}

// TrialSites folds an allowlist group's report-only rows (the rows of source
// that are observe and carry no account) from sinceMS on by destination, most
// seen first and then by name. The destination is what the node recorded: the
// main domain, or "(ip)".
func (r *DestHitRepo) TrialSites(ctx context.Context, source string, sinceMS int64) ([]domain.DestTrialSite, error) {
	var rows []struct {
		Dest string
		N    int64
		Last int64
	}
	if err := r.db.WithContext(ctx).Model(&destHitRow{}).
		Where("source = ? AND action = ? AND user_id = 0 AND hour_ms >= ?", source, domain.DestHitObserve, sinceMS).
		Select("dest, SUM(count) AS n, MAX(last_ms) AS last").Group("dest").Order("n DESC, dest ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("fold dest trial rows: %w", err)
	}
	out := make([]domain.DestTrialSite, len(rows))
	for i, row := range rows {
		out[i] = domain.DestTrialSite{Site: row.Dest, Count: row.N, LastMS: row.Last}
	}
	// Ties are ordered here, by bytes: the dialects' collations disagree on
	// punctuation ("(ip)" sorts first in SQLite and after letters in PostgreSQL).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Site < out[j].Site
	})
	return out, nil
}

// TrialDropped is how many allowlist trial rows the nodes dropped (their
// aggregator was full) in [sinceMS, untilMS); zero bounds are open. It is the
// overflow DroppedInRange leaves out: a report is missing these.
func (r *DestHitRepo) TrialDropped(ctx context.Context, sinceMS, untilMS int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&destAuditDropRow{}).Where("kind = ?", domain.DestDropNodeTrial)
	if sinceMS > 0 {
		q = q.Where("hour_ms >= ?", sinceMS)
	}
	if untilMS > 0 {
		q = q.Where("hour_ms < ?", untilMS)
	}
	var total *int64
	if err := q.Select("SUM(count)").Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum dest trial drops: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// ---- per-site usage (tier B) ----

// usageScope selects one account's usage rows in [sinceMS, untilMS) on one panel
// (0: every panel). Zero bounds are open.
func usageScope(q *gorm.DB, userID, panelID, sinceMS, untilMS int64) *gorm.DB {
	q = q.Where("user_id = ?", userID)
	if panelID > 0 {
		q = q.Where("panel_id = ?", panelID)
	}
	if sinceMS > 0 {
		q = q.Where("hour_ms >= ?", sinceMS)
	}
	if untilMS > 0 {
		q = q.Where("hour_ms < ?", untilMS)
	}
	return q
}

// UserUsage is one account's usage in [sinceMS, untilMS) (a panel of 0 is every
// panel): its limit most-used sites, then what the other sites add up to, so
// the total is never short. Ties are ordered by site, by bytes, because the
// dialects' collations disagree on punctuation ("(ip)").
//
// The database finds the cut and the totals; only the sites above it, and the
// names of those tied exactly at it, come back, so an account with a long tail
// of one-off sites is not loaded whole. The reads share one transaction, so the
// total, the site count and the list cannot disagree the way separate
// statements can when a batch lands between them.
func (r *DestHitRepo) UserUsage(ctx context.Context, userID, panelID, sinceMS, untilMS int64, limit int) (domain.DestUsageSummary, error) {
	out := domain.DestUsageSummary{Items: []domain.DestUsageSite{}}
	if limit < 1 {
		limit = 1
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		grouped := func() *gorm.DB {
			return usageScope(tx.Model(&destUsageRow{}), userID, panelID, sinceMS, untilMS).Select("site, SUM(count) AS n").Group("site")
		}
		var totals struct {
			Sites int64
			Total int64
		}
		if err := tx.Table("(?) AS u", grouped()).Select("COUNT(*) AS sites, COALESCE(SUM(n), 0) AS total").Scan(&totals).Error; err != nil {
			return fmt.Errorf("total dest usage: %w", err)
		}
		out.Total = totals.Total
		if totals.Sites == 0 {
			return nil
		}
		type siteCount struct {
			Site string
			N    int64
		}
		var top []siteCount
		if totals.Sites <= int64(limit) {
			if err := grouped().Scan(&top).Error; err != nil {
				return fmt.Errorf("fold dest usage: %w", err)
			}
		} else {
			// The count of the limit-th site: everything above it is in, and
			// of the sites tied at it only as many as are needed are.
			var cut int64
			if err := tx.Table("(?) AS u", grouped()).Select("n").Order("n DESC").Limit(1).Offset(limit - 1).Scan(&cut).Error; err != nil {
				return fmt.Errorf("find dest usage cut: %w", err)
			}
			// Sites tied exactly at the cut all come back (their order is decided
			// in Go, in byte order). For an account with a long tail of one-off
			// sites that is the tail: accepted, since it is one account's sites
			// over at most the retention, read once per click by an administrator.
			if err := grouped().Having("SUM(count) >= ?", cut).Scan(&top).Error; err != nil {
				return fmt.Errorf("fold dest usage: %w", err)
			}
		}
		sort.Slice(top, func(i, j int) bool {
			if top[i].N != top[j].N {
				return top[i].N > top[j].N
			}
			return top[i].Site < top[j].Site
		})
		if len(top) > limit {
			top = top[:limit]
		}
		var shown int64
		for _, row := range top {
			out.Items = append(out.Items, domain.DestUsageSite{Site: row.Site, Count: row.N})
			shown += row.N
		}
		out.OtherSites = totals.Sites - int64(len(top))
		out.OtherCount = totals.Total - shown
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return out, err
}

// UsageDropped is how many usage rows are missing from [sinceMS, untilMS) on one
// panel (0: every panel): what the nodes could not aggregate and what the
// panel's hourly usage budget refused. Zero bounds are open.
func (r *DestHitRepo) UsageDropped(ctx context.Context, sinceMS, untilMS, panelID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&destAuditDropRow{}).Where("kind IN ?", domain.DestUsageDropKinds())
	if panelID > 0 {
		q = q.Where("panel_id = ?", panelID)
	}
	if sinceMS > 0 {
		q = q.Where("hour_ms >= ?", sinceMS)
	}
	if untilMS > 0 {
		q = q.Where("hour_ms < ?", untilMS)
	}
	var total *int64
	if err := q.Select("SUM(count)").Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum dest usage drops: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}
