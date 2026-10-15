package sqlstore

import (
	"database/sql"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Both collections are ordered by client ID. Read each complete attachment
// directly into its owning current client, without a reflected model slice or
// a second map of attachment slices. The caller owns the snapshot transaction.
func loadNativeDesiredAttachments(tx *gorm.DB, clients []ports.NativeDesiredClient, ids []int64) error {
	rows, err := tx.Model(&pspClientInboundRow{}).
		Select("client_id", "node_id", "flow_override", "state", "applied_version", "applied_email", "applied_uuid", "applied_password", "first_failed_at").
		Where("client_id IN ?", ids).Order("client_id, node_id").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	var row domain.PSPClientInbound
	var state string
	var version sql.Null[uint64]
	var failedAt sql.NullTime
	index := 0
	for rows.Next() {
		if err := rows.Scan(&row.ClientID, &row.NodeID, &row.FlowOverride, &state, &version, &row.AppliedEmail, &row.AppliedUUID, &row.AppliedPassword, &failedAt); err != nil {
			return err
		}
		for index < len(clients) && clients[index].Client.ID < row.ClientID {
			index++
		}
		if index == len(clients) || clients[index].Client.ID != row.ClientID {
			continue
		}
		row.State, row.AppliedVersion = domain.ClientApplyState(state), version.V
		row.FirstFailedAt = nil
		if failedAt.Valid {
			stamp := failedAt.Time
			row.FirstFailedAt = &stamp
		}
		clients[index].Inbounds = append(clients[index].Inbounds, row)
	}
	return rows.Err()
}
