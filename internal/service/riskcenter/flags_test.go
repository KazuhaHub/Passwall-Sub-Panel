package riskcenter

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// A review record stores the acting admin's id, never a name (flag_records
// is name-free). The page names each one by the admin's CURRENT name, in
// one lookup; an admin deleted since, or params that do not parse, simply
// have no name. Only review records are read for an actor: a detector's
// record whose params happen to carry "by" names nobody.
func TestFlags_ResolvesReviewActors(t *testing.T) {
	h := newHarness()
	h.user(3, "root")
	h.user(5, "someone")
	h.flags.rows = []domain.FlagRecord{
		domain.ReviewFlag(7, domain.FlagReviewDismissed, domain.ReviewFlagParams{By: 3, Levels: domain.AttentionLevels{"geo": domain.FlagLevelFlagged}}, testNow),
		domain.ReviewFlag(7, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: 4}, testNow),
		domain.ReviewFlag(8, domain.FlagReviewUntrusted, domain.ReviewFlagParams{By: 3}, testNow),
		{UserID: 7, Source: domain.FlagSourceReview, Event: domain.FlagReviewUndismissed, Params: json.RawMessage(`{"by":"x"}`)},
		{UserID: 7, Source: domain.FlagSourceGeo, Event: domain.FlagEnterFlagged, Params: json.RawMessage(`{"by":5}`)},
	}
	h.flags.total = 5

	pg, err := h.svc.Flags(t.Context(), ports.FlagRecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Records) != 5 || pg.Total != 5 {
		t.Fatalf("page = %d records of %d, want the store's 5 of 5", len(pg.Records), pg.Total)
	}
	if want := map[int64]string{3: "root"}; !reflect.DeepEqual(pg.Actors, want) {
		t.Fatalf("actors = %v, want %v", pg.Actors, want)
	}
	if len(h.users.listed) != 1 || !reflect.DeepEqual(h.users.listed[0], []int64{3, 4}) {
		t.Fatalf("names read for %v, want one lookup of [3 4]", h.users.listed)
	}

	// No review record: no lookup at all.
	h.users.listed = nil
	h.flags.rows = h.flags.rows[4:]
	if pg, err = h.svc.Flags(t.Context(), ports.FlagRecordFilter{}); err != nil || len(pg.Actors) != 0 || len(h.users.listed) != 0 {
		t.Fatalf("actors %v, lookups %v (%v); want none", pg.Actors, h.users.listed, err)
	}
}
