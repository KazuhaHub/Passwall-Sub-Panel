package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nodeTrafficRepo struct{ db *gorm.DB }

// InsertBatch packs all supplied node snapshots into one SQL roundtrip
// per batchSize chunk. Mirrors trafficRepo.InsertBatch — used by
// PollOnce's end-of-cycle flush.
func (r *nodeTrafficRepo) InsertBatch(ctx context.Context, snaps []*domain.NodeTrafficSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	rows := make([]nodeTrafficRow, len(snaps))
	for i, s := range snaps {
		rows[i] = nodeTrafficRow{
			NodeID:     s.NodeID,
			UpBytes:    s.UpBytes,
			DownBytes:  s.DownBytes,
			TotalBytes: s.TotalBytes,
			CapturedAt: s.CapturedAt,
		}
	}
	return r.db.WithContext(ctx).CreateInBatches(&rows, batchSize).Error
}

func (r *nodeTrafficRepo) Insert(ctx context.Context, s *domain.NodeTrafficSnapshot) error {
	row := nodeTrafficRow{
		NodeID:     s.NodeID,
		UpBytes:    s.UpBytes,
		DownBytes:  s.DownBytes,
		TotalBytes: s.TotalBytes,
		CapturedAt: s.CapturedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	s.ID = row.ID
	return nil
}

func (r *nodeTrafficRepo) LatestForNode(ctx context.Context, nodeID int64) (*domain.NodeTrafficSnapshot, error) {
	var row nodeTrafficRow
	tx := r.db.WithContext(ctx).
		Where("node_id = ?", nodeID).
		Order("id DESC").
		Limit(1).
		Find(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

func (r *nodeTrafficRepo) LastBefore(ctx context.Context, nodeID int64, before time.Time) (*domain.NodeTrafficSnapshot, error) {
	var row nodeTrafficRow
	tx := r.db.WithContext(ctx).
		Where("node_id = ? AND captured_at < ?", nodeID, before).
		Order("captured_at DESC").
		Limit(1).
		Find(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

func (r *nodeTrafficRepo) ListByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]*domain.NodeTrafficSnapshot, error) {
	var rows []nodeTrafficRow
	err := r.db.WithContext(ctx).
		Where("node_id = ? AND captured_at >= ? AND captured_at < ?", nodeID, since, until).
		// id ASC makes "last row in a chart bucket" deterministic on ties (see
		// trafficRepo.ListByUser) so Postgres can't shift bucket deltas.
		Order("captured_at ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.NodeTrafficSnapshot, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

// PruneBefore deletes node_traffic_snapshots rows captured strictly before
// cutoff. Idx_node_time supports the range delete. See trafficRepo.PruneBefore
// for the retention rationale.
func (r *nodeTrafficRepo) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("captured_at < ?", cutoff).
		Delete(&nodeTrafficRow{})
	return res.RowsAffected, res.Error
}

// PruneHourlyBefore deletes node_traffic_snapshots_hourly rows with
// bucket_start strictly before cutoff. Driven by TrafficHistoryDays.
func (r *nodeTrafficRepo) PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("bucket_start < ?", cutoff).
		Delete(&nodeTrafficHourlyRow{})
	return res.RowsAffected, res.Error
}
