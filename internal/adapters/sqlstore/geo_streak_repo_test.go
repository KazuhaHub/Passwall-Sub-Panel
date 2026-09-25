package sqlstore

import (
	"context"
	"reflect"
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
	if err := ensureTestSchema(db); err != nil {
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
	if err := ensureTestSchema(db); err != nil {
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

// A user the poll did not judge this cycle is absent from the cycle's map.
// Their row must SURVIVE: deleting unmentioned rows would let a flagged
// account clear itself simply by not being judged for one poll. (An idle user
// is judged — the poll re-saves them with the streak frozen — so this is about
// users the cycle skipped, not about idling.)
func TestGeoStreakRepo_AbsentUserKeepsTheirRow(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {Streak: domain.GeoStreak{Over: 3, Flagged: true}},
		8: {Streak: domain.GeoStreak{Under: 1}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Next cycle judges only user 8 — user 7 was not judged at all.
	if err := r.Save(ctx, map[int64]domain.GeoRecord{8: {Streak: domain.GeoStreak{Under: 2}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got[7].Streak.Flagged {
		t.Fatal("an unjudged user's latched flag was dropped; skipping a cycle must not acquit")
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

// sampleEvidence is a realistic v1 evidence value: two provinces of one
// country, some sources excluded, most of the upstream window stale. One
// non-ASCII name, because the column holds whatever the geo database says.
func sampleEvidence() domain.GeoEvidence {
	return domain.GeoEvidence{
		V: domain.GeoEvidenceVersion,
		Spots: []domain.GeoSpot{
			{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 2},
			{CC: "CN", Region: "湖南", City: "长沙", N: 1},
		},
		Excluded: domain.GeoExcluded{Shared: 1, Infra: 2},
		Stale:    21,
		Coverage: domain.GeoCoverage{Placed: 3, RegionKnown: 3, CityKnown: 3},
		Networks: 3,
		Spread:   domain.GeoSpread{Countries: 1, Regions: 2, RegionCountry: "CN", Cities: 2, CityCountry: "CN"},
	}
}

// listed returns one user's record through List, the admin view's read.
func listed(t *testing.T, r *GeoStreakRepo, uid int64) domain.GeoRecord {
	t.Helper()
	rows, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, rec := range rows {
		if rec.UserID == uid {
			return rec
		}
	}
	t.Fatalf("user %d not listed (rows: %d)", uid, len(rows))
	return domain.GeoRecord{}
}

// Everything v2 adds to a verdict must survive the store, or a restart
// quietly changes what the detector knows.
//
// The tier and the suspension streak are STATE, not decoration: losing the
// tier makes a latched flag unable to say why it was raised, and losing
// BanOver resets the sustain window on every deploy — a sharer who times a
// restart would never be suspended. Concurrent, Excluded and the evidence are
// what an operator weighs before acting; a verdict whose evidence came back
// empty reads as "nothing found" rather than "not recorded".
func TestGeoStreakRepo_TierBanOverConcurrentExcludedEvidenceRoundTrip(t *testing.T) {
	r := newStreakRepo(t)
	want := domain.GeoRecord{
		UserID:     7,
		Streak:     domain.GeoStreak{Over: 4, Flagged: true, Tier: domain.GeoTierRegion, BanOver: 2},
		State:      domain.GeoStateFlagged,
		Reason:     "in 2 regions of CN at once ([CN/Guangdong CN/湖南]); tolerance is 1",
		Places:     []string{"CN"},
		LiveIPs:    24,
		Concurrent: 3,
		Excluded:   3,
		Evidence:   sampleEvidence(),
		Complete:   true,
	}
	if err := r.Save(context.Background(), map[int64]domain.GeoRecord{7: want}); err != nil {
		t.Fatalf("save: %v", err)
	}
	g := listed(t, r, 7)
	if g.Streak != want.Streak {
		t.Fatalf("streak = %+v, want %+v (tier and ban streak must survive the store)", g.Streak, want.Streak)
	}
	if g.Concurrent != want.Concurrent || g.Excluded != want.Excluded {
		t.Fatalf("concurrent/excluded = %d/%d, want %d/%d", g.Concurrent, g.Excluded, want.Concurrent, want.Excluded)
	}
	if !reflect.DeepEqual(g.Evidence, want.Evidence) {
		t.Fatalf("evidence = %+v, want %+v", g.Evidence, want.Evidence)
	}
}

// An upgraded install's rows were written by a build that knew none of the
// new columns. AutoMigrate gives the scalar ones their defaults and leaves
// evidence NULL (a text column carries no DEFAULT on MySQL). Such a row must
// read as "no evidence recorded" (v 0), not fail the whole read — a scan
// error here would blank the admin view and, through Load, make every poll
// judge without history until the rows were rewritten.
//
// The empty string is the other shape "nothing" can take in a text column.
func TestGeoStreakRepo_LegacyNullEvidenceReadsAsNone(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.db.WithContext(ctx).Exec(
		"INSERT INTO geo_streaks (user_id, flagged, state, places, updated_at) VALUES (?, ?, ?, ?, ?)",
		int64(7), true, string(domain.GeoStateFlagged), "DE,JP", int64(1_700_000_000_000),
	).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := r.db.WithContext(ctx).Exec(
		"INSERT INTO geo_streaks (user_id, state, evidence, updated_at) VALUES (?, ?, ?, ?)",
		int64(8), string(domain.GeoStateClean), "", int64(1_700_000_000_000),
	).Error; err != nil {
		t.Fatalf("insert empty-evidence row: %v", err)
	}

	loaded, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load over legacy rows must not error: %v", err)
	}
	for _, uid := range []int64{7, 8} {
		g := listed(t, r, uid)
		if !reflect.DeepEqual(g.Evidence, domain.GeoEvidence{}) {
			t.Fatalf("user %d: evidence = %+v, want none (v 0)", uid, g.Evidence)
		}
		if g.Streak.Tier != domain.GeoTierNone || g.Streak.BanOver != 0 || g.Concurrent != 0 || g.Excluded != 0 {
			t.Fatalf("user %d: new columns = %+v concurrent %d excluded %d, want their zero defaults",
				uid, g.Streak, g.Concurrent, g.Excluded)
		}
		if _, ok := loaded[uid]; !ok {
			t.Fatalf("user %d missing from Load", uid)
		}
	}
	// The latch an old build set is still a latch after the upgrade.
	if !loaded[7].Streak.Flagged {
		t.Fatal("a legacy latched flag was lost across the upgrade")
	}
}

// Load is the poll's read and runs every cycle over the whole table; the poll
// never reads evidence, which is the one sizeable column. It still needs the
// streak (tier and ban streak included) and when the user was last judged.
func TestGeoStreakRepo_LoadSkipsEvidence(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	want := domain.GeoRecord{
		UserID:   7,
		Streak:   domain.GeoStreak{Over: 2, Tier: domain.GeoTierCity, BanOver: 1},
		State:    domain.GeoStateSuspect,
		Evidence: sampleEvidence(),
	}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: want}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	g := loaded[7]
	if !reflect.DeepEqual(g.Evidence, domain.GeoEvidence{}) {
		t.Fatalf("Load returned evidence %+v; the poll's read must not pull the evidence column", g.Evidence)
	}
	if g.Streak != want.Streak {
		t.Fatalf("Load streak = %+v, want %+v", g.Streak, want.Streak)
	}
	if g.UpdatedAtMS <= 0 {
		t.Fatalf("Load UpdatedAtMS = %d, want the last judgement time", g.UpdatedAtMS)
	}
	if l := listed(t, r, 7); !reflect.DeepEqual(l.Evidence, want.Evidence) {
		t.Fatalf("List evidence = %+v, want %+v", l.Evidence, want.Evidence)
	}
}

// The row is an upsert, and an upsert only rewrites the columns it names. A
// new column missing from that list keeps its FIRST value forever: the tier
// of a flag raised last month, a ban streak that never resets, evidence from
// a different cycle than the verdict beside it. Zero values included — a
// streak consumed back to 0, and a verdict saved with no evidence, must
// overwrite rather than leave the old value in place.
func TestGeoStreakRepo_UpsertUpdatesNewColumns(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	first := domain.GeoRecord{
		UserID:     7,
		Streak:     domain.GeoStreak{Over: 3, Tier: domain.GeoTierCity, BanOver: 3},
		Concurrent: 4,
		Excluded:   2,
		Evidence:   sampleEvidence(),
	}
	second := domain.GeoRecord{
		UserID:     7,
		Streak:     domain.GeoStreak{Under: 1, Flagged: true, Tier: domain.GeoTierRegion},
		Concurrent: 1,
		Evidence: domain.GeoEvidence{
			V:        domain.GeoEvidenceVersion,
			Spots:    []domain.GeoSpot{{CC: "JP", Region: "Kanto", City: "Tokyo", N: 1}},
			Coverage: domain.GeoCoverage{Placed: 1, RegionKnown: 1, CityKnown: 1},
			Networks: 1,
			Spread:   domain.GeoSpread{Countries: 1, Regions: 1, RegionCountry: "JP", Cities: 1, CityCountry: "JP"},
		},
	}
	for _, rec := range []domain.GeoRecord{first, second} {
		if err := r.Save(ctx, map[int64]domain.GeoRecord{7: rec}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	g := listed(t, r, 7)
	if g.Streak != second.Streak {
		t.Fatalf("streak = %+v, want the second save's %+v", g.Streak, second.Streak)
	}
	if g.Concurrent != 1 || g.Excluded != 0 {
		t.Fatalf("concurrent/excluded = %d/%d, want 1/0 from the second save", g.Concurrent, g.Excluded)
	}
	if !reflect.DeepEqual(g.Evidence, second.Evidence) {
		t.Fatalf("evidence = %+v, want the second save's %+v", g.Evidence, second.Evidence)
	}

	third := second
	third.Evidence = domain.GeoEvidence{}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{7: third}); err != nil {
		t.Fatalf("third save: %v", err)
	}
	if g := listed(t, r, 7); !reflect.DeepEqual(g.Evidence, domain.GeoEvidence{}) {
		t.Fatalf("evidence = %+v after a save with none; stale evidence must not outlive its verdict", g.Evidence)
	}
}

// Places are stored comma-joined, so a comma inside one place would come back
// as two. Anything that reads the length would then count a place that does
// not exist — and a place count is exactly what a verdict is about.
func TestGeoStreakRepo_PlacesWithCommasDoNotSplit(t *testing.T) {
	r := newStreakRepo(t)
	if err := r.Save(context.Background(), map[int64]domain.GeoRecord{
		7: {UserID: 7, Places: []string{"US/Washington, D.C.", "JP"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	g := listed(t, r, 7)
	if want := []string{"US/Washington D.C.", "JP"}; !reflect.DeepEqual(g.Places, want) {
		t.Fatalf("places = %#v, want %#v", g.Places, want)
	}
}

// "No evidence recorded" has one stored form: NULL, the same as a row an
// older build wrote. A verdict saved without evidence must not leave an
// object saying v 0 in its place — a reader filtering on NULL would then
// count it as recorded.
func TestGeoStreakRepo_NoEvidenceIsStoredAsNull(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateIdle},
		8: {UserID: 8, State: domain.GeoStateClean, Evidence: sampleEvidence()},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var nulls int64
	if err := r.db.WithContext(ctx).Table("geo_streaks").Where("evidence IS NULL").Count(&nulls).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if nulls != 1 {
		t.Fatalf("rows with NULL evidence = %d, want 1 (only the verdict saved without evidence)", nulls)
	}
}
