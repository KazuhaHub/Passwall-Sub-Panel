package domain

import "time"

type DestUsageQuery struct {
	UserID, PanelID int64
	Since, Until    time.Time
	Limit           int
}

type DestUsageSite struct {
	Site  string `json:"site"`
	Count int64  `json:"count"`
}

type DestUsagePage struct {
	Items      []DestUsageSite `json:"items"`
	TotalSites int64           `json:"total_sites"`
	TotalCount int64           `json:"total_count"`
	Losses     DestAuditLosses `json:"losses"`
}

// A positive account is mandatory even at the repository boundary. Transport
// and application layers additionally enforce audited access and retention.
func NormalizeDestinationUsageQuery(q DestUsageQuery) (DestUsageQuery, error) {
	if q.UserID <= 0 || q.PanelID < 0 || q.Since.IsZero() || q.Since.UnixMilli() <= 0 || !q.Until.After(q.Since) || q.Until.Sub(q.Since) > 31*24*time.Hour {
		return q, ErrValidation
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 200 {
		return q, ErrValidation
	}
	q.Since, q.Until = q.Since.UTC(), q.Until.UTC()
	return q, nil
}
