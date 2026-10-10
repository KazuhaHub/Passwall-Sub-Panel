package domain

import (
	"errors"
	"time"
)

var ErrDestAuditLossExpired = errors.New("destination audit loss retry expired")

// DestAuditLoss contains receiver-estimated rows, never node events or account
// and destination values. ReceivedAt and BatchID stay fixed across retries.
type DestAuditLoss struct {
	HourMS, PanelID int64
	Kind, Reason    string
	Rows            int64
}

type DestAuditLossBatch struct {
	BatchID    string
	ReceivedAt time.Time
	Losses     []DestAuditLoss
}

// DestAuditControl is the narrow current collection cache value. Unavailable
// means deleted, non-native or unreadable; callers must fail closed.
type DestAuditControl struct {
	PanelID   int64
	Collect   AuditCollect
	Revision  uint64
	Available bool
}

type DestAuditPruned struct {
	Hits, Trial, Usage, Loss, Batches, Budget, Orphans int64
}

// Loss units stay independent. These are observed panel totals, never a
// complete account history or a sum of rows and connection events.
type DestAuditLosses struct {
	Rows      int64  `json:"rows"`
	Events    int64  `json:"events"`
	Unmatched int64  `json:"unmatched"`
	Scope     string `json:"scope"`
	Complete  bool   `json:"complete"`
}

type DestAuditPanelStats struct {
	Hits   int64
	Losses DestAuditLosses
}

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
