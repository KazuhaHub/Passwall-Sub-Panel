package sqlstore

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type DestAuditRepo struct {
	db    *gorm.DB
	gates *keyedmutex.Map[int64]
	now   func() time.Time
}

const auditChunkRows = 1000
const auditStatementRows = 200

var errAuditStorage = errors.New("destination audit storage write failed")

func NewDestAuditRepo(db *gorm.DB) *DestAuditRepo {
	return &DestAuditRepo{db: db, gates: &keyedmutex.Map[int64]{}}
}

func (r *DestAuditRepo) ResolveDestinationAuditUsers(ctx context.Context, ids []int64) (map[int64]bool, error) {
	if len(ids) > protocol.MaxAuditUsage {
		return nil, domain.ErrValidation
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, domain.ErrValidation
		}
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	known := make(map[int64]bool, len(ids))
	for start := 0; start < len(ids); start += auditStatementRows {
		var rows []struct{ ID int64 }
		if err := r.privateDB(ctx).Model(&userRow{}).Select("id").Where("id IN ?", ids[start:min(start+auditStatementRows, len(ids))]).Find(&rows).Error; err != nil {
			return nil, auditStorageError(err)
		}
		for _, row := range rows {
			known[row.ID] = true
		}
	}
	return known, nil
}

// BeginDestinationAudit commits the marker, whole-batch budget reservation,
// node/mapping losses and first chunk together. There is no durable telemetry ACK.
func (r *DestAuditRepo) BeginDestinationAudit(ctx context.Context, b domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	if err := validateAuditBatch(b); err != nil {
		return domain.DestAuditBegin{}, err
	}
	unlock := r.gates.Lock(b.PanelID)
	defer unlock()
	var out domain.DestAuditBegin
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		reason, err := auditPermission(tx, b.AgentID, b.PanelID, b.CollectRevision, b.Kind)
		if err != nil {
			return err
		}
		if reason != "" {
			out.Rejected = reason
			return nil
		}
		// The durable agent owner is locked by auditPermission. This precheck
		// handles CLIENT_FOUND_ROWS, which makes a MySQL no-op report one row.
		var existing []destAuditBatchRow
		if err := tx.Where("agent_id = ? AND batch_id = ?", b.AgentID, b.BatchID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) > 0 {
			out.Duplicate = true
			return nil
		}
		marker := destAuditBatchRow{AgentID: b.AgentID, BatchID: b.BatchID, Kind: b.Kind, HourMS: b.HourMS, ReceivedAt: b.ReceivedAt.UTC()}
		insert := tx.Clauses(auditKeepConflict(tx.Dialector.Name(), "agent_id", "batch_id")).Create(&marker)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			out.Duplicate = true
			return nil
		}
		budget := destAuditIngestBudgetRow{AgentID: b.AgentID, ReceivedHourMS: b.ReceivedHourMS, Kind: b.Kind}
		if err := tx.Clauses(auditKeepConflict(tx.Dialector.Name(), "agent_id", "received_hour_ms", "kind")).Create(&budget).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND received_hour_ms = ? AND kind = ?", b.AgentID, b.ReceivedHourMS, b.Kind).First(&budget).Error; err != nil {
			return err
		}
		limit := int64(20000)
		if b.Kind == "usage" {
			limit = 40000
		}
		if budget.RowsReserved < 0 || budget.RowsReserved > limit {
			return domain.ErrUnavailable
		}
		rows := len(b.Hits) + len(b.Usage)
		out.Reserved = min(rows, int(limit-budget.RowsReserved))
		if out.Reserved > 0 {
			update := tx.Model(&destAuditIngestBudgetRow{}).Where("agent_id = ? AND received_hour_ms = ? AND kind = ? AND rows_reserved = ? AND rows_reserved <= ?", b.AgentID, b.ReceivedHourMS, b.Kind, budget.RowsReserved, limit-int64(out.Reserved)).Update("rows_reserved", gorm.Expr("rows_reserved + ?", out.Reserved))
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return domain.ErrConflict
			}
		}
		losses := make([]destAuditLossHourlyRow, 0, 5)
		if b.Dropped > 0 {
			losses = append(losses, destAuditLossHourlyRow{ObservedHourMS: b.HourMS, PanelID: b.PanelID, Kind: b.Kind, Reason: "node_dropped", Events: auditUnsigned(b.Dropped)})
		}
		if b.Unmatched > 0 {
			losses = append(losses, destAuditLossHourlyRow{ObservedHourMS: b.HourMS, PanelID: b.PanelID, Kind: b.Kind, Reason: "node_unmatched", Unmatched: auditUnsigned(b.Unmatched)})
		}
		for _, reason := range []string{"unknown_subject", "out_of_range"} {
			if n := b.Losses[reason]; n > 0 {
				losses = append(losses, destAuditLossHourlyRow{ObservedHourMS: b.ReceivedHourMS, PanelID: b.PanelID, Kind: b.Kind, Reason: reason, Rows: n})
			}
		}
		if rows > out.Reserved {
			losses = append(losses, destAuditLossHourlyRow{ObservedHourMS: b.ReceivedHourMS, PanelID: b.PanelID, Kind: b.Kind, Reason: "over_budget", Rows: int64(rows - out.Reserved)})
		}
		if len(losses) > 0 {
			if err := tx.Clauses(auditLossConflict(tx.Dialector.Name())).Create(&losses).Error; err != nil {
				return err
			}
		}
		out.Stored = min(out.Reserved, auditChunkRows)
		hits, usage := b.Hits, b.Usage
		if b.Kind == "usage" {
			usage = usage[:out.Stored]
		} else {
			hits = hits[:out.Stored]
		}
		return writeAuditRows(tx, hits, usage)
	})
	if err != nil {
		return domain.DestAuditBegin{}, auditStorageError(err)
	}
	return out, nil
}

func (r *DestAuditRepo) WriteDestinationAuditChunk(ctx context.Context, b domain.DestAuditChunk) (string, error) {
	if b.PanelID <= 0 || b.AgentID == "" || b.BatchID == "" || b.CollectRevision == 0 || len(b.Hits)+len(b.Usage) > auditChunkRows {
		return "", domain.ErrValidation
	}
	if err := validateAuditRows(b.PanelID, b.Kind, b.Hits, b.Usage); err != nil {
		return "", err
	}
	unlock := r.gates.Lock(b.PanelID)
	defer unlock()
	reason := ""
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		reason, err = auditPermission(tx, b.AgentID, b.PanelID, b.CollectRevision, b.Kind)
		if err != nil || reason != "" {
			return err
		}
		var marker destAuditBatchRow
		if err := tx.Where("agent_id = ? AND batch_id = ? AND kind = ?", b.AgentID, b.BatchID, b.Kind).First(&marker).Error; err != nil {
			return err
		}
		for _, row := range b.Hits {
			if row.HourMS != marker.HourMS {
				return domain.ErrValidation
			}
		}
		for _, row := range b.Usage {
			if row.HourMS != marker.HourMS {
				return domain.ErrValidation
			}
		}
		return writeAuditRows(tx, b.Hits, b.Usage)
	})
	return reason, auditStorageError(err)
}

// Audit parameters and database errors can contain private destination values.
// Disable SQL tracing for this boundary even when the main database is in Debug
// mode, and return a fixed error rather than a dialect's value-bearing detail.
func (r *DestAuditRepo) privateDB(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}
func auditStorageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrUnavailable) {
		return err
	}
	if errors.Is(err, domain.ErrValidation) {
		return domain.ErrValidation
	}
	if errors.Is(err, domain.ErrDestAuditLossExpired) {
		return domain.ErrDestAuditLossExpired
	}
	return errAuditStorage
}
func auditUnsigned(n uint64) int64 {
	if n > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}

func auditPermission(tx *gorm.DB, agentID string, panelID int64, revision uint64, kind string) (string, error) {
	// Serialize global agent/batch identity on its durable owner, including
	// concurrent processes. The per-panel process gate separately orders writes
	// against native metadata updates.
	var agent nodeAgentRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("agent_id", "panel_id", "observed_core_engine", "observed_capabilities").Where("agent_id = ?", agentID).First(&agent).Error; err != nil {
		return "", err
	}
	if agent.PanelID != panelID {
		return "", domain.ErrConflict
	}
	var panel xuiPanelRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "kind", "audit_collect", "audit_collect_revision").First(&panel, panelID).Error; err != nil {
		return "", err
	}
	collect := domain.AuditCollect(panel.AuditCollect)
	if domain.NormalizePanelKind(domain.PanelKind(panel.Kind)) != domain.PanelKindPSP || !collect.Valid() || panel.AuditCollectRevision < 1 {
		return "", domain.ErrUnavailable
	}
	if uint64(panel.AuditCollectRevision) != revision {
		return "stale_collect_revision", nil
	}
	if collect == domain.AuditCollectOff {
		return "collect_off", nil
	}
	if kind == "usage" && collect != domain.AuditCollectHitsAndUsage {
		return "collect_off", nil
	}
	if agent.ObservedCoreEngine != string(domain.NodeCoreXray) || !slices.Contains(agent.ObservedCapabilities, protocol.CapabilityAuditHits) || (kind == "usage" && !slices.Contains(agent.ObservedCapabilities, protocol.CapabilityAuditUsage)) {
		return "no_capability", nil
	}
	return "", nil
}

func validateAuditBatch(b domain.DestAuditBatch) error {
	if b.AgentID == "" || len(b.BatchID) != 32 || strings.IndexFunc(b.BatchID, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) >= 0 || b.PanelID <= 0 || b.HourMS <= 0 || b.HourMS%protocol.AuditHourMS != 0 || b.ReceivedHourMS <= 0 || b.ReceivedHourMS%protocol.AuditHourMS != 0 || b.ReceivedAt.IsZero() || b.ReceivedAt.UTC().Truncate(time.Hour).UnixMilli() != b.ReceivedHourMS || b.CollectRevision == 0 {
		return domain.ErrValidation
	}
	if len(b.Hits) > protocol.MaxAuditHits || len(b.Usage) > protocol.MaxAuditUsage {
		return domain.ErrValidation
	}
	for reason, n := range b.Losses {
		if (reason != "unknown_subject" && reason != "out_of_range") || n < 0 {
			return domain.ErrValidation
		}
	}
	for _, row := range b.Hits {
		if row.HourMS != b.HourMS {
			return domain.ErrValidation
		}
	}
	for _, row := range b.Usage {
		if row.HourMS != b.HourMS {
			return domain.ErrValidation
		}
	}
	return validateAuditRows(b.PanelID, b.Kind, b.Hits, b.Usage)
}

func validateAuditRows(panelID int64, kind string, hits []domain.DestHit, usage []domain.DestUsage) error {
	switch kind {
	case "block", "observe", "trial":
		if len(usage) > 0 {
			return domain.ErrValidation
		}
	case "usage":
		if len(hits) > 0 {
			return domain.ErrValidation
		}
	default:
		return domain.ErrValidation
	}
	type hitKey struct {
		hour, user           int64
		source, action, dest string
		port                 int
	}
	seen := make(map[hitKey]bool, len(hits))
	for _, row := range hits {
		if row.PanelID != panelID || row.HourMS <= 0 || row.HourMS%protocol.AuditHourMS != 0 || row.Count <= 0 || row.Source == "" || len(row.Source) > 24 || row.Dest == "" || len(row.Dest) > 253 || row.Port < 0 || row.Port > 65535 || row.FirstAt.UnixMilli() < row.HourMS || row.LastAt.Before(row.FirstAt) || row.LastAt.UnixMilli() >= row.HourMS+protocol.AuditHourMS {
			return domain.ErrValidation
		}
		if row.Source[0] != 'p' && row.Source[0] != 'g' {
			return domain.ErrValidation
		}
		id, err := strconv.ParseInt(row.Source[1:], 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != row.Source[1:] {
			return domain.ErrValidation
		}
		if kind == "trial" {
			if row.UserID != 0 || row.Port != 0 || row.Action != "observe" || row.Source[0] != 'g' {
				return domain.ErrValidation
			}
		} else {
			if row.UserID <= 0 || (kind == "block" && row.Action != "block") || (kind == "observe" && (row.Action != "observe" || row.Source[0] != 'p')) {
				return domain.ErrValidation
			}
		}
		key := hitKey{row.HourMS, row.UserID, row.Source, row.Action, row.Dest, row.Port}
		if seen[key] {
			return domain.ErrValidation
		}
		seen[key] = true
	}
	type usageKey struct {
		hour, user int64
		site       string
	}
	seenUsage := make(map[usageKey]bool, len(usage))
	for _, row := range usage {
		if row.PanelID != panelID || row.HourMS <= 0 || row.HourMS%protocol.AuditHourMS != 0 || row.UserID <= 0 || row.Count <= 0 || row.Site == "" || len(row.Site) > 253 {
			return domain.ErrValidation
		}
		key := usageKey{row.HourMS, row.UserID, row.Site}
		if seenUsage[key] {
			return domain.ErrValidation
		}
		seenUsage[key] = true
	}
	return nil
}

func auditKeepConflict(dialect string, columns ...string) clause.OnConflict {
	oc := clause.OnConflict{}
	for _, column := range columns {
		oc.Columns = append(oc.Columns, clause.Column{Name: column})
	}
	if dialect == "mysql" {
		oc.DoUpdates = clause.Assignments(map[string]any{columns[0]: clause.Column{Name: columns[0]}})
	} else {
		oc.DoNothing = true
	}
	return oc
}

func auditIncoming(dialect, column string) string {
	if dialect == "mysql" {
		return "VALUES(`" + column + "`)"
	}
	return "excluded." + column
}
func auditAdd(dialect, table, column string) clause.Expr {
	current := table + "." + column
	incoming := auditIncoming(dialect, column)
	return gorm.Expr("CASE WHEN " + current + " > 9223372036854775807 - " + incoming + " THEN 9223372036854775807 ELSE " + current + " + " + incoming + " END")
}
func auditHitConflict(dialect string) clause.OnConflict {
	oc := clause.OnConflict{}
	for _, key := range []string{"hour_ms", "panel_id", "user_id", "source", "action", "dest", "port"} {
		oc.Columns = append(oc.Columns, clause.Column{Name: key})
	}
	least, greatest := "LEAST", "GREATEST"
	if dialect == "sqlite" {
		least, greatest = "MIN", "MAX"
	}
	oc.DoUpdates = clause.Assignments(map[string]any{"count": auditAdd(dialect, "dest_hits", "count"), "first_at": gorm.Expr(least + "(dest_hits.first_at, " + auditIncoming(dialect, "first_at") + ")"), "last_at": gorm.Expr(greatest + "(dest_hits.last_at, " + auditIncoming(dialect, "last_at") + ")")})
	return oc
}
func auditUsageConflict(dialect string) clause.OnConflict {
	oc := clause.OnConflict{}
	for _, key := range []string{"hour_ms", "panel_id", "user_id", "site"} {
		oc.Columns = append(oc.Columns, clause.Column{Name: key})
	}
	oc.DoUpdates = clause.Assignments(map[string]any{"count": auditAdd(dialect, "dest_usage_hourly", "count")})
	return oc
}
func auditLossConflict(dialect string) clause.OnConflict {
	oc := clause.OnConflict{}
	for _, key := range []string{"observed_hour_ms", "panel_id", "kind", "reason"} {
		oc.Columns = append(oc.Columns, clause.Column{Name: key})
	}
	oc.DoUpdates = clause.Assignments(map[string]any{"rows": auditAdd(dialect, "dest_audit_loss_hourly", "rows"), "events": auditAdd(dialect, "dest_audit_loss_hourly", "events"), "unmatched": auditAdd(dialect, "dest_audit_loss_hourly", "unmatched")})
	return oc
}

func writeAuditRows(tx *gorm.DB, hits []domain.DestHit, usage []domain.DestUsage) error {
	// The chunk already has an explicit outer transaction. Do not open another
	// transaction/savepoint around statement batches; every statement still
	// commits or rolls back with this chunk, including wrapped test dialects.
	tx = tx.Session(&gorm.Session{SkipDefaultTransaction: true})
	if len(hits) > 0 {
		rows := make([]destHitRow, len(hits))
		for i, row := range hits {
			rows[i] = destHitRow{HourMS: row.HourMS, PanelID: row.PanelID, UserID: row.UserID, Source: row.Source, Action: row.Action, Dest: row.Dest, Port: row.Port, Count: row.Count, FirstAt: row.FirstAt.UTC(), LastAt: row.LastAt.UTC()}
		}
		if err := tx.Clauses(auditHitConflict(tx.Dialector.Name())).CreateInBatches(&rows, auditStatementRows).Error; err != nil {
			return err
		}
	}
	if len(usage) > 0 {
		rows := make([]destUsageHourlyRow, len(usage))
		for i, row := range usage {
			rows[i] = destUsageHourlyRow{HourMS: row.HourMS, PanelID: row.PanelID, UserID: row.UserID, Site: row.Site, Count: row.Count}
		}
		if err := tx.Clauses(auditUsageConflict(tx.Dialector.Name())).CreateInBatches(&rows, auditStatementRows).Error; err != nil {
			return err
		}
	}
	return nil
}

var _ ports.DestAuditRepo = (*DestAuditRepo)(nil)
