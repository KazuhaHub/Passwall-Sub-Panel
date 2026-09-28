package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// risk_reviews is one row per account an admin reviewed: its dismissal (what
// was accepted, per source, with the verdict time) and its trust. Current
// state, not history — the history is flag_records with source review, and
// Save writes both in one transaction. These tests pin what the queue and the
// review actions rely on: every field back, zeroes that really overwrite, a
// record that cannot be written taking the row with it, only rows in force of
// living accounts listed, the note bounded in characters, and a stored
// snapshot nobody can read costing only itself.

func newRiskReviewRepo(t *testing.T) (*RiskReviewRepo, ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewRiskReviewRepo(db), NewRepos(db).User, db
}

func countReviewRows(t *testing.T, db *gorm.DB, where string) int64 {
	t.Helper()
	var n int64
	q := "SELECT COUNT(*) FROM risk_reviews"
	if where != "" {
		q += " WHERE " + where
	}
	if err := db.Raw(q).Scan(&n).Error; err != nil {
		t.Fatalf("count risk_reviews (%s): %v", where, err)
	}
	return n
}

// dismissedReview is a full row: a dismissal of three sources (geo_auto's
// verdict time is the hold's), a CJK note, and a trust.
func dismissedReview(uid int64) domain.RiskReview {
	return domain.RiskReview{
		UserID:        uid,
		DismissedAtMS: 1_790_000_000_000,
		DismissedBy:   7,
		Accepted: domain.DismissSnapshot{
			domain.FlagSourceGeo:             {Level: domain.FlagLevelFlagged, AtMS: 1_789_999_999_000},
			string(domain.RiskKindDevices):   {Level: domain.FlagLevelSuspect, AtMS: 1_789_999_990_000},
			domain.FlagSourceGeoAuto:         {Level: domain.FlagLevelSuspended, AtMS: 1_789_999_000_000},
			string(domain.RiskKindSubSpread): {Level: domain.FlagLevelFlagged, AtMS: 0},
		},
		Note:        "出差，已电话确认",
		Trusted:     true,
		TrustedAtMS: 1_790_000_000_500,
		TrustedBy:   8,
		UpdatedAtMS: 1_790_000_000_500,
	}
}

func reviewRecord(uid int64, ev domain.FlagEvent) domain.FlagRecord {
	return domain.ReviewFlag(uid, ev, domain.ReviewFlagParams{By: 7}, time.UnixMilli(1_790_000_000_000))
}

func getReview(t *testing.T, r *RiskReviewRepo, uid int64) (domain.RiskReview, bool) {
	t.Helper()
	got, ok, err := r.Get(context.Background(), uid)
	if err != nil {
		t.Fatalf("Get(%d): %v", uid, err)
	}
	return got, ok
}

// Every field comes back as written, the snapshot's verdict times included
// (a 0 time stays 0: "unknown, read as the dismissal time"). An account with
// no row is (zero, false, nil) — not an error, the common case. The record
// Save appends is the one it was handed: source review, the admin's id, and
// neither a name nor the note.
func TestRiskReviewRepo_SaveAndGetRoundTrip(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	other := createRiskUser(t, users, 2, "")
	want := dismissedReview(u.ID)
	if err := r.Save(ctx, want, reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, ok := getReview(t, r, u.ID)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get = %+v, %v; want %+v, true", got, ok, want)
	}
	if got, ok := getReview(t, r, other.ID); ok || !reflect.DeepEqual(got, domain.RiskReview{}) {
		t.Fatalf("Get(no row) = %+v, %v; want the zero review, false", got, ok)
	}

	recs, total, err := NewFlagRecordRepo(db).List(ctx, ports.FlagRecordFilter{Source: domain.FlagSourceReview})
	if err != nil || total != 1 || len(recs) != 1 {
		t.Fatalf("review records = %+v (total %d), %v; want exactly one", recs, total, err)
	}
	rec := recs[0]
	if rec.UserID != u.ID || rec.Event != domain.FlagReviewDismissed || rec.Code != string(domain.FlagReviewDismissed) ||
		rec.Level != domain.FlagLevelNone || string(rec.Params) != `{"by":7}` || rec.AtMS != 1_790_000_000_000 {
		t.Fatalf("review record = %+v; want the dismissed record of user %d with params {\"by\":7}", rec, u.ID)
	}
	if strings.Contains(string(rec.Params), want.Note) {
		t.Fatalf("review record params %s carry the note", rec.Params)
	}
}

// Save writes the WHOLE row: an undismiss is zeroes and a NULL snapshot, an
// untrust is false. An upsert that skipped a zero — GORM's habit with a
// column default — would leave the account dismissed or trusted for good.
func TestRiskReviewRepo_SaveWritesZeroes(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	if err := r.Save(ctx, dismissedReview(u.ID), reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("save: %v", err)
	}

	cleared := domain.RiskReview{UserID: u.ID, UpdatedAtMS: 1_790_000_100_000}
	if err := r.Save(ctx, cleared, reviewRecord(u.ID, domain.FlagReviewUntrusted)); err != nil {
		t.Fatalf("save cleared: %v", err)
	}
	got, ok := getReview(t, r, u.ID)
	if !ok || !reflect.DeepEqual(got, cleared) {
		t.Fatalf("Get = %+v, %v; want %+v, true (a cleared row stays, all zero)", got, ok, cleared)
	}
	if n := countReviewRows(t, db, "dismissed_levels IS NULL"); n != 1 {
		t.Fatalf("rows with a NULL snapshot = %d, want 1: no dismissal is stored as NULL, not as {}", n)
	}
	if n := countFlagRows(t, db, "source = 'review'"); n != 2 {
		t.Fatalf("review records = %d, want 2 (one per Save)", n)
	}
}

// The row and its record are one transaction. A record that cannot be
// written takes the row with it: an action whose history is missing would
// read, later, as a state nobody chose.
func TestRiskReviewRepo_SaveRollsBackWhenTheRecordInsertFails(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	// The previous state, written without a record so that "no record
	// exists" below is exact.
	if err := db.Create(&riskReviewRow{UserID: u.ID, Trusted: true, TrustedAtMS: 5, TrustedBy: 3, UpdatedAtMS: 5}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := getReview(t, r, u.ID)
	if err := db.Callback().Create().Before("gorm:create").Register("test:fail_review_records", func(tx *gorm.DB) {
		if tx.Statement.Table == "flag_records" {
			_ = tx.AddError(errors.New("flag store refused"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	if err := r.Save(ctx, dismissedReview(u.ID), reviewRecord(u.ID, domain.FlagReviewDismissed)); err == nil {
		t.Fatal("Save succeeded although its review record was refused")
	}
	after, ok := getReview(t, r, u.ID)
	if !ok || !reflect.DeepEqual(after, before) {
		t.Fatalf("row after the failed save = %+v, want the previous %+v — the upsert must roll back with the record", after, before)
	}
	if n := countFlagRows(t, db, ""); n != 0 {
		t.Fatalf("flag records = %d, want 0", n)
	}
}

// List is the queue's read: rows IN FORCE (a dismissal or a trust) of
// accounts that still exist, by user id. A cleared row covers nothing, and
// a deleted account — or an id that never existed — is nobody to list.
func TestRiskReviewRepo_ListOnlyRowsInForceOfLivingUsers(t *testing.T) {
	r, users, _ := newRiskReviewRepo(t)
	ctx := context.Background()
	dismissed := createRiskUser(t, users, 1, "")
	trusted := createRiskUser(t, users, 2, "")
	cleared := createRiskUser(t, users, 3, "")
	gone := createRiskUser(t, users, 4, "")
	const neverExisted = int64(987654)

	dismissedRow := dismissedReview(dismissed.ID)
	dismissedRow.Trusted, dismissedRow.TrustedAtMS, dismissedRow.TrustedBy = false, 0, 0
	trustedRow := domain.RiskReview{UserID: trusted.ID, Trusted: true, TrustedAtMS: 9, TrustedBy: 1, UpdatedAtMS: 9}
	for _, rev := range []domain.RiskReview{
		trustedRow, dismissedRow,
		{UserID: cleared.ID, UpdatedAtMS: 9},
		dismissedReview(gone.ID),
		dismissedReview(neverExisted),
	} {
		if err := r.Save(ctx, rev, reviewRecord(rev.UserID, domain.FlagReviewDismissed)); err != nil {
			t.Fatalf("save %d: %v", rev.UserID, err)
		}
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []domain.RiskReview{dismissedRow, trustedRow}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %+v\nwant %+v", got, want)
	}
}

// ListTrusted is what the detectors read once per judging step: the ids of
// trusted accounts that still exist, ascending. A dismissal is not a trust.
func TestRiskReviewRepo_ListTrusted(t *testing.T) {
	r, users, _ := newRiskReviewRepo(t)
	ctx := context.Background()
	first := createRiskUser(t, users, 1, "")
	dismissedOnly := createRiskUser(t, users, 2, "")
	gone := createRiskUser(t, users, 3, "")
	last := createRiskUser(t, users, 4, "")
	untrusted := createRiskUser(t, users, 5, "")

	trust := func(uid int64) domain.RiskReview {
		return domain.RiskReview{UserID: uid, Trusted: true, TrustedAtMS: 9, TrustedBy: 1, UpdatedAtMS: 9}
	}
	dismissed := dismissedReview(dismissedOnly.ID)
	dismissed.Trusted, dismissed.TrustedAtMS, dismissed.TrustedBy = false, 0, 0
	for _, rev := range []domain.RiskReview{
		trust(last.ID), dismissed, trust(gone.ID), trust(first.ID), trust(untrusted.ID),
		{UserID: untrusted.ID, UpdatedAtMS: 10},
	} {
		if err := r.Save(ctx, rev, reviewRecord(rev.UserID, domain.FlagReviewTrusted)); err != nil {
			t.Fatalf("save %d: %v", rev.UserID, err)
		}
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	got, err := r.ListTrusted(ctx)
	if err != nil {
		t.Fatalf("ListTrusted: %v", err)
	}
	if want := []int64{first.ID, last.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListTrusted = %v, want %v", got, want)
	}
}

// Everything Save refuses, it refuses before writing anything. The note is
// bounded in CHARACTERS — varchar(200) is characters on PostgreSQL and
// MySQL — so 200 CJK characters (600 bytes) fit and 201 do not. A snapshot
// that could not be read back the same way, a dismissal without a snapshot
// (but the stored one carried through,
// TestRiskReviewRepo_TrustAndUntrustKeepAnUnreadableDismissal) or a snapshot
// without a dismissal, and a record that is not this account's review are all
// refused as validation errors.
func TestRiskReviewRepo_RejectsLongNotesAndBadSnapshots(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	other := createRiskUser(t, users, 2, "")
	withNote := func(note string) domain.RiskReview {
		rev := dismissedReview(u.ID)
		rev.Note = note
		return rev
	}
	withAccepted := func(a domain.DismissSnapshot) domain.RiskReview {
		rev := dismissedReview(u.ID)
		rev.Accepted = a
		return rev
	}
	notDismissed := dismissedReview(u.ID)
	notDismissed.DismissedAtMS = 0
	noSnapshot := dismissedReview(u.ID)
	noSnapshot.Accepted = nil
	otherRecord := reviewRecord(other.ID, domain.FlagReviewDismissed)
	notReview := reviewRecord(u.ID, domain.FlagReviewDismissed)
	notReview.Source = domain.FlagSourceGeo

	for _, c := range []struct {
		name string
		rev  domain.RiskReview
		rec  domain.FlagRecord
	}{
		{"201 characters", withNote(strings.Repeat("审", domain.ReviewNoteMaxRunes+1)), reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"a negative verdict time", withAccepted(domain.DismissSnapshot{domain.FlagSourceGeo: {Level: domain.FlagLevelFlagged, AtMS: -1}}), reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"geo cannot be suspended", withAccepted(domain.DismissSnapshot{domain.FlagSourceGeo: {Level: domain.FlagLevelSuspended, AtMS: 1}}), reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"an unknown source", withAccepted(domain.DismissSnapshot{"review": {Level: domain.FlagLevelFlagged, AtMS: 1}}), reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"a snapshot without a dismissal", notDismissed, reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"a dismissal without a snapshot", noSnapshot, reviewRecord(u.ID, domain.FlagReviewDismissed)},
		{"no account", domain.RiskReview{UpdatedAtMS: 1}, reviewRecord(0, domain.FlagReviewUndismissed)},
		{"another account's record", dismissedReview(u.ID), otherRecord},
		{"a record that is not a review", dismissedReview(u.ID), notReview},
	} {
		if err := r.Save(ctx, c.rev, c.rec); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: Save error = %v, want domain.ErrValidation", c.name, err)
		}
	}
	if n, m := countReviewRows(t, db, ""), countFlagRows(t, db, ""); n != 0 || m != 0 {
		t.Fatalf("after the refused saves: %d rows, %d records; want nothing written", n, m)
	}

	longest := withNote(strings.Repeat("审", domain.ReviewNoteMaxRunes))
	if err := r.Save(ctx, longest, reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("200 characters: %v", err)
	}
	if got, _ := getReview(t, r, u.ID); got.Note != longest.Note {
		t.Fatalf("note read back as %d characters, want the 200 written", len([]rune(got.Note)))
	}
}

// The hourly cleanup deletes the rows of accounts that no longer exist, and
// only those.
func TestRiskReviewRepo_PurgeOrphans(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	const neverExisted = int64(987654)
	for _, uid := range []int64{kept.ID, gone.ID, neverExisted} {
		if err := r.Save(ctx, dismissedReview(uid), reviewRecord(uid, domain.FlagReviewDismissed)); err != nil {
			t.Fatalf("save %d: %v", uid, err)
		}
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := r.PurgeOrphans(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PurgeOrphans = %d, %v; want 2, nil", n, err)
	}
	if all, own := countReviewRows(t, db, ""), countReviewRows(t, db, fmt.Sprintf("user_id = %d", kept.ID)); all != 1 || own != 1 {
		t.Fatalf("rows left: %d in all, %d of the existing account; want 1 and 1", all, own)
	}
	if again, err := r.PurgeOrphans(ctx); err != nil || again != 0 {
		t.Fatalf("second purge = %d, %v; want 0, nil", again, err)
	}
}

// A stored snapshot nobody can read — edited behind the store's back, or
// written by a build that knew other sources — reads as a dismissal that
// accepted NOTHING: every current attention then reopens it, the safe
// direction. Never an error, which would fail the whole queue for one row.
func TestRiskReviewRepo_UnreadableLevelsReadAsAnEmptySnapshot(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	for i, stored := range []string{
		"not json",
		"",
		`["geo"]`,
		`{"geo":{"level":"bogus","at_ms":1}}`,
		`{"geo":{"level":"flagged","at_ms":-5}}`,
	} {
		u := createRiskUser(t, users, i+1, "")
		want := dismissedReview(u.ID)
		if err := r.Save(ctx, want, reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := db.Exec("UPDATE risk_reviews SET dismissed_levels = ? WHERE user_id = ?", stored, u.ID).Error; err != nil {
			t.Fatalf("corrupt the snapshot: %v", err)
		}
		want.Accepted = nil

		got, ok := getReview(t, r, u.ID)
		if !ok || !got.Dismissed() || len(got.Accepted) != 0 {
			t.Fatalf("%q: Get = %+v, %v; want a dismissal with an empty snapshot", stored, got, ok)
		}
		got.Accepted = nil
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: the other fields = %+v, want %+v", stored, got, want)
		}
	}
	listed, err := r.List(ctx)
	if err != nil || len(listed) != 5 {
		t.Fatalf("List = %d rows, %v; want all 5, no error", len(listed), err)
	}
	for _, rev := range listed {
		if !rev.Dismissed() || len(rev.Accepted) != 0 {
			t.Fatalf("List row %+v, want a dismissal with an empty snapshot", rev)
		}
	}
}

// A trust or an untrust rewrites the whole row it read, the dismissal
// included. When the stored snapshot could not be read, that dismissal comes
// back with an empty snapshot, and it must go back exactly as stored: the
// action changes the trust and nothing else, the column keeps its value (a
// build that can read it still finds it), and the account never has to be
// undismissed before it can be trusted or untrusted. Only that dismissal: one
// without a snapshot that is not the stored one, or that drops a snapshot the
// store can read, is refused as before and changes nothing.
func TestRiskReviewRepo_TrustAndUntrustKeepAnUnreadableDismissal(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	storedLevels := func(uid int64) *string {
		t.Helper()
		var rows []riskReviewRow
		if err := db.Where("user_id = ?", uid).Find(&rows).Error; err != nil || len(rows) != 1 {
			t.Fatalf("read the row of %d: %d rows, %v", uid, len(rows), err)
		}
		return rows[0].DismissedLevels
	}
	sameColumn := func(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	store := func(uid int64, col *string) {
		t.Helper()
		var err error
		if col == nil {
			err = db.Exec("UPDATE risk_reviews SET dismissed_levels = NULL WHERE user_id = ?", uid).Error
		} else {
			err = db.Exec("UPDATE risk_reviews SET dismissed_levels = ? WHERE user_id = ?", *col, uid).Error
		}
		if err != nil {
			t.Fatalf("overwrite the snapshot of %d: %v", uid, err)
		}
	}
	reviewRecords := func(uid int64) int64 {
		return countFlagRows(t, db, fmt.Sprintf("source = 'review' AND user_id = %d", uid))
	}
	untrustedDismissal := func(uid int64) domain.RiskReview {
		rev := dismissedReview(uid)
		rev.Trusted, rev.TrustedAtMS, rev.TrustedBy = false, 0, 0
		return rev
	}

	// A source this build does not know (a downgrade), a hand edit, and a
	// dismissal stored with no snapshot at all: all read as accepting nothing.
	unknownSource, notJSON := `{"future_source":{"level":"flagged","at_ms":1}}`, "not json"
	for i, stored := range []*string{&unknownSource, &notJSON, nil} {
		name := "NULL"
		if stored != nil {
			name = *stored
		}
		u := createRiskUser(t, users, i+1, "")
		if err := r.Save(ctx, untrustedDismissal(u.ID), reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
			t.Fatalf("%s: save: %v", name, err)
		}
		store(u.ID, stored)

		trusted, _ := getReview(t, r, u.ID)
		trusted.Trusted, trusted.TrustedAtMS, trusted.TrustedBy = true, 1_790_000_200_000, 8
		trusted.UpdatedAtMS = 1_790_000_200_000
		if err := r.Save(ctx, trusted, reviewRecord(u.ID, domain.FlagReviewTrusted)); err != nil {
			t.Fatalf("%s: trusting an account whose dismissal is unreadable: %v", name, err)
		}
		if got, ok := getReview(t, r, u.ID); !ok || !reflect.DeepEqual(got, trusted) {
			t.Fatalf("%s: after the trust Get = %+v, %v; want %+v", name, got, ok, trusted)
		}
		if got := storedLevels(u.ID); !sameColumn(got, stored) {
			t.Fatalf("%s: the trust rewrote the stored snapshot to %v", name, got)
		}

		untrusted, _ := getReview(t, r, u.ID)
		untrusted.Trusted, untrusted.TrustedAtMS, untrusted.TrustedBy = false, 0, 0
		untrusted.UpdatedAtMS = 1_790_000_300_000
		if err := r.Save(ctx, untrusted, reviewRecord(u.ID, domain.FlagReviewUntrusted)); err != nil {
			t.Fatalf("%s: untrusting an account whose dismissal is unreadable: %v", name, err)
		}
		if got, ok := getReview(t, r, u.ID); !ok || !reflect.DeepEqual(got, untrusted) {
			t.Fatalf("%s: after the untrust Get = %+v, %v; want %+v", name, got, ok, untrusted)
		}
		if got := storedLevels(u.ID); !sameColumn(got, stored) {
			t.Fatalf("%s: the untrust rewrote the stored snapshot to %v", name, got)
		}
		if n := reviewRecords(u.ID); n != 3 {
			t.Fatalf("%s: review records = %d, want 3 (dismissed, trusted, untrusted)", name, n)
		}
	}

	// Not the stored dismissal: a new one that accepts nothing.
	unreadable := createRiskUser(t, users, 4, "")
	if err := r.Save(ctx, untrustedDismissal(unreadable.ID), reviewRecord(unreadable.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("save: %v", err)
	}
	store(unreadable.ID, &notJSON)
	before, _ := getReview(t, r, unreadable.ID)
	redismissed := before
	redismissed.DismissedAtMS++
	if err := r.Save(ctx, redismissed, reviewRecord(unreadable.ID, domain.FlagReviewDismissed)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a new dismissal accepting nothing: Save error = %v, want domain.ErrValidation", err)
	}
	if after, _ := getReview(t, r, unreadable.ID); !reflect.DeepEqual(after, before) || !sameColumn(storedLevels(unreadable.ID), &notJSON) {
		t.Fatalf("after the refused save: %+v, want %+v with the stored snapshot kept", after, before)
	}

	// A snapshot the store CAN read, dropped by the caller.
	readable := createRiskUser(t, users, 5, "")
	if err := r.Save(ctx, untrustedDismissal(readable.ID), reviewRecord(readable.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("save: %v", err)
	}
	before, _ = getReview(t, r, readable.ID)
	dropped := before
	dropped.Accepted = nil
	dropped.Trusted, dropped.TrustedAtMS, dropped.TrustedBy = true, 1_790_000_200_000, 8
	if err := r.Save(ctx, dropped, reviewRecord(readable.ID, domain.FlagReviewTrusted)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a readable snapshot dropped: Save error = %v, want domain.ErrValidation", err)
	}
	if after, _ := getReview(t, r, readable.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("after the refused save: %+v, want %+v", after, before)
	}
	if a, b := reviewRecords(unreadable.ID), reviewRecords(readable.ID); a != 1 || b != 1 {
		t.Fatalf("review records after the refused saves = %d and %d, want 1 and 1", a, b)
	}
}

// The column's JSON is the domain's contract, so a snapshot written by one
// build reads the same in the next.
func TestRiskReviewRepo_StoresTheSnapshotContract(t *testing.T) {
	r, users, db := newRiskReviewRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	rev := domain.RiskReview{
		UserID: u.ID, DismissedAtMS: 10, DismissedBy: 1,
		Accepted:    domain.DismissSnapshot{domain.FlagSourceGeo: {Level: domain.FlagLevelFlagged, AtMS: 5}},
		UpdatedAtMS: 10,
	}
	if err := r.Save(ctx, rev, reviewRecord(u.ID, domain.FlagReviewDismissed)); err != nil {
		t.Fatalf("save: %v", err)
	}
	var stored string
	if err := db.Raw("SELECT dismissed_levels FROM risk_reviews WHERE user_id = ?", u.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("read the column: %v", err)
	}
	var got, want any
	_ = json.Unmarshal([]byte(stored), &got)
	_ = json.Unmarshal([]byte(`{"geo":{"level":"flagged","at_ms":5}}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dismissed_levels = %s, want {\"geo\":{\"level\":\"flagged\",\"at_ms\":5}}", stored)
	}
}

// ---- helpers for the queue's reads, shared by their tests ----

// chunkSpanningIDs is a >500-id request whose real ids straddle a chunk
// boundary once sorted: 499 ids that never exist (negative) ahead of first,
// then rest, then 600 more that never exist. first is the last id of the
// first chunk and rest open the second, so a read that only asked the first
// chunk — or asked one IN list of all 1100 — is caught by the result or by
// countReads. rest must be greater than first.
func chunkSpanningIDs(first int64, rest ...int64) []int64 {
	ids := make([]int64, 0, 1100+len(rest))
	for i := int64(499); i >= 1; i-- {
		ids = append(ids, -i)
	}
	ids = append(ids, first)
	ids = append(ids, rest...)
	for i := int64(1); i <= 600; i++ {
		ids = append(ids, 10_000_000+i)
	}
	return ids
}

// countReads counts the statements read from table from now on, through
// either of GORM's read paths (Find/First/Count/Pluck run the query
// callbacks, Scan and Rows the row callbacks). How a store method splits an
// IN list is invisible in its result, so its chunking is pinned by this.
func countReads(t *testing.T, db *gorm.DB, table string) func() int64 {
	t.Helper()
	var n atomic.Int64
	count := func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			n.Add(1)
		}
	}
	if err := db.Callback().Query().Before("gorm:query").Register("test:count_reads_"+table, count); err != nil {
		t.Fatalf("register query counter: %v", err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register("test:count_rows_"+table, count); err != nil {
		t.Fatalf("register row counter: %v", err)
	}
	return n.Load
}
