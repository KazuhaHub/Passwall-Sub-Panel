package domain

import "time"

type DestGroupTrialQuery struct {
	GroupID      int64
	Since, Until time.Time
}

// This storage projection contains anonymous supplied sites only. Current
// list coverage and the effective retention window belong to the application.
type DestGroupTrialSite struct {
	Site   string `json:"site"`
	Count  int64  `json:"count"`
	LastAt int64  `json:"last_at"`
}

type DestGroupTrialData struct {
	Items          []DestGroupTrialSite `json:"items"`
	TotalCount     int64                `json:"total_count"`
	DroppedInRange int64                `json:"dropped_in_range"`
	Losses         DestAuditLosses      `json:"losses"`
}

func NormalizeDestinationGroupTrialQuery(q DestGroupTrialQuery) (DestGroupTrialQuery, error) {
	if q.GroupID <= 0 || q.Since.IsZero() || q.Since.UnixMilli() <= 0 || !q.Until.After(q.Since) || q.Until.Sub(q.Since) > 31*24*time.Hour {
		return q, ErrValidation
	}
	q.Since, q.Until = q.Since.UTC(), q.Until.UTC()
	return q, nil
}
