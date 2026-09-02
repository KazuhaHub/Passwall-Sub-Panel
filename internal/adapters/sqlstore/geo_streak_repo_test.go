package sqlstore

import (
	"context"
	"testing"

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
	want := map[int64]domain.GeoStreak{
		7: {Over: 3, Under: 0, Flagged: true},
		8: {Over: 0, Under: 5, Flagged: false},
	}
	if err := r.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for uid, w := range want {
		if got[uid] != w {
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
	if err := NewGeoStreakRepo(db).Save(ctx, map[int64]domain.GeoStreak{
		7: {Over: 4, Flagged: true},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second repo over the same database stands in for a restarted process.
	got, err := NewGeoStreakRepo(db).Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got[7].Flagged || got[7].Over != 4 {
		t.Fatalf("after a reopen: got %+v, want the flag and the streak intact", got[7])
	}
}

// Written every cycle, so an upsert rather than an insert. A second save for
// the same user must overwrite, not collide or duplicate.
func TestGeoStreakRepo_SaveIsIdempotentAndUpdates(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoStreak{7: {Over: 1}}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := r.Save(ctx, map[int64]domain.GeoStreak{7: {Over: 0, Under: 2, Flagged: false}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 — the second save must update, not insert", len(got))
	}
	if got[7].Over != 0 || got[7].Under != 2 {
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
	if err := r.Save(ctx, map[int64]domain.GeoStreak{
		7: {Over: 3, Flagged: true},
		8: {Under: 1},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Next cycle sees only user 8 — user 7 went idle.
	if err := r.Save(ctx, map[int64]domain.GeoStreak{8: {Under: 2}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got[7].Flagged {
		t.Fatal("an idle user's latched flag was dropped; disconnecting must not acquit")
	}
	if got[8].Under != 2 {
		t.Fatalf("user 8 = %+v, want the updated streak", got[8])
	}
}

// An empty cycle writes nothing rather than truncating. A fleet where nobody
// is connected must not clear every latched flag.
func TestGeoStreakRepo_EmptySaveIsANoOpNotATruncate(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoStreak{7: {Flagged: true}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := r.Save(ctx, nil); err != nil {
		t.Fatalf("empty save: %v", err)
	}
	got, _ := r.Load(ctx)
	if !got[7].Flagged {
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
