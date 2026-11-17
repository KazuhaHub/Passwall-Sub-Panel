package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// transitForwardRow is the sync service's state for one managed relay line:
// the id its forward key is minted from, what the entry server last
// confirmed, and the relayed traffic. The line itself (address, port, entry
// server) stays in nodes.relays, which administrators own; keeping this state
// in its own row per line means the sync service never rewrites that column
// and two entry servers confirming lines of the same landing cannot lose each
// other's writes.
type transitForwardRow struct {
	ID      int64  `gorm:"primaryKey;autoIncrement"`
	NodeID  int64  `gorm:"not null;uniqueIndex:uk_transit_forward,priority:1"`
	RelayID string `gorm:"size:128;not null;uniqueIndex:uk_transit_forward,priority:2"`

	AppliedEntryPanelID int64  `gorm:"not null;default:0"`
	AppliedPort         int    `gorm:"not null;default:0"`
	AppliedTarget       string `gorm:"size:253;not null;default:''"`
	AppliedTargetPort   int    `gorm:"not null;default:0"`
	AppliedAt           *time.Time

	UpBytes          int64  `gorm:"not null;default:0"`
	DownBytes        int64  `gorm:"not null;default:0"`
	LastRawUp        int64  `gorm:"not null;default:0"`
	LastRawDown      int64  `gorm:"not null;default:0"`
	LastCounterEpoch uint64 `gorm:"not null;default:0"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (transitForwardRow) TableName() string { return "transit_forwards" }

func (r transitForwardRow) toDomain() domain.TransitForward {
	return domain.TransitForward{
		ID: r.ID, NodeID: r.NodeID, RelayID: r.RelayID,
		Applied: domain.ManagedForward{
			EntryPanelID: r.AppliedEntryPanelID, Port: r.AppliedPort,
			Target: r.AppliedTarget, TargetPort: r.AppliedTargetPort,
		},
		AppliedAt: r.AppliedAt,
		UpBytes:   r.UpBytes, DownBytes: r.DownBytes,
		LastRawUp: r.LastRawUp, LastRawDown: r.LastRawDown, LastCounterEpoch: r.LastCounterEpoch,
	}
}

type transitRepo struct{ db *gorm.DB }

// NewTransitRepo returns the managed relay state store.
func NewTransitRepo(db *gorm.DB) *transitRepo { return &transitRepo{db: db} }

// Ensure returns the row for a managed line, creating it on first sight. It is
// safe under concurrent callers: the unique (node_id, relay_id) index decides,
// and the loser reads the winner's row.
func (r *transitRepo) Ensure(ctx context.Context, nodeID int64, relayID string) (domain.TransitForward, error) {
	if nodeID <= 0 || relayID == "" {
		return domain.TransitForward{}, fmt.Errorf("transit forward needs a node and a relay id")
	}
	db := r.db.WithContext(ctx)
	row := transitForwardRow{NodeID: nodeID, RelayID: relayID}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return domain.TransitForward{}, err
	}
	var stored transitForwardRow
	if err := db.Where("node_id = ? AND relay_id = ?", nodeID, relayID).First(&stored).Error; err != nil {
		return domain.TransitForward{}, err
	}
	return stored.toDomain(), nil
}

// ListByNodes returns the rows of the given landing nodes.
func (r *transitRepo) ListByNodes(ctx context.Context, nodeIDs []int64) ([]domain.TransitForward, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var rows []transitForwardRow
	if err := r.db.WithContext(ctx).Where("node_id IN ?", nodeIDs).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.TransitForward, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// ListByIDs returns the rows with the given ids (unknown ids are absent).
func (r *transitRepo) ListByIDs(ctx context.Context, ids []int64) ([]domain.TransitForward, error) {
	out := make([]domain.TransitForward, 0, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		var rows []transitForwardRow
		if err := r.db.WithContext(ctx).Where("id IN ?", ids[start:min(start+chunk, len(ids))]).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.toDomain())
		}
	}
	return out, nil
}

// SetApplied records what the entry confirmed. It reports whether the stored
// confirmation changed, so the caller refreshes rendering only on a
// transition and not on every replayed receipt.
func (r *transitRepo) SetApplied(ctx context.Context, id int64, applied domain.ManagedForward, at time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Model(&transitForwardRow{}).
		Where("id = ? AND NOT (applied_at IS NOT NULL AND applied_entry_panel_id = ? AND applied_port = ? AND applied_target = ? AND applied_target_port = ?)",
			id, applied.EntryPanelID, applied.Port, applied.Target, applied.TargetPort).
		Updates(map[string]any{
			"applied_entry_panel_id": applied.EntryPanelID, "applied_port": applied.Port,
			"applied_target": applied.Target, "applied_target_port": applied.TargetPort,
			"applied_at": at,
		})
	return result.RowsAffected > 0, result.Error
}

// ClearApplied withdraws a confirmation (the entry rejected or dropped the
// forward). It reports whether there was one to withdraw.
func (r *transitRepo) ClearApplied(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&transitForwardRow{}).
		Where("id = ? AND applied_at IS NOT NULL", id).
		Updates(map[string]any{
			"applied_entry_panel_id": 0, "applied_port": 0, "applied_target": "", "applied_target_port": 0,
			"applied_at": nil,
		})
	return result.RowsAffected > 0, result.Error
}

// RecordCounters folds one cumulative sample into the lifetime totals. A new
// counter epoch is a reset (the entry's core restarted): the whole sample is
// new traffic. Within an epoch a sample below the last one is also read as a
// reset rather than as negative traffic.
func (r *transitRepo) RecordCounters(ctx context.Context, id int64, up, down int64, epoch uint64) error {
	if up < 0 || down < 0 {
		return errors.New("transit counters cannot be negative")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row transitForwardRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		deltaUp, deltaDown := up, down
		if epoch == row.LastCounterEpoch {
			if up >= row.LastRawUp {
				deltaUp = up - row.LastRawUp
			}
			if down >= row.LastRawDown {
				deltaDown = down - row.LastRawDown
			}
		}
		return tx.Model(&transitForwardRow{}).Where("id = ?", id).Updates(map[string]any{
			"up_bytes": row.UpBytes + deltaUp, "down_bytes": row.DownBytes + deltaDown,
			"last_raw_up": up, "last_raw_down": down, "last_counter_epoch": epoch,
		}).Error
	})
}

// attachTransitState fills RelayLine.ManagedReady on loaded nodes: a managed
// line is ready when its entry confirmed exactly the forward the line now asks
// for. Nodes without managed lines cost nothing.
func attachTransitState(ctx context.Context, db *gorm.DB, nodes []*domain.Node) error {
	if err := attachEgressState(ctx, db, nodes); err != nil {
		return err
	}
	var ids []int64
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, relay := range node.Relays {
			if relay.Managed() {
				ids = append(ids, node.ID)
				break
			}
		}
	}
	forwardIDs := map[probeLineRef]int64{}
	if len(ids) > 0 {
		forwards, err := (&transitRepo{db: db}).ListByNodes(ctx, ids)
		if err != nil {
			return fmt.Errorf("load managed relay state: %w", err)
		}
		byLine := make(map[probeLineRef]domain.TransitForward, len(forwards))
		for _, forward := range forwards {
			byLine[probeLineRef{forward.NodeID, forward.RelayID}] = forward
			forwardIDs[probeLineRef{forward.NodeID, forward.RelayID}] = forward.ID
		}
		for _, node := range nodes {
			if node == nil {
				continue
			}
			for i := range node.Relays {
				relay := &node.Relays[i]
				relay.ManagedReady = false
				if !relay.Managed() {
					continue
				}
				state, exists := byLine[probeLineRef{node.ID, relay.ID}]
				desired, publishable := domain.DesiredManagedForward(node, *relay)
				relay.ManagedReady = exists && publishable && state.AppliedAt != nil && state.Applied == desired
			}
		}
	}
	return attachProbeState(ctx, db, nodes, forwardIDs)
}
