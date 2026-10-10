package domain

type DestRecentHits struct {
	Days   int             `json:"days"`
	Items  []DestUserHit   `json:"items"`
	Losses DestAuditLosses `json:"losses"`
	// Current client projections, not historical hit panels, determine availability.
	ClientPanelIDs []int64 `json:"-"`
}

type DestUserHit struct {
	Source     string                   `json:"source"`
	SourceName *string                  `json:"source_name"`
	Action     string                   `json:"action"`
	Count      int64                    `json:"count"`
	TopDests   []DestUserHitDestination `json:"top_dests"`
	Panels     []DestUserHitPanel       `json:"panels"`
	LastAt     int64                    `json:"last_at"`
}

type DestUserHitDestination struct {
	Dest  string `json:"dest"`
	Port  int    `json:"port"`
	Count int64  `json:"count"`
}

type DestUserHitPanel struct {
	PanelID int64   `json:"panel_id"`
	Name    *string `json:"name"`
}
