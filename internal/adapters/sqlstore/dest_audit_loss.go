package sqlstore

import (
	"context"
	"strings"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

// Receiver batches live in process memory and are never handed to another
// process. Share retry gates across repository instances/connections too.
var receiverLossGates keyedmutex.Map[string]

func (r *DestAuditRepo) FlushDestinationAuditLoss(ctx context.Context, b domain.DestAuditLossBatch) error {
	if !validReceiverLossBatch(b) {
		return domain.ErrValidation
	}
	unlock := receiverLossGates.Lock(b.BatchID)
	defer unlock()
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []destAuditBatchRow
		if err := tx.Where("agent_id = ? AND batch_id = ?", "", b.BatchID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) != 0 {
			if existing[0].Kind != "receiver_loss" {
				return domain.ErrUnavailable
			}
			return nil
		}
		now := r.lossNow()
		// Retry validity is shorter than marker retention. A frozen increment
		// cannot be replayed after a cleanup removed its durable identity.
		if b.ReceivedAt.Before(now.Add(-48*time.Hour)) || b.ReceivedAt.After(now.Add(time.Hour)) {
			return domain.ErrDestAuditLossExpired
		}
		// Empty agent IDs are forbidden by both node creation and ingestion;
		// this internal namespace cannot collide with a node-owned marker.
		marker := destAuditBatchRow{BatchID: b.BatchID, Kind: "receiver_loss", HourMS: b.ReceivedAt.UTC().Truncate(time.Hour).UnixMilli(), ReceivedAt: b.ReceivedAt.UTC()}
		insert := tx.Clauses(auditKeepConflict(tx.Dialector.Name(), "agent_id", "batch_id")).Create(&marker)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			return nil
		}
		rows := make([]destAuditLossHourlyRow, len(b.Losses))
		for i, loss := range b.Losses {
			rows[i] = destAuditLossHourlyRow{ObservedHourMS: loss.HourMS, PanelID: loss.PanelID, Kind: loss.Kind, Reason: loss.Reason, Rows: loss.Rows}
		}
		return tx.Clauses(auditLossConflict(tx.Dialector.Name())).Create(&rows).Error
	})
	return auditStorageError(err)
}

func validReceiverLossBatch(b domain.DestAuditLossBatch) bool {
	if len(b.BatchID) != 32 || strings.IndexFunc(b.BatchID, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) >= 0 || b.ReceivedAt.IsZero() || len(b.Losses) == 0 || len(b.Losses) > auditStatementRows {
		return false
	}
	keys := make(map[domain.DestAuditLoss]bool, len(b.Losses))
	for _, loss := range b.Losses {
		if loss.HourMS <= 0 || loss.HourMS%protocol.AuditHourMS != 0 || loss.PanelID <= 0 || loss.Rows <= 0 {
			return false
		}
		switch loss.Kind {
		case "block", "observe", "trial", "usage":
		default:
			return false
		}
		switch loss.Reason {
		case "queue_full", "collect_off", "stale_collect_revision", "ingest_error", "unknown_subject", "out_of_range", "over_budget":
		default:
			return false
		}
		loss.Rows = 0
		if keys[loss] {
			return false
		}
		keys[loss] = true
	}
	return true
}

func (r *DestAuditRepo) lossNow() time.Time {
	if r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

var _ ports.DestAuditLossRepo = (*DestAuditRepo)(nil)
