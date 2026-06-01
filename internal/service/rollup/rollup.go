// Package rollup downsamples the raw 5-minute traffic snapshot tables
// into per-hour UTC delta buckets. The hourly tables are the SOLE source
// for the admin / user traffic charts (HistoryFor / NodeHistoryFor read
// them directly); raw is kept only as a short 7-day window feeding this
// rollup (and the live quota math), while long-window history lives on the
// *_hourly tables, gated by the admin-tunable TrafficHistoryDays setting.
//
// Only the USER and NODE tiers are rolled up — those are the only tiers any
// chart reads. The per-client tier was previously rolled up too but nothing
// ever read client_traffic_snapshots_hourly, so it was pure write-only dead
// storage (the largest table by far); that rollup was removed. Existing
// client_*_hourly rows age out via PruneHourlyBefore.
//
// Why per-hour-UTC + group-in-Go-by-local-TZ at query time (rather than
// pre-bucketing per panel TZ): industry-standard pattern (Prometheus,
// InfluxDB, ClickHouse). Storing UTC keeps the data canonical, and a 24h
// LA day is just a defined UTC range — query layer does the math. The
// alternative (rolling up in panel TZ) bakes the TZ choice into storage,
// which corrupts the data the moment anyone queries from a different TZ.
//
// Design:
//   - Aggregation: MAX-MIN within each (entity, bucket_hour) group, since
//     the raw snapshots store *Lifetime* cumulative counters (managed by
//     internal/service/traffic's LastRaw monotonic-delta logic), so values
//     in a bucket are guaranteed monotonic-increasing — MAX-MIN equals the
//     real delta within that hour.
//   - Idempotency: each *_hourly table has UNIQUE(entity_id, bucket_start),
//     and writes go through GORM's OnConflict clause ("upsert"). Re-running
//     RollupOnce against the same raw rows is a no-op (or harmlessly
//     updates with the same values).
//   - Backfill: the same code path that handles incremental rollup also
//     handles the very first run after upgrade — it just sees more
//     un-rolled-up raw rows and processes them all in one batch. Raw is
//     bounded to a small window (~7 days) so the cost stays trivial.
//   - Boundary: we roll up through the CURRENT UTC hour (including the
//     partial, still-filling hour). Because every write is an idempotent
//     upsert and RollupOnce runs each traffic-poll cycle, the current hour's
//     bucket simply grows toward its final delta on each pass — so the chart's
//     "today" stays live (≤ one poll interval stale) instead of waiting for
//     the hour to close. The whole-hour-aligned raw prune (7d) never touches
//     the current hour, so re-rolling it stays correct until it closes.
package rollup

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Service is the rollup orchestrator. One instance per process; safe for
// concurrent ticks because every write goes through an idempotent upsert.
type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// RollupOnce aggregates the raw user/node tables into their *_hourly tables,
// through the current UTC hour. Idempotent (upsert), so it is called both on
// every traffic-poll cycle (for chart freshness) and in the hourly cleanup
// loop (rollup-before-prune ordering).
func (s *Service) RollupOnce(ctx context.Context) error {
	// Include the current, still-filling hour: rows captured up to now. The
	// idempotent upsert + per-poll cadence keep the open hour's bucket current.
	cutoff := time.Now()

	if userRows, err := s.rollupUser(ctx, cutoff); err != nil {
		return fmt.Errorf("user rollup: %w", err)
	} else if userRows > 0 {
		log.Info("traffic rollup (user)", "buckets_upserted", userRows)
	}

	if nodeRows, err := s.rollupNode(ctx, cutoff); err != nil {
		return fmt.Errorf("node rollup: %w", err)
	} else if nodeRows > 0 {
		log.Info("traffic rollup (node)", "buckets_upserted", nodeRows)
	}
	return nil
}

// ---- user-level rollup ----------------------------------------------------

type userRawRow struct {
	UserID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

type userHourlyKey struct {
	UserID      int64
	BucketStart time.Time
}

// trafficHourlyRow / clientTrafficHourlyRow / nodeTrafficHourlyRow are
// declared in internal/adapters/mysql/schema.go; we deliberately do NOT
// import them here to keep the rollup package adapter-agnostic. We INSERT
// via raw table-name strings + maps so the rollup service has no compile-
// time dependency on the adapter package (avoids an import cycle the
// moment a future rollup-aware repo wants to live next to the schema).

func (s *Service) rollupUser(ctx context.Context, cutoff time.Time) (int64, error) {
	var raws []userRawRow
	if err := s.db.WithContext(ctx).
		Table("traffic_snapshots").
		Select("user_id, up_bytes, down_bytes, total_bytes, captured_at").
		Find(&raws).Error; err != nil {
		return 0, err
	}
	if len(raws) == 0 {
		return 0, nil
	}

	type agg struct {
		minUp, maxUp, minDown, maxDown, minTotal, maxTotal int64
		seen                                               bool
	}
	// Cutoff filtering happens in Go rather than SQL: the captured_at column
	// roundtrips through GORM as a time.Time, but SQLite driver storage uses
	// TZ-offset-bearing strings whose lexicographic order doesn't match the
	// semantic order when values straddle TZ boundaries. raw is bounded to a
	// small window (default 7 days) so the table scan is cheap; doing the
	// filter post-fetch gives unambiguous time.Time.Before() semantics.
	buckets := make(map[userHourlyKey]*agg, len(raws)/4+1)
	for _, r := range raws {
		if !r.CapturedAt.Before(cutoff) {
			continue
		}
		k := userHourlyKey{UserID: r.UserID, BucketStart: hourFloor(r.CapturedAt)}
		b, ok := buckets[k]
		if !ok {
			b = &agg{}
			buckets[k] = b
		}
		if !b.seen {
			b.minUp, b.maxUp = r.UpBytes, r.UpBytes
			b.minDown, b.maxDown = r.DownBytes, r.DownBytes
			b.minTotal, b.maxTotal = r.TotalBytes, r.TotalBytes
			b.seen = true
			continue
		}
		if r.UpBytes < b.minUp {
			b.minUp = r.UpBytes
		}
		if r.UpBytes > b.maxUp {
			b.maxUp = r.UpBytes
		}
		if r.DownBytes < b.minDown {
			b.minDown = r.DownBytes
		}
		if r.DownBytes > b.maxDown {
			b.maxDown = r.DownBytes
		}
		if r.TotalBytes < b.minTotal {
			b.minTotal = r.TotalBytes
		}
		if r.TotalBytes > b.maxTotal {
			b.maxTotal = r.TotalBytes
		}
	}

	rows := make([]map[string]any, 0, len(buckets))
	for k, b := range buckets {
		up, down, total := b.maxUp-b.minUp, b.maxDown-b.minDown, b.maxTotal-b.minTotal
		// Skip "no traffic this hour" buckets — they'd be a row of zeros
		// per (entity, hour) for every idle hour, which on a quiet panel
		// can dominate the table without contributing to charts. Chart
		// query treats missing buckets as zero, so dropping them is
		// lossless.
		if up == 0 && down == 0 && total == 0 {
			continue
		}
		rows = append(rows, map[string]any{
			"user_id":      k.UserID,
			"bucket_start": k.BucketStart,
			"up_bytes":     up,
			"down_bytes":   down,
			"total_bytes":  total,
		})
	}
	return s.upsert(ctx, "traffic_snapshots_hourly", []string{"user_id", "bucket_start"}, rows)
}

// ---- node-level rollup ----------------------------------------------------

type nodeRawRow struct {
	NodeID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

type nodeHourlyKey struct {
	NodeID      int64
	BucketStart time.Time
}

func (s *Service) rollupNode(ctx context.Context, cutoff time.Time) (int64, error) {
	var raws []nodeRawRow
	if err := s.db.WithContext(ctx).
		Table("node_traffic_snapshots").
		Select("node_id, up_bytes, down_bytes, total_bytes, captured_at").
		Find(&raws).Error; err != nil {
		return 0, err
	}
	if len(raws) == 0 {
		return 0, nil
	}

	type agg struct {
		minUp, maxUp, minDown, maxDown, minTotal, maxTotal int64
		seen                                               bool
	}
	// See rollupUser for why cutoff filtering happens in Go, not SQL.
	buckets := make(map[nodeHourlyKey]*agg, len(raws)/4+1)
	for _, r := range raws {
		if !r.CapturedAt.Before(cutoff) {
			continue
		}
		k := nodeHourlyKey{NodeID: r.NodeID, BucketStart: hourFloor(r.CapturedAt)}
		b, ok := buckets[k]
		if !ok {
			b = &agg{}
			buckets[k] = b
		}
		if !b.seen {
			b.minUp, b.maxUp = r.UpBytes, r.UpBytes
			b.minDown, b.maxDown = r.DownBytes, r.DownBytes
			b.minTotal, b.maxTotal = r.TotalBytes, r.TotalBytes
			b.seen = true
			continue
		}
		if r.UpBytes < b.minUp {
			b.minUp = r.UpBytes
		}
		if r.UpBytes > b.maxUp {
			b.maxUp = r.UpBytes
		}
		if r.DownBytes < b.minDown {
			b.minDown = r.DownBytes
		}
		if r.DownBytes > b.maxDown {
			b.maxDown = r.DownBytes
		}
		if r.TotalBytes < b.minTotal {
			b.minTotal = r.TotalBytes
		}
		if r.TotalBytes > b.maxTotal {
			b.maxTotal = r.TotalBytes
		}
	}

	rows := make([]map[string]any, 0, len(buckets))
	for k, b := range buckets {
		up, down, total := b.maxUp-b.minUp, b.maxDown-b.minDown, b.maxTotal-b.minTotal
		if up == 0 && down == 0 && total == 0 {
			continue
		}
		rows = append(rows, map[string]any{
			"node_id":      k.NodeID,
			"bucket_start": k.BucketStart,
			"up_bytes":     up,
			"down_bytes":   down,
			"total_bytes":  total,
		})
	}
	return s.upsert(ctx, "node_traffic_snapshots_hourly", []string{"node_id", "bucket_start"}, rows)
}

// upsert pushes the batched rows through GORM's OnConflict clause so MySQL
// uses "ON DUPLICATE KEY UPDATE" and SQLite uses "ON CONFLICT(...) DO
// UPDATE" — both keyed by the unique-index columns supplied. Idempotent.
func (s *Service) upsert(ctx context.Context, table string, conflictCols []string, rows []map[string]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	conflict := make([]clause.Column, len(conflictCols))
	for i, c := range conflictCols {
		conflict[i] = clause.Column{Name: c}
	}
	res := s.db.WithContext(ctx).
		Table(table).
		Clauses(clause.OnConflict{
			Columns:   conflict,
			DoUpdates: clause.AssignmentColumns([]string{"up_bytes", "down_bytes", "total_bytes"}),
		}).
		CreateInBatches(rows, 500)
	return res.RowsAffected, res.Error
}

// hourFloor truncates t to the start of its containing UTC hour. The
// rollup pipeline canonicalises everything to UTC; per-TZ query bucketing
// happens at read time.
func hourFloor(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
}
