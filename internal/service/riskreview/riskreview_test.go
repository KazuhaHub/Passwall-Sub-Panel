package riskreview

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// testNow is the fixed clock every test reads.
var testNow = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

// admin is the acting admin of every test. Its UPN is for logs only and
// must never reach a stored row or record.
var admin = Actor{ID: 1, UPN: "owner-admin@example.test"}

// fakeStore is risk_reviews: one row per account and the review records
// saved with them, each Save one row and one record like the real store's
// transaction. saveDelay widens the window between an action's check and its
// write, so an unserialized pair of actions would both pass the check.
type fakeStore struct {
	mu        sync.Mutex
	rows      map[int64]domain.RiskReview
	records   []domain.FlagRecord
	getErr    error
	saveErr   error
	saveDelay time.Duration
}

func (f *fakeStore) Get(_ context.Context, userID int64) (domain.RiskReview, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return domain.RiskReview{}, false, f.getErr
	}
	r, ok := f.rows[userID]
	return r, ok, nil
}

func (f *fakeStore) Save(_ context.Context, rev domain.RiskReview, rec domain.FlagRecord) error {
	if f.saveDelay > 0 {
		time.Sleep(f.saveDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.rows == nil {
		f.rows = map[int64]domain.RiskReview{}
	}
	f.rows[rev.UserID] = rev
	f.records = append(f.records, rec)
	return nil
}

func (f *fakeStore) row(userID int64) (domain.RiskReview, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	return r, ok
}

func (f *fakeStore) saved() []domain.FlagRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.FlagRecord(nil), f.records...)
}

// fakeAttention is the risk center's one-account read over fakeStore: the
// fresh levels and verdict times it is given, the review row as stored now
// (a row covering nothing reads as no review, as the real read has it), the
// trusted account's location sources masked, and the REAL reopen rule over
// the steps and the history bound it is given.
type fakeAttention struct {
	mu          sync.Mutex
	store       *fakeStore
	levels      map[int64]domain.AttentionLevels
	atMS        map[int64]map[string]int64
	steps       map[int64][]domain.FlagStep
	historyFrom int64
	err         error
	calls       int
}

func (f *fakeAttention) UserAttention(ctx context.Context, userID int64) (domain.AccountAttention, error) {
	f.mu.Lock()
	f.calls++
	err := f.err
	levels, at, steps, historyFrom := f.levels[userID], f.atMS[userID], f.steps[userID], f.historyFrom
	f.mu.Unlock()
	if err != nil {
		return domain.AccountAttention{}, err
	}
	rev, has, err := f.store.Get(ctx, userID)
	if err != nil {
		return domain.AccountAttention{}, err
	}
	has = has && (rev.Dismissed() || rev.Trusted)
	if !has {
		rev = domain.RiskReview{}
	}
	levels = domain.MaskTrusted(levels, rev.Trusted)
	a := domain.AccountAttention{Levels: levels, AtMS: at, Review: rev, HasReview: has}
	a.State = domain.EvaluateReview(rev, domain.ReviewInputs{
		Now: levels, Trusted: rev.Trusted, Steps: steps, HistoryFromMS: historyFrom,
	})
	return a, nil
}

func (f *fakeAttention) set(userID int64, levels domain.AttentionLevels, at map[string]int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.levels == nil {
		f.levels, f.atMS = map[int64]domain.AttentionLevels{}, map[int64]map[string]int64{}
	}
	f.levels[userID], f.atMS[userID] = levels, at
}

func (f *fakeAttention) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeUsers struct {
	byID map[int64]*domain.User
	err  error
}

func (f *fakeUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

// fakeResumer is the user service's geo_auto lift. When block is set, a
// resume signals entered and then waits for block to close.
type fakeResumer struct {
	mu      sync.Mutex
	calls   []int64
	lifted  bool
	err     error
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeResumer) ResumeGeoAutoIfHeld(_ context.Context, userID int64) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, userID)
	lifted, err, block, entered := f.lifted, f.err, f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if block != nil {
		<-block
	}
	return lifted, err
}

func (f *fakeResumer) called() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.calls...)
}

type fixture struct {
	svc     *Service
	store   *fakeStore
	att     *fakeAttention
	users   *fakeUsers
	resumer *fakeResumer
}

// newFixture has accounts 7 and 8 and nothing stored.
func newFixture() *fixture {
	store := &fakeStore{}
	f := &fixture{
		store:   store,
		att:     &fakeAttention{store: store},
		users:   &fakeUsers{byID: map[int64]*domain.User{7: {ID: 7, UPN: "alice"}, 8: {ID: 8, UPN: "bob"}}},
		resumer: &fakeResumer{lifted: true},
	}
	f.svc = New(Deps{Store: f.store, Attention: f.att, Users: f.users, Resumer: f.resumer, Now: func() time.Time { return testNow }})
	return f
}

// params decodes a review record's params into a plain map, so a test sees
// exactly the keys the SPA would.
func params(t *testing.T, rec domain.FlagRecord) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Params, &out); err != nil {
		t.Fatalf("params %s: %v", rec.Params, err)
	}
	return out
}

// A dismissal accepts exactly what the account shows now: every present
// source with its level and the time of the verdict it was read from — the
// reopen rule's per-source cutoffs — the hold included. The row names the
// admin by id, keeps the note (trimmed) and is stamped now; the record
// carries the admin's id and the levels, and is stamped now too.
func TestDismiss_SnapshotsTheCurrentLevelsWithVerdictTimes(t *testing.T) {
	f := newFixture()
	f.att.set(7,
		domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "devices": domain.FlagLevelSuspect, "geo_auto": domain.FlagLevelSuspended},
		map[string]int64{"geo": 100, "devices": 200, "geo_auto": 300})

	rev, err := f.svc.Dismiss(t.Context(), 7, admin, "  seen on a call  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := testNow.UnixMilli()
	want := domain.RiskReview{
		UserID: 7, DismissedAtMS: now, DismissedBy: admin.ID, Note: "seen on a call", UpdatedAtMS: now,
		Accepted: domain.DismissSnapshot{
			"geo":      {Level: domain.FlagLevelFlagged, AtMS: 100},
			"devices":  {Level: domain.FlagLevelSuspect, AtMS: 200},
			"geo_auto": {Level: domain.FlagLevelSuspended, AtMS: 300},
		},
	}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("returned review = %+v, want %+v", rev, want)
	}
	if stored, _ := f.store.row(7); !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored review = %+v, want %+v", stored, want)
	}
	recs := f.store.saved()
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one", recs)
	}
	if got := string(recs[0].Params); got != `{"by":1,"levels":{"devices":"suspect","geo":"flagged","geo_auto":"suspended"}}` {
		t.Fatalf("record params = %s", got)
	}
	if recs[0].AtMS != now || recs[0].Event != domain.FlagReviewDismissed || recs[0].UserID != 7 {
		t.Fatalf("record = %+v, want a dismissed record of 7 stamped now", recs[0])
	}
}

// Nothing to accept is nothing to dismiss: an account with no attention,
// and a trusted account whose only attention is a location source (masked),
// are refused as a conflict and nothing is written.
func TestDismiss_RefusesNothingToDismiss(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.Dismiss(t.Context(), 7, admin, "", nil); !errors.Is(err, ErrNothingToDismiss) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dismiss of a quiet account = %v, want ErrNothingToDismiss (a conflict)", err)
	}

	f.store.rows = map[int64]domain.RiskReview{8: {UserID: 8, Trusted: true, TrustedAtMS: 5, TrustedBy: 2, UpdatedAtMS: 5}}
	f.att.set(8, domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "login_country": domain.FlagLevelSuspect}, nil)
	if _, err := f.svc.Dismiss(t.Context(), 8, admin, "", nil); !errors.Is(err, ErrNothingToDismiss) {
		t.Fatalf("dismiss of a trusted account with location attention only = %v, want ErrNothingToDismiss", err)
	}
	if recs := f.store.saved(); len(recs) != 0 {
		t.Fatalf("records = %+v, want none", recs)
	}
}

// A dismissal still in force is not dismissed twice: a second admin (or a
// stale tab) gets a conflict, not a second record.
func TestDismiss_RefusesWhenAlreadyInForce(t *testing.T) {
	f := newFixture()
	stored := domain.RiskReview{UserID: 7, DismissedAtMS: 50, DismissedBy: 2, Note: "first",
		Accepted: domain.DismissSnapshot{"geo": {Level: domain.FlagLevelFlagged, AtMS: 40}}, UpdatedAtMS: 50}
	f.store.rows = map[int64]domain.RiskReview{7: stored}
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, map[string]int64{"geo": 40})

	if _, err := f.svc.Dismiss(t.Context(), 7, admin, "again", nil); !errors.Is(err, ErrAlreadyDismissed) {
		t.Fatalf("dismiss in force = %v, want ErrAlreadyDismissed", err)
	}
	if got, _ := f.store.row(7); !reflect.DeepEqual(got, stored) {
		t.Fatalf("row = %+v, want it untouched", got)
	}
	if recs := f.store.saved(); len(recs) != 0 {
		t.Fatalf("records = %+v, want none", recs)
	}
}

// A dismissal that no longer covers the account — reopened by an
// escalation, or lapsed because its history may be pruned — is replaced:
// dismissing again is the admin accepting what they now see, with a new
// snapshot, a new time, a new note.
func TestDismiss_ReplacesAReopenedOrLapsedDismissal(t *testing.T) {
	t.Run("reopened", func(t *testing.T) {
		f := newFixture()
		f.store.rows = map[int64]domain.RiskReview{7: {UserID: 7, DismissedAtMS: 50, DismissedBy: 2, Note: "old",
			Accepted: domain.DismissSnapshot{"geo": {Level: domain.FlagLevelSuspect, AtMS: 40}}, UpdatedAtMS: 50}}
		f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, map[string]int64{"geo": 900})

		rev, err := f.svc.Dismiss(t.Context(), 7, admin, "new", nil)
		if err != nil {
			t.Fatal(err)
		}
		if rev.DismissedAtMS != testNow.UnixMilli() || rev.DismissedBy != admin.ID || rev.Note != "new" ||
			!reflect.DeepEqual(rev.Accepted, domain.DismissSnapshot{"geo": {Level: domain.FlagLevelFlagged, AtMS: 900}}) {
			t.Fatalf("re-dismissal = %+v, want the escalation accepted now", rev)
		}
	})
	t.Run("lapsed", func(t *testing.T) {
		f := newFixture()
		f.store.rows = map[int64]domain.RiskReview{7: {UserID: 7, DismissedAtMS: 50, DismissedBy: 2,
			Accepted: domain.DismissSnapshot{"devices": {Level: domain.FlagLevelFlagged, AtMS: 40}}, UpdatedAtMS: 50}}
		f.att.set(7, domain.AttentionLevels{"devices": domain.FlagLevelFlagged}, map[string]int64{"devices": 40})
		f.att.historyFrom = 1000 // the dismissal's cutoff (40) is older: its records may be gone

		rev, err := f.svc.Dismiss(t.Context(), 7, admin, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if rev.DismissedAtMS != testNow.UnixMilli() {
			t.Fatalf("re-dismissal of a lapsed dismissal = %+v, want it replaced now", rev)
		}
	})
}

// Dismiss states what the admin saw. A current level WORSE than the one
// the admin was shown — or a source the admin was not shown at all — is a
// conflict and nothing is written: an admin must never accept an escalation
// they never saw. A level that has since dropped is accepted as it is now.
func TestDismiss_RefusesWhenWorseThanExpected(t *testing.T) {
	now := domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "devices": domain.FlagLevelSuspect}
	for _, tc := range []struct {
		name     string
		expected domain.AttentionLevels
	}{
		{"a worse level", domain.AttentionLevels{"geo": domain.FlagLevelSuspect, "devices": domain.FlagLevelSuspect}},
		{"a source not shown", domain.AttentionLevels{"geo": domain.FlagLevelFlagged}},
		{"nothing shown", domain.AttentionLevels{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.att.set(7, now, nil)
			if _, err := f.svc.Dismiss(t.Context(), 7, admin, "", tc.expected); !errors.Is(err, ErrChanged) {
				t.Fatalf("dismiss expecting %v = %v, want ErrChanged", tc.expected, err)
			}
			if _, ok := f.store.row(7); ok || len(f.store.saved()) != 0 {
				t.Fatal("a refused dismissal wrote")
			}
		})
	}

	f := newFixture()
	f.att.set(7, now, map[string]int64{"geo": 1, "devices": 2})
	rev, err := f.svc.Dismiss(t.Context(), 7, admin, "", domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "devices": domain.FlagLevelFlagged})
	if err != nil {
		t.Fatalf("dismiss after devices dropped = %v, want it accepted", err)
	}
	if got := rev.Accepted["devices"].Level; got != domain.FlagLevelSuspect {
		t.Fatalf("accepted devices = %q, want the current suspect", got)
	}
}

// An older client sends no expectation: no check, the current levels are
// accepted as they are.
func TestDismiss_NoExpectedMeansNoCheck(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "usage_shift": domain.FlagLevelFlagged}, nil)
	if _, err := f.svc.Dismiss(t.Context(), 7, admin, "", nil); err != nil {
		t.Fatalf("dismiss with no expectation = %v, want it accepted", err)
	}
}

// An expectation must name attention sources at their possible levels.
// Anything else is a malformed request (a validation error), never read as
// "changed", and nothing is written.
func TestDismiss_RefusesAnInvalidExpected(t *testing.T) {
	for _, expected := range []domain.AttentionLevels{
		{"review": domain.FlagLevelFlagged},
		{"geo": domain.FlagLevelSuspended},
		{"geo_auto": domain.FlagLevelFlagged},
	} {
		f := newFixture()
		f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)
		_, err := f.svc.Dismiss(t.Context(), 7, admin, "", expected)
		if !errors.Is(err, domain.ErrValidation) || errors.Is(err, ErrChanged) {
			t.Fatalf("dismiss expecting %v = %v, want a validation error", expected, err)
		}
		if len(f.store.saved()) != 0 {
			t.Fatal("a refused dismissal wrote")
		}
	}
}

// The note is bounded in CHARACTERS, the unit risk_reviews.note is sized in
// on every dialect: 200 CJK characters (600 bytes) fit, 201 do not. Outer
// whitespace is not the admin's text and does not count.
func TestDismiss_NoteLimitIsRunes(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)
	_, err := f.svc.Dismiss(t.Context(), 7, admin, strings.Repeat("界", domain.ReviewNoteMaxRunes+1), nil)
	if !errors.Is(err, ErrNoteTooLong) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("201 characters = %v, want ErrNoteTooLong (a validation error)", err)
	}
	if len(f.store.saved()) != 0 {
		t.Fatal("a refused dismissal wrote")
	}
	note := strings.Repeat("界", domain.ReviewNoteMaxRunes)
	rev, err := f.svc.Dismiss(t.Context(), 7, admin, " \n"+note+"\t ", nil)
	if err != nil {
		t.Fatalf("200 characters = %v, want them accepted", err)
	}
	if rev.Note != note {
		t.Fatalf("note = %q, want the 200 characters trimmed", rev.Note)
	}
}

// The note is admin-only free text and may carry an address or a name: it is
// kept on the review row and never copied into the record, which is
// contractually address-free and name-free. Nor is the admin's name.
func TestDismiss_NoteNeverReachesTheRecord(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)
	const note = "alice travels, called her from 198.51.100.7"
	rev, err := f.svc.Dismiss(t.Context(), 7, admin, note, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Note != note {
		t.Fatalf("row note = %q, want it kept", rev.Note)
	}
	rec := f.store.saved()[0]
	for _, leaked := range []string{"198.51.100.7", "alice", admin.UPN} {
		if strings.Contains(string(rec.Params), leaked) || strings.Contains(rec.Code, leaked) {
			t.Fatalf("record %+v carries %q", rec, leaked)
		}
	}
	if got := params(t, rec); len(got) != 2 || got["by"] != float64(admin.ID) || got["levels"] == nil {
		t.Fatalf("record params = %v, want exactly by and levels", got)
	}
}

// Undismissing clears the dismissal whole — time, admin, snapshot and note —
// and records who did it. It acts on the stored dismissal, whether or not it
// still covers the account.
func TestUndismiss_ClearsTheDismissal(t *testing.T) {
	f := newFixture()
	f.store.rows = map[int64]domain.RiskReview{7: {UserID: 7, DismissedAtMS: 50, DismissedBy: 2, Note: "old",
		Accepted: domain.DismissSnapshot{"geo": {Level: domain.FlagLevelSuspect, AtMS: 40}}, UpdatedAtMS: 50}}
	// Reopened: the dismissal no longer covers the account, and is still undone.
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)

	rev, err := f.svc.Undismiss(t.Context(), 7, admin)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RiskReview{UserID: 7, UpdatedAtMS: testNow.UnixMilli()}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("undismissed = %+v, want %+v", rev, want)
	}
	if stored, _ := f.store.row(7); !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored = %+v, want %+v", stored, want)
	}
	recs := f.store.saved()
	if len(recs) != 1 || recs[0].Event != domain.FlagReviewUndismissed || string(recs[0].Params) != `{"by":1}` {
		t.Fatalf("records = %+v, want one undismissed record with params {by}", recs)
	}
}

// Nothing stored to undo is a conflict: no row, or a row that only trusts.
func TestUndismiss_RefusesWhenNotDismissed(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.Undismiss(t.Context(), 7, admin); !errors.Is(err, ErrNotDismissed) {
		t.Fatalf("undismiss with no row = %v, want ErrNotDismissed", err)
	}
	f.store.rows = map[int64]domain.RiskReview{8: {UserID: 8, Trusted: true, TrustedAtMS: 5, TrustedBy: 2, UpdatedAtMS: 5}}
	if _, err := f.svc.Undismiss(t.Context(), 8, admin); !errors.Is(err, ErrNotDismissed) {
		t.Fatalf("undismiss of a trusted-only row = %v, want ErrNotDismissed", err)
	}
	if len(f.store.saved()) != 0 {
		t.Fatal("a refused undismiss wrote")
	}
}

// Trust is recorded whatever the account shows (a quiet account can be
// trusted ahead of a trip); the hold is lifted only when the admin asks.
func TestTrust_ResumesOnlyWhenAsked(t *testing.T) {
	f := newFixture()
	res, err := f.svc.Trust(t.Context(), 7, admin, false)
	if err != nil {
		t.Fatal(err)
	}
	now := testNow.UnixMilli()
	want := domain.RiskReview{UserID: 7, Trusted: true, TrustedAtMS: now, TrustedBy: admin.ID, UpdatedAtMS: now}
	if !reflect.DeepEqual(res.Review, want) || res.Resumed || res.ResumeErr != nil {
		t.Fatalf("trust = %+v, want %+v and no resume", res, want)
	}
	if calls := f.resumer.called(); len(calls) != 0 {
		t.Fatalf("resumer called %v without being asked", calls)
	}

	res, err = f.svc.Trust(t.Context(), 8, admin, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed || res.ResumeErr != nil || !res.Review.Trusted {
		t.Fatalf("trust with resume = %+v, want trusted and resumed", res)
	}
	if calls := f.resumer.called(); !reflect.DeepEqual(calls, []int64{8}) {
		t.Fatalf("resumer calls = %v, want [8]", calls)
	}

	// Not held: nothing to lift is not an error.
	f.resumer.lifted = false
	f.store.rows[8] = domain.RiskReview{UserID: 8}
	res, err = f.svc.Trust(t.Context(), 8, admin, true)
	if err != nil || res.Resumed || res.ResumeErr != nil {
		t.Fatalf("trust with resume of an account not held = %+v, %v; want trusted, not resumed, no error", res, err)
	}
}

// The resume holds the user lock through a panel push that can take
// minutes. It runs after the review lock is released, so another action on
// the same account completes meanwhile instead of waiting for the push.
func TestTrust_ResumeRunsAfterTheReviewLockIsReleased(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"devices": domain.FlagLevelSuspect}, nil)
	f.resumer.block = make(chan struct{})
	f.resumer.entered = make(chan struct{})

	type trustOutcome struct {
		res TrustResult
		err error
	}
	trusted := make(chan trustOutcome, 1)
	go func() {
		res, err := f.svc.Trust(context.Background(), 7, admin, true)
		trusted <- trustOutcome{res, err}
	}()
	select {
	case <-f.resumer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the resume never started")
	}

	dismissed := make(chan error, 1)
	go func() {
		_, err := f.svc.Dismiss(context.Background(), 7, admin, "", nil)
		dismissed <- err
	}()
	select {
	case err := <-dismissed:
		if err != nil {
			t.Fatalf("dismiss during the resume = %v", err)
		}
	case <-time.After(5 * time.Second):
		close(f.resumer.block)
		t.Fatal("a second action waited for the resume: the review lock is held across it")
	}
	close(f.resumer.block)
	out := <-trusted
	if out.err != nil || !out.res.Resumed {
		t.Fatalf("trust = %+v, %v; want resumed", out.res, out.err)
	}
}

// A resume that fails never undoes the trust. Lifted but the push could not
// be queued is a warning (Resumed with an error); not lifted at all is an
// error (not Resumed). Neither is the action's own error.
func TestTrust_ResumeFailureKeepsTheTrust(t *testing.T) {
	pushErr := errors.New("push failed and could not be queued")
	for _, tc := range []struct {
		name   string
		lifted bool
	}{
		{"lifted, push not queued", true},
		{"not lifted", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.resumer.lifted, f.resumer.err = tc.lifted, pushErr
			res, err := f.svc.Trust(t.Context(), 7, admin, true)
			if err != nil {
				t.Fatalf("trust = %v, want the trust to stand", err)
			}
			if res.Resumed != tc.lifted || !errors.Is(res.ResumeErr, pushErr) {
				t.Fatalf("trust result = %+v, want Resumed %v with the resume's error", res, tc.lifted)
			}
			if stored, _ := f.store.row(7); !stored.Trusted {
				t.Fatalf("stored = %+v, want trusted", stored)
			}
		})
	}
}

// Trusting a trusted account is a conflict — and asks nothing of the
// resumer, even when the second request asked to resume.
func TestTrust_RefusesTwice(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.Trust(t.Context(), 7, admin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Trust(t.Context(), 7, admin, true); !errors.Is(err, ErrAlreadyTrusted) {
		t.Fatalf("second trust = %v, want ErrAlreadyTrusted", err)
	}
	if calls := f.resumer.called(); len(calls) != 0 {
		t.Fatalf("resumer calls = %v, want none", calls)
	}
	if recs := f.store.saved(); len(recs) != 1 {
		t.Fatalf("records = %d, want the first trust's only", len(recs))
	}
}

// Untrust clears the trust whole and records who did it.
func TestUntrust_ClearsTheTrust(t *testing.T) {
	f := newFixture()
	f.store.rows = map[int64]domain.RiskReview{7: {UserID: 7, Trusted: true, TrustedAtMS: 5, TrustedBy: 2, UpdatedAtMS: 5}}
	rev, err := f.svc.Untrust(t.Context(), 7, admin)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RiskReview{UserID: 7, UpdatedAtMS: testNow.UnixMilli()}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("untrusted = %+v, want %+v", rev, want)
	}
	recs := f.store.saved()
	if len(recs) != 1 || recs[0].Event != domain.FlagReviewUntrusted || string(recs[0].Params) != `{"by":1}` {
		t.Fatalf("records = %+v, want one untrusted record with params {by}", recs)
	}
}

func TestUntrust_RefusesWhenNotTrusted(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.Untrust(t.Context(), 7, admin); !errors.Is(err, ErrNotTrusted) {
		t.Fatalf("untrust with no row = %v, want ErrNotTrusted", err)
	}
	if len(f.store.saved()) != 0 {
		t.Fatal("a refused untrust wrote")
	}
}

// Each action rewrites only its own half of the row: a dismissal keeps the
// trust, a trust keeps the dismissal and its note, and undoing one keeps the
// other.
func TestActions_KeepTheOtherHalfOfTheRow(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"devices": domain.FlagLevelFlagged}, map[string]int64{"devices": 30})
	if _, err := f.svc.Trust(t.Context(), 7, Actor{ID: 2}, false); err != nil {
		t.Fatal(err)
	}
	rev, err := f.svc.Dismiss(t.Context(), 7, admin, "note", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rev.Trusted || rev.TrustedBy != 2 || rev.TrustedAtMS != testNow.UnixMilli() || rev.Note != "note" {
		t.Fatalf("dismissal of a trusted account = %+v, want the trust kept", rev)
	}
	rev, err = f.svc.Untrust(t.Context(), 7, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !rev.Dismissed() || rev.DismissedBy != admin.ID || rev.Note != "note" || rev.Accepted == nil || rev.Trusted {
		t.Fatalf("untrust of a dismissed account = %+v, want the dismissal kept", rev)
	}
	if _, err := f.svc.Trust(t.Context(), 7, admin, false); err != nil {
		t.Fatal(err)
	}
	rev, err = f.svc.Undismiss(t.Context(), 7, admin)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Dismissed() || !rev.Trusted || rev.TrustedBy != admin.ID {
		t.Fatalf("undismiss of a trusted account = %+v, want the trust kept", rev)
	}
}

// One record per action, each the admin's action by id and nothing else:
// source review, the action's event as its code, no level, no state, no
// name; only a dismissal carries levels.
func TestActions_WriteOneReviewRecordEach(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"sub_spread": domain.FlagLevelSuspect}, nil)
	ctx := t.Context()
	if _, err := f.svc.Dismiss(ctx, 7, admin, "note", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Undismiss(ctx, 7, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Trust(ctx, 7, admin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Untrust(ctx, 7, admin); err != nil {
		t.Fatal(err)
	}
	recs := f.store.saved()
	events := []domain.FlagEvent{domain.FlagReviewDismissed, domain.FlagReviewUndismissed, domain.FlagReviewTrusted, domain.FlagReviewUntrusted}
	if len(recs) != len(events) {
		t.Fatalf("records = %+v, want one per action", recs)
	}
	for i, rec := range recs {
		if rec.Source != domain.FlagSourceReview || rec.Event != events[i] || rec.Code != string(events[i]) ||
			rec.Level != domain.FlagLevelNone || rec.PrevLevel != domain.FlagLevelNone || rec.State != "" ||
			rec.UserID != 7 || rec.AtMS != testNow.UnixMilli() || rec.UPN != "" || rec.DisplayName != "" {
			t.Fatalf("record %d = %+v, want a bare %s review record", i, rec, events[i])
		}
		p := params(t, rec)
		if p["by"] != float64(admin.ID) {
			t.Fatalf("record %d params = %v, want by %d", i, p, admin.ID)
		}
		if _, has := p["levels"]; has != (i == 0) {
			t.Fatalf("record %d params = %v: only the dismissal carries levels", i, p)
		}
		if strings.Contains(string(rec.Params), admin.UPN) || strings.Contains(string(rec.Params), "note") {
			t.Fatalf("record %d params = %s carry a name or the note", i, rec.Params)
		}
	}
}

// A missing account is ErrNotFound (the handler's 404) for every action,
// before anything is read about it or written.
func TestActions_UnknownUserIsNotFound(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	actions := map[string]func() error{
		"dismiss":   func() error { _, err := f.svc.Dismiss(ctx, 99, admin, "", nil); return err },
		"undismiss": func() error { _, err := f.svc.Undismiss(ctx, 99, admin); return err },
		"trust":     func() error { _, err := f.svc.Trust(ctx, 99, admin, true); return err },
		"untrust":   func() error { _, err := f.svc.Untrust(ctx, 99, admin); return err },
	}
	for name, act := range actions {
		if err := act(); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s of a missing account = %v, want ErrNotFound", name, err)
		}
	}
	if f.att.callCount() != 0 || len(f.store.saved()) != 0 || len(f.resumer.called()) != 0 {
		t.Fatal("an action on a missing account read its attention, wrote or resumed")
	}
}

// A store that cannot read or write fails the action with its error, which
// is never mistaken for a refusal (no conflict).
func TestActions_StoreErrorsPassThrough(t *testing.T) {
	boom := errors.New("database is locked")
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)
	f.store.saveErr = boom
	if _, err := f.svc.Dismiss(t.Context(), 7, admin, "", nil); !errors.Is(err, boom) || errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dismiss with a failing store = %v, want the store's error", err)
	}
	if _, err := f.svc.Trust(t.Context(), 7, admin, true); !errors.Is(err, boom) {
		t.Fatalf("trust with a failing store = %v, want the store's error", err)
	}
	if calls := f.resumer.called(); len(calls) != 0 {
		t.Fatalf("resumer calls = %v: a trust that was not saved must not resume", calls)
	}
	f.store.saveErr = nil
	f.att.err = boom
	if _, err := f.svc.Dismiss(t.Context(), 7, admin, "", nil); !errors.Is(err, boom) {
		t.Fatalf("dismiss with a failing attention read = %v, want its error", err)
	}
}

// Two admins dismissing one account at the same moment: exactly one
// dismissal is written, the other admin gets ErrAlreadyDismissed — never a
// duplicate record. The store's slow write is the window an unserialized
// pair would both pass the check in.
func TestActions_SerializePerAccount(t *testing.T) {
	f := newFixture()
	f.att.set(7, domain.AttentionLevels{"geo": domain.FlagLevelFlagged}, nil)
	f.store.saveDelay = 30 * time.Millisecond

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, by := range []Actor{admin, {ID: 2, UPN: "second"}} {
		go func() {
			<-start
			_, err := f.svc.Dismiss(context.Background(), 7, by, "", nil)
			errs <- err
		}()
	}
	close(start)
	var ok, refused int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyDismissed):
			refused++
		default:
			t.Fatalf("concurrent dismiss = %v", err)
		}
	}
	if ok != 1 || refused != 1 || len(f.store.saved()) != 1 {
		t.Fatalf("concurrent dismissals: %d written, %d refused, %d records; want 1, 1, 1", ok, refused, len(f.store.saved()))
	}
}
