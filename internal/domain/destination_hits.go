package domain

import "time"

type DestHitQuery struct {
	Since, Until                                 time.Time
	UserID, PanelID                              int64
	Source, SourceKind, Action, Keyword, GroupBy string
	IncludeTrial                                 bool
	Page, PageSize                               int
}

type DestHitSummary struct {
	Block   int64 `json:"block"`
	Deny    int64 `json:"deny"`
	Observe int64 `json:"observe"`
	Users   int64 `json:"users"`
}

type DestHitSource struct {
	Source string  `json:"source"`
	Name   *string `json:"name"`
}

type DestHitRecord struct {
	Hour       int64   `json:"hour"`
	PanelID    int64   `json:"panel_id"`
	PanelName  *string `json:"panel_name"`
	UserID     int64   `json:"user_id"`
	UserUPN    *string `json:"user_upn"`
	Source     string  `json:"source"`
	SourceName *string `json:"source_name"`
	Action     string  `json:"action"`
	Dest       string  `json:"dest"`
	Port       int     `json:"port"`
	Count      int64   `json:"count"`
	FirstAt    int64   `json:"first_at"`
	LastAt     int64   `json:"last_at"`
}

type DestHitGroup struct {
	Key     string  `json:"key"`
	Name    *string `json:"name"`
	Count   int64   `json:"count"`
	Users   int64   `json:"user_count"`
	Sources int64   `json:"source_count"`
	LastAt  int64   `json:"last_at"`
}

// The transport selects Records or Groups as items according to GroupBy.
// Summary deliberately ignores action/source/keyword/trial filters. Losses
// contain observed panel totals and never acquire account-level attribution.
type DestHitPage struct {
	Records        []DestHitRecord
	Groups         []DestHitGroup
	GroupBy        string
	Total          int64
	Page, PageSize int
	Summary        DestHitSummary
	Sources        []DestHitSource
	DroppedInRange int64
	Losses         DestAuditLosses
}
