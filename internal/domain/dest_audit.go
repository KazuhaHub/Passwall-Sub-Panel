package domain

import "time"

// Destination-policy hit recording ("audit"). A native node reports which
// destination rules fired; the panel stores them as hourly aggregates. What is
// stored is bounded by design: the rule that fired, who it fired for, the
// destination and port the client asked for, and counts. It never holds the
// client's source address, and the rows age out on a retention setting.

// AuditCollect is the admin's per-panel choice of how much a native node
// records. It is stored on the panel row and written only through the panel
// repo's narrow native-metadata writer, never by a whole-row save.
type AuditCollect string

const (
	// AuditCollectOff records nothing: the node is told not to attach hit
	// webhooks to its rules.
	AuditCollectOff AuditCollect = "off"
	// AuditCollectHits records which rule fired for whom (tier A). The default.
	AuditCollectHits AuditCollect = "hits"
	// AuditCollectHitsAndUsage adds per-site usage (tier B). It is part of the
	// model so a stored value survives, but no node declares the capability yet,
	// so it is never effective.
	AuditCollectHitsAndUsage AuditCollect = "hits_and_usage"
)

// Valid reports whether c is one of the three levels.
func (c AuditCollect) Valid() bool {
	return c == AuditCollectOff || c == AuditCollectHits || c == AuditCollectHitsAndUsage
}

// NormalizeAuditCollect resolves what a row or a request holds. Empty is the
// default (hits), which is what a row created before the column existed reads
// as. A value that is not a level reads as OFF: the choice is about recording
// people's activity, and junk must not switch it on.
func NormalizeAuditCollect(c AuditCollect) AuditCollect {
	switch {
	case c == "":
		return AuditCollectHits
	case c.Valid():
		return c
	}
	return AuditCollectOff
}

// Normalized is NormalizeAuditCollect of c.
func (c AuditCollect) Normalized() AuditCollect { return NormalizeAuditCollect(c) }

// DestHit is one aggregated hour of one rule's hits for one person and
// destination. It is the stored form: Source is p<policy id> for a policy or a
// source rule's stored form s<node id>, never the node's x-suffixed rule id.
type DestHit struct {
	HourMS  int64
	PanelID int64
	// UserID is 0 for an allowlist trial row, which the node folds to group x
	// site before it leaves the node.
	UserID int64
	Source string
	// Action is "block" or "observe" (report-only: the connection was allowed
	// and WOULD HAVE BEEN BLOCKED), as it was when the hit happened.
	Action string
	Dest   string
	Port   int
	Count  int64
	// FirstMS / LastMS are the earliest and latest hit in the hour.
	FirstMS int64
	LastMS  int64
}

// DestHitAction values.
const (
	DestHitBlock   = "block"
	DestHitObserve = "observe"
)

// DestBatchResult is what storing one batch did.
type DestBatchResult struct {
	// Duplicate: the batch id was already stored, nothing was written.
	Duplicate bool
	// Stored is how many rows were written before any failure.
	Stored int
}

// Drop kinds: why rows that were reported are not in the table. Stored per
// hour so a time range can say how much of it is missing.
const (
	DestDropNodeHits   = "node_hits"
	DestDropNodeTrial  = "node_trial"
	DestDropOverBudget = "over_budget"
	DestDropQueueFull  = "queue_full"
	DestDropIngest     = "ingest_error"
)

// DestHitFilter selects hit rows. Zero fields select everything; times are
// UTC milliseconds on the hour grid the rows are stored on.
type DestHitFilter struct {
	UserID  int64
	PanelID int64
	Source  string
	Action  string
	SinceMS int64
	UntilMS int64
	Keyword string
	// SourceKind is "", "policy" (p*), "source" (s*) or "group" (g*).
	SourceKind string
	// IncludeTrial lists the allowlist trial rows (a group's observe), which a
	// listing leaves out unless asked.
	IncludeTrial bool
	Page         int
	PageSize     int
	// GroupBy is "", "site", "user" or "policy".
	GroupBy string
}

// DestHitRow is one row of a hit listing. For a grouped listing the fields the
// grouping folds away are zero.
type DestHitRow struct {
	HourMS  int64
	PanelID int64
	UserID  int64
	Source  string
	Action  string
	Dest    string
	Port    int
	Count   int64
	FirstMS int64
	LastMS  int64
}

// DestHitSummary counts the rows a time/user/panel selection holds, whatever
// the action and source filters.
type DestHitSummary struct {
	// Block is blocked connections of policies and source rules; Deny those of
	// an allowlist group's catch-all; Observe the report-only ones ("would have
	// been blocked"), allowlist trial rows excluded.
	Block   int64
	Deny    int64
	Observe int64
	Users   int64
}

// DestSourceCount is how many hits one source has, and when it last had one.
type DestSourceCount struct {
	Source string
	Action string
	Count  int64
	LastMS int64
}

// DestUserSourceHits is one user's hits on one source in a window.
type DestUserSourceHits struct {
	Source  string
	Action  string
	Count   int64
	LastMS  int64
	PanelID int64
	Dest    string
}

// AuditWindowHours is how far back a reported hour may be from now, and how far
// ahead: a node's clock is not trusted further than that.
const (
	AuditHourPastWindow   = 48 * time.Hour
	AuditHourFutureWindow = time.Hour
)

// AuditBatchMarkerRetention is how long a batch marker is kept: the accepted
// past window plus a day of margin. A marker that is pruned while a node can
// still re-send rows of an hour the window accepts would let that re-send count
// twice.
const AuditBatchMarkerRetention = AuditHourPastWindow + 24*time.Hour

type destHitKey struct {
	hour, panel, user int64
	source, action    string
	dest              string
	port              int
}

// MergeDestHits folds rows that share an aggregation key into one: counts add,
// the first instant is the earliest and the last the latest. The order of first
// appearance is kept.
//
// It exists for PostgreSQL: a multi-row INSERT ... ON CONFLICT DO UPDATE that
// names one key twice is an error ("cannot affect row a second time") and fails
// the whole statement. Rows of one batch DO collide after mapping, because p12x1
// and p12x2 both become p12, so the write path merges before it builds a
// statement (the store does it again, where the SQL is, as the last line).
func MergeDestHits(rows []DestHit) []DestHit {
	index := make(map[destHitKey]int, len(rows))
	out := make([]DestHit, 0, len(rows))
	for _, r := range rows {
		k := destHitKey{r.HourMS, r.PanelID, r.UserID, r.Source, r.Action, r.Dest, r.Port}
		i, seen := index[k]
		if !seen {
			index[k] = len(out)
			out = append(out, r)
			continue
		}
		m := &out[i]
		m.Count += r.Count
		m.FirstMS = min(m.FirstMS, r.FirstMS)
		m.LastMS = max(m.LastMS, r.LastMS)
	}
	return out
}
