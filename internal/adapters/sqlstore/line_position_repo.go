package sqlstore

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// subscriptionLinePositionRow is the explicit place of one RELAY line on the
// subscription sort scale (the integer space nodes.sort_order and
// nodes_separator.sort_order share). A line without a row has never been
// positioned and stays right after its landing node; that is what makes an
// empty table render exactly what the panel rendered before this table existed.
//
// Why a side table and not a field of the relays JSON: UpdateMetadata rewrites
// the whole relays column from the edit request, so an editor that does not
// know the field would wipe it, and a reorder would have to read-modify-write
// every landing's JSON. A table is column-scoped by construction.
//
// Only relay lines get rows. The direct line is the node row itself and its
// position is nodes.sort_order, so there is no second copy to drift. A
// "direct" row is refused at the writer.
//
// Cross-dialect: no text column, no JSON, no default. The composite key needs
// autoIncrement:false on the integer part (same as dest_hits), or MySQL and
// Postgres would make node_id the serial column. line_id is size:128 like
// psp_clients.line_id and ValidateRelayIDs's bound.
type subscriptionLinePositionRow struct {
	NodeID    int64  `gorm:"column:node_id;primaryKey;autoIncrement:false"`
	LineID    string `gorm:"column:line_id;primaryKey;size:128"`
	SortOrder int    `gorm:"column:sort_order;not null"`
	UpdatedAt time.Time
}

func (subscriptionLinePositionRow) TableName() string { return "subscription_line_positions" }

// linePositionChunk bounds the IN list so a large fleet stays under the
// placeholder limit of every dialect.
const linePositionChunk = 500

// attachLinePositions fills RelayLine.Position on loaded nodes with one query
// per chunk of nodes that carry relays. Rows whose line no longer exists are
// ignored (they are inert and the next reorder rewrites the table), and the
// pointer carries through group.VisibleLines' by-value copies of the relays.
func attachLinePositions(ctx context.Context, db *gorm.DB, nodes []*domain.Node) error {
	byID := make(map[int64]*domain.Node)
	var ids []int64
	for _, n := range nodes {
		if n == nil || len(n.Relays) == 0 {
			continue
		}
		byID[n.ID] = n
		ids = append(ids, n.ID)
	}
	for start := 0; start < len(ids); start += linePositionChunk {
		end := min(start+linePositionChunk, len(ids))
		var rows []subscriptionLinePositionRow
		if err := db.WithContext(ctx).Where("node_id IN ?", ids[start:end]).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			n := byID[row.NodeID]
			for i := range n.Relays {
				if n.Relays[i].ID == row.LineID {
					pos := row.SortOrder
					n.Relays[i].Position = &pos
					break
				}
			}
		}
	}
	return nil
}
