package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (r *nodeAgentRepo) RecordAppliedBatch(ctx context.Context, agentID string, receipts []ports.NodeAgentStreamApplied, seenAt time.Time) ([]*domain.NodeAgentStream, error) {
	if agentID == "" || len(receipts) == 0 || len(receipts) > 3 {
		return nil, errors.New("record applied batch: owner and one to three receipts required")
	}
	names, unique := make([]string, 0, len(receipts)), make(map[domain.NodeAgentStreamName]bool)
	for _, receipt := range receipts {
		if !receipt.Stream.Valid() || unique[receipt.Stream] {
			return nil, errors.New("record applied batch: valid unique streams required")
		}
		unique[receipt.Stream] = true
		names = append(names, string(receipt.Stream))
	}
	seenAt = seenAt.UTC()
	var documents []*domain.NodeAgentStream
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		agent, err := lockNodeAgentByAgentID(tx, agentID)
		if err != nil {
			return err
		}
		var rows []nodeAgentStreamRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND stream IN ?", agentID, names).Order("stream").Find(&rows).Error; err != nil {
			return err
		}
		byName := make(map[domain.NodeAgentStreamName]*nodeAgentStreamRow, len(rows))
		for i := range rows {
			byName[domain.NodeAgentStreamName(rows[i].Stream)] = &rows[i]
		}
		// Validate the complete vector before writing or publishing its documents.
		for _, receipt := range receipts {
			current := byName[receipt.Stream]
			if current == nil {
				return domain.ErrNotFound
			}
			if receipt.Epoch > agent.Epoch {
				return fmt.Errorf("record applied batch: reported epoch %d exceeds desired epoch %d", receipt.Epoch, agent.Epoch)
			}
			if receipt.Epoch == agent.Epoch && receipt.Version > current.DesiredVersion {
				return fmt.Errorf("record applied batch: reported version %d exceeds desired version %d", receipt.Version, current.DesiredVersion)
			}
			if receipt.Epoch == agent.Epoch && receipt.Version == current.DesiredVersion && receipt.ETag != current.DesiredETag {
				return errors.New("record applied batch: reported etag conflicts with desired version")
			}
		}
		if err := tx.Model(&nodeAgentRow{}).Where("id = ?", agent.ID).Update("last_seen", seenAt).Error; err != nil {
			return err
		}
		updates := make([]nodeAgentStreamRow, 0, len(receipts))
		for _, receipt := range receipts {
			current := byName[receipt.Stream]
			// The caller needs the issuer state seen before recording this ACK,
			// just as GetStream followed by RecordApplied returned previously.
			documents = append(documents, streamRowToDomain(current))
			pendingSince := current.PendingSince
			if receipt.Epoch == agent.Epoch && receipt.ETag != "" && receipt.ETag == current.DesiredETag {
				pendingSince = nil
			} else if pendingSince == nil {
				pendingSince = &seenAt
			}
			updates = append(updates, nodeAgentStreamRow{
				ID: current.ID, AppliedEpoch: receipt.Epoch, AppliedVersion: receipt.Version,
				AppliedETag: receipt.ETag, PendingSince: pendingSince,
			})
		}
		return updateAppliedStreamVector(tx, agentID, updates, seenAt)
	})
	if err != nil {
		return nil, err
	}
	return documents, nil
}

// The caller holds the owner and stream locks and has validated the complete
// one-to-three vector. Keep each stream's receipt and pending clock distinct,
// but send only one UPDATE while the control round holds the transaction.
func updateAppliedStreamVector(tx *gorm.DB, agentID string, rows []nodeAgentStreamRow, seenAt time.Time) error {
	columns := []struct {
		name  string
		value func(nodeAgentStreamRow) any
	}{
		{"applied_epoch", func(r nodeAgentStreamRow) any { return r.AppliedEpoch }},
		{"applied_version", func(r nodeAgentStreamRow) any { return r.AppliedVersion }},
		{"applied_etag", func(r nodeAgentStreamRow) any { return r.AppliedETag }},
		{"pending_since", func(r nodeAgentStreamRow) any { return r.PendingSince }},
	}
	updates := map[string]any{"last_seen": seenAt}
	for _, column := range columns {
		var expression strings.Builder
		expression.WriteString("CASE id")
		args := make([]any, 0, 2*len(rows))
		for _, row := range rows {
			expression.WriteString(" WHEN ? THEN ?")
			args = append(args, row.ID, column.value(row))
		}
		// ELSE keeps the destination column's SQL type, including NULL clocks,
		// rather than letting PostgreSQL infer a text-only parameter CASE.
		expression.WriteString(" ELSE " + column.name + " END")
		updates[column.name] = gorm.Expr(expression.String(), args...)
	}
	ids := make([]int64, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	return tx.Model(&nodeAgentStreamRow{}).Where("agent_id = ? AND id IN ?", agentID, ids).Updates(updates).Error
}

var _ ports.NodeAgentAppliedBatchRepo = (*nodeAgentRepo)(nil)
