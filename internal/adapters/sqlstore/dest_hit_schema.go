package sqlstore

import "time"

// Destination-policy hit records.
//
// dest_hits is an hourly aggregate: one row per (hour, panel, user, source,
// action, destination, port), with a count and the first and last instant in
// the hour. It holds NO client source address (the protocol does not carry one
// and the table has no column for it), and rows are pruned on a retention
// setting by the hourly cleanup.
//
// THE FIRST AND LAST INSTANTS ARE BIGINT MILLISECONDS, like the hour, and not a
// datetime: MySQL's datetime has no fractional seconds by default (the instants
// would lose their milliseconds), and SQLite stores a time as text, so MIN/MAX
// across rows would be a string comparison. A bigint compares the same on every
// dialect, which is what lets one LEAST/GREATEST (MIN/MAX on SQLite) merge the
// rows of a re-sent hour.
//
// THE PRIMARY KEY IS WIDE ON PURPOSE. It is the aggregation key, and MySQL's
// utf8mb4 puts it near 1.2 KiB, under the 3072-byte InnoDB limit
// (TestDestHitRow_PrimaryKeyFitsMySQL).

const (
	destHitSourceWidth = 24
	destHitActionWidth = 8
	destHitDestWidth   = 253
	destBatchIDWidth   = 32
	destDropKindWidth  = 16
)

type destHitRow struct {
	HourMS  int64  `gorm:"column:hour_ms;primaryKey;autoIncrement:false;index:idx_dest_hit_user,priority:2;index:idx_dest_hit_source,priority:2;index:idx_dest_hit_panel,priority:2"`
	PanelID int64  `gorm:"column:panel_id;primaryKey;autoIncrement:false;index:idx_dest_hit_panel,priority:1"`
	UserID  int64  `gorm:"column:user_id;primaryKey;autoIncrement:false;index:idx_dest_hit_user,priority:1"`
	Source  string `gorm:"column:source;primaryKey;size:24;index:idx_dest_hit_source,priority:1"`
	Action  string `gorm:"column:action;primaryKey;size:8"`
	Dest    string `gorm:"column:dest;primaryKey;size:253"`
	Port    int    `gorm:"column:port;primaryKey;autoIncrement:false"`
	Count   int64  `gorm:"column:count;not null;default:0"`
	FirstMS int64  `gorm:"column:first_ms;not null;default:0"`
	LastMS  int64  `gorm:"column:last_ms;not null;default:0"`
}

func (destHitRow) TableName() string { return "dest_hits" }

// destAuditBatchRow remembers a stored batch id, so a batch the node sends
// again after the panel restarted (when the in-memory memory of it is gone) is
// counted once.
type destAuditBatchRow struct {
	AgentID    string    `gorm:"column:agent_id;primaryKey;size:64"`
	BatchID    string    `gorm:"column:batch_id;primaryKey;size:32"`
	ReceivedAt time.Time `gorm:"column:received_at;not null;index:idx_dest_audit_batch_received"`
}

func (destAuditBatchRow) TableName() string { return "dest_audit_batches" }

// destAuditDropRow counts reported rows that did not become hit rows, per hour
// and panel, so a time range can say how much of it is missing. The hour is the
// one the loss belongs to (the row's own hour where it has one, otherwise the
// hour the loss was noticed in).
type destAuditDropRow struct {
	HourMS  int64  `gorm:"column:hour_ms;primaryKey;autoIncrement:false"`
	PanelID int64  `gorm:"column:panel_id;primaryKey;autoIncrement:false"`
	Kind    string `gorm:"column:kind;primaryKey;size:16"`
	Count   int64  `gorm:"column:count;not null;default:0"`
}

func (destAuditDropRow) TableName() string { return "dest_audit_drops" }
