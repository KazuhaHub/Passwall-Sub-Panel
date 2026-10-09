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

// DestAuditBatch is server-owned input to the first ingestion transaction.
// Rows are validated and merged before this boundary. ReceivedAt determines
// durable dedup retention; ReceivedHourMS is the receiver's budget bucket.
type DestAuditBatch struct {
	AgentID, BatchID, Kind          string
	PanelID, HourMS, ReceivedHourMS int64
	ReceivedAt                      time.Time
	CollectRevision                 uint64
	Hits                            []DestHit
	Usage                           []DestUsage
	Dropped, Unmatched              uint64
	Losses                          map[string]int64
}

type DestAuditBegin struct {
	Duplicate        bool
	Rejected         string
	Reserved, Stored int
}

type DestAuditChunk struct {
	AgentID, BatchID, Kind string
	PanelID                int64
	CollectRevision        uint64
	Hits                   []DestHit
	Usage                  []DestUsage
}
