package sqlstore

import (
	"context"
	"errors"
	"fmt"
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
			if err := tx.Model(&nodeAgentStreamRow{}).Where("id = ?", current.ID).Updates(map[string]any{
				"applied_epoch": receipt.Epoch, "applied_version": receipt.Version, "applied_etag": receipt.ETag,
				"pending_since": pendingSince, "last_seen": seenAt,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return documents, nil
}

var _ ports.NodeAgentAppliedBatchRepo = (*nodeAgentRepo)(nil)
