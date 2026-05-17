package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type trafficRepo struct{ db *gorm.DB }

// batchSize controls how many rows GORM packs into one INSERT statement.
// MySQL caps the bound parameters per statement at 65535; with snapshot
// rows around 8 fields each, 500 stays well below that with headroom.
// SQLite has no equivalent hard limit but the same batch size keeps
// memory pressure predictable.
const batchSize = 500

// InsertBatch packs all supplied user snapshots into a single SQL
// roundtrip per batchSize chunk. Returns nil for an empty input so the
// caller's no-snapshots-yet code path stays a no-op. Caller is expected
// to have already populated each snapshot's fields; we don't echo back
// generated IDs because PollOnce doesn't consume them.
func (r *trafficRepo) InsertBatch(ctx context.Context, snaps []*domain.TrafficSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	rows := make([]trafficRow, len(snaps))
	for i, s := range snaps {
		rows[i] = trafficRow{
			UserID:     s.UserID,
			UpBytes:    s.UpBytes,
			DownBytes:  s.DownBytes,
			TotalBytes: s.TotalBytes,
			CapturedAt: s.CapturedAt,
		}
	}
	return r.db.WithContext(ctx).CreateInBatches(&rows, batchSize).Error
}

// InsertClientBatch is the per-client counterpart of InsertBatch. This
// is the dominant per-poll INSERT count on the panel (N users × M
// inbounds × per-client snapshots) so the batch packing yields the
// largest wall-clock improvement when admin clicks "Poll Now".
func (r *trafficRepo) InsertClientBatch(ctx context.Context, snaps []*domain.ClientTrafficSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	rows := make([]clientTrafficRow, len(snaps))
	for i, s := range snaps {
		rows[i] = clientTrafficRow{
			UserID:      s.UserID,
			PanelID:     s.PanelID,
			InboundID:   s.InboundID,
			ClientEmail: s.ClientEmail,
			UpBytes:     s.UpBytes,
			DownBytes:   s.DownBytes,
			TotalBytes:  s.TotalBytes,
			CapturedAt:  s.CapturedAt,
		}
	}
	return r.db.WithContext(ctx).CreateInBatches(&rows, batchSize).Error
}

func (r *trafficRepo) Insert(ctx context.Context, s *domain.TrafficSnapshot) error {
	row := trafficRow{
		UserID:     s.UserID,
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

func (r *trafficRepo) LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error) {
	var row trafficRow
	tx := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
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

func (r *trafficRepo) LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error) {
	var row trafficRow
	tx := r.db.WithContext(ctx).
		Where("user_id = ? AND captured_at < ?", userID, before).
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

func (r *trafficRepo) ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error) {
	var rows []trafficRow
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND captured_at >= ? AND captured_at < ?", userID, since, until).
		Order("captured_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.TrafficSnapshot, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *trafficRepo) InsertClient(ctx context.Context, s *domain.ClientTrafficSnapshot) error {
	row := clientTrafficRow{
		UserID:      s.UserID,
		PanelID:     s.PanelID,
		InboundID:   s.InboundID,
		ClientEmail: s.ClientEmail,
		UpBytes:     s.UpBytes,
		DownBytes:   s.DownBytes,
		TotalBytes:  s.TotalBytes,
		CapturedAt:  s.CapturedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	s.ID = row.ID
	return nil
}

func (r *trafficRepo) LatestForClient(ctx context.Context, userID int64, panelID int64, inboundID int, email string) (*domain.ClientTrafficSnapshot, error) {
	var row clientTrafficRow
	tx := r.db.WithContext(ctx).
		Where("user_id = ? AND panel_id = ? AND inbound_id = ? AND client_email = ?", userID, panelID, inboundID, email).
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
