package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	// connExclusionWidth is connection_history.exclusion's width: the
	// longest reason ("internal") with room to spare. The column holds one
	// of the detector's four reasons or "", and Record refuses anything
	// else.
	connExclusionWidth = 16
	// connHistoryBatch is how many rows one upsert statement carries. Every
	// value is bound, so a statement holds 14 parameters per row; 200 rows
	// stay far below every driver's parameter limit (SQLite's is the
	// smallest, at 32766).
	connHistoryBatch = 200
	// connHistoryPageSize is List's page when the caller asks for none: the
	// admin handler's own default. This table is the one that holds
	// addresses, so no read returns all of it unasked.
	connHistoryPageSize = 25
)

// connectionHistoryRow is one account's connection from one SOURCE (an IPv4
// address or an IPv6 /64) through one panel node, merged across DETECTOR
// SAMPLES: the traffic poll records the accounts it judged, at most once per
// account per half poll interval, so seen_count counts judgements and a
// "poll now" click cannot inflate it.
//
// THE ONLY TABLE NEW CODE WRITES AN IP ADDRESS TO (docs/connection-limits.md
// §14.7). Every other store of the detector keeps places and counts, never an
// address; this one exists because the owner asked for per-account history
// with the address, and it is fenced accordingly:
//   - admin-only endpoints read it;
//   - risk.connection_retention_days ages every row out by its LAST
//     sighting (a week by default, at most 90 days, never "keep forever");
//   - reads JOIN users, so a deleted account's rows vanish at once, and the
//     hourly cleanup deletes them (PurgeOrphans);
//   - its SQL is logged without bound values (redactParams).
//
// No foreign key to users, for the reason risk_signals has none: a user
// delete must never depend on an observation table. No text column: every
// string is a varchar, which is what makes a NOT NULL empty-string default
// portable (MySQL refuses a DEFAULT on TEXT). Times are integer unix milliseconds,
// identical on every dialect — no zone-bearing strings to compare.
//
// The composite key is the connection itself. GORM marks none of it
// auto-increment (it does so only for a lone integer key). Its MySQL index
// length is 8 + 8 + 4×64 + 4×64 = 528 bytes, well inside InnoDB's 3072.
type connectionHistoryRow struct {
	UserID    int64  `gorm:"primaryKey;index:idx_connhist_user_last,priority:1"`
	PanelID   int64  `gorm:"primaryKey"`
	Node      string `gorm:"primaryKey;size:64"`
	SourceKey string `gorm:"primaryKey;size:64"`
	// IP is the source's smallest member at the last sighting, for display.
	IP string `gorm:"size:64;not null;default:''"`
	// Exclusion is the rule that set the source aside at the last
	// sighting, "" when the detector judged it. Named exclusion, not
	// excluded: EXCLUDED is the proposed-row pseudo-table of an ON CONFLICT
	// on PostgreSQL and SQLite.
	Exclusion string `gorm:"size:16;not null;default:''"`
	// The place at the last sighting: names and a region code, never a
	// coordinate.
	CountryCode string `gorm:"size:8;not null;default:''"`
	Country     string `gorm:"size:64;not null;default:''"`
	Region      string `gorm:"size:128;not null;default:''"`
	RegionCode  string `gorm:"size:8;not null;default:''"`
	City        string `gorm:"size:128;not null;default:''"`
	// FirstSeenMS is written once, by the insert; the upsert never names
	// it. LastSeenMS is the retention column, so it carries its own index
	// besides the per-account one (the per-account history reads
	// user_id, last_seen_ms).
	FirstSeenMS int64 `gorm:"column:first_seen_ms;not null"`
	LastSeenMS  int64 `gorm:"column:last_seen_ms;not null;index:idx_connhist_user_last,priority:2;index:idx_connhist_last"`
	SeenCount   int64 `gorm:"column:seen_count;not null;default:0"`
}

func (connectionHistoryRow) TableName() string { return "connection_history" }

// redactParams is the logger connection_history's statements run under: the
// database's own logger, except that a logged statement keeps its
// placeholders instead of its values.
//
// PSP logs a failed query, and any query slower than 200 ms, with its SQL
// (conn.go), and GORM renders the bound values into that SQL. For this table
// a bound value is an address — the row's IP, its source, a search term an
// admin typed — and a log line outlives every retention the table promises.
// GORM asks the logger for a ParamsFilter before it renders (callbacks.go)
// and, handed no values, leaves "?" (or "$1") where each one was. The SQL
// stays useful — which statement failed, on which table — and says nothing
// about whom.
//
// It is a type rather than a logger option because the session carrying it
// is the store's own: every other table's statements keep their values in
// the log, as they always have.
//
// NEVER Scan on this store. db.Scan swaps the session's logger for GORM's
// trace recorder while the statement runs (finisher_api.go), and the
// recorder renders every value whatever this filter says: a failed search
// would log the address the admin searched for. Find, Create, Exec and Count
// run their callbacks under the session's logger. The search was written
// with Scan first, and TestConnectionHistoryRepo_FailedQueryLogsNoAddress —
// which drives the insert, the search, the prune and the purge through a
// failure — caught exactly that.
type redactParams struct{ logger.Interface }

// LogMode keeps the redaction when GORM or a caller changes the level
// (db.Debug() does): a bare delegation would return the inner logger and
// drop it.
func (l redactParams) LogMode(level logger.LogLevel) logger.Interface {
	return redactParams{l.Interface.LogMode(level)}
}

// ParamsFilter withholds every bound value from the logged SQL. The
// statement itself still executes with them.
func (redactParams) ParamsFilter(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

// ConnectionHistoryRepo is the connection_history store. A concrete type
// built from the database handle rather than a ports.Repos field, like
// RiskSignalRepo: the poll that records, the admin view that lists and the
// cleanup that prunes each declare the narrow interface they need.
type ConnectionHistoryRepo struct {
	// db is a session of the caller's handle whose logger is wrapped in
	// redactParams, so every statement below — including the ones inside a
	// transaction, which inherits the session's logger — logs no value.
	db *gorm.DB
}

// NewConnectionHistoryRepo builds the store on a session of db that logs its
// SQL without bound values. NewDB drops any condition the handle carried, so
// the store starts from the table alone.
func NewConnectionHistoryRepo(db *gorm.DB) *ConnectionHistoryRepo {
	return &ConnectionHistoryRepo{db: db.Session(&gorm.Session{NewDB: true, Logger: redactParams{db.Logger}})}
}

// connHistoryUpsert names every column a later sample rewrites. An upsert
// rewrites only what it names, so a column left out keeps its FIRST value
// forever — a source that became a shared exit still read as judged, a place
// never updated. first_seen_ms is deliberately absent (the first sighting is
// the insert's), and seen_count is incremented from the stored row, not taken
// from the proposed one. The conflict columns are named explicitly, not left
// to the dialect to infer from the key.
//
// The increment references the stored row by the table's name, which all
// three dialects accept inside the conflict clause (PostgreSQL and SQLite's
// ON CONFLICT DO UPDATE, MySQL's ON DUPLICATE KEY UPDATE, where GORM renders
// the plain columns as VALUES(col) beside it).
func connHistoryUpsert() clause.OnConflict {
	set := clause.AssignmentColumns([]string{"ip", "exclusion", "country_code", "country", "region", "region_code", "city", "last_seen_ms"})
	set = append(set, clause.Assignment{Column: clause.Column{Name: "seen_count"}, Value: gorm.Expr("connection_history.seen_count + 1")})
	return clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "panel_id"}, {Name: "node"}, {Name: "source_key"}},
		DoUpdates: set,
	}
}

// Record merges one detector sample's connections into the history, stamped
// with the sample's time (now when zero): a new (account, panel, node,
// source) is inserted with that time as both its first and last sighting and
// a count of 1; a known one gets it as its last sighting, the sample's
// address, exclusion and place, and one more to its count.
//
// The whole batch is checked first, and a value the columns cannot hold on
// every dialect — or an exclusion that is not one of the detector's reasons —
// is a domain.ErrValidation with nothing written. The producer cuts every
// value to these widths (CollectLiveConnections, ConnPlaceOf), so this is the
// backstop for one that forgets; left to the database, SQLite would store it,
// PostgreSQL refuse it and MySQL truncate or refuse it by SQL mode.
//
// One key named twice is collapsed in Go, the later value winning:
// PostgreSQL refuses an upsert that touches one row twice, and SQLite and
// MySQL would count two samples for one. Then 200 rows per statement inside
// one transaction, so a failed statement rolls back the ones before it and
// the sample is recorded whole or not at all.
func (r *ConnectionHistoryRepo) Record(ctx context.Context, conns []domain.LiveConnection, at time.Time) error {
	if len(conns) == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	atMS := at.UnixMilli()
	type key struct {
		userID, panelID int64
		node, source    string
	}
	index := make(map[key]int, len(conns))
	rows := make([]connectionHistoryRow, 0, len(conns))
	for _, c := range conns {
		if err := validateConnection(c); err != nil {
			return err
		}
		row := connectionHistoryRow{
			UserID: c.UserID, PanelID: c.PanelID, Node: c.Node, SourceKey: c.SourceKey,
			IP: c.IP, Exclusion: c.Exclusion,
			CountryCode: c.Place.CountryCode, Country: c.Place.Country, Region: c.Place.Region,
			RegionCode: c.Place.RegionCode, City: c.Place.City,
			FirstSeenMS: atMS, LastSeenMS: atMS, SeenCount: 1,
		}
		k := key{c.UserID, c.PanelID, c.Node, c.SourceKey}
		if i, dup := index[k]; dup {
			rows[i] = row
			continue
		}
		index[k] = len(rows)
		rows = append(rows, row)
	}
	upsert := connHistoryUpsert()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(rows); start += connHistoryBatch {
			batch := rows[start:min(start+connHistoryBatch, len(rows))]
			if err := tx.Clauses(upsert).Create(&batch).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record connection history: %w", err)
	}
	return nil
}

// validateConnection refuses a connection the table cannot hold identically
// on every dialect. Lengths are in bytes, the stricter bound for anything
// that is not ASCII. The error names the column and the account, never the
// value: it may be an address, and an error may reach a log.
func validateConnection(c domain.LiveConnection) error {
	for _, f := range []struct {
		column string
		value  string
		width  int
	}{
		{"node", c.Node, domain.LiveConnNodeMaxBytes},
		{"source_key", c.SourceKey, domain.LiveConnKeyMaxBytes},
		{"ip", c.IP, domain.LiveConnIPMaxBytes},
		{"exclusion", c.Exclusion, connExclusionWidth},
		{"country_code", c.Place.CountryCode, domain.ConnPlaceCountryCodeMaxBytes},
		{"country", c.Place.Country, domain.ConnPlaceCountryMaxBytes},
		{"region", c.Place.Region, domain.ConnPlaceRegionMaxBytes},
		{"region_code", c.Place.RegionCode, domain.ConnPlaceRegionCodeMaxBytes},
		{"city", c.Place.City, domain.ConnPlaceCityMaxBytes},
	} {
		if len(f.value) > f.width {
			return fmt.Errorf("%w: connection of user %d: %s is %d bytes, over %d", domain.ErrValidation, c.UserID, f.column, len(f.value), f.width)
		}
	}
	if c.SourceKey == "" {
		// Half the key: an empty source would merge every source-less
		// connection of an account on a node into one row.
		return fmt.Errorf("%w: connection of user %d has no source", domain.ErrValidation, c.UserID)
	}
	if !knownExclusion(c.Exclusion) {
		return fmt.Errorf("%w: connection of user %d has an unknown exclusion reason", domain.ErrValidation, c.UserID)
	}
	return nil
}

// knownExclusion reports whether reason is "" (judged) or one of the
// detector's four reasons: the closed set the history's filter can name.
func knownExclusion(reason string) bool {
	switch reason {
	case "", domain.AddressExcludedInternal, domain.AddressExcludedListed, domain.AddressExcludedInfra, domain.AddressExcludedShared:
		return true
	}
	return false
}

// connHistoryListRow is one row of List's JOIN: the stored columns and the
// account's names. Read with Find, never Scan (see redactParams).
type connHistoryListRow struct {
	UserID      int64  `gorm:"column:user_id"`
	PanelID     int64  `gorm:"column:panel_id"`
	Node        string `gorm:"column:node"`
	SourceKey   string `gorm:"column:source_key"`
	IP          string `gorm:"column:ip"`
	Exclusion   string `gorm:"column:exclusion"`
	CountryCode string `gorm:"column:country_code"`
	Country     string `gorm:"column:country"`
	Region      string `gorm:"column:region"`
	RegionCode  string `gorm:"column:region_code"`
	City        string `gorm:"column:city"`
	FirstSeenMS int64  `gorm:"column:first_seen_ms"`
	LastSeenMS  int64  `gorm:"column:last_seen_ms"`
	SeenCount   int64  `gorm:"column:seen_count"`
	UPN         string `gorm:"column:upn"`
	DisplayName string `gorm:"column:display_name"`
}

// connHistorySortAllowlist maps the API's sort names to table-qualified
// columns: every read JOINs users, where a bare id or name would be
// ambiguous on PostgreSQL. Admin input never reaches ORDER BY otherwise.
var connHistorySortAllowlist = map[string]string{
	"last_seen":  "connection_history.last_seen_ms",
	"first_seen": "connection_history.first_seen_ms",
	"count":      "connection_history.seen_count",
	"ip":         "connection_history.ip",
	"user_id":    "connection_history.user_id",
}

// List is the admin history read: the rows whose account still exists, with
// the account's upn and display name, filtered, sorted (newest last sighting
// first unless asked otherwise) and paged, plus the total the filter matches.
//
// JOIN, not LEFT JOIN: a deleted account's rows are invisible from the moment
// it is deleted, not from the next hourly purge. Ties are broken by the key,
// so a page boundary never reorders rows that share a sighting. The total
// skips its COUNT when the first page already shows everything
// (inferTotalOrCount), as the sub-log list does.
func (r *ConnectionHistoryRepo) List(ctx context.Context, f ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, int64, error) {
	switch f.Exclusion {
	case "", ports.ConnExclusionKept, ports.ConnExclusionExcluded:
	default:
		if !knownExclusion(f.Exclusion) {
			// The value is not echoed: it is admin input to an address
			// table, and an error may reach a log.
			return nil, 0, fmt.Errorf("%w: unknown connection history exclusion filter", domain.ErrValidation)
		}
	}
	if f.PageSize <= 0 {
		f.PageSize = connHistoryPageSize
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if strings.TrimSpace(f.SortBy) == "" {
		f.SortBy = "last_seen"
	}
	if strings.TrimSpace(f.SortDir) == "" {
		f.SortDir = "desc"
	}

	// applyFilters builds the WHERE on a fresh query, reused by the page and
	// by the count so the total always describes the rows returned.
	applyFilters := func(q *gorm.DB) *gorm.DB {
		q = q.Table("connection_history").Joins("JOIN users ON users.id = connection_history.user_id")
		if f.UserID != nil {
			q = q.Where("connection_history.user_id = ?", *f.UserID)
		}
		if f.PanelID != nil {
			q = q.Where("connection_history.panel_id = ?", *f.PanelID)
		}
		switch f.Exclusion {
		case "":
		case ports.ConnExclusionKept:
			q = q.Where("connection_history.exclusion = ?", "")
		case ports.ConnExclusionExcluded:
			q = q.Where("connection_history.exclusion <> ?", "")
		default:
			q = q.Where("connection_history.exclusion = ?", f.Exclusion)
		}
		if f.Since != nil {
			q = q.Where("connection_history.last_seen_ms >= ?", f.Since.UnixMilli())
		}
		if f.Until != nil {
			q = q.Where("connection_history.last_seen_ms <= ?", f.Until.UnixMilli())
		}
		if kw := keywordLike(f.Search); kw != "" {
			q = q.Where(likeCols(
				"connection_history.ip", "connection_history.source_key", "connection_history.country_code",
				"connection_history.region", "connection_history.city", "users.upn",
			), kw, kw, kw, kw, kw, kw)
		}
		return q
	}

	var rows []connHistoryListRow
	q := applyFilters(r.db.WithContext(ctx)).Select("connection_history.user_id, connection_history.panel_id, " +
		"connection_history.node, connection_history.source_key, connection_history.ip, connection_history.exclusion, " +
		"connection_history.country_code, connection_history.country, connection_history.region, " +
		"connection_history.region_code, connection_history.city, connection_history.first_seen_ms, " +
		"connection_history.last_seen_ms, connection_history.seen_count, " +
		"users.upn AS upn, users.display_name AS display_name")
	if err := applyPagination(q, f.Pagination, connHistorySortAllowlist, connHistorySortAllowlist["last_seen"]).
		Order("connection_history.user_id").Order("connection_history.panel_id").
		Order("connection_history.node").Order("connection_history.source_key").
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list connection history: %w", err)
	}
	total, err := inferTotalOrCount(applyFilters(r.db.WithContext(ctx)), f.Pagination, len(rows))
	if err != nil {
		return nil, 0, fmt.Errorf("count connection history: %w", err)
	}

	out := make([]domain.ConnectionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ConnectionRecord{
			UserID: row.UserID, UPN: row.UPN, DisplayName: row.DisplayName,
			PanelID: row.PanelID, Node: row.Node, SourceKey: row.SourceKey, IP: row.IP, Exclusion: row.Exclusion,
			Place: domain.ConnPlace{
				CountryCode: row.CountryCode, Country: row.Country, Region: row.Region,
				RegionCode: row.RegionCode, City: row.City,
			},
			FirstSeenMS: row.FirstSeenMS, LastSeenMS: row.LastSeenMS, Count: row.SeenCount,
		})
	}
	return out, total, nil
}

// DeleteBefore deletes the rows last seen before cutoff and returns how many
// it deleted: retention by LAST sighting, so a source first seen a month ago
// and still connecting is current, not old. last_seen_ms is an integer, so
// the comparison is identical on every dialect.
func (r *ConnectionHistoryRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM connection_history WHERE last_seen_ms < ?", cutoff.UnixMilli())
	if res.Error != nil {
		return 0, fmt.Errorf("prune connection history: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// PurgeOrphans deletes the rows of accounts that no longer exist and returns
// how many it deleted. The hourly cleanup runs it whatever the retention, so
// a deleted account's addresses last at most an hour (and List hides them
// meanwhile).
//
// A NOT IN subquery on another table is portable as written: MySQL's
// restriction (error 1093) is on a subquery naming the table being deleted
// from, and users.id is a primary key, so NOT IN never meets a NULL.
func (r *ConnectionHistoryRepo) PurgeOrphans(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM connection_history WHERE user_id NOT IN (SELECT id FROM users)")
	if res.Error != nil {
		return 0, fmt.Errorf("purge connection history orphans: %w", res.Error)
	}
	return res.RowsAffected, nil
}
