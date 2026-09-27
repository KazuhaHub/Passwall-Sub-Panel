package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// flag_records is the append-only history of attention changes: one row per
// account per change of level, with the code and the address-free params the
// admin UI renders it from. These tests pin what its reader and its producers
// rely on — every column back, the store's own stamp when none was given; a
// batch refused whole when one record cannot be held the same on every
// dialect; filters that narrow and an order that reads as time; a deleted
// account's records hidden at once and purged by the hourly cleanup; and a
// prune by the record's time.

func newFlagRecordRepo(t *testing.T) (*FlagRecordRepo, ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewFlagRecordRepo(db), NewRepos(db).User, db
}

func countFlagRows(t *testing.T, db *gorm.DB, where string) int64 {
	t.Helper()
	var n int64
	q := "SELECT COUNT(*) FROM flag_records"
	if where != "" {
		q += " WHERE " + where
	}
	if err := db.Raw(q).Scan(&n).Error; err != nil {
		t.Fatalf("count flag_records (%s): %v", where, err)
	}
	return n
}

func geoFlag(uid int64, ev domain.FlagEvent, level, prev domain.FlagLevel, at int64) domain.FlagRecord {
	return domain.FlagRecord{
		UserID: uid, Source: domain.FlagSourceGeo, Event: ev, Level: level, PrevLevel: prev,
		State: domain.GeoState(level), Code: "suspect", Params: json.RawMessage(`{"over":1}`), AtMS: at,
	}
}

func TestFlagRecordRepo_AppendRoundTrips(t *testing.T) {
	r, users, _ := newFlagRecordRepo(t)
	ctx := context.Background()
	alice := createRiskUser(t, users, 1, "Alice")

	before := time.Now().UnixMilli()
	in := []domain.FlagRecord{
		{
			UserID: alice.ID, Source: domain.FlagSourceGeo, Event: domain.FlagEnterFlagged,
			Level: domain.FlagLevelFlagged, PrevLevel: domain.FlagLevelSuspect,
			State: domain.GeoStateFlagged, Code: "flagged_sustained",
			Params: json.RawMessage(`{"over":3,"under":0,"ban_over":0,"flagged":true,"tier":"country","evidence":{"v":3}}`),
			AtMS:   1_758_000_000_000,
			// Read side only: the store keeps no name, so these are ignored.
			UPN: "forged", DisplayName: "Forged", ID: 999,
		},
		// No time and no params: the store stamps now and stores NULL.
		{UserID: alice.ID, Source: domain.FlagSourceGeoAuto, Event: domain.FlagAutoLiftedAdmin,
			Level: domain.FlagLevelNone, PrevLevel: domain.FlagLevelSuspended, Code: "admin_resume"},
		// A JSON null is "no params" too, stored in the one form.
		{UserID: alice.ID, Source: string(domain.RiskKindDevices), Event: domain.FlagLeaveFlagged,
			Level: domain.FlagLevelNone, PrevLevel: domain.FlagLevelFlagged, State: domain.GeoStateIdle,
			Code: string(domain.RiskCodeNoFetches), Params: json.RawMessage(" null "), AtMS: 1_700_000_000_000},
	}
	if err := r.Append(ctx, in); err != nil {
		t.Fatalf("append: %v", err)
	}
	after := time.Now().UnixMilli()

	got, total, err := r.List(ctx, ports.FlagRecordFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("list = %d rows (total %d), want 3", len(got), total)
	}
	// Newest first: the stamped one, then the two given times.
	stamped, first, last := got[0], got[1], got[2]
	if stamped.AtMS < before || stamped.AtMS > after {
		t.Fatalf("unstamped record at %d, want the store's now in [%d, %d]", stamped.AtMS, before, after)
	}
	if stamped.Params != nil {
		t.Fatalf("nil params read back as %q, want nil", stamped.Params)
	}
	if last.Params != nil {
		t.Fatalf("JSON-null params read back as %q, want nil", last.Params)
	}
	want := in[0]
	want.UPN, want.DisplayName = alice.UPN, "Alice"
	want.ID = first.ID
	if first.ID <= 0 || first.ID == 999 {
		t.Fatalf("id = %d, want the store's own", first.ID)
	}
	gotParams, wantParams := string(first.Params), string(want.Params)
	first.Params, want.Params = nil, nil
	if fmt.Sprintf("%+v", first) != fmt.Sprintf("%+v", want) {
		t.Fatalf("round trip = %+v\nwant         %+v", first, want)
	}
	if gotParams != wantParams {
		t.Fatalf("params = %s, want verbatim %s", gotParams, wantParams)
	}
	if stamped.Source != domain.FlagSourceGeoAuto || stamped.Event != domain.FlagAutoLiftedAdmin ||
		stamped.PrevLevel != domain.FlagLevelSuspended || stamped.Level != domain.FlagLevelNone || stamped.State != "" {
		t.Fatalf("stamped record = %+v", stamped)
	}
}

// A record the columns cannot hold the same on every dialect is refused, and
// the batch with it: SQLite would store an over-long value, PostgreSQL would
// refuse it and MySQL would truncate or refuse it by SQL mode. Params are
// served verbatim as a raw JSON value, so a record whose params are not JSON
// would fail the admin's whole page. A record with no source or no event
// could never be filtered to, or said.
func TestFlagRecordRepo_RejectsOverlongOrInvalid(t *testing.T) {
	jsonOf := func(n int) json.RawMessage { return json.RawMessage(`"` + strings.Repeat("a", n-2) + `"`) }
	for _, tc := range []struct {
		name string
		edit func(*domain.FlagRecord)
	}{
		{"empty source", func(f *domain.FlagRecord) { f.Source = "" }},
		{"source over 24 bytes", func(f *domain.FlagRecord) { f.Source = strings.Repeat("s", 25) }},
		{"empty event", func(f *domain.FlagRecord) { f.Event = "" }},
		{"event over 24 bytes", func(f *domain.FlagRecord) { f.Event = domain.FlagEvent(strings.Repeat("e", 25)) }},
		{"level over 16 bytes", func(f *domain.FlagRecord) { f.Level = domain.FlagLevel(strings.Repeat("l", 17)) }},
		{"previous level over 16 bytes", func(f *domain.FlagRecord) { f.PrevLevel = domain.FlagLevel(strings.Repeat("p", 17)) }},
		{"state over 16 bytes", func(f *domain.FlagRecord) { f.State = domain.GeoState(strings.Repeat("t", 17)) }},
		{"code over 32 bytes", func(f *domain.FlagRecord) { f.Code = strings.Repeat("c", 33) }},
		{"params over 65535 bytes", func(f *domain.FlagRecord) { f.Params = jsonOf(65536) }},
		{"params not JSON", func(f *domain.FlagRecord) { f.Params = json.RawMessage(`{"over":`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, users, db := newFlagRecordRepo(t)
			u := createRiskUser(t, users, 1, "")
			bad := geoFlag(u.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 2)
			tc.edit(&bad)
			err := r.Append(context.Background(), []domain.FlagRecord{
				geoFlag(u.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1), bad,
			})
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Append error = %v, want domain.ErrValidation", err)
			}
			if n := countFlagRows(t, db, ""); n != 0 {
				t.Fatalf("rows written = %d, want 0 — a refused batch writes nothing", n)
			}
		})
	}

	t.Run("at the limits", func(t *testing.T) {
		r, users, _ := newFlagRecordRepo(t)
		u := createRiskUser(t, users, 1, "")
		full := domain.FlagRecord{
			UserID: u.ID, Source: strings.Repeat("s", 24), Event: domain.FlagEvent(strings.Repeat("e", 24)),
			Level: domain.FlagLevel(strings.Repeat("l", 16)), PrevLevel: domain.FlagLevel(strings.Repeat("p", 16)),
			State: domain.GeoState(strings.Repeat("t", 16)), Code: strings.Repeat("c", 32), Params: jsonOf(65535), AtMS: 5,
		}
		if err := r.Append(context.Background(), []domain.FlagRecord{full}); err != nil {
			t.Fatalf("append at the limits: %v", err)
		}
		got, _, err := r.List(context.Background(), ports.FlagRecordFilter{})
		if err != nil || len(got) != 1 {
			t.Fatalf("list = %d rows, %v; want 1", len(got), err)
		}
		g := got[0]
		if g.Source != full.Source || g.Event != full.Event || g.Level != full.Level || g.PrevLevel != full.PrevLevel ||
			g.State != full.State || g.Code != full.Code || string(g.Params) != string(full.Params) {
			t.Fatal("a value at its column's limit did not come back whole")
		}
	})
}

// A history reads newest first, and records written in the same millisecond
// in the order they were written (the later id first). Every filter narrows
// the list and its total together; a level of "cleared" is the records that
// moved to no attention; an unknown source, level or event is refused rather
// than answered with nothing.
func TestFlagRecordRepo_ListFiltersNewestFirst(t *testing.T) {
	r, users, _ := newFlagRecordRepo(t)
	ctx := context.Background()
	a := createRiskUser(t, users, 1, "")
	b := createRiskUser(t, users, 2, "")
	recs := []domain.FlagRecord{
		geoFlag(a.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1000),
		geoFlag(a.ID, domain.FlagEnterFlagged, domain.FlagLevelFlagged, domain.FlagLevelSuspect, 2000),
		domain.GeoAutoFlag(a.ID, domain.FlagAutoSuspended, "country", map[string]any{"tier": "country"}, time.UnixMilli(2000)),
		domain.GeoAutoFlag(a.ID, domain.FlagAutoLiftedExpiry, "expired", nil, time.UnixMilli(3000)),
		geoFlag(a.ID, domain.FlagLeaveFlagged, domain.FlagLevelNone, domain.FlagLevelFlagged, 4000),
		{UserID: b.ID, Source: string(domain.RiskKindUsageShift), Event: domain.FlagEnterFlagged,
			Level: domain.FlagLevelFlagged, PrevLevel: domain.FlagLevelNone, State: domain.GeoStateFlagged,
			Code: string(domain.RiskCodeSustained), AtMS: 2500},
	}
	if err := r.Append(ctx, recs); err != nil {
		t.Fatalf("append: %v", err)
	}
	type row struct {
		user int64
		ev   domain.FlagEvent
	}
	list := func(f ports.FlagRecordFilter) ([]row, int64) {
		t.Helper()
		got, total, err := r.List(ctx, f)
		if err != nil {
			t.Fatalf("list %+v: %v", f, err)
		}
		out := make([]row, 0, len(got))
		for _, g := range got {
			out = append(out, row{g.UserID, g.Event})
		}
		return out, total
	}
	eq := func(name string, got []row, total int64, want []row, wantTotal int64) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) || total != wantTotal {
			t.Errorf("%s: %v (total %d), want %v (total %d)", name, got, total, want, wantTotal)
		}
	}

	all, total := list(ports.FlagRecordFilter{})
	eq("all", all, total, []row{
		{a.ID, domain.FlagLeaveFlagged},
		{a.ID, domain.FlagAutoLiftedExpiry},
		{b.ID, domain.FlagEnterFlagged},
		// Same millisecond: the later write first.
		{a.ID, domain.FlagAutoSuspended},
		{a.ID, domain.FlagEnterFlagged},
		{a.ID, domain.FlagEnterSuspect},
	}, 6)

	uid := b.ID
	got, total := list(ports.FlagRecordFilter{UserID: &uid})
	eq("user", got, total, []row{{b.ID, domain.FlagEnterFlagged}}, 1)

	got, total = list(ports.FlagRecordFilter{Source: domain.FlagSourceGeoAuto})
	eq("source", got, total, []row{{a.ID, domain.FlagAutoLiftedExpiry}, {a.ID, domain.FlagAutoSuspended}}, 2)

	got, total = list(ports.FlagRecordFilter{Level: "flagged"})
	eq("level flagged", got, total, []row{{b.ID, domain.FlagEnterFlagged}, {a.ID, domain.FlagEnterFlagged}}, 2)

	got, total = list(ports.FlagRecordFilter{Level: "suspended"})
	eq("level suspended", got, total, []row{{a.ID, domain.FlagAutoSuspended}}, 1)

	got, total = list(ports.FlagRecordFilter{Level: ports.FlagLevelCleared})
	eq("level cleared", got, total, []row{{a.ID, domain.FlagLeaveFlagged}, {a.ID, domain.FlagAutoLiftedExpiry}}, 2)

	got, total = list(ports.FlagRecordFilter{Event: string(domain.FlagEnterSuspect)})
	eq("event", got, total, []row{{a.ID, domain.FlagEnterSuspect}}, 1)

	since, until := time.UnixMilli(2000), time.UnixMilli(3000)
	got, total = list(ports.FlagRecordFilter{Since: &since, Until: &until})
	eq("since and until, inclusive", got, total, []row{
		{a.ID, domain.FlagAutoLiftedExpiry}, {b.ID, domain.FlagEnterFlagged},
		{a.ID, domain.FlagAutoSuspended}, {a.ID, domain.FlagEnterFlagged},
	}, 4)

	// Paged: the page is the slice of the order, the total the whole set.
	got, total = list(ports.FlagRecordFilter{Pagination: ports.Pagination{Page: 2, PageSize: 4}})
	eq("page 2 of 4", got, total, []row{{a.ID, domain.FlagEnterFlagged}, {a.ID, domain.FlagEnterSuspect}}, 6)
	// An admin's sort does not reorder a history.
	got, total = list(ports.FlagRecordFilter{Pagination: ports.Pagination{PageSize: 2, SortBy: "user_id", SortDir: "asc"}})
	eq("sort ignored", got, total, []row{{a.ID, domain.FlagLeaveFlagged}, {a.ID, domain.FlagAutoLiftedExpiry}}, 6)

	for _, f := range []ports.FlagRecordFilter{{Source: "geos"}, {Level: "none"}, {Event: "entered_flagged"}} {
		if _, _, err := r.List(ctx, f); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("List(%+v) error = %v, want domain.ErrValidation", f, err)
		}
	}
}

// flag_records has no foreign key to users. A deleted account's records are
// not somebody the admin can open, so every read hides them from the moment
// of the delete — nor is an id that never existed shown.
func TestFlagRecordRepo_ListHidesOrphans(t *testing.T) {
	r, users, _ := newFlagRecordRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	const neverExisted = int64(987654)
	var recs []domain.FlagRecord
	for _, uid := range []int64{kept.ID, gone.ID, neverExisted} {
		recs = append(recs, geoFlag(uid, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1))
	}
	if err := r.Append(ctx, recs); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	got, total, err := r.List(ctx, ports.FlagRecordFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].UserID != kept.ID {
		t.Fatalf("List = %+v (total %d), want only user %d's record", got, total, kept.ID)
	}
}

// Retention is by the record's own time: DeleteBefore removes exactly the
// records strictly older than the cutoff, and says how many.
func TestFlagRecordRepo_DeleteBefore(t *testing.T) {
	r, users, db := newFlagRecordRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Append(ctx, []domain.FlagRecord{
		geoFlag(u.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1000),
		geoFlag(u.ID, domain.FlagEnterFlagged, domain.FlagLevelFlagged, domain.FlagLevelSuspect, 1999),
		geoFlag(u.ID, domain.FlagLeaveFlagged, domain.FlagLevelNone, domain.FlagLevelFlagged, 2000),
		geoFlag(u.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 3000),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	n, err := r.DeleteBefore(ctx, time.UnixMilli(2000))
	if err != nil || n != 2 {
		t.Fatalf("DeleteBefore = %d, %v; want 2, nil", n, err)
	}
	if left := countFlagRows(t, db, "at_ms >= 2000"); left != 2 || countFlagRows(t, db, "") != 2 {
		t.Fatalf("rows left = %d at or after the cutoff, %d in all; want 2 and 2", left, countFlagRows(t, db, ""))
	}
}

// The hourly cleanup deletes what deleted accounts left, and nothing an
// existing account owns: flag records keep no name, so a deleted account's
// history has nobody to belong to.
func TestFlagRecordRepo_PurgeOrphans(t *testing.T) {
	r, users, db := newFlagRecordRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	if err := r.Append(ctx, []domain.FlagRecord{
		geoFlag(kept.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1),
		geoFlag(kept.ID, domain.FlagEnterFlagged, domain.FlagLevelFlagged, domain.FlagLevelSuspect, 2),
		geoFlag(gone.ID, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, 1),
		geoFlag(gone.ID, domain.FlagEnterFlagged, domain.FlagLevelFlagged, domain.FlagLevelSuspect, 2),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	n, err := r.PurgeOrphans(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PurgeOrphans = %d, %v; want 2, nil", n, err)
	}
	if left := countFlagRows(t, db, fmt.Sprintf("user_id = %d", kept.ID)); left != 2 || countFlagRows(t, db, "") != 2 {
		t.Fatalf("rows left: %d of the existing account's, %d in all; want 2 and 2", left, countFlagRows(t, db, ""))
	}
	if again, err := r.PurgeOrphans(ctx); err != nil || again != 0 {
		t.Fatalf("second purge = %d, %v; want 0, nil", again, err)
	}
}

// A fleet's worth of records in one batch spans several statements, and all
// of them land.
func TestFlagRecordRepo_AppendSpansBatches(t *testing.T) {
	r, _, db := newFlagRecordRepo(t)
	recs := make([]domain.FlagRecord, 0, 450)
	for uid := int64(1); uid <= 450; uid++ {
		recs = append(recs, geoFlag(uid, domain.FlagEnterSuspect, domain.FlagLevelSuspect, domain.FlagLevelNone, uid))
	}
	if err := r.Append(context.Background(), recs); err != nil {
		t.Fatalf("append: %v", err)
	}
	if n := countFlagRows(t, db, ""); n != 450 {
		t.Fatalf("rows = %d, want 450", n)
	}
}

// Every source and event the domain can produce fits its column, and the
// store's width checks agree with the DDL: a column narrowed without its
// constant would let a value through to be stored, refused or truncated
// depending on the dialect.
func TestFlagRecordRow_WidthsMatchTheColumns(t *testing.T) {
	s, err := schema.Parse(&flagRecordRow{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse flagRecordRow: %v", err)
	}
	for column, want := range map[string]int{
		"source": flagSourceWidth, "event": flagEventWidth, "level": flagLevelWidth,
		"prev_level": flagLevelWidth, "state": flagStateWidth, "code": flagCodeWidth,
	} {
		f := s.LookUpField(column)
		if f == nil {
			t.Fatalf("flag_records has no %s column", column)
		}
		if f.Size != want {
			t.Fatalf("flag_records.%s is size %d, Append checks %d", column, f.Size, want)
		}
	}
	if f := s.LookUpField("params"); f == nil || f.DataType != "text" {
		t.Fatalf("flag_records.params must be text (flagParamsMaxBytes is TEXT's capacity)")
	}
	// The risk transitions are written from rows risk_signals already
	// accepted, so its widths must fit here or the flag insert could fail a
	// save risk_signals would take.
	if flagSourceWidth < riskKindWidth || flagStateWidth < riskStateWidth || flagCodeWidth < riskCodeWidth || flagParamsMaxBytes < riskEvidenceMaxBytes {
		t.Fatal("a risk signal that fits risk_signals must fit flag_records")
	}
	for _, src := range domain.FlagSources() {
		if len(src) > flagSourceWidth {
			t.Errorf("source %q is over %d bytes", src, flagSourceWidth)
		}
	}
	for _, ev := range domain.FlagEvents() {
		if len(ev) > flagEventWidth {
			t.Errorf("event %q is over %d bytes", ev, flagEventWidth)
		}
	}
}
