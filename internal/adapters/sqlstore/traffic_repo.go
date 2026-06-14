package sqlstore

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

// LatestForUsers returns the latest snapshot row for every userID in one SQL
// roundtrip. The poll calls it ONCE at the top of PollOnce, replacing N
// per-user LatestForUser SELECTs in the inner loop. Users with no prior
// snapshot are simply absent from the returned map — callers should treat
// absence the same as the single-user form's ErrNotFound. Returns an empty
// map for an empty input so callers don't need to gate the no-users path.
//
// Implementation: inner SELECT picks MAX(id) per user (id is monotonically
// increasing, so this matches LatestForUser's `Order("id DESC").Limit(1)`
// tie-breaker exactly — picking by MAX(captured_at) could split-tie if two
// snapshots ever share a timestamp). The outer SELECT pulls the full row
// for each chosen id. IN ? and INNER JOIN are portable across SQLite, MySQL,
// and Postgres.
func (r *trafficRepo) LatestForUsers(ctx context.Context, userIDs []int64) (map[int64]*domain.TrafficSnapshot, error) {
	if len(userIDs) == 0 {
		return map[int64]*domain.TrafficSnapshot{}, nil
	}
	var rows []trafficRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.* FROM traffic_snapshots t
		INNER JOIN (
			SELECT user_id, MAX(id) AS mid
			FROM traffic_snapshots
			WHERE user_id IN ?
			GROUP BY user_id
		) m ON t.id = m.mid
	`, userIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*domain.TrafficSnapshot, len(rows))
	for i := range rows {
		d := rows[i].toDomain()
		out[d.UserID] = d
	}
	return out, nil
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

// LastBeforeForUsers is the batched form of LastBefore — one SQL query
// returns the latest snapshot row strictly before `before` for every
// userID in one shot. Same MAX(id) trick as LatestForUsers, with the
// added `captured_at < before` predicate. Users with no prior row are
// absent from the result map (callers treat absence as "no baseline,
// use latest.TotalBytes as today's delta").
func (r *trafficRepo) LastBeforeForUsers(ctx context.Context, userIDs []int64, before time.Time) (map[int64]*domain.TrafficSnapshot, error) {
	if len(userIDs) == 0 {
		return map[int64]*domain.TrafficSnapshot{}, nil
	}
	var rows []trafficRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.* FROM traffic_snapshots t
		INNER JOIN (
			SELECT user_id, MAX(id) AS mid
			FROM traffic_snapshots
			WHERE user_id IN ? AND captured_at < ?
			GROUP BY user_id
		) m ON t.id = m.mid
	`, userIDs, before).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*domain.TrafficSnapshot, len(rows))
	for i := range rows {
		d := rows[i].toDomain()
		out[d.UserID] = d
	}
	return out, nil
}

// LastBeforeForUserClients returns the most recent per-client snapshot before
// `before` for every client owned by one user, keyed by ClientMatchKey. Same
// MAX(id) pattern as LastBeforeForUsers, grouped by the client tuple instead
// of user_id; idx_client_time (user_id, panel_id, inbound_id, client_email,
// captured_at) covers both the WHERE and the GROUP BY.
func (r *trafficRepo) LastBeforeForUserClients(ctx context.Context, userID int64, before time.Time) (map[string]*domain.ClientTrafficSnapshot, error) {
	var rows []clientTrafficRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.* FROM client_traffic_snapshots t
		INNER JOIN (
			SELECT panel_id, inbound_id, client_email, MAX(id) AS mid
			FROM client_traffic_snapshots
			WHERE user_id = ? AND captured_at < ?
			GROUP BY panel_id, inbound_id, client_email
		) m ON t.id = m.mid
	`, userID, before).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*domain.ClientTrafficSnapshot, len(rows))
	for i := range rows {
		row := rows[i]
		out[domain.ClientMatchKey(row.PanelID, row.InboundID, row.ClientEmail)] = &domain.ClientTrafficSnapshot{
			ID:          row.ID,
			UserID:      row.UserID,
			PanelID:     row.PanelID,
			InboundID:   row.InboundID,
			ClientEmail: row.ClientEmail,
			UpBytes:     row.UpBytes,
			DownBytes:   row.DownBytes,
			TotalBytes:  row.TotalBytes,
			CapturedAt:  row.CapturedAt,
		}
	}
	return out, nil
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
		// id ASC makes "last row in a chart bucket" deterministic when two
		// snapshots share captured_at — otherwise Postgres could pick either,
		// shifting the bucket's cumulative-counter delta run-to-run.
		Order("captured_at ASC, id ASC").
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

// ListHourlyByUser returns the user's rolled-up hourly delta buckets whose
// bucket_start falls in [since, until). Bounds are converted to UTC because
// bucket_start is stored UTC (hourFloor) and the SQLite driver compares times
// as TZ-bearing strings — UTC-vs-UTC keeps the lexicographic order semantically
// correct (MySQL/Postgres compare as real datetimes regardless). This is the
// long-window source for HistoryFor (raw is only kept 7 days).
func (r *trafficRepo) ListHourlyByUser(ctx context.Context, userID int64, since, until time.Time) ([]domain.HourlyTraffic, error) {
	var rows []trafficHourlyRow
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND bucket_start >= ? AND bucket_start < ?", userID, since.UTC(), until.UTC()).
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

// SumHourlyAllUsers sums every user's hourly buckets per bucket_start in one
// GROUP BY query (the all-scope chart's single-query source). UTC bounds for the
// same TZ-string-ordering reason as ListHourlyByUser.
func (r *trafficRepo) SumHourlyAllUsers(ctx context.Context, since, until time.Time) ([]domain.HourlyTraffic, error) {
	var rows []trafficHourlyRow
	err := r.db.WithContext(ctx).
		Model(&trafficHourlyRow{}).
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

// PruneBefore deletes user-level and per-client traffic snapshots captured
// strictly before cutoff. Driven by the fixed rawTrafficRetentionDays
// constant in internal/app — raw covers "today + a few days buffer", with
// long-window history living on the *_hourly tables (see
// PruneHourlyBefore). The (user_id, captured_at) and idx_client_time
// composite indexes already support efficient range deletes.
func (r *trafficRepo) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	userRes := r.db.WithContext(ctx).
		Where("captured_at < ?", cutoff).
		Delete(&trafficRow{})
	if userRes.Error != nil {
		return 0, userRes.Error
	}
	clientRes := r.db.WithContext(ctx).
		Where("captured_at < ?", cutoff).
		Delete(&clientTrafficRow{})
	if clientRes.Error != nil {
		return userRes.RowsAffected, clientRes.Error
	}
	return userRes.RowsAffected + clientRes.RowsAffected, nil
}

// PruneHourlyBefore deletes rolled-up hourly rows from both
// traffic_snapshots_hourly and client_traffic_snapshots_hourly with
// bucket_start strictly before cutoff. Driven by TrafficHistoryDays (admin-
// tunable; default 730). idx_traffic_hourly_bucket / idx_client_hourly_bucket
// cover the range delete.
func (r *trafficRepo) PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	userRes := r.db.WithContext(ctx).
		Where("bucket_start < ?", cutoff).
		Delete(&trafficHourlyRow{})
	if userRes.Error != nil {
		return 0, userRes.Error
	}
	clientRes := r.db.WithContext(ctx).
		Where("bucket_start < ?", cutoff).
		Delete(&clientTrafficHourlyRow{})
	if clientRes.Error != nil {
		return userRes.RowsAffected, clientRes.Error
	}
	return userRes.RowsAffected + clientRes.RowsAffected, nil
}
