package domain

import (
	"testing"
	"time"
)

func TestDestinationUsageQueryRequiresOneAccountAndBoundedWindow(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	base := DestUsageQuery{UserID: 7, Since: now.Add(-24 * time.Hour), Until: now}
	q, err := NormalizeDestinationUsageQuery(base)
	if err != nil || q.Limit != 20 {
		t.Fatalf("default query: %+v %v", q, err)
	}
	for _, change := range []func(*DestUsageQuery){
		func(q *DestUsageQuery) { q.UserID = 0 },
		func(q *DestUsageQuery) { q.UserID = -1 },
		func(q *DestUsageQuery) { q.PanelID = -1 },
		func(q *DestUsageQuery) { q.Since = time.Time{} },
		func(q *DestUsageQuery) { q.Since = q.Until },
		func(q *DestUsageQuery) { q.Since = q.Until.Add(-32 * 24 * time.Hour) },
		func(q *DestUsageQuery) { q.Limit = 201 },
		func(q *DestUsageQuery) { q.Limit = -1 },
	} {
		q := base
		change(&q)
		if _, err := NormalizeDestinationUsageQuery(q); err != ErrValidation {
			t.Fatalf("accepted unsafe query: %+v %v", q, err)
		}
	}
}
