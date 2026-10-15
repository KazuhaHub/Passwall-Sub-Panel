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
	return r.load(ctx, panelID, nil)
}

func (r *nativeDesiredRepo) LoadClient(ctx context.Context, panelID int64, email string) (*ports.NativeDesiredSnapshot, error) {
	if email == "" {
		return nil, errors.New("load native desired client snapshot: email required")
	}
	return r.load(ctx, panelID, &email)
}

func (r *nativeDesiredRepo) load(ctx context.Context, panelID int64, email *string) (*ports.NativeDesiredSnapshot, error) {
	if panelID == 0 {
		return nil, errors.New("load native desired snapshot: panel ID required")
	}
	result := &ports.NativeDesiredSnapshot{}
	db := r.db.WithContext(ctx)
	if email != nil {
		// A lookup must not put its email in slow/error/debug SQL logs.
		db = db.Session(&gorm.Session{Logger: redactParams{db.Logger}})
	}
	err := db.Transaction(func(tx *gorm.DB) error {
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

		query := tx.Where("panel_id = ?", panelID)
		if email != nil {
			query = query.Where("email = ?", *email)
		}
		clients, err := loadNativeDesiredClients(query, email)
		if err != nil {
			return err
		}
		result.Clients = clients
		if len(clients) == 0 {
			return nil
		}
		ids := make([]int64, len(clients))
		for i := range clients {
			ids[i] = clients[i].Client.ID
		}
		var attachmentRows []pspClientInboundRow
		if err := tx.Where("client_id IN ?", ids).Order("client_id, node_id").Find(&attachmentRows).Error; err != nil {
			return err
		}
		byClient := make(map[int64][]domain.PSPClientInbound, len(clients))
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
var _ ports.NativeDesiredClientSnapshotRepo = (*nativeDesiredRepo)(nil)
