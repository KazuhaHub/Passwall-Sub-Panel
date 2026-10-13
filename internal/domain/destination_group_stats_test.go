package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestDestinationGroupStatsQueryClonesIDsAndBoundsBothWindows(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).In(time.FixedZone("fixture", -7*3600))
	base := DestGroupStatsQuery{GroupIDs: []int64{7, 2, 7}, TrialSince: now.Add(-7 * 24 * time.Hour), DeniedSince: now.Add(-24 * time.Hour), Until: now}
	got, err := NormalizeDestinationGroupStatsQuery(base)
	if err != nil || !reflect.DeepEqual(got.GroupIDs, []int64{2, 7}) || !reflect.DeepEqual(base.GroupIDs, []int64{7, 2, 7}) || got.TrialSince.Location() != time.UTC || got.DeniedSince.Location() != time.UTC || got.Until.Location() != time.UTC {
		t.Fatalf("normalized group stats: %+v %v", got, err)
	}
	for _, change := range []func(*DestGroupStatsQuery){
		func(q *DestGroupStatsQuery) { q.GroupIDs = []int64{0} },
		func(q *DestGroupStatsQuery) { q.GroupIDs = []int64{-1} },
		func(q *DestGroupStatsQuery) { q.TrialSince = time.Time{} },
		func(q *DestGroupStatsQuery) { q.DeniedSince = time.Time{} },
		func(q *DestGroupStatsQuery) { q.TrialSince = q.Until },
		func(q *DestGroupStatsQuery) { q.DeniedSince = q.Until },
		func(q *DestGroupStatsQuery) { q.TrialSince = q.Until.Add(-32 * 24 * time.Hour) },
		func(q *DestGroupStatsQuery) { q.DeniedSince = q.Until.Add(-32 * 24 * time.Hour) },
	} {
		q := base
		change(&q)
		if _, err := NormalizeDestinationGroupStatsQuery(q); err != ErrValidation {
			t.Fatalf("unsafe stats query accepted: %+v %v", q, err)
		}
	}
}
