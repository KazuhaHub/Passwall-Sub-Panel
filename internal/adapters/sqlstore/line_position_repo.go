package sqlstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// pruneLinePositionsTx drops the position rows of lines that are no longer in
// relays. Inert orphans would be harmless, but they are never read again and
// would otherwise accumulate.
func pruneLinePositionsTx(tx *gorm.DB, nodeID int64, relays []domain.RelayLine) error {
	q := tx.Where("node_id = ?", nodeID)
	if len(relays) > 0 {
		keep := make([]string, len(relays))
		for i, r := range relays {
			keep[i] = r.ID
		}
		q = q.Where("line_id NOT IN ?", keep)
	}
	return q.Delete(&subscriptionLinePositionRow{}).Error
}

// ApplySubscriptionOrder is the one-transaction subscription reorder (see
// ports.NodeRepo). Order of work inside the transaction: node and separator
// sort_order first, then the old line rows are cleared and the new set is
// inserted, so a failure anywhere (a duplicate key on the last insert, a lost
// connection) rolls the whole thing back and the previous order stays intact.
//
// It never checks that an ID exists: UPDATE ... RowsAffected is not portable
// (MySQL reports 0 for an unchanged value), and the service has already
// validated the set against the current rows. An insert for a node that was
// deleted in the meantime is an inert orphan.
func (r *nodeRepo) ApplySubscriptionOrder(ctx context.Context, w ports.SubscriptionOrderWrite) error {
	for _, line := range w.Lines {
		// The direct line is the node row, whose place is nodes.sort_order. A
		// row for it would be a second copy that could disagree.
		if line.LineID == "" || line.LineID == domain.DirectLineID || len(line.LineID) > 128 {
			return fmt.Errorf("%w: invalid line id %q for a position row", domain.ErrValidation, line.LineID)
		}
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range w.Nodes {
			if err := tx.Model(&nodeRow{}).Where("id = ?", u.NodeID).
				Update("sort_order", u.SortOrder).Error; err != nil {
				return err
			}
		}
		for _, u := range w.Separators {
			if err := tx.Model(&separatorRow{}).Where("id = ?", u.SeparatorID).
				Update("sort_order", u.SortOrder).Error; err != nil {
				return err
			}
		}
		// Every reorder rewrites the whole set, so stale and orphan rows go too.
		if err := tx.Where("1 = 1").Delete(&subscriptionLinePositionRow{}).Error; err != nil {
			return err
		}
		for _, line := range w.Lines {
			row := subscriptionLinePositionRow{NodeID: line.NodeID, LineID: line.LineID, SortOrder: line.SortOrder}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
