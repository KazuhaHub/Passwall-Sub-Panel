package sqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The widths of risk_signals' bounded columns, checked by Save before anything
// is written. Left to the database, an over-long value is stored by SQLite,
// refused by PostgreSQL and truncated or refused by MySQL depending on its
// SQL mode — one refresh's batch would succeed on one install and fail on the
// next. Checked here, it fails the same way everywhere. They must equal the
// size tags on riskSignalRow (TestRiskSignalRow_WidthsMatchTheColumns).
const (
	riskKindWidth  = 24
	riskStateWidth = 16
	riskCodeWidth  = 32
	// riskEvidenceMaxBytes is MySQL's TEXT capacity, the smallest of the
	// three dialects'. The evaluators cap their lists far below it; this is
	// the backstop that keeps a runaway one from failing on MySQL alone.
	riskEvidenceMaxBytes = 65535
)

// riskSignalRow is one account's latest verdict for one observe-only signal.
//
// Overwritten every run (hourly by default); no history here. The worker
// recomputes each signal from a whole window every run, so an older row is
// an older window, not a fact the next run needs — and keeping them would
// grow without bound for values nothing reads twice. What an admin does
// need from the past — when a signal became suspect or flagged, and when it
// stopped — Save records in flag_records as it overwrites the row.
//
// No foreign key to users. Deleting a user cascades nothing
// (user_repo.go's hard delete), so reads JOIN users to hide what a deleted
// account left, and PurgeOrphans deletes it on the worker's next run. A
// foreign key would instead make every user delete depend on this table,
// which is observation only and must never block an admin action.
//
// Composite primary key (user_id, kind), and GORM marks neither column
// auto-increment: it does so only for a lone integer key, or for the one
// field of a composite key tagged autoIncrement.
type riskSignalRow struct {
	UserID int64  `gorm:"primaryKey"`
	Kind   string `gorm:"primaryKey;size:24"`
	// State is one of v2's seven GeoState values; Code names the evaluator
	// branch (domain.RiskCode). Codes and numbers, never prose: the SPA
	// localizes them, and a stored English sentence would be English forever.
	State string `gorm:"size:16;not null;default:''"`
	Code  string `gorm:"size:32;not null;default:''"`
	// Evidence is the evaluator's evidence as JSON — places, day masks, client
	// labels with a short digest prefix, byte totals; never an address.
	//
	// text, NULLABLE, and NO DEFAULT: MySQL refuses a DEFAULT on a TEXT column
	// (error 1101; TestSchemaNoDefaultOnTextColumns). NULL for idle, disabled
	// and exempt verdicts, so nothing outlives the window it described. A
	// plain *string rather than a custom Valuer: the value is already JSON,
	// and a pointer is the one type every driver reads NULL into unaided.
	Evidence *string `gorm:"column:evidence;type:text"`
	// UpdatedAt is when the row was last written (unix ms). The bell counts
	// only fresh flags by it, so a row the worker stopped rewriting — a dead
	// loop, a skipped kind — falls out of the count instead of staying lit.
	UpdatedAt int64 `gorm:"autoUpdateTime:milli"`
}

func (riskSignalRow) TableName() string { return "risk_signals" }

// RiskSignalRepo is the risk_signals store. A concrete type built from the
// database handle rather than a ports.Repos field, like GeoStreakRepo: each
// consumer — the worker that writes it, the admin view that lists it, the
// bell that counts it — declares the narrow interface it needs, so no reader
// is handed a writer and the writer is handed nothing but this table.
type RiskSignalRepo struct{ db *gorm.DB }

func NewRiskSignalRepo(db *gorm.DB) *RiskSignalRepo { return &RiskSignalRepo{db: db} }

// Save upserts this run's rows by (user_id, kind), and records in
// flag_records every (user, kind) whose attention level the run changed.
//
// The whole batch is checked before anything is written. A kind that is empty
// or wider than its column, a state or code wider than its column, evidence
// that is not valid JSON or will not fit a TEXT column, or one (user, kind)
// named twice returns a domain.ErrValidation and writes nothing. Evidence has
// to be valid because the admin API serves it verbatim as a raw JSON value:
// one bad row would fail that response for every account. A duplicate key is
// refused because the dialects disagree about it — PostgreSQL rejects an
// upsert that touches one row twice, SQLite and MySQL silently keep the last.
//
// THE TRANSITIONS ARE FOUND HERE because nowhere else can find them. The
// worker never reads its own rows — risk.Deps.Store is Save and PurgeOrphans
// only (TestRiskServiceCannotWriteServiceState) — so the previous state
// exists only in this table, until the upsert overwrites it. Inside one
// explicit transaction Save therefore reads every row's (user_id, kind,
// state) — the whole table, at most four rows per account, three narrow
// columns — then upserts, then inserts a flag record for each saved signal
// whose level moved (domain.RiskFlagTransition: suspect and flagged are the
// levels, unknown included in "none", as the bell reads it). Either write
// failing rolls back both: a verdict saved without its record would read as
// the previous state next run, and the change would never be recorded at
// all. The records are built from rows that passed validateRiskSignal, and
// flag_records' columns are at least as wide, so the history cannot stall
// the worker on data.
//
// EVERY STATEMENT RUNS ON tx. On SQLite the transaction holds the pool's one
// connection (conn.go), and a statement on r.db would wait for it until the
// context gave up (TestRiskSignalRepo_SaveUsesOnlyTheTransaction). The
// worker is a single goroutine, so no two Saves read the same previous state.
//
// Upsert rather than delete-then-insert, so a crash mid-write can never leave
// the table empty; 200 rows per statement, batched by hand inside the one
// transaction (CreateInBatches would nest a transaction — a SAVEPOINT — for
// more than one batch), so a failed batch rolls back the batches before it
// and the run keeps its previous rows whole.
//
// Every mutable column is named in DoUpdates. An upsert rewrites only the
// columns it names, so one left out keeps its FIRST value forever — last
// week's evidence beside today's idle verdict. The conflict columns are named
// explicitly, not left to the dialect to infer from the key.
//
// Nil, empty and JSON-null evidence are all stored as NULL, the one form of
// "no evidence", in either table. UpdatedAtMS, UPN and DisplayName are
// ignored: the store stamps the time itself, and the names belong to users.
func (r *RiskSignalRepo) Save(ctx context.Context, signals []domain.RiskSignal) error {
	if len(signals) == 0 {
		return nil
	}
	seen := make(map[riskSignalKey]struct{}, len(signals))
	rows := make([]riskSignalRow, 0, len(signals))
	for _, s := range signals {
		if err := validateRiskSignal(s); err != nil {
			return err
		}
		k := riskSignalKey{s.UserID, string(s.Kind)}
		if _, dup := seen[k]; dup {
			return fmt.Errorf("%w: risk signal %q for user %d appears twice in one save", domain.ErrValidation, s.Kind, s.UserID)
		}
		seen[k] = struct{}{}
		rows = append(rows, riskSignalRow{
			UserID:   s.UserID,
			Kind:     string(s.Kind),
			State:    string(s.State),
			Code:     string(s.Code),
			Evidence: riskEvidenceColumn(s.Evidence),
		})
	}
	upsert := clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"state", "code", "evidence", "updated_at"}),
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		prev, err := riskSignalStates(tx)
		if err != nil {
			return err
		}
		for start := 0; start < len(rows); start += riskSignalBatch {
			batch := rows[start:min(start+riskSignalBatch, len(rows))]
			if err := tx.Clauses(upsert).Create(&batch).Error; err != nil {
				return err
			}
		}
		atMS := time.Now().UnixMilli()
		var flags []domain.FlagRecord
		for _, s := range signals {
			before, had := prev[riskSignalKey{s.UserID, string(s.Kind)}]
			if rec, ok := domain.RiskFlagTransition(before, had, s, atMS); ok {
				flags = append(flags, rec)
			}
		}
		if err := insertFlagRecords(tx, flags); err != nil {
			return fmt.Errorf("record risk signal transitions: %w", err)
		}
		return nil
	})
}

// riskSignalBatch is how many rows one upsert statement carries.
const riskSignalBatch = 200

// riskSignalKey is a row's primary key.
type riskSignalKey struct {
	userID int64
	kind   string
}

// riskSignalStates reads the stored state of every row, on tx and only tx
// (see Save). The whole table rather than the saved keys: it holds at most
// four rows per account, and one three-column scan is cheaper and simpler
// than an IN list chunked under each dialect's parameter limit.
func riskSignalStates(tx *gorm.DB) (map[riskSignalKey]domain.GeoState, error) {
	var rows []struct {
		UserID int64  `gorm:"column:user_id"`
		Kind   string `gorm:"column:kind"`
		State  string `gorm:"column:state"`
	}
	if err := tx.Table("risk_signals").Select("user_id, kind, state").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read risk signal states: %w", err)
	}
	out := make(map[riskSignalKey]domain.GeoState, len(rows))
	for _, row := range rows {
		out[riskSignalKey{row.UserID, row.Kind}] = domain.GeoState(row.State)
	}
	return out, nil
}

// validateRiskSignal refuses a row the store cannot hold identically on every
// dialect. Lengths are in bytes. The kinds, states and codes are ASCII by
// construction, where bytes and characters agree, and for anything else a
// byte bound is the stricter one, so nothing that passes can overflow a
// varchar anywhere; MySQL's TEXT limit is in bytes to begin with.
func validateRiskSignal(s domain.RiskSignal) error {
	switch {
	case s.Kind == "" || len(s.Kind) > riskKindWidth:
		return fmt.Errorf("%w: risk signal kind %q must be 1..%d bytes", domain.ErrValidation, s.Kind, riskKindWidth)
	case len(s.State) > riskStateWidth:
		return fmt.Errorf("%w: risk signal state %q is over %d bytes", domain.ErrValidation, s.State, riskStateWidth)
	case len(s.Code) > riskCodeWidth:
		return fmt.Errorf("%w: risk signal code %q is over %d bytes", domain.ErrValidation, s.Code, riskCodeWidth)
	case len(s.Evidence) > riskEvidenceMaxBytes:
		return fmt.Errorf("%w: risk signal %q evidence is %d bytes, over %d", domain.ErrValidation, s.Kind, len(s.Evidence), riskEvidenceMaxBytes)
	case len(s.Evidence) > 0 && !json.Valid(s.Evidence):
		// The kind, not the evidence: evidence is never echoed into an error,
		// which may reach a log.
		return fmt.Errorf("%w: risk signal %q evidence is not valid JSON", domain.ErrValidation, s.Kind)
	}
	return nil
}

// riskEvidenceColumn maps evidence to its column: NULL for nil, empty or a
// JSON null, the JSON text otherwise. A JSON null is what json.Marshal makes
// of a nil evidence pointer, so a caller that marshals without checking still
// stores "nothing" in its one form rather than as the string "null".
func riskEvidenceColumn(e json.RawMessage) *string {
	if len(e) == 0 || bytes.Equal(bytes.TrimSpace(e), []byte("null")) {
		return nil
	}
	s := string(e)
	return &s
}

// riskEvidenceFrom is the reverse, and forgiving: an empty or unparseable
// column reads as nil, the same as NULL. Save never writes either, but a row
// edited behind its back must cost only its own evidence — served verbatim,
// one malformed value would fail the admin response for every account.
func riskEvidenceFrom(col *string) json.RawMessage {
	if col == nil || *col == "" || !json.Valid([]byte(*col)) {
		return nil
	}
	return json.RawMessage(*col)
}

// List returns every row whose user still exists, with the user's UPN and
// display name, ordered by user_id then kind — the admin risk view's read.
//
// JOIN, not LEFT JOIN: a row a deleted account left is not an account an
// admin can open, and the worker purges it on its next run anyway. Whole-table
// because the view shows the fleet; the table holds at most one row per
// account per kind.
func (r *RiskSignalRepo) List(ctx context.Context) ([]domain.RiskSignal, error) {
	out, err := listRiskSignals(r.db.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list risk signals: %w", err)
	}
	return out, nil
}

// ListByUsers is List for the asked accounts only — every row, evidence and
// names included, by user_id then kind: the evidence for one page of the
// risk center's queue, or one account's drawer. Read in IN lists of
// idReadChunk ids.
func (r *RiskSignalRepo) ListByUsers(ctx context.Context, userIDs []int64) ([]domain.RiskSignal, error) {
	var out []domain.RiskSignal
	for _, chunk := range idChunks(userIDs) {
		rows, err := listRiskSignals(r.db.WithContext(ctx).Where("risk_signals.user_id IN ?", chunk))
		if err != nil {
			return nil, fmt.Errorf("list risk signals by user: %w", err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// listRiskSignals runs List's JOIN on q, which may narrow it.
func listRiskSignals(q *gorm.DB) ([]domain.RiskSignal, error) {
	var rows []struct {
		UserID      int64
		Kind        string
		State       string
		Code        string
		Evidence    *string
		UpdatedAt   int64
		UPN         string
		DisplayName string
	}
	err := q.Table("risk_signals").
		Select("risk_signals.user_id AS user_id, risk_signals.kind AS kind, risk_signals.state AS state, " +
			"risk_signals.code AS code, risk_signals.evidence AS evidence, risk_signals.updated_at AS updated_at, " +
			"users.upn AS upn, users.display_name AS display_name").
		Joins("JOIN users ON users.id = risk_signals.user_id").
		Order("risk_signals.user_id, risk_signals.kind").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.RiskSignal, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.RiskSignal{
			UserID:      row.UserID,
			Kind:        domain.RiskKind(row.Kind),
			State:       domain.GeoState(row.State),
			Code:        domain.RiskCode(row.Code),
			Evidence:    riskEvidenceFrom(row.Evidence),
			UpdatedAtMS: row.UpdatedAt,
			UPN:         row.UPN,
			DisplayName: row.DisplayName,
		})
	}
	return out, nil
}

// AttentionLevels returns the risk center's risk-signal read: the rows of
// existing accounts at attention — suspect or flagged; unknown is "cannot
// tell", which domain.RiskAttention reads as none — written at or after
// since, by user_id then kind. Four narrow columns and never the evidence,
// for GeoStreakRepo.AttentionLevels' reason. JOIN users because there is no
// foreign key; bounded by updated_at so a row the worker stopped rewriting
// (a dead loop, a skipped kind) stops counting. updated_at is unix ms, so the
// bound is an integer comparison on every dialect, and inclusive.
func (r *RiskSignalRepo) AttentionLevels(ctx context.Context, since time.Time) ([]ports.SignalAttentionRow, error) {
	var rows []struct {
		UserID    int64  `gorm:"column:user_id"`
		Kind      string `gorm:"column:kind"`
		State     string `gorm:"column:state"`
		UpdatedAt int64  `gorm:"column:updated_at"`
	}
	err := r.db.WithContext(ctx).Table("risk_signals").
		Select("risk_signals.user_id AS user_id, risk_signals.kind AS kind, risk_signals.state AS state, "+
			"risk_signals.updated_at AS updated_at").
		Joins("JOIN users ON users.id = risk_signals.user_id").
		Where("risk_signals.state IN ? AND risk_signals.updated_at >= ?",
			[]string{string(domain.GeoStateSuspect), string(domain.GeoStateFlagged)}, since.UnixMilli()).
		Order("risk_signals.user_id, risk_signals.kind").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("risk signal attention levels: %w", err)
	}
	out := make([]ports.SignalAttentionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ports.SignalAttentionRow{
			UserID: row.UserID, Kind: domain.RiskKind(row.Kind), State: domain.GeoState(row.State), UpdatedAtMS: row.UpdatedAt,
		})
	}
	return out, nil
}

// PurgeOrphans deletes the rows of accounts that no longer exist and returns
// how many it deleted. The worker runs it every refresh, so what a deleted
// account left lasts at most one run (and List hides it meanwhile).
//
// A NOT IN subquery on another table is portable as written: MySQL's
// restriction (error 1093) is on a subquery naming the table being deleted
// from, and users.id is a primary key, so NOT IN never meets a NULL.
func (r *RiskSignalRepo) PurgeOrphans(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM risk_signals WHERE user_id NOT IN (SELECT id FROM users)")
	return res.RowsAffected, res.Error
}
