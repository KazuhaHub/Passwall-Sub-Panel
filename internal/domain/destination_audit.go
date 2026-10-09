package domain

import "time"

// DestHit is one logical destination key after rule-fragment IDs have been
// mapped to their stable source. Trial keys use UserID and Port zero.
type DestHit struct {
	HourMS, PanelID, UserID int64
	Source, Action, Dest    string
	Port                    int
	Count                   int64
	FirstAt, LastAt         time.Time
}

type DestUsage struct {
	HourMS, PanelID, UserID int64
	Site                    string
	Count                   int64
}
