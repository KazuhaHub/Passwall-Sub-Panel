package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationHits(ctx context.Context, q domain.DestHitQuery) (domain.DestHitPage, error) {
	q, err := normalizeHitQuery(q)
	if err != nil {
		return domain.DestHitPage{}, err
	}
	out := domain.DestHitPage{Records: []domain.DestHitRecord{}, Groups: []domain.DestHitGroup{}, Sources: []domain.DestHitSource{},
		GroupBy: q.GroupBy, Page: q.Page, PageSize: q.PageSize, Losses: domain.DestAuditLosses{Scope: "panel"}}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := readHitSummary(tx, q, &out.Summary); err != nil {
			return err
		}
		names, err := readHitSources(tx, q, &out.Sources)
		if err != nil {
			return err
		}
		if q.GroupBy == "none" {
			if err := readHitRecords(tx, q, names, &out); err != nil {
				return err
			}
		} else if err := readHitGroups(tx, q, names, &out); err != nil {
			return err
		}
		if err := readHitLosses(tx, q, &out.Losses); err != nil {
			return err
		}
		out.DroppedInRange = out.Losses.Rows
		return nil
	}, options)
	if err != nil {
		return domain.DestHitPage{}, auditStorageError(err)
	}
	return out, nil
}

func normalizeHitQuery(q domain.DestHitQuery) (domain.DestHitQuery, error) {
	if q.Since.IsZero() || q.Since.UnixMilli() <= 0 || !q.Until.After(q.Since) || q.Until.Sub(q.Since) > 31*24*time.Hour || q.UserID < 0 || q.PanelID < 0 {
		return q, domain.ErrValidation
	}
	if q.Source != "" {
		if _, _, ok := hitSourceID(q.Source); !ok {
			return q, domain.ErrValidation
		}
	}
	if q.SourceKind != "" && q.SourceKind != "policy" && q.SourceKind != "group" {
		return q, domain.ErrValidation
	}
	if q.Action != "" && q.Action != "block" && q.Action != "observe" {
		return q, domain.ErrValidation
	}
	if q.GroupBy == "" {
		q.GroupBy = "none"
	}
	if q.GroupBy != "none" && q.GroupBy != "site" && q.GroupBy != "user" && q.GroupBy != "policy" {
		return q, domain.ErrValidation
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 50
	}
	if q.Page < 1 || q.PageSize < 1 || q.PageSize > 200 || (q.Page-1) > math.MaxInt/q.PageSize {
		return q, domain.ErrValidation
	}
	q.Keyword = strings.TrimSpace(q.Keyword)
	if !utf8.ValidString(q.Keyword) || len(q.Keyword) > 4096 {
		return q, domain.ErrValidation
	}
	return q, nil
}

func hitSourceID(value string) (byte, int64, bool) {
	if len(value) < 2 || (value[0] != 'p' && value[0] != 'g') {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(value[1:], 10, 64)
	return value[0], id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value[1:]
}

func hitTimeScope(tx *gorm.DB, q domain.DestHitQuery) *gorm.DB {
	return tx.Model(&destHitRow{}).Where("hour_ms >= ? AND hour_ms < ?", q.Since.UTC().Truncate(time.Hour).UnixMilli(), q.Until.UnixMilli())
}
func hitBaseScope(tx *gorm.DB, q domain.DestHitQuery) *gorm.DB {
	query := hitTimeScope(tx, q)
	if q.UserID > 0 {
		query = query.Where("user_id = ?", q.UserID)
	}
	if q.PanelID > 0 {
		query = query.Where("panel_id = ?", q.PanelID)
	}
	return query
}
func hitTrialPredicate(tx *gorm.DB) string {
	return "action = 'observe' AND " + auditExactGroupSource(tx.Dialector.Name())
}
func hitFilteredScope(tx *gorm.DB, q domain.DestHitQuery) *gorm.DB {
	query := hitBaseScope(tx, q)
	if !q.IncludeTrial {
		query = query.Where("NOT (" + hitTrialPredicate(tx) + ")")
	}
	if q.Source != "" {
		query = query.Where("source = ?", q.Source)
	}
	if q.SourceKind == "policy" {
		query = query.Where("source LIKE 'p%'")
	}
	if q.SourceKind == "group" {
		query = query.Where("source LIKE 'g%'")
	}
	if q.Action != "" {
		query = query.Where("action = ?", q.Action)
	}
	if q.Keyword != "" {
		query = query.Where(likeCols("dest"), keywordLike(q.Keyword))
	}
	return query
}

func readHitSummary(tx *gorm.DB, q domain.DestHitQuery, out *domain.DestHitSummary) error {
	rows, err := hitBaseScope(tx, q).Select("source, action, count, COUNT(*) AS key_count").Group("source, action, count").Rows()
	if err != nil {
		return err
	}
	for rows.Next() {
		var source, action string
		var count, keys int64
		if err := rows.Scan(&source, &action, &count, &keys); err != nil {
			rows.Close()
			return err
		}
		kind, _, ok := hitSourceID(source)
		if !ok || count < 0 || keys < 1 || (action != "block" && action != "observe") {
			rows.Close()
			return domain.ErrUnavailable
		}
		var total *int64
		switch {
		case action == "block" && kind == 'p':
			total = &out.Block
		case action == "block" && kind == 'g':
			total = &out.Deny
		case action == "observe" && kind == 'p':
			total = &out.Observe
		}
		if total != nil {
			*total = auditReadAdd(*total, hitReadProduct(count, keys))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return hitBaseScope(tx, q).Where("user_id > 0 AND NOT (" + hitTrialPredicate(tx) + ")").Distinct("user_id").Count(&out.Users).Error
}

func hitReadProduct(count, keys int64) int64 {
	if count > 0 && keys > math.MaxInt64/count {
		return math.MaxInt64
	}
	return count * keys
}

func readHitRecords(tx *gorm.DB, q domain.DestHitQuery, names map[string]string, out *domain.DestHitPage) error {
	query := hitFilteredScope(tx, q)
	if err := query.Session(&gorm.Session{}).Count(&out.Total).Error; err != nil {
		return err
	}
	var rows []destHitRow
	// Every primary-key component is a stable tie-breaker. No input becomes
	// an ORDER expression and no external query can request an unbounded page.
	if err := query.Session(&gorm.Session{}).Order("hour_ms DESC, panel_id, user_id, source, action, dest, port").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&rows).Error; err != nil {
		return err
	}
	userIDs, panelIDs := []int64{}, []int64{}
	for _, v := range rows {
		if v.UserID > 0 {
			userIDs = append(userIDs, v.UserID)
		}
		panelIDs = append(panelIDs, v.PanelID)
	}
	users, err := readHitDisplayNames(tx, &userRow{}, "upn", userIDs)
	if err != nil {
		return err
	}
	panels, err := readHitDisplayNames(tx, &xuiPanelRow{}, "name", panelIDs)
	if err != nil {
		return err
	}
	for _, v := range rows {
		if _, _, ok := hitSourceID(v.Source); !ok || v.Count < 0 {
			return domain.ErrUnavailable
		}
		out.Records = append(out.Records, domain.DestHitRecord{Hour: v.HourMS, PanelID: v.PanelID, PanelName: hitName(panels, v.PanelID), UserID: v.UserID, UserUPN: hitName(users, v.UserID), Source: v.Source, SourceName: hitName(names, v.Source), Action: v.Action, Dest: v.Dest, Port: v.Port, Count: v.Count, FirstAt: v.FirstAt.UnixMilli(), LastAt: v.LastAt.UnixMilli()})
	}
	return nil
}

func readHitLosses(tx *gorm.DB, q domain.DestHitQuery, out *domain.DestAuditLosses) error {
	kinds := []string{"block", "observe"}
	if q.IncludeTrial {
		kinds = append(kinds, "trial")
	}
	query := tx.Model(&destAuditLossHourlyRow{}).Where("observed_hour_ms >= ? AND observed_hour_ms < ? AND kind IN ?", q.Since.UTC().Truncate(time.Hour).UnixMilli(), q.Until.UnixMilli(), kinds)
	if q.PanelID > 0 {
		query = query.Where("panel_id = ?", q.PanelID)
	} else if q.UserID > 0 {
		current := tx.Model(&pspClientRow{}).Select("panel_id").Where("user_id = ?", q.UserID)
		history := hitTimeScope(tx, q).Select("panel_id").Where("user_id = ?", q.UserID)
		query = query.Where("(panel_id IN (?) OR panel_id IN (?))", current, history)
	} else if q.Source != "" {
		var err error
		query, err = hitSourceLossScope(tx, query, q)
		if err != nil {
			return err
		}
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

func hitSourceLossScope(tx, query *gorm.DB, q domain.DestHitQuery) (*gorm.DB, error) {
	history := hitTimeScope(tx, q).Select("panel_id").Where("source = ?", q.Source)
	kind, id, _ := hitSourceID(q.Source)
	groups := []int64{id}
	if kind == 'p' {
		var policy destPolicyRow
		err := tx.Select("id", "scope", "group_ids").Where("id = ?", id).First(&policy).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return query.Where("panel_id IN (?)", history), nil
		}
		if err != nil {
			return nil, err
		}
		if policy.Scope == "all" {
			return query, nil
		}
		if policy.Scope != "groups" {
			return nil, domain.ErrUnavailable
		}
		groups = []int64(policy.GroupIDs)
	}
	current := tx.Model(&pspClientRow{}).Select("psp_clients.panel_id").Joins("JOIN users ON users.id = psp_clients.user_id").Joins("JOIN groups_ ON groups_.id = users.group_id").Where("users.group_id IN ?", groups)
	return query.Where("(panel_id IN (?) OR panel_id IN (?))", current, history), nil
}

var _ ports.DestHitReadRepo = (*DestAuditRepo)(nil)
