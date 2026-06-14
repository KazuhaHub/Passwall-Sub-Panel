package sqlstore

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

// LatestForNodes mirrors trafficRepo.LatestForUsers — single SQL query
// returning the latest snapshot row for every node in one shot. Used by
// the admin /traffic/nodes/top dashboard to avoid N+1 LatestForNode
// round-trips.
func (r *nodeTrafficRepo) LatestForNodes(ctx context.Context, nodeIDs []int64) (map[int64]*domain.NodeTrafficSnapshot, error) {
	if len(nodeIDs) == 0 {
		return map[int64]*domain.NodeTrafficSnapshot{}, nil
	}
	var rows []nodeTrafficRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.* FROM node_traffic_snapshots t
		INNER JOIN (
			SELECT node_id, MAX(id) AS mid
			FROM node_traffic_snapshots
			WHERE node_id IN ?
			GROUP BY node_id
		) m ON t.id = m.mid
	`, nodeIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*domain.NodeTrafficSnapshot, len(rows))
	for i := range rows {
		d := rows[i].toDomain()
		out[d.NodeID] = d
	}
	return out, nil
}

// LastBeforeForNodes mirrors trafficRepo.LastBeforeForUsers. Returns
// the most recent snapshot strictly before `before` for every node;
// nodes with no prior snapshot are absent from the result map.
func (r *nodeTrafficRepo) LastBeforeForNodes(ctx context.Context, nodeIDs []int64, before time.Time) (map[int64]*domain.NodeTrafficSnapshot, error) {
	if len(nodeIDs) == 0 {
		return map[int64]*domain.NodeTrafficSnapshot{}, nil
	}
	var rows []nodeTrafficRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.* FROM node_traffic_snapshots t
		INNER JOIN (
			SELECT node_id, MAX(id) AS mid
			FROM node_traffic_snapshots
			WHERE node_id IN ? AND captured_at < ?
			GROUP BY node_id
		) m ON t.id = m.mid
	`, nodeIDs, before).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*domain.NodeTrafficSnapshot, len(rows))
	for i := range rows {
		d := rows[i].toDomain()
		out[d.NodeID] = d
	}
	return out, nil
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

// ListHourlyByNode returns the node's rolled-up hourly delta buckets whose
// bucket_start is in [since, until). See trafficRepo.ListHourlyByUser for why
// bounds are forced to UTC. Long-window source for NodeHistoryFor.
func (r *nodeTrafficRepo) ListHourlyByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]domain.HourlyTraffic, error) {
	var rows []nodeTrafficHourlyRow
	err := r.db.WithContext(ctx).
		Where("node_id = ? AND bucket_start >= ? AND bucket_start < ?", nodeID, since.UTC(), until.UTC()).
		Order("bucket_start ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.HourlyTraffic, len(rows))
	for i := range rows {
		out[i] = domain.HourlyTraffic{
			BucketStart: rows[i].BucketStart,
			UpBytes:     rows[i].UpBytes,
			DownBytes:   rows[i].DownBytes,
			TotalBytes:  rows[i].TotalBytes,
		}
	}
	return out, nil
}

// SumHourlyAllNodes sums every node's hourly buckets per bucket_start in one
// GROUP BY query (the all-scope node chart's single-query source). UTC bounds for
// the same TZ-string-ordering reason as ListHourlyByNode.
func (r *nodeTrafficRepo) SumHourlyAllNodes(ctx context.Context, since, until time.Time) ([]domain.HourlyTraffic, error) {
	var rows []nodeTrafficHourlyRow
	err := r.db.WithContext(ctx).
		Model(&nodeTrafficHourlyRow{}).
		Select("bucket_start, SUM(up_bytes) AS up_bytes, SUM(down_bytes) AS down_bytes, SUM(total_bytes) AS total_bytes").
		Where("bucket_start >= ? AND bucket_start < ?", since.UTC(), until.UTC()).
		Group("bucket_start").
		Order("bucket_start ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.HourlyTraffic, len(rows))
	for i := range rows {
		out[i] = domain.HourlyTraffic{
			BucketStart: rows[i].BucketStart,
			UpBytes:     rows[i].UpBytes,
			DownBytes:   rows[i].DownBytes,
			TotalBytes:  rows[i].TotalBytes,
		}
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
