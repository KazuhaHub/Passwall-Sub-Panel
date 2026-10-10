package domain

import (
	"testing"
	"time"
)

func TestDestinationGroupTrialQueryRequiresOneGroupAndBoundedWindow(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).In(time.FixedZone("fixture", -7*3600))
	base := DestGroupTrialQuery{GroupID: 7, Since: now.Add(-24 * time.Hour), Until: now}
	got, err := NormalizeDestinationGroupTrialQuery(base)
	if err != nil || got.Since.Location() != time.UTC || got.Until.Location() != time.UTC || !got.Since.Equal(base.Since) {
		t.Fatalf("normalized query: %+v %v", got, err)
	}
	for _, change := range []func(*DestGroupTrialQuery){
		func(q *DestGroupTrialQuery) { q.GroupID = 0 },
		func(q *DestGroupTrialQuery) { q.GroupID = -1 },
		func(q *DestGroupTrialQuery) { q.Since = time.Time{} },
		func(q *DestGroupTrialQuery) { q.Since = time.UnixMilli(-1) },
		func(q *DestGroupTrialQuery) { q.Since = q.Until },
		func(q *DestGroupTrialQuery) { q.Until = q.Since.Add(-time.Hour) },
		func(q *DestGroupTrialQuery) { q.Since = q.Until.Add(-31*24*time.Hour - time.Millisecond) },
	} {
		q := base
		change(&q)
		if _, err := NormalizeDestinationGroupTrialQuery(q); err != ErrValidation {
			t.Fatalf("accepted unsafe query: %+v %v", q, err)
		}
	}
}
