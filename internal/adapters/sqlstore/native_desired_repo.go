package sqlstore

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// nativeDesiredRepo reads the native agent's config/roster closure inside one
// database transaction. Services must not assemble this snapshot by calling
// NodeRepo and PSPClientRepo separately: a concurrent membership resync could
// otherwise mint a roster that references a config listener version which was
// never a real database state.
type nativeDesiredRepo struct{ db *gorm.DB }

func (r *nativeDesiredRepo) Load(ctx context.Context, panelID int64) (*ports.NativeDesiredSnapshot, error) {
	if panelID == 0 {
		return nil, errors.New("load native desired snapshot: panel ID required")
	}
	result := &ports.NativeDesiredSnapshot{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var nodeRows []nodeRow
		if err := tx.Where("panel_id = ?", panelID).Order("id").Find(&nodeRows).Error; err != nil {
			return err
		}
		result.Nodes = make([]*domain.Node, len(nodeRows))
		for i := range nodeRows {
			node, err := nodeRows[i].toDomain()
			if err != nil {
				return err
			}
			result.Nodes[i] = node
		}

		var clientRows []pspClientRow
		if err := tx.Where("panel_id = ?", panelID).Order("id").Find(&clientRows).Error; err != nil {
			return err
		}
		result.Clients = make([]ports.NativeDesiredClient, len(clientRows))
		if len(clientRows) == 0 {
			return nil
		}
		ids := make([]int64, len(clientRows))
		for i := range clientRows {
			ids[i] = clientRows[i].ID
			result.Clients[i].Client = rowToPSPClient(&clientRows[i])
		}
		var attachmentRows []pspClientInboundRow
		if err := tx.Where("client_id IN ?", ids).Order("client_id, node_id").Find(&attachmentRows).Error; err != nil {
			return err
		}
		byClient := make(map[int64][]domain.PSPClientInbound, len(clientRows))
		for _, row := range attachmentRows {
			byClient[row.ClientID] = append(byClient[row.ClientID], domain.PSPClientInbound{
				ClientID: row.ClientID, NodeID: row.NodeID,
				FlowOverride: row.FlowOverride, State: domain.ClientApplyState(row.State),
				AppliedVersion: row.AppliedVersion, AppliedEmail: row.AppliedEmail,
				AppliedUUID: row.AppliedUUID, AppliedPassword: row.AppliedPassword,
				FirstFailedAt: row.FirstFailedAt,
			})
		}
		for i := range result.Clients {
			result.Clients[i].Inbounds = byClient[result.Clients[i].Client.ID]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ ports.NativeDesiredSnapshotRepo = (*nativeDesiredRepo)(nil)
