package sqlstore

import (
	"context"
	"reflect"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// sampleEvidence is a realistic evidence value at the current version, with
// no why (TestGeoStreakRepo_WhyRoundTrips adds one): two provinces of one
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

// The why rides inside the evidence column and must come back whole through
// List, the admin view's read: the code a UI localizes from, and the policy
// the verdict was judged against. A why lost on the way back renders every
// row as the stored English, and a partial one renders a sentence with the
// wrong numbers in it.
func TestGeoStreakRepo_WhyRoundTrips(t *testing.T) {
	r := newStreakRepo(t)
	ev := sampleEvidence()
	ev.Why = &domain.GeoWhy{
		Code: domain.GeoWhyFlaggedSustained, Tier: domain.GeoTierRegion, Scope: domain.GeoScopeCity,
		Tol:       domain.GeoTolerances{Countries: 1, Regions: 3, Cities: 2},
		FlagAfter: 4, ClearAfter: 8, MinPlacedRatio: 0.25,
	}
	if err := r.Save(context.Background(), map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateFlagged, Evidence: ev},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	g := listed(t, r, 7)
	if g.Evidence.Why == nil {
		t.Fatalf("why lost in the store: evidence = %+v", g.Evidence)
	}
	if *g.Evidence.Why != *ev.Why {
		t.Fatalf("why = %+v, want %+v", *g.Evidence.Why, *ev.Why)
	}
	if !reflect.DeepEqual(g.Evidence, ev) {
		t.Fatalf("evidence = %+v, want %+v", g.Evidence, ev)
	}
}

// A row a v1 build wrote has evidence and no why. After the upgrade it must
// still read as that: v 1 (so a reader knows which build wrote it and falls
// back to the stored English) and no why — not a zero why, which a reader
// would try to localize as a code of "".
func TestGeoStreakRepo_V1EvidenceReadsWithoutWhy(t *testing.T) {
	r := newStreakRepo(t)
	ctx := context.Background()
	if err := r.db.WithContext(ctx).Exec(
		"INSERT INTO geo_streaks (user_id, state, evidence, updated_at) VALUES (?, ?, ?, ?)",
		int64(7), string(domain.GeoStateSuspect), `{"v":1,"spots":[]}`, int64(1_700_000_000_000),
	).Error; err != nil {
		t.Fatalf("insert v1 row: %v", err)
	}
	g := listed(t, r, 7)
	if g.Evidence.V != 1 || g.Evidence.Why != nil {
		t.Fatalf("evidence = v%d why %+v, want v1 with no why", g.Evidence.V, g.Evidence.Why)
	}
}

// What v3 adds rides inside the evidence column and must come back through
// List: a spot's region code (what the SPA names a province by in Chinese)
// and the concurrent distance. Either one dropped by the store renders the
// row as a v3 that measured nothing, which is a different statement from the
// one the poll made.
func TestGeoStreakRepo_V3EvidenceRoundTrips(t *testing.T) {
	r := newStreakRepo(t)
	ev := sampleEvidence()
	ev.Spots[0].RC = "GD"
	ev.Spread.MaxKm = 540
	if err := r.Save(context.Background(), map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateSuspect, Evidence: ev},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	g := listed(t, r, 7)
	if g.Evidence.Spots[0].RC != "GD" || g.Evidence.Spread.MaxKm != 540 {
		t.Fatalf("evidence = %+v, want the first spot's rc GD and max_km 540", g.Evidence)
	}
	if !reflect.DeepEqual(g.Evidence, ev) {
		t.Fatalf("evidence = %+v, want %+v", g.Evidence, ev)
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

// ---- CountFlagged: the notification bell's geo_anomaly count ----

// newStreakRepoWithUsers is newStreakRepo plus the users repository over the
// same database: CountFlagged joins users, so its fixtures need real rows.
func newStreakRepoWithUsers(t *testing.T) (*GeoStreakRepo, ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewGeoStreakRepo(db), NewRepos(db).User, db
}

// The bell counts the LATCH, not the last state. A flagged account that went
// idle, or whose panel could not be read, keeps its flag (the streak freezes)
// and must keep the bell lit — counting state=flagged would let it drop off
// by disconnecting. An account over tolerance that has not latched yet
// (suspect) is not flagged.
func TestGeoStreakRepo_CountFlaggedCountsTheLatchNotTheState(t *testing.T) {
	r, users, _ := newStreakRepoWithUsers(t)
	ctx := context.Background()
	recs := map[int64]domain.GeoRecord{}
	for i, rec := range []domain.GeoRecord{
		{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}},
		{State: domain.GeoStateIdle, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}},
		{State: domain.GeoStateUnknown, Streak: domain.GeoStreak{Under: 2, Flagged: true, Tier: domain.GeoTierCountry}},
		{State: domain.GeoStateSuspect, Streak: domain.GeoStreak{Over: 2, Tier: domain.GeoTierRegion}},
		{State: domain.GeoStateClean, Streak: domain.GeoStreak{Under: 6}},
	} {
		u := createServiceStateUser(t, users, i+1)
		rec.UserID = u.ID
		recs[u.ID] = rec
	}
	if err := r.Save(ctx, recs); err != nil {
		t.Fatalf("save: %v", err)
	}

	n, err := r.CountFlagged(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountFlagged: %v", err)
	}
	if n != 3 {
		t.Fatalf("CountFlagged = %d, want 3 (flagged, idle-latched, unknown-latched; not suspect, not clean)", n)
	}
}

// geo_streaks has no foreign key to users, so a deleted account leaves its
// row behind. The bell must not light for somebody who no longer exists —
// the admin could not find them to review.
func TestGeoStreakRepo_CountFlaggedIgnoresDeletedUsers(t *testing.T) {
	r, users, _ := newStreakRepoWithUsers(t)
	ctx := context.Background()
	kept := createServiceStateUser(t, users, 1)
	gone := createServiceStateUser(t, users, 2)
	const neverExisted = int64(987654)
	flagged := domain.GeoRecord{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{kept.ID: flagged, gone.ID: flagged, neverExisted: flagged}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := r.CountFlagged(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountFlagged: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountFlagged = %d, want 1 (a row whose user is gone is not an account to review)", n)
	}
}

// A row the detector has stopped judging (the user lost every client, or the
// poll is dead) keeps its last latch forever — Save leaves unjudged rows
// alone on purpose. The bell counts only rows judged since the cutoff, so a
// stale latch stops lighting it instead of staying on screen indefinitely.
func TestGeoStreakRepo_CountFlaggedIgnoresRowsTheDetectorStoppedJudging(t *testing.T) {
	r, users, db := newStreakRepoWithUsers(t)
	ctx := context.Background()
	fresh := createServiceStateUser(t, users, 1)
	stale := createServiceStateUser(t, users, 2)
	flagged := domain.GeoRecord{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{fresh.ID: flagged, stale.ID: flagged}); err != nil {
		t.Fatalf("save: %v", err)
	}
	now := time.Now()
	if err := db.Exec("UPDATE geo_streaks SET updated_at = ? WHERE user_id = ?",
		now.Add(-25*time.Hour).UnixMilli(), stale.ID).Error; err != nil {
		t.Fatalf("age the row: %v", err)
	}

	n, err := r.CountFlagged(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountFlagged: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountFlagged = %d, want 1 (a latch last judged 25 hours ago is outside a 24-hour window)", n)
	}
}

// Every save is a judgement, so every save must move updated_at — the upsert
// names it in DoUpdates for exactly that. Left out, the column keeps the
// row's FIRST insert time forever, and two readers go wrong without a single
// error: the poll's sample spacing compares now against that first judgement,
// so after one half-interval every "poll now" click counts as a sample again;
// and the bell's CountFlagged(now-24h) drops a latch the poll re-judges every
// cycle once the row turns a day old. The row is aged by hand, the way the
// test above does, so a second save within the same millisecond cannot pass
// by accident.
func TestGeoStreakRepo_ReSaveAdvancesUpdatedAt(t *testing.T) {
	r, users, db := newStreakRepoWithUsers(t)
	ctx := context.Background()
	u := createServiceStateUser(t, users, 1)
	flagged := domain.GeoRecord{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{u.ID: flagged}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	aged := time.Now().Add(-25 * time.Hour).UnixMilli()
	if err := db.Exec("UPDATE geo_streaks SET updated_at = ? WHERE user_id = ?", aged, u.ID).Error; err != nil {
		t.Fatalf("age the row: %v", err)
	}

	before := time.Now()
	if err := r.Save(ctx, map[int64]domain.GeoRecord{u.ID: flagged}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	loaded, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// GORM stamps the column from the process clock at save time
	// (autoUpdateTime:milli), not from the database's, so it cannot read
	// earlier than the moment just before the save.
	if got := loaded[u.ID].UpdatedAtMS; got < before.UnixMilli() {
		t.Fatalf("updated_at = %d after a re-save, want the second save's time (>= %d); the aged value was %d",
			got, before.UnixMilli(), aged)
	}
	n, err := r.CountFlagged(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountFlagged: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountFlagged = %d, want 1 — a latch judged just now is inside a 24-hour window", n)
	}
}

// ---- the risk center's evidence-free reads ----

// setStreakUpdatedAt pins a row's judgement time, for the freshness bounds.
func setStreakUpdatedAt(t *testing.T, db *gorm.DB, uid, ms int64) {
	t.Helper()
	if err := db.Exec("UPDATE geo_streaks SET updated_at = ? WHERE user_id = ?", ms, uid).Error; err != nil {
		t.Fatalf("set updated_at: %v", err)
	}
}

// AttentionLevels is the queue's geo read: the rows at attention — the LATCH
// (flagged, whatever the last state: an idle or unreadable latched account
// is still flagged, because those samples freeze the streak) or a ramp
// (over > 0, suspect) — judged at or after since. A clean row is not
// attention and a row the poll stopped judging is not fresh. The boundary is
// inclusive, the bell's comparison.
func TestGeoStreakRepo_AttentionLevelsIsLatchedOrRampingAndFresh(t *testing.T) {
	r, users, db := newStreakRepoWithUsers(t)
	ctx := context.Background()
	since := time.Now().Add(-24 * time.Hour)
	recs := map[int64]domain.GeoRecord{}
	var ids []int64
	for i, rec := range []domain.GeoRecord{
		{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}},
		{State: domain.GeoStateIdle, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}},
		{State: domain.GeoStateUnknown, Streak: domain.GeoStreak{Under: 2, Flagged: true, Tier: domain.GeoTierCountry}},
		{State: domain.GeoStateSuspect, Streak: domain.GeoStreak{Over: 2, Tier: domain.GeoTierRegion}},
		{State: domain.GeoStateClean, Streak: domain.GeoStreak{Under: 6}},
		{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}}, // stale
		{State: domain.GeoStateSuspect, Streak: domain.GeoStreak{Over: 1}},                // judged exactly at since
	} {
		u := createServiceStateUser(t, users, i+1)
		rec.UserID = u.ID
		rec.Evidence = sampleEvidence()
		recs[u.ID] = rec
		ids = append(ids, u.ID)
	}
	if err := r.Save(ctx, recs); err != nil {
		t.Fatalf("save: %v", err)
	}
	setStreakUpdatedAt(t, db, ids[5], since.UnixMilli()-1)
	setStreakUpdatedAt(t, db, ids[6], since.UnixMilli())
	loaded, err := r.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got, err := r.AttentionLevels(ctx, since)
	if err != nil {
		t.Fatalf("AttentionLevels: %v", err)
	}
	var want []ports.GeoAttentionRow
	for _, i := range []int{0, 1, 2, 3, 6} {
		rec := loaded[ids[i]]
		want = append(want, ports.GeoAttentionRow{
			UserID: ids[i], Flagged: rec.Streak.Flagged, Over: rec.Streak.Over, UpdatedAtMS: rec.UpdatedAtMS,
		})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AttentionLevels = %+v\nwant %+v", got, want)
	}
	if got[4].UpdatedAtMS != since.UnixMilli() {
		t.Fatalf("the boundary row reads updated_at %d, want %d", got[4].UpdatedAtMS, since.UnixMilli())
	}
}

// geo_streaks has no foreign key: a deleted account's row stays until the
// poll stops judging it, and an id that never existed is nobody to review.
func TestGeoStreakRepo_AttentionLevelsSkipsDeletedAccounts(t *testing.T) {
	r, users, _ := newStreakRepoWithUsers(t)
	ctx := context.Background()
	kept := createServiceStateUser(t, users, 1)
	gone := createServiceStateUser(t, users, 2)
	const neverExisted = int64(987654)
	flagged := domain.GeoRecord{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}}
	if err := r.Save(ctx, map[int64]domain.GeoRecord{kept.ID: flagged, gone.ID: flagged, neverExisted: flagged}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	got, err := r.AttentionLevels(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("AttentionLevels: %v", err)
	}
	if len(got) != 1 || got[0].UserID != kept.ID || !got[0].Flagged || got[0].Over != 3 {
		t.Fatalf("AttentionLevels = %+v, want only the existing account's latched row", got)
	}
}

// ListByUsers is the page's evidence: the whole row of each asked account,
// any state, evidence included; an account with no row is absent. Asked in
// chunks of 500, so a long page never meets a dialect's parameter limit.
func TestGeoStreakRepo_ListByUsers(t *testing.T) {
	r, users, db := newStreakRepoWithUsers(t)
	ctx := context.Background()
	a := createServiceStateUser(t, users, 1)
	b := createServiceStateUser(t, users, 2)
	c := createServiceStateUser(t, users, 3)
	noRow := createServiceStateUser(t, users, 4)
	recs := map[int64]domain.GeoRecord{
		a.ID: {UserID: a.ID, State: domain.GeoStateClean, Streak: domain.GeoStreak{Under: 6}, Complete: true},
		b.ID: {UserID: b.ID, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}, Evidence: sampleEvidence(), Complete: true},
		c.ID: {UserID: c.ID, State: domain.GeoStateSuspect, Streak: domain.GeoStreak{Over: 1}, Evidence: sampleEvidence(), Places: []string{"CN", "US"}, Complete: true},
	}
	if err := r.Save(ctx, recs); err != nil {
		t.Fatalf("save: %v", err)
	}
	full := map[int64]domain.GeoRecord{}
	for _, rec := range []int64{a.ID, b.ID, c.ID} {
		full[rec] = listed(t, r, rec)
	}
	want := []domain.GeoRecord{full[a.ID], full[c.ID]}

	got, err := r.ListByUsers(ctx, []int64{c.ID, noRow.ID, a.ID})
	if err != nil {
		t.Fatalf("ListByUsers: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListByUsers = %+v\nwant %+v", got, want)
	}
	if got[1].Evidence.V == 0 {
		t.Fatal("ListByUsers dropped the evidence")
	}

	reads := countReads(t, db, "geo_streaks")
	got, err = r.ListByUsers(ctx, chunkSpanningIDs(a.ID, c.ID, noRow.ID))
	if err != nil {
		t.Fatalf("ListByUsers over 1100 ids: %v", err)
	}
	if !reflect.DeepEqual(got, want) || reads() != 3 {
		t.Fatalf("ListByUsers over 1100 ids = %d rows in %d reads, want %d in 3", len(got), reads(), len(want))
	}
}

// CountFreshUnknown is the queue's "the database cannot place N accounts"
// line: existing accounts whose LAST verdict is unknown, judged at or after
// since.
func TestGeoStreakRepo_CountFreshUnknown(t *testing.T) {
	r, users, db := newStreakRepoWithUsers(t)
	ctx := context.Background()
	since := time.Now().Add(-24 * time.Hour)
	unknown := domain.GeoRecord{State: domain.GeoStateUnknown, Streak: domain.GeoStreak{Under: 1}}
	recs := map[int64]domain.GeoRecord{}
	var ids []int64
	for i, rec := range []domain.GeoRecord{
		unknown, // fresh: in
		unknown, // stale: out
		unknown, // deleted: out
		{State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}},
		unknown, // judged exactly at since: in
	} {
		u := createServiceStateUser(t, users, i+1)
		recs[u.ID] = rec
		ids = append(ids, u.ID)
	}
	if err := r.Save(ctx, recs); err != nil {
		t.Fatalf("save: %v", err)
	}
	setStreakUpdatedAt(t, db, ids[1], since.UnixMilli()-1)
	setStreakUpdatedAt(t, db, ids[4], since.UnixMilli())
	if err := users.Delete(ctx, ids[2]); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := r.CountFreshUnknown(ctx, since)
	if err != nil || n != 2 {
		t.Fatalf("CountFreshUnknown = %d, %v; want 2, nil", n, err)
	}
}
