package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The widths of flag_records' bounded columns, checked before anything is
// written for the reason risk_signals checks its own: left to the database,
// an over-long value is stored by SQLite, refused by PostgreSQL and truncated
// or refused by MySQL by SQL mode. They must equal the size tags on
// flagRecordRow (TestFlagRecordRow_WidthsMatchTheColumns), and each is at
// least its risk_signals counterpart — source ≥ kind, state, code, params ≥
// evidence — so a risk transition built from a row risk_signals accepted can
// never be refused here and stall the risk worker on its own data.
const (
	flagSourceWidth = 24
	flagEventWidth  = 24
	flagLevelWidth  = 16
	flagStateWidth  = 16
	flagCodeWidth   = 32
	// flagParamsMaxBytes is MySQL's TEXT capacity, the smallest of the three
	// dialects', as for risk_signals.evidence.
	flagParamsMaxBytes = 65535
	// flagRecordBatch is how many rows one insert statement carries: nine
	// bound values per row, far below every driver's parameter limit.
	flagRecordBatch = 200
	// flagRecordPageSize is List's page when the caller asks for none: the
	// admin handler's own default. A history grows without bound between
	// prunes, so no read returns all of it unasked.
	flagRecordPageSize = 25
	// flagRecordMaxPageSize is applyPagination's cap, kept for the same
	// reason.
	flagRecordMaxPageSize = 200
)

// flagRecordRow is one change of one account's attention level on one
// source: the geo verdict, its automatic suspension, or a risk signal.
//
// APPEND-ONLY. Nothing updates a row; the producers insert and the hourly
// cleanup deletes by age (risk.flag_record_retention_days, 90 days by
// default) and by orphan. The history is only worth reading if it cannot
// have been rewritten, and an append-only table has no upsert to forget a
// column.
//
// Address-free: Code is a branch name and Params the address-free evidence
// or numbers the change was judged from (domain.FlagRecord). No UPN: reads
// JOIN users, so a deleted account's records vanish at once, and
// PurgeOrphans deletes them within the hour. No foreign key, for the reason
// risk_signals has none: a user delete must never depend on an observation
// table.
//
// The key is an auto-increment id (GORM marks a lone integer primary key
// auto-increment; the tag says so anyway), which is also the tie-break of
// the history's order: two records of one millisecond read in the order they
// were written. SQLite never reuses one (glebarez emits AUTOINCREMENT). Times
// are integer unix milliseconds, identical on every dialect. The per-account
// index serves the user lookup's read (user_id, at_ms), the time index the
// fleet list and the retention prune.
type flagRecordRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UserID    int64  `gorm:"not null;index:idx_flag_user_at,priority:1"`
	Source    string `gorm:"size:24;not null;default:''"`
	Event     string `gorm:"size:24;not null;default:''"`
	Level     string `gorm:"size:16;not null;default:''"`
	PrevLevel string `gorm:"size:16;not null;default:''"`
	State     string `gorm:"size:16;not null;default:''"`
	Code      string `gorm:"size:32;not null;default:''"`
	// Params is JSON: text, NULLABLE and NO DEFAULT (MySQL refuses a DEFAULT
	// on TEXT; TestSchemaNoDefaultOnTextColumns). Written and read through
	// riskEvidenceColumn and riskEvidenceFrom, so "no params" has the one
	// stored form NULL, and a value edited behind the store's back costs
	// only its own params.
	Params *string `gorm:"column:params;type:text"`
	AtMS   int64   `gorm:"column:at_ms;not null;index:idx_flag_user_at,priority:2;index:idx_flag_at"`
}

func (flagRecordRow) TableName() string { return "flag_records" }

// FlagRecordRepo is the flag_records store. A concrete type built from the
// database handle rather than a ports.Repos field, like RiskSignalRepo: the
// producers that append, the admin view that lists and the cleanup that
// prunes each declare the narrow interface they need. The risk signals'
// transitions are not appended through it — RiskSignalRepo.Save writes them
// in its own transaction, through insertFlagRecords.
type FlagRecordRepo struct{ db *gorm.DB }

func NewFlagRecordRepo(db *gorm.DB) *FlagRecordRepo { return &FlagRecordRepo{db: db} }

// Append writes records as one unit: every record is checked first, and one
// the columns cannot hold on every dialect is a domain.ErrValidation with
// nothing written; then 200 rows per statement inside one transaction, so a
// failed statement rolls back the ones before it. A record's AtMS of 0 is
// stamped with now; its ID, UPN and DisplayName are ignored.
func (r *FlagRecordRepo) Append(ctx context.Context, recs []domain.FlagRecord) error {
	if len(recs) == 0 {
		return nil
	}
	rows, err := flagRecordRows(recs, time.Now())
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return createFlagRows(tx, rows)
	}); err != nil {
		return fmt.Errorf("append flag records: %w", err)
	}
	return nil
}

// insertFlagRecords writes records on tx, checked and stamped as Append does
// them. It is RiskSignalRepo.Save's half of the risk transitions, which must
// commit or roll back with the upsert that caused them.
//
// ONLY tx. Save calls it inside its transaction, and on SQLite that
// transaction holds the pool's one connection (conn.go): a statement issued
// on any other handle would wait for that connection until its context gave
// up (TestRiskSignalRepo_SaveUsesOnlyTheTransaction).
func insertFlagRecords(tx *gorm.DB, recs []domain.FlagRecord) error {
	if len(recs) == 0 {
		return nil
	}
	rows, err := flagRecordRows(recs, time.Now())
	if err != nil {
		return err
	}
	return createFlagRows(tx, rows)
}

// createFlagRows inserts rows in statements of flagRecordBatch on tx. Batched
// by hand rather than by CreateInBatches, which wraps more than one batch in
// a nested transaction — a SAVEPOINT inside Save's. One explicit transaction
// with plain statements in it behaves the same on every dialect.
func createFlagRows(tx *gorm.DB, rows []flagRecordRow) error {
	for start := 0; start < len(rows); start += flagRecordBatch {
		batch := rows[start:min(start+flagRecordBatch, len(rows))]
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
	}
	return nil
}

// flagRecordRows validates every record and maps it to its row, stamping a
// zero AtMS with now. The first record that cannot be held refuses the lot.
func flagRecordRows(recs []domain.FlagRecord, now time.Time) ([]flagRecordRow, error) {
	nowMS := now.UnixMilli()
	rows := make([]flagRecordRow, 0, len(recs))
	for _, rec := range recs {
		if err := validateFlagRecord(rec); err != nil {
			return nil, err
		}
		at := rec.AtMS
		if at == 0 {
			at = nowMS
		}
		rows = append(rows, flagRecordRow{
			UserID: rec.UserID, Source: rec.Source, Event: string(rec.Event),
			Level: string(rec.Level), PrevLevel: string(rec.PrevLevel), State: string(rec.State),
			Code: rec.Code, Params: riskEvidenceColumn(rec.Params), AtMS: at,
		})
	}
	return rows, nil
}

// validateFlagRecord refuses a record the table cannot hold identically on
// every dialect. Lengths are in bytes, the stricter bound for anything that
// is not ASCII. A record with no source or no event could never be filtered
// to or said. The error names the field and the account, never a value:
// params in particular are never echoed, and an error may reach a log.
func validateFlagRecord(rec domain.FlagRecord) error {
	for _, f := range []struct {
		field    string
		value    string
		width    int
		required bool
	}{
		{"source", rec.Source, flagSourceWidth, true},
		{"event", string(rec.Event), flagEventWidth, true},
		{"level", string(rec.Level), flagLevelWidth, false},
		{"prev_level", string(rec.PrevLevel), flagLevelWidth, false},
		{"state", string(rec.State), flagStateWidth, false},
		{"code", rec.Code, flagCodeWidth, false},
	} {
		if f.required && f.value == "" {
			return fmt.Errorf("%w: flag record of user %d has no %s", domain.ErrValidation, rec.UserID, f.field)
		}
		if len(f.value) > f.width {
			return fmt.Errorf("%w: flag record of user %d: %s is %d bytes, over %d", domain.ErrValidation, rec.UserID, f.field, len(f.value), f.width)
		}
	}
	switch {
	case len(rec.Params) > flagParamsMaxBytes:
		return fmt.Errorf("%w: flag record of user %d: params are %d bytes, over %d", domain.ErrValidation, rec.UserID, len(rec.Params), flagParamsMaxBytes)
	case len(rec.Params) > 0 && !json.Valid(rec.Params):
		// Served verbatim as a raw JSON value: one bad record would fail
		// the admin's whole page.
		return fmt.Errorf("%w: flag record of user %d: params are not valid JSON", domain.ErrValidation, rec.UserID)
	}
	return nil
}

// flagRecordListRow is one row of List's JOIN: the stored columns and the
// account's names.
type flagRecordListRow struct {
	ID          int64   `gorm:"column:id"`
	UserID      int64   `gorm:"column:user_id"`
	Source      string  `gorm:"column:source"`
	Event       string  `gorm:"column:event"`
	Level       string  `gorm:"column:level"`
	PrevLevel   string  `gorm:"column:prev_level"`
	State       string  `gorm:"column:state"`
	Code        string  `gorm:"column:code"`
	Params      *string `gorm:"column:params"`
	AtMS        int64   `gorm:"column:at_ms"`
	UPN         string  `gorm:"column:upn"`
	DisplayName string  `gorm:"column:display_name"`
}

// List is the admin history read: the records whose account still exists,
// with the account's upn and display name, filtered, newest first (ties in
// the order written) and paged, plus the total the filter matches.
//
// The order is fixed: a history is read in time order, and an admin's sort
// by any other column would make it a different list. JOIN, not LEFT JOIN: a
// deleted account's records are invisible from the moment it is deleted, not
// from the next hourly purge. An unknown source, level or event is a
// domain.ErrValidation rather than an empty answer, which would read as
// "nothing ever happened". The total skips its COUNT when the first page
// already shows everything (inferTotalOrCount).
func (r *FlagRecordRepo) List(ctx context.Context, f ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error) {
	if f.Source != "" && !slices.Contains(domain.FlagSources(), f.Source) {
		return nil, 0, fmt.Errorf("%w: unknown flag record source filter", domain.ErrValidation)
	}
	if f.Event != "" && !slices.Contains(domain.FlagEvents(), domain.FlagEvent(f.Event)) {
		return nil, 0, fmt.Errorf("%w: unknown flag record event filter", domain.ErrValidation)
	}
	level := f.Level
	switch domain.FlagLevel(f.Level) {
	case "", domain.FlagLevelSuspect, domain.FlagLevelFlagged, domain.FlagLevelSuspended:
	default:
		if f.Level != ports.FlagLevelCleared {
			return nil, 0, fmt.Errorf("%w: unknown flag record level filter", domain.ErrValidation)
		}
		// Cleared is the records that moved to no attention, stored as "".
		level = string(domain.FlagLevelNone)
	}
	if f.PageSize <= 0 {
		f.PageSize = flagRecordPageSize
	}
	f.PageSize = min(f.PageSize, flagRecordMaxPageSize)
	if f.Page < 1 {
		f.Page = 1
	}

	// applyFilters builds the WHERE on a fresh query, reused by the page and
	// by the count so the total always describes the rows returned.
	applyFilters := func(q *gorm.DB) *gorm.DB {
		q = q.Table("flag_records").Joins("JOIN users ON users.id = flag_records.user_id")
		if f.UserID != nil {
			q = q.Where("flag_records.user_id = ?", *f.UserID)
		}
		if f.Source != "" {
			q = q.Where("flag_records.source = ?", f.Source)
		}
		if f.Level != "" {
			q = q.Where("flag_records.level = ?", level)
		}
		if f.Event != "" {
			q = q.Where("flag_records.event = ?", f.Event)
		}
		if f.Since != nil {
			q = q.Where("flag_records.at_ms >= ?", f.Since.UnixMilli())
		}
		if f.Until != nil {
			q = q.Where("flag_records.at_ms <= ?", f.Until.UnixMilli())
		}
		return q
	}

	var rows []flagRecordListRow
	if err := applyFilters(r.db.WithContext(ctx)).
		Select("flag_records.id, flag_records.user_id, flag_records.source, flag_records.event, " +
			"flag_records.level, flag_records.prev_level, flag_records.state, flag_records.code, " +
			"flag_records.params, flag_records.at_ms, users.upn AS upn, users.display_name AS display_name").
		Order("flag_records.at_ms DESC").Order("flag_records.id DESC").
		Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list flag records: %w", err)
	}
	total, err := inferTotalOrCount(applyFilters(r.db.WithContext(ctx)), f.Pagination, len(rows))
	if err != nil {
		return nil, 0, fmt.Errorf("count flag records: %w", err)
	}

	out := make([]domain.FlagRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.FlagRecord{
			ID: row.ID, UserID: row.UserID, Source: row.Source, Event: domain.FlagEvent(row.Event),
			Level: domain.FlagLevel(row.Level), PrevLevel: domain.FlagLevel(row.PrevLevel),
			State: domain.GeoState(row.State), Code: row.Code, Params: riskEvidenceFrom(row.Params),
			AtMS: row.AtMS, UPN: row.UPN, DisplayName: row.DisplayName,
		})
	}
	return out, total, nil
}

// DeleteBefore deletes the records older than cutoff and returns how many it
// deleted: retention by the record's own time. at_ms is an integer, so the
// comparison is identical on every dialect.
func (r *FlagRecordRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM flag_records WHERE at_ms < ?", cutoff.UnixMilli())
	if res.Error != nil {
		return 0, fmt.Errorf("prune flag records: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// PurgeOrphans deletes the records of accounts that no longer exist and
// returns how many it deleted. The hourly cleanup runs it whatever the
// retention, so a deleted account's history lasts at most an hour (and List
// hides it meanwhile). Portable as written, for the reason
// RiskSignalRepo.PurgeOrphans gives.
func (r *FlagRecordRepo) PurgeOrphans(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM flag_records WHERE user_id NOT IN (SELECT id FROM users)")
	if res.Error != nil {
		return 0, fmt.Errorf("purge flag record orphans: %w", res.Error)
	}
	return res.RowsAffected, nil
}
