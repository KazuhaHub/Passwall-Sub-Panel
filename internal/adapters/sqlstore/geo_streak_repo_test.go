package sqlstore

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The streak is the only thing standing between a stable verdict and a jittery
// one, and it is persisted precisely so a restart cannot clear a latched flag.
// Every case here is about surviving something.

func newStreakRepo(t *testing.T) *GeoStreakRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewGeoStreakRepo(db)
}

func TestGeoStreakRepo_RoundTrip(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	want := map[int64]domain.GeoRecord{
		7: {Streak: domain.GeoStreak{Over: 3, Under: 0, Flagged: true}},
		8: {Streak: domain.GeoStreak{Over: 0, Under: 5, Flagged: false}},
	}
	if err := r.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for uid, w := range want {
		if got[uid].Streak != w.Streak {
			t.Fatalf("user %d: got %+v, want %+v", uid, got[uid], w)
		}
	}
}

// The reason this table exists. A latched flag must be the same after the
// process that set it is gone — otherwise a deploy acquits everyone being
// watched, which is both wrong and trivially exploitable once noticed.
func TestGeoStreakRepo_FlagSurvivesAReopen(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	ctx := context.Background()
	if err := NewGeoStreakRepo(db).Save(ctx, map[int64]domain.GeoRecord{
		7: {Streak: domain.GeoStreak{Over: 4, Flagged: true}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second repo over the same database stands in for a restarted process.
	got, err := NewGeoStreakRepo(db).Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got[7].Streak.Flagged || got[7].Streak.Over != 4 {
		t.Fatalf("after a reopen: got %+v, want the flag and the streak intact", got[7])
	}
}

// Written every cycle, so an upsert rather than an insert. A second save for
// the same user must overwrite, not collide or duplicate.
func TestGeoStreakRepo_SaveIsIdempotentAndUpdates(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: {Streak: domain.GeoStreak{Over: 1}}}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: {Streak: domain.GeoStreak{Over: 0, Under: 2, Flagged: false}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 — the second save must update, not insert", len(got))
	}
	if got[7].Streak.Over != 0 || got[7].Streak.Under != 2 {
		t.Fatalf("got %+v, want the second value", got[7])
	}
}

// A user with no live connections is not evaluated at all, so they are absent
// from the cycle's map. Their row must SURVIVE: idling is the easiest evasion
// there is, and deleting unmentioned rows would let a flagged account clear
// itself simply by disconnecting for one poll.
func TestGeoStreakRepo_AbsentUserKeepsTheirRow(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {Streak: domain.GeoStreak{Over: 3, Flagged: true}},
		8: {Streak: domain.GeoStreak{Under: 1}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Next cycle sees only user 8 — user 7 went idle.
	if err := r.Save(ctx, map[int64]domain.GeoRecord{8: {Streak: domain.GeoStreak{Under: 2}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got[7].Streak.Flagged {
		t.Fatal("an idle user's latched flag was dropped; disconnecting must not acquit")
	}
	if got[8].Streak.Under != 2 {
		t.Fatalf("user 8 = %+v, want the updated streak", got[8])
	}
}

// An empty cycle writes nothing rather than truncating. A fleet where nobody
// is connected must not clear every latched flag.
func TestGeoStreakRepo_EmptySaveIsANoOpNotATruncate(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: {Streak: domain.GeoStreak{Flagged: true}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := r.Save(ctx, nil); err != nil {
		t.Fatalf("empty save: %v", err)
	}
	got, _ := r.Load(ctx)
	if !got[7].Streak.Flagged {
		t.Fatal("an empty cycle cleared a latched flag")
	}
}

func TestGeoStreakRepo_LoadOnEmptyTableIsEmptyNotAnError(t *testing.T) {
	got, err := newStreakRepo(t).Load(context.Background())
	if err != nil {
		t.Fatalf("load on a fresh install must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d rows, want none", len(got))
	}
}

// The verdict must round-trip alongside the counters that produced it.
//
// An operator deciding whether to act needs the reason, not just the flag.
// "Flagged" with no "because they were in 2 places for 3 consecutive checks,
// and the tolerance is 1" is not a basis for touching somebody's account.
func TestGeoStreakRepo_VerdictRoundTripsWithTheStreak(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	want := domain.GeoRecord{
		UserID:   7,
		Streak:   domain.GeoStreak{Over: 3, Flagged: true},
		State:    domain.GeoStateFlagged,
		Reason:   "in 2 places at once ([DE JP]); tolerance is 1",
		Places:   []string{"DE", "JP"},
		LiveIPs:  4,
		Complete: false,
	}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: want}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	g := got[7]
	if g.State != want.State || g.Reason != want.Reason {
		t.Fatalf("state/reason = %q / %q, want %q / %q", g.State, g.Reason, want.State, want.Reason)
	}
	if len(g.Places) != 2 || g.Places[0] != "DE" || g.Places[1] != "JP" {
		t.Fatalf("places = %v, want [DE JP]", g.Places)
	}
	if g.LiveIPs != 4 {
		t.Fatalf("liveIPs = %d, want 4", g.LiveIPs)
	}
	// The half a reader is most likely to miss: this count was a FLOOR, not a
	// total, because a panel could not be read. Losing that turns a partial
	// count into a clean bill of health.
	if g.Complete {
		t.Fatal("Complete=false did not survive; a floor would be shown as a total")
	}
}

// No places is a real state (nobody connected, or nothing placeable) and must
// come back as an empty list rather than a list containing one empty string.
// A phantom place would be counted by anything that reads the length.
func TestGeoStreakRepo_EmptyPlacesIsNotOneBlankPlace(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateIdle},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := r.Load(ctx)
	if len(got[7].Places) != 0 {
		t.Fatalf("places = %#v, want none", got[7].Places)
	}
}

// A reason is assembled from a policy an admin controls, so nothing this
// package owns bounds its length. Truncating beats failing the write: losing
// the tail of an explanation costs readability, failing costs the whole
// fleet's hysteresis for that cycle.
func TestGeoStreakRepo_OverlongReasonIsTruncatedNotRejected(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	long := ""
	for i := 0; i < 200; i++ {
		long += "long reason "
	}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {UserID: 7, Reason: long, Streak: domain.GeoStreak{Flagged: true}},
	}); err != nil {
		t.Fatalf("an overlong reason must not fail the write: %v", err)
	}
	got, _ := r.Load(ctx)
	if !got[7].Streak.Flagged {
		t.Fatal("the streak was lost along with the truncated reason")
	}
	if len(got[7].Reason) > 512 {
		t.Fatalf("reason len = %d, want <= 512", len(got[7].Reason))
	}
}

// List is the admin view's read: same rows, ordered.
func TestGeoStreakRepo_ListReturnsEveryRecord(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Flagged: true}},
		8: {UserID: 8, State: domain.GeoStateClean},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	seen := map[int64]domain.GeoState{}
	for _, rec := range got {
		seen[rec.UserID] = rec.State
	}
	if seen[7] != domain.GeoStateFlagged || seen[8] != domain.GeoStateClean {
		t.Fatalf("states = %v", seen)
	}
}

// Truncation must land on a rune boundary.
//
// Reasons carry city names and admin-typed co-travel text, so the 512-byte cut
// routinely falls inside a multi-byte sequence. A byte-wise slice stores an
// invalid string — some drivers reject it outright, and every reader renders a
// replacement character exactly where the explanation was.
func TestGeoStreakRepo_TruncationDoesNotSplitARune(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	// Every rune is 3 bytes, so a 512-byte cut cannot land on a boundary by
	// chance: 512 is not divisible by 3.
	long := ""
	for i := 0; i < 400; i++ {
		long += "东京"
	}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {UserID: 7, Reason: long, Places: []string{long}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !utf8.ValidString(got[7].Reason) {
		t.Fatalf("reason is not valid UTF-8 after truncation: %q", got[7].Reason)
	}
	for _, p := range got[7].Places {
		if !utf8.ValidString(p) {
			t.Fatalf("place is not valid UTF-8 after truncation: %q", p)
		}
	}
	if got[7].Reason == "" {
		t.Fatal("truncation must keep what fits, not discard everything")
	}
}
