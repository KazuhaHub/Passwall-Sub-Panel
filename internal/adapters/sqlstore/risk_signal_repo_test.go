package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// risk_signals holds one row per (account, signal): the latest verdict, rewritten
// every hour, never history. These tests pin what its three readers rely on —
// the admin list (every column back, deleted accounts hidden), the bell (one
// count per account, fresh rows only) and the worker's own hygiene (an upsert
// that forgets no column, NULL evidence for a verdict with nothing to show,
// orphans purged) — and that a value the store cannot hold on every dialect is
// refused before anything is written.

func newRiskSignalRepo(t *testing.T) (*RiskSignalRepo, ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewRiskSignalRepo(db), NewRepos(db).User, db
}

// createRiskUser inserts a real users row: the store JOINs users on every read,
// so a signal for an id with no row is, by design, invisible.
func createRiskUser(t *testing.T, users ports.UserRepo, n int, display string) *domain.User {
	t.Helper()
	u := &domain.User{
		UPN: fmt.Sprintf("risk-%d@example.test", n), DisplayName: display, Role: domain.RoleUser,
		SubToken: fmt.Sprintf("st-risk-%d", n), UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", 700+n),
		GroupID: 1, TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := users.Create(context.Background(), u); err != nil {
		t.Fatalf("create user %d: %v", n, err)
	}
	return u
}

func countRiskRows(t *testing.T, db *gorm.DB, where string) int64 {
	t.Helper()
	var n int64
	q := "SELECT COUNT(*) FROM risk_signals"
	if where != "" {
		q += " WHERE " + where
	}
	if err := db.Raw(q).Scan(&n).Error; err != nil {
		t.Fatalf("count risk_signals (%s): %v", where, err)
	}
	return n
}

func TestRiskSignalRepo_SaveRoundTripsEveryColumn(t *testing.T) {
	r, users, _ := newRiskSignalRepo(t)
	ctx := context.Background()
	alice := createRiskUser(t, users, 1, "Alice")
	bob := createRiskUser(t, users, 2, "Bob")

	before := time.Now().UnixMilli()
	// Saved out of order, with a writer-side UpdatedAtMS and UPN the store must
	// not take: the time is the store's own stamp, the names come from users.
	in := []domain.RiskSignal{
		{UserID: bob.ID, Kind: domain.RiskKindLoginCountry, State: domain.GeoStateSuspect, Code: domain.RiskCodeNewCountry,
			Evidence: json.RawMessage(`{"v":1,"logins":[{"cc":"JP","at_ms":1758000000000,"method":"password"}]}`)},
		{UserID: alice.ID, Kind: domain.RiskKindUsageShift, State: domain.GeoStateFlagged, Code: domain.RiskCodeSustained,
			Evidence: json.RawMessage(`{"v":1,"days":[1073741824,0,5]}`), UpdatedAtMS: 1, UPN: "forged"},
		{UserID: alice.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateIdle, Code: domain.RiskCodeNoFetches},
	}
	if err := r.Save(ctx, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	after := time.Now().UnixMilli()

	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	type key struct {
		uid  int64
		kind domain.RiskKind
	}
	wantOrder := []key{
		{alice.ID, domain.RiskKindSubSpread},
		{alice.ID, domain.RiskKindUsageShift},
		{bob.ID, domain.RiskKindLoginCountry},
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("List returned %d rows, want %d: %+v", len(got), len(wantOrder), got)
	}
	byKey := map[key]domain.RiskSignal{}
	for _, s := range in {
		byKey[key{s.UserID, s.Kind}] = s
	}
	names := map[int64][2]string{alice.ID: {alice.UPN, "Alice"}, bob.ID: {bob.UPN, "Bob"}}
	for i, g := range got {
		k := key{g.UserID, g.Kind}
		if k != wantOrder[i] {
			t.Fatalf("row %d is %+v, want %+v (ordered by user_id, kind)", i, k, wantOrder[i])
		}
		w := byKey[k]
		if g.State != w.State || g.Code != w.Code {
			t.Fatalf("row %+v: state/code = %q/%q, want %q/%q", k, g.State, g.Code, w.State, w.Code)
		}
		if string(g.Evidence) != string(w.Evidence) {
			t.Fatalf("row %+v: evidence = %q, want %q", k, g.Evidence, w.Evidence)
		}
		if (g.Evidence == nil) != (w.Evidence == nil) {
			t.Fatalf("row %+v: evidence nil = %v, want %v", k, g.Evidence == nil, w.Evidence == nil)
		}
		if g.UpdatedAtMS < before || g.UpdatedAtMS > after {
			t.Fatalf("row %+v: updated_at = %d, want the store's stamp in [%d, %d]", k, g.UpdatedAtMS, before, after)
		}
		if n := names[g.UserID]; g.UPN != n[0] || g.DisplayName != n[1] {
			t.Fatalf("row %+v: upn/display = %q/%q, want %q/%q from users", k, g.UPN, g.DisplayName, n[0], n[1])
		}
	}
}

// The worker rewrites every row every hour. An upsert changes only the columns
// it names, so a column left out of the update keeps its FIRST value forever:
// evidence from a week-old window beside today's idle verdict, or an
// updated_at that makes a live row look stale to the bell.
func TestRiskSignalRepo_UpsertRewritesEveryMutableColumn(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Save(ctx, []domain.RiskSignal{{
		UserID: u.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateFlagged, Code: domain.RiskCodeSpread,
		Evidence: json.RawMessage(`{"v":1,"provinces":[{"cc":"CN","region":"Guangdong"}]}`),
	}}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	aged := time.Now().Add(-25 * time.Hour).UnixMilli()
	if err := db.Exec("UPDATE risk_signals SET updated_at = ? WHERE user_id = ?", aged, u.ID).Error; err != nil {
		t.Fatalf("age the row: %v", err)
	}

	start := time.Now().UnixMilli()
	if err := r.Save(ctx, []domain.RiskSignal{{
		UserID: u.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateIdle, Code: domain.RiskCodeNoFetches,
	}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 — the second save must update, not insert", len(got))
	}
	g := got[0]
	if g.State != domain.GeoStateIdle || g.Code != domain.RiskCodeNoFetches {
		t.Fatalf("state/code = %q/%q, want idle/no_fetches", g.State, g.Code)
	}
	if g.Evidence != nil {
		t.Fatalf("evidence = %q, want nil — the idle verdict must not keep the flagged window's provinces", g.Evidence)
	}
	if g.UpdatedAtMS < start {
		t.Fatalf("updated_at = %d, want >= %d — the upsert must restamp the row", g.UpdatedAtMS, start)
	}
}

// A verdict with nothing to show writes NULL, not the text "null" and not an
// empty string: NULL is the one stored form of "no evidence", and it is what
// guarantees nothing outlives the window it described. A JSON null is the
// same "nothing" — it is what json.Marshal makes of a nil evidence pointer —
// so it is stored the same way rather than as a four-letter string.
func TestRiskSignalRepo_IdleWritesNullEvidence(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Save(ctx, []domain.RiskSignal{
		{UserID: u.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateIdle, Code: domain.RiskCodeNoFetches},
		{UserID: u.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateDisabled, Code: domain.RiskCodeSignalOff,
			Evidence: json.RawMessage{}},
		{UserID: u.ID, Kind: domain.RiskKindLoginCountry, State: domain.GeoStateExempt, Code: domain.RiskCodeAllowAnywhere,
			Evidence: json.RawMessage(" null\n")},
		{UserID: u.ID, Kind: domain.RiskKindUsageShift, State: domain.GeoStateClean, Code: domain.RiskCodeWithin,
			Evidence: json.RawMessage(`{"v":1}`)},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if n := countRiskRows(t, db, "evidence IS NULL"); n != 3 {
		t.Fatalf("rows with NULL evidence = %d, want 3 (nil, empty and JSON null evidence)", n)
	}
	if n := countRiskRows(t, db, "evidence IS NOT NULL"); n != 1 {
		t.Fatalf("rows with evidence = %d, want 1", n)
	}
}

// risk_signals has no foreign key to users, and deleting a user cascades
// nothing. The admin list must not show a signal for an account nobody can
// open — nor for an id that never existed.
func TestRiskSignalRepo_ListHidesOrphans(t *testing.T) {
	r, users, _ := newRiskSignalRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	const neverExisted = int64(987654)
	var rows []domain.RiskSignal
	for _, uid := range []int64{kept.ID, gone.ID, neverExisted} {
		rows = append(rows, domain.RiskSignal{UserID: uid, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver})
	}
	if err := r.Save(ctx, rows); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].UserID != kept.ID {
		t.Fatalf("List = %+v, want only user %d's row", got, kept.ID)
	}
}

// The purge is the worker's hourly cleanup of what a deleted account left
// behind. It must take exactly those rows and nothing an existing account
// owns, and say how many it took.
func TestRiskSignalRepo_PurgeOrphansDeletesOnlyOrphans(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	if err := r.Save(ctx, []domain.RiskSignal{
		{UserID: kept.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: domain.RiskCodeWithin},
		{UserID: kept.ID, Kind: domain.RiskKindUsageShift, State: domain.GeoStateSuspect, Code: domain.RiskCodeBuilding},
		{UserID: gone.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := r.PurgeOrphans(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("PurgeOrphans = %d, want 1 (the deleted account's one row)", n)
	}
	if total := countRiskRows(t, db, ""); total != 2 {
		t.Fatalf("rows left = %d, want 2 (both of the existing account's)", total)
	}
	if left := countRiskRows(t, db, fmt.Sprintf("user_id = %d", kept.ID)); left != 2 {
		t.Fatalf("existing account's rows = %d, want 2", left)
	}
	if again, err := r.PurgeOrphans(ctx); err != nil || again != 0 {
		t.Fatalf("second purge = %d, %v; want 0, nil", again, err)
	}
}

// The bell's risk_signals entry counts ACCOUNTS, not rows: one account flagged
// on two signals is one account to review. Only flagged counts (suspect is
// below the line), only rows the worker judged since the cutoff (a row it
// stopped rewriting must not keep the bell lit), and only accounts that still
// exist (the admin could not open the others).
func TestRiskSignalRepo_CountFlaggedUsersCountsDistinctFreshExistingUsers(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	twice := createRiskUser(t, users, 1, "")
	suspect := createRiskUser(t, users, 2, "")
	stale := createRiskUser(t, users, 3, "")
	deleted := createRiskUser(t, users, 4, "")
	once := createRiskUser(t, users, 5, "")
	const neverExisted = int64(987654)
	flag := func(uid int64, kind domain.RiskKind) domain.RiskSignal {
		return domain.RiskSignal{UserID: uid, Kind: kind, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver}
	}
	if err := r.Save(ctx, []domain.RiskSignal{
		flag(twice.ID, domain.RiskKindDevices),
		flag(twice.ID, domain.RiskKindSubSpread),
		{UserID: suspect.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateSuspect, Code: domain.RiskCodeOverBuilding},
		flag(stale.ID, domain.RiskKindDevices),
		flag(deleted.ID, domain.RiskKindDevices),
		flag(once.ID, domain.RiskKindUsageShift),
		flag(neverExisted, domain.RiskKindDevices),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	now := time.Now()
	if err := db.Exec("UPDATE risk_signals SET updated_at = ? WHERE user_id = ?",
		now.Add(-25*time.Hour).UnixMilli(), stale.ID).Error; err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if err := users.Delete(ctx, deleted.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := r.CountFlaggedUsers(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountFlaggedUsers: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountFlaggedUsers = %d, want 2 (the twice-flagged account once, plus the other fresh flagged account)", n)
	}
}

// saveMustRefuse asserts Save rejects the batch as a validation error and that
// NOTHING was written — not even the valid row ahead of the bad one.
func saveMustRefuse(t *testing.T, r *RiskSignalRepo, db *gorm.DB, rows []domain.RiskSignal) {
	t.Helper()
	err := r.Save(context.Background(), rows)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Save error = %v, want domain.ErrValidation", err)
	}
	if n := countRiskRows(t, db, ""); n != 0 {
		t.Fatalf("rows written = %d, want 0 — a refused batch must write nothing", n)
	}
}

func validRiskRow(uid int64) domain.RiskSignal {
	return domain.RiskSignal{UserID: uid, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: domain.RiskCodeWithin,
		Evidence: json.RawMessage(`{"v":1}`)}
}

// The kind is half the primary key. An empty one would be a row no reader can
// name, and a second empty-kind signal for the same account would overwrite it.
func TestRiskSignalRepo_RejectsAnEmptyKind(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	u := createRiskUser(t, users, 1, "")
	bad := validRiskRow(u.ID)
	bad.Kind = ""
	saveMustRefuse(t, r, db, []domain.RiskSignal{validRiskRow(u.ID), bad})
}

// The admin API serves evidence as a raw JSON value, so one malformed row would
// fail the whole response for every account. Refused at the door instead.
func TestRiskSignalRepo_RejectsInvalidEvidenceJSON(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	u := createRiskUser(t, users, 1, "")
	bad := validRiskRow(u.ID)
	bad.Kind = domain.RiskKindUsageShift
	bad.Evidence = json.RawMessage(`{"v":1,`)
	saveMustRefuse(t, r, db, []domain.RiskSignal{validRiskRow(u.ID), bad})
}

// A value wider than its column is refused on every dialect alike. Left to the
// database, SQLite would store it, PostgreSQL would fail the statement and
// MySQL would do either depending on its SQL mode — the same batch succeeding
// on one install and failing on another.
func TestRiskSignalRepo_RejectsValuesThatDoNotFitTheirColumns(t *testing.T) {
	// A JSON string literal of exactly n bytes, quotes included.
	jsonOf := func(n int) json.RawMessage { return json.RawMessage(`"` + strings.Repeat("a", n-2) + `"`) }
	for _, tc := range []struct {
		name string
		edit func(*domain.RiskSignal)
	}{
		{"kind over 24 bytes", func(s *domain.RiskSignal) { s.Kind = domain.RiskKind(strings.Repeat("k", 25)) }},
		{"state over 16 bytes", func(s *domain.RiskSignal) { s.State = domain.GeoState(strings.Repeat("s", 17)) }},
		{"code over 32 bytes", func(s *domain.RiskSignal) { s.Code = domain.RiskCode(strings.Repeat("c", 33)) }},
		{"evidence over 65535 bytes", func(s *domain.RiskSignal) { s.Evidence = jsonOf(65536) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, users, db := newRiskSignalRepo(t)
			u := createRiskUser(t, users, 1, "")
			bad := validRiskRow(u.ID)
			bad.Kind = domain.RiskKindSubSpread
			tc.edit(&bad)
			saveMustRefuse(t, r, db, []domain.RiskSignal{validRiskRow(u.ID), bad})
		})
	}

	// Exactly at every limit is stored whole, on every dialect: the bounds are
	// the columns' own, not a stricter guess.
	t.Run("at the limits", func(t *testing.T) {
		r, users, _ := newRiskSignalRepo(t)
		u := createRiskUser(t, users, 1, "")
		full := domain.RiskSignal{
			UserID: u.ID, Kind: domain.RiskKind(strings.Repeat("k", 24)),
			State: domain.GeoState(strings.Repeat("s", 16)), Code: domain.RiskCode(strings.Repeat("c", 32)),
			Evidence: jsonOf(65535),
		}
		if err := r.Save(context.Background(), []domain.RiskSignal{full}); err != nil {
			t.Fatalf("save at the limits: %v", err)
		}
		got, err := r.List(context.Background())
		if err != nil || len(got) != 1 {
			t.Fatalf("list = %d rows, %v; want 1", len(got), err)
		}
		g := got[0]
		if g.Kind != full.Kind || g.State != full.State || g.Code != full.Code || string(g.Evidence) != string(full.Evidence) {
			t.Fatalf("a value at its column's limit did not come back whole")
		}
	})
}

// One batch naming the same (account, kind) twice is a worker bug, and the
// dialects disagree about it: PostgreSQL refuses an upsert that touches one
// row twice, SQLite and MySQL quietly keep the last. Refused on all three.
func TestRiskSignalRepo_RejectsADuplicateKey(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	u := createRiskUser(t, users, 1, "")
	second := validRiskRow(u.ID)
	second.State, second.Code = domain.GeoStateFlagged, domain.RiskCodeOver
	saveMustRefuse(t, r, db, []domain.RiskSignal{validRiskRow(u.ID), second})
}

// The hourly save carries one row per account per signal, so a fleet's worth
// spans several 200-row statements. Every one of them lands.
func TestRiskSignalRepo_SaveSpansBatches(t *testing.T) {
	r, _, db := newRiskSignalRepo(t)
	rows := make([]domain.RiskSignal, 0, 450)
	for uid := int64(1); uid <= 450; uid++ {
		rows = append(rows, domain.RiskSignal{UserID: uid, Kind: domain.RiskKindUsageShift, State: domain.GeoStateIdle, Code: domain.RiskCodeNoUsage})
	}
	if err := r.Save(context.Background(), rows); err != nil {
		t.Fatalf("save: %v", err)
	}
	if n := countRiskRows(t, db, ""); n != 450 {
		t.Fatalf("rows = %d, want 450", n)
	}
}

// Save refuses anything that is not JSON, but a row can still be edited behind
// the store's back. One such row must not fail the admin list for every
// account: it reads as "no evidence", the same as NULL.
func TestRiskSignalRepo_ListReadsUnusableEvidenceAsNull(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Save(ctx, []domain.RiskSignal{
		{UserID: u.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1}`)},
		{UserID: u.ID, Kind: domain.RiskKindUsageShift, State: domain.GeoStateSuspect, Code: domain.RiskCodeBuilding, Evidence: json.RawMessage(`{"v":1}`)},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	for kind, raw := range map[domain.RiskKind]string{domain.RiskKindDevices: "", domain.RiskKindUsageShift: `{"v":1,`} {
		if err := db.Exec("UPDATE risk_signals SET evidence = ? WHERE user_id = ? AND kind = ?", raw, u.ID, string(kind)).Error; err != nil {
			t.Fatalf("corrupt %s: %v", kind, err)
		}
	}
	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	for _, g := range got {
		if g.Evidence != nil {
			t.Fatalf("%s: evidence = %q, want nil", g.Kind, g.Evidence)
		}
		if g.State == "" {
			t.Fatalf("%s: the rest of the row was lost with the evidence", g.Kind)
		}
	}
}

// Save's width checks are only as good as their agreement with the DDL. A
// column widened without its constant would refuse values the table holds;
// one narrowed without it would let a value through to be treated three ways
// by three dialects — exactly what the check exists to stop.
func TestRiskSignalRow_WidthsMatchTheColumns(t *testing.T) {
	s, err := schema.Parse(&riskSignalRow{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse riskSignalRow: %v", err)
	}
	for column, want := range map[string]int{"kind": riskKindWidth, "state": riskStateWidth, "code": riskCodeWidth} {
		f := s.LookUpField(column)
		if f == nil {
			t.Fatalf("risk_signals has no %s column", column)
		}
		if f.Size != want {
			t.Fatalf("risk_signals.%s is size %d, Save checks %d", column, f.Size, want)
		}
	}
	f := s.LookUpField("evidence")
	if f == nil {
		t.Fatal("risk_signals has no evidence column")
	}
	if f.DataType != "text" {
		t.Fatalf("risk_signals.evidence has type %q, want text (riskEvidenceMaxBytes is TEXT's capacity)", f.DataType)
	}
}

// flagRowsInOrder reads every flag record in the order it was written.
func flagRowsInOrder(t *testing.T, db *gorm.DB) []flagRecordRow {
	t.Helper()
	var rows []flagRecordRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("read flag_records: %v", err)
	}
	return rows
}

// The risk worker never reads its own rows (risk.Deps.Store is Save and
// PurgeOrphans), so the change of attention level is found where the
// previous state still is: inside Save, before the upsert overwrites it.
// Each save records exactly the (account, signal) pairs whose level moved,
// each with the verdict's state and code, the level it moved from, and the
// evidence it was judged from as params — byte for byte what risk_signals
// stores.
func TestRiskSignalRepo_SaveRecordsAttentionTransitions(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	a := createRiskUser(t, users, 1, "")
	b := createRiskUser(t, users, 2, "")
	devicesEv := json.RawMessage(`{"v":1,"devices":[{"label":"clash-verge","id4":"ab12"}]}`)
	loginEv := json.RawMessage(`{"v":1,"logins":[{"cc":"JP","at_ms":1758000000000,"method":"password"}]}`)

	save := func(signals ...domain.RiskSignal) (int64, int64) {
		t.Helper()
		before := time.Now().UnixMilli()
		if err := r.Save(ctx, signals); err != nil {
			t.Fatalf("save: %v", err)
		}
		return before, time.Now().UnixMilli()
	}
	lo1, hi1 := save(
		domain.RiskSignal{UserID: a.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateSuspect, Code: domain.RiskCodeOverBuilding, Evidence: devicesEv},
		domain.RiskSignal{UserID: a.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateClean, Code: domain.RiskCodeWithin, Evidence: json.RawMessage(`{"v":1}`)},
		domain.RiskSignal{UserID: b.ID, Kind: domain.RiskKindLoginCountry, State: domain.GeoStateFlagged, Code: domain.RiskCodeNewCountry, Evidence: loginEv},
	)
	save(
		domain.RiskSignal{UserID: a.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver, Evidence: devicesEv},
		domain.RiskSignal{UserID: a.ID, Kind: domain.RiskKindSubSpread, State: domain.GeoStateClean, Code: domain.RiskCodeWithin, Evidence: json.RawMessage(`{"v":1}`)},
		domain.RiskSignal{UserID: b.ID, Kind: domain.RiskKindLoginCountry, State: domain.GeoStateUnknown, Code: domain.RiskCodeGeoUnavailable},
	)
	save(
		domain.RiskSignal{UserID: a.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateIdle, Code: domain.RiskCodeNoFetches},
	)

	type rec struct {
		user          int64
		source, event string
		level, prev   string
		state, code   string
		params        string
		paramsNull    bool
	}
	var got []rec
	rows := flagRowsInOrder(t, db)
	for _, row := range rows {
		g := rec{user: row.UserID, source: row.Source, event: row.Event, level: row.Level, prev: row.PrevLevel,
			state: row.State, code: row.Code, paramsNull: row.Params == nil}
		if row.Params != nil {
			g.params = *row.Params
		}
		got = append(got, g)
	}
	want := []rec{
		{a.ID, "devices", "enter_suspect", "suspect", "", "suspect", "over_building", string(devicesEv), false},
		{b.ID, "login_country", "enter_flagged", "flagged", "", "flagged", "new_country", string(loginEv), false},
		{a.ID, "devices", "enter_flagged", "flagged", "suspect", "flagged", "over", string(devicesEv), false},
		{b.ID, "login_country", "leave_flagged", "", "flagged", "unknown", "geo_unavailable", "", true},
		{a.ID, "devices", "leave_flagged", "", "flagged", "idle", "no_fetches", "", true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("flag records:\n got %+v\nwant %+v", got, want)
	}
	if at := rows[0].AtMS; at < lo1 || at > hi1 {
		t.Fatalf("first record at %d, want the save's time in [%d, %d]", at, lo1, hi1)
	}
}

// idle ↔ clean, unknown ↔ idle and an unchanged suspect are not changes of
// attention: the bell never showed them. A history full of them would bury
// the ones that matter, so none is recorded — and a first-ever verdict below
// suspect has no attention to have entered.
func TestRiskSignalRepo_SaveRecordsNothingForIdleCleanChurn(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	for i, states := range [][2]domain.GeoState{
		{domain.GeoStateIdle, domain.GeoStateSuspect},
		{domain.GeoStateClean, domain.GeoStateSuspect},
		{domain.GeoStateIdle, domain.GeoStateSuspect},
		{domain.GeoStateUnknown, domain.GeoStateSuspect},
		{domain.GeoStateDisabled, domain.GeoStateSuspect},
		{domain.GeoStateClean, domain.GeoStateSuspect},
	} {
		if err := r.Save(ctx, []domain.RiskSignal{
			{UserID: u.ID, Kind: domain.RiskKindUsageShift, State: states[0], Code: domain.RiskCodeWithin},
			{UserID: u.ID, Kind: domain.RiskKindDevices, State: states[1], Code: domain.RiskCodeOverBuilding},
		}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	rows := flagRowsInOrder(t, db)
	if len(rows) != 1 || rows[0].Event != string(domain.FlagEnterSuspect) || rows[0].Source != string(domain.RiskKindDevices) {
		t.Fatalf("flag records = %+v; want only the devices signal's first enter_suspect", rows)
	}
}

// The upsert and the records of what it changed are one transaction. If the
// records cannot be written the verdicts are not either — otherwise the next
// run would read the new state as the previous one and the change would
// never be recorded at all.
func TestRiskSignalRepo_SaveRollsBackBothTablesTogether(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Save(ctx, []domain.RiskSignal{
		{UserID: u.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: domain.RiskCodeWithin},
	}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("test:fail_flag_records", func(tx *gorm.DB) {
		if tx.Statement.Table == "flag_records" {
			_ = tx.AddError(errors.New("flag store refused"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	err := r.Save(ctx, []domain.RiskSignal{
		{UserID: u.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1}`)},
	})
	if err == nil {
		t.Fatal("Save succeeded although its flag record was refused")
	}
	got, lerr := r.List(ctx)
	if lerr != nil || len(got) != 1 {
		t.Fatalf("list = %d rows, %v", len(got), lerr)
	}
	if got[0].State != domain.GeoStateClean {
		t.Fatalf("stored state %s after the failed save, want the previous clean — the upsert must roll back with the records", got[0].State)
	}
	if n := countFlagRows(t, db, ""); n != 0 {
		t.Fatalf("flag records = %d, want 0", n)
	}
}

// SQLite has ONE connection (conn.go), and the transaction holds it. A
// statement of Save issued on the store's handle instead of the
// transaction's would wait for that connection until its context gave up —
// a worker that hangs every run. Run on a one-connection pool with a
// bounded context, a save that transitions both reads, upserts and inserts:
// it finishes, or one of its statements left the transaction.
func TestRiskSignalRepo_SaveUsesOnlyTheTransaction(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	u := createRiskUser(t, users, 1, "")
	for i, state := range []domain.GeoState{domain.GeoStateSuspect, domain.GeoStateFlagged, domain.GeoStateClean} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := r.Save(ctx, []domain.RiskSignal{{UserID: u.ID, Kind: domain.RiskKindDevices, State: state, Code: domain.RiskCodeOver}})
		cancel()
		if err != nil {
			t.Fatalf("save %d on a one-connection pool: %v — a statement ran outside the transaction", i, err)
		}
	}
	if n := countFlagRows(t, db, ""); n != 3 {
		t.Fatalf("flag records = %d, want 3 (enter suspect, enter flagged, leave flagged)", n)
	}
}

// ---- the risk center's evidence-free reads ----

// AttentionLevels is the queue's risk read: rows at attention (suspect or
// flagged — unknown is "cannot tell", not attention) of existing accounts,
// written at or after since, by account then kind, without the evidence.
// The boundary is inclusive, the bell's comparison.
func TestRiskSignalRepo_AttentionLevelsIsSuspectOrFlaggedAndFresh(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	since := time.Now().Add(-time.Hour)
	a := createRiskUser(t, users, 1, "")
	clean := createRiskUser(t, users, 2, "")
	unknown := createRiskUser(t, users, 3, "")
	stale := createRiskUser(t, users, 4, "")
	boundary := createRiskUser(t, users, 5, "")
	gone := createRiskUser(t, users, 6, "")
	sig := func(uid int64, kind domain.RiskKind, state domain.GeoState) domain.RiskSignal {
		return domain.RiskSignal{UserID: uid, Kind: kind, State: state, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1}`)}
	}
	if err := r.Save(ctx, []domain.RiskSignal{
		sig(a.ID, domain.RiskKindSubSpread, domain.GeoStateSuspect),
		sig(a.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		sig(a.ID, domain.RiskKindUsageShift, domain.GeoStateClean),
		sig(clean.ID, domain.RiskKindUsageShift, domain.GeoStateClean),
		sig(unknown.ID, domain.RiskKindLoginCountry, domain.GeoStateUnknown),
		sig(stale.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		sig(boundary.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		sig(gone.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	for uid, ms := range map[int64]int64{stale.ID: since.UnixMilli() - 1, boundary.ID: since.UnixMilli()} {
		if err := db.Exec("UPDATE risk_signals SET updated_at = ? WHERE user_id = ?", ms, uid).Error; err != nil {
			t.Fatalf("set updated_at: %v", err)
		}
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	stamped := map[riskSignalKey]int64{}
	all, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, s := range all {
		stamped[riskSignalKey{s.UserID, string(s.Kind)}] = s.UpdatedAtMS
	}

	got, err := r.AttentionLevels(ctx, since)
	if err != nil {
		t.Fatalf("AttentionLevels: %v", err)
	}
	row := func(uid int64, kind domain.RiskKind, state domain.GeoState) ports.SignalAttentionRow {
		return ports.SignalAttentionRow{UserID: uid, Kind: kind, State: state, UpdatedAtMS: stamped[riskSignalKey{uid, string(kind)}]}
	}
	want := []ports.SignalAttentionRow{
		row(a.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		row(a.ID, domain.RiskKindSubSpread, domain.GeoStateSuspect),
		row(boundary.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AttentionLevels = %+v\nwant %+v", got, want)
	}
	if got[2].UpdatedAtMS != since.UnixMilli() {
		t.Fatalf("the boundary row reads updated_at %d, want %d", got[2].UpdatedAtMS, since.UnixMilli())
	}
}

// ListByUsers is the page's evidence: every row of each asked existing
// account, with its evidence and names, by account then kind. Asked in
// chunks of 500.
func TestRiskSignalRepo_ListByUsers(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	ctx := context.Background()
	a := createRiskUser(t, users, 1, "Alice")
	b := createRiskUser(t, users, 2, "Bob")
	notAsked := createRiskUser(t, users, 3, "")
	gone := createRiskUser(t, users, 4, "")
	sig := func(uid int64, kind domain.RiskKind, state domain.GeoState) domain.RiskSignal {
		return domain.RiskSignal{UserID: uid, Kind: kind, State: state, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1,"n":2}`)}
	}
	if err := r.Save(ctx, []domain.RiskSignal{
		sig(b.ID, domain.RiskKindDevices, domain.GeoStateClean),
		sig(a.ID, domain.RiskKindUsageShift, domain.GeoStateSuspect),
		sig(a.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		sig(notAsked.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
		sig(gone.ID, domain.RiskKindDevices, domain.GeoStateFlagged),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	all, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var want []domain.RiskSignal
	for _, s := range all {
		if s.UserID == a.ID || s.UserID == b.ID {
			want = append(want, s)
		}
	}
	if len(want) != 3 || want[0].UPN == "" || want[0].DisplayName != "Alice" || want[0].Evidence == nil {
		t.Fatalf("fixture: %+v", want)
	}

	got, err := r.ListByUsers(ctx, []int64{b.ID, gone.ID, a.ID})
	if err != nil {
		t.Fatalf("ListByUsers: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListByUsers = %+v\nwant %+v", got, want)
	}

	reads := countReads(t, db, "risk_signals")
	got, err = r.ListByUsers(ctx, chunkSpanningIDs(a.ID, b.ID, gone.ID))
	if err != nil {
		t.Fatalf("ListByUsers over 1100 ids: %v", err)
	}
	if !reflect.DeepEqual(got, want) || reads() != 3 {
		t.Fatalf("ListByUsers over 1100 ids = %d rows in %d reads, want %d in 3", len(got), reads(), len(want))
	}
}
