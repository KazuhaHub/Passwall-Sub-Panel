package domain

import (
	"slices"
	"time"
)

type DestGroupStatsQuery struct {
	GroupIDs                       []int64
	TrialSince, DeniedSince, Until time.Time
}

// Observed counters only. No destination, account or completeness claim leaves
// this projection. Trial sites retain the producer's supplied site boundaries.
type DestGroupStats struct {
	TrialSites, TrialCount, DeniedCount int64
}

func NormalizeDestinationGroupStatsQuery(q DestGroupStatsQuery) (DestGroupStatsQuery, error) {
	for _, since := range []time.Time{q.TrialSince, q.DeniedSince} {
		if since.IsZero() || since.UnixMilli() <= 0 || !q.Until.After(since) || q.Until.Sub(since) > 31*24*time.Hour {
			return q, ErrValidation
		}
	}
	q.GroupIDs = slices.Clone(q.GroupIDs)
	slices.Sort(q.GroupIDs)
	q.GroupIDs = slices.Compact(q.GroupIDs)
	for _, id := range q.GroupIDs {
		if id <= 0 {
			return q, ErrValidation
		}
	}
	q.TrialSince, q.DeniedSince, q.Until = q.TrialSince.UTC(), q.DeniedSince.UTC(), q.Until.UTC()
	return q, nil
}
