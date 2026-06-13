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
// LA day is just a defined UTC range — query layer does the math.
//
// Aggregation model — TIME-PROPORTIONAL DISTRIBUTION (a.k.a. counter
// normalisation, the RRDtool / MRTG / Cacti approach):
//
// The raw snapshots store *Lifetime* cumulative counters (monotonic, managed by
// internal/service/traffic's LastRaw delta logic). For each pair of consecutive
// samples (a, b) of one entity, the traffic that flowed is the counter delta
// b-a, spread over the wall-clock interval [a, b]. We attribute that delta to the
// UTC-hour buckets the interval overlaps, weighted by the FRACTION of the
// interval that falls in each hour. Equivalently: we linearly interpolate the
// cumulative counter to each :00 boundary and take consecutive-boundary diffs, so
// an hour's value is exactly its true wall-clock-hour usage — no dependence on a
// sample landing at :00 (the poll cadence is left untouched).
//
// This supersedes the older MAX-MIN + "carry-in" scheme, which attributed the
// WHOLE boundary-crossing chunk to the later hour (a ≤ one-poll-interval phase
// error). Proportional split is the unbiased estimate and conserves the total
// exactly (the per-hour fractions of every segment sum back to the segment delta).
//
// Edge handling:
//
//   - Counter reset (delta < 0, e.g. xray restart between samples): that
//     component clamps to 0 — the cross-sample traffic during a reset is
//     unknowable, so it is not attributed (can't go negative).
//
//   - Data gap > the heartbeat (panel unreachable for an extended period; the
//     heartbeat is derived from the poll cadence): the segment is DROPPED rather
//     than smeared linearly across the missing hours — we can't know when within
//     the gap the traffic flowed (RRDtool's "heartbeat" / UNKNOWN). Mirrors the
//     old "no carry-in across a missing hour".
//
//   - Open (still-filling) hour: its last segment ends at the latest sample, so
//     the bucket grows toward its final value on each pass — the chart's "today"
//     stays live (≤ one poll interval stale).
//
//   - First ever hour / oldest surviving hour after the 7-day raw prune: its left
//     :00 boundary has no sample before it, so its value can't be (re)computed in
//     full — see emitFromAccs' left-completeness split.
//
//   - Idempotency: each *_hourly table has UNIQUE(entity_id, bucket_start) and
//     writes go through GORM's OnConflict upsert; re-running on the same raw rows
//     reproduces the same values.
//
//   - Two passes: RollupOnce re-emits EVERY bucket from a full raw scan (hourly
//     cleanup's rollup-before-prune pass + first-run backfill). RollupRecent emits
//     only buckets in the last few hours from a bounded raw window
//     (withRecentRawWindow) — called every traffic poll to keep "today" live
//     without re-scanning/re-writing the whole 7-day raw set each cycle.
package rollup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// rollupRecentEmitWindow bounds how far back RollupRecent (the per-poll pass)
// re-upserts buckets. Anything older than this is left to the hourly cleanup
// pass — it's already final, so re-writing it every 5 minutes is pure write
// amplification. A few hours of overlap absorbs delayed/missed poll cycles.
const rollupRecentEmitWindow = 3 * time.Hour

// minHeartbeat is the floor for the gap heartbeat (see Service.heartbeat). At the
// default 5-min poll cadence the heartbeat is this 1h floor: a fully-missing hour
// (>= 60min sample gap) is never back-filled by smearing (matching the old "no
// carry-in across a missing hour"), while sub-hour outages still interpolate.
const minHeartbeat = time.Hour

// Service is the rollup orchestrator. One instance per process; safe for
// concurrent ticks because every write goes through an idempotent upsert.
type Service struct {
	db *gorm.DB
	// heartbeat is the largest gap between two consecutive raw samples across
	// which traffic is still distributed; a segment spanning a larger gap is
	// DROPPED (a data gap — we can't know when within it the traffic flowed,
	// RRDtool's UNKNOWN). Derived from the poll interval so a coarse cadence
	// doesn't silently drop every segment (e.g. a 90-min poll would exceed a
	// fixed 1h heartbeat on every normal segment and blank the chart).
	heartbeat time.Duration
	// now is the clock seam (defaults to time.Now). Tests pin it to a fixed
	// instant so the raw-window bound and the bucket math are deterministic and
	// timezone-independent — the SQLite driver compares captured_at as
	// TZ-offset-bearing strings (see withRecentRawWindow), so a test mixing
	// time.Now() (process TZ) with hourFloor() (UTC) fixtures was flaky in any
	// non-UTC environment.
	now func() time.Time
}

// New builds the rollup service. pollInterval is the traffic poll cadence; the
// gap heartbeat is derived from it (see Service.heartbeat). Pass 0 to use the
// 1h floor (tests / unknown cadence).
func New(db *gorm.DB, pollInterval time.Duration) *Service {
	return &Service{db: db, heartbeat: heartbeatFor(pollInterval), now: time.Now}
}

// heartbeatFor returns the gap heartbeat for a poll cadence: max(1h floor,
// 2.5x the interval). The 2.5x tolerates ~1–2 missed polls before a gap is
// treated as a real outage; the 1h floor keeps the common small-interval case
// from smearing across a fully-missing hour.
func heartbeatFor(pollInterval time.Duration) time.Duration {
	hb := time.Duration(float64(pollInterval) * 2.5)
	if hb < minHeartbeat {
		return minHeartbeat
	}
	return hb
}

// RollupOnce aggregates the raw user/node tables into their *_hourly tables,
// re-emitting EVERY bucket. Idempotent (upsert). Used by the hourly cleanup
// loop (rollup-before-prune) and first-run backfill.
func (s *Service) RollupOnce(ctx context.Context) error {
	return s.rollup(ctx, time.Time{})
}

// RollupRecent aggregates the same tables but only re-emits buckets within the
// last rollupRecentEmitWindow. Called every traffic poll to keep the open hour
// (the chart's "today") live without re-upserting the whole raw window each
// cycle. The bounded raw window still includes enough look-back that the
// recent buckets are computed identically to RollupOnce.
func (s *Service) RollupRecent(ctx context.Context) error {
	return s.rollup(ctx, s.now().Add(-rollupRecentEmitWindow))
}

// rollup runs the user + node passes. emitSince bounds which buckets are
// upserted (zero = all); see emitFromAccs.
func (s *Service) rollup(ctx context.Context, emitSince time.Time) error {
	// Include the current, still-filling hour: rows captured up to now. The
	// idempotent upsert + per-poll cadence keep the open hour's bucket current.
	cutoff := s.now()

	if userRows, err := s.rollupUser(ctx, cutoff, emitSince); err != nil {
		return fmt.Errorf("user rollup: %w", err)
	} else if userRows > 0 {
		log.Info("traffic rollup (user)", "buckets_upserted", userRows)
	}

	if nodeRows, err := s.rollupNode(ctx, cutoff, emitSince); err != nil {
		return fmt.Errorf("node rollup: %w", err)
	} else if nodeRows > 0 {
		log.Info("traffic rollup (node)", "buckets_upserted", nodeRows)
	}
	return nil
}

// ---- shared aggregation ---------------------------------------------------

// rawSample is one cumulative-counter reading for some entity (user or node).
type rawSample struct {
	entityID   int64
	upBytes    int64
	downBytes  int64
	totalBytes int64
	capturedAt time.Time
}

type entityHourKey struct {
	id     int64
	bucket time.Time
}

// hourAcc accumulates the time-proportional bytes attributed to one (entity,
// hour) bucket. Float because each segment contributes a fraction of its delta;
// rounded to int64 at emit time.
type hourAcc struct {
	up, down, total float64
}

// accumulate distributes each entity's consecutive-sample counter deltas across
// the UTC hour buckets they span, weighted by the time fraction in each hour
// (linear interpolation of the cumulative counter to the hour boundaries — the
// RRDtool / MRTG normalisation model). Returns the per-(entity,hour) float
// accumulators plus the earliest sample time per entity (used by emitFromAccs to
// decide left-completeness).
//
// Samples at/after cutoff (future clock-skew) are dropped. Time comparisons use
// time.Time semantics (instant-based, TZ-independent), so no captured_at TZ-
// string pitfall here — unlike the SQL window bound (see withRecentRawWindow).
func accumulate(raws []rawSample, cutoff time.Time, heartbeat time.Duration) (map[entityHourKey]*hourAcc, map[int64]time.Time) {
	byEntity := make(map[int64][]rawSample)
	for _, r := range raws {
		if !r.capturedAt.Before(cutoff) {
			continue
		}
		byEntity[r.entityID] = append(byEntity[r.entityID], r)
	}
	buckets := make(map[entityHourKey]*hourAcc)
	earliest := make(map[int64]time.Time, len(byEntity))
	for id, samples := range byEntity {
		sort.Slice(samples, func(i, j int) bool { return samples[i].capturedAt.Before(samples[j].capturedAt) })
		earliest[id] = samples[0].capturedAt
		for i := 1; i < len(samples); i++ {
			distributeSegment(buckets, id, samples[i-1], samples[i], heartbeat)
		}
	}
	return buckets, earliest
}

// distributeSegment splits the [a,b] cumulative delta across the UTC hours the
// segment overlaps, each hour getting the delta times its time fraction of the
// segment. Resets clamp to 0; gaps beyond the heartbeat are dropped.
func distributeSegment(buckets map[entityHourKey]*hourAcc, id int64, a, b rawSample, heartbeat time.Duration) {
	dur := b.capturedAt.Sub(a.capturedAt)
	if dur <= 0 || dur > heartbeat {
		return
	}
	dUp := nonNeg(b.upBytes - a.upBytes)
	dDown := nonNeg(b.downBytes - a.downBytes)
	dTotal := nonNeg(b.totalBytes - a.totalBytes)
	if dUp == 0 && dDown == 0 && dTotal == 0 {
		return
	}
	durSec := dur.Seconds()
	for h := hourFloor(a.capturedAt); h.Before(b.capturedAt); h = h.Add(time.Hour) {
		segStart := a.capturedAt
		if h.After(segStart) {
			segStart = h
		}
		segEnd := b.capturedAt
		if hEnd := h.Add(time.Hour); hEnd.Before(segEnd) {
			segEnd = hEnd
		}
		frac := segEnd.Sub(segStart).Seconds() / durSec
		if frac <= 0 {
			continue
		}
		k := entityHourKey{id: id, bucket: h}
		acc := buckets[k]
		if acc == nil {
			acc = &hourAcc{}
			buckets[k] = acc
		}
		acc.up += float64(dUp) * frac
		acc.down += float64(dDown) * frac
		acc.total += float64(dTotal) * frac
	}
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// emitFromAccs rounds the float accumulators to int byte deltas and splits the
// rows into:
//   - overwrite: LEFT-COMPLETE buckets — a real sample exists at or before the
//     hour's :00 start, so the segment crossing that boundary is present and the
//     value is authoritative on every re-emit (the still-filling current hour
//     grows toward its final value this way).
//   - keepIfNew: buckets whose left edge is NOT anchored by a sample — the first
//     ever hour, or the oldest surviving hour once the 7-day raw prune removes the
//     sample before its :00. Their recomputed value would be SHORT by the missing
//     cross-boundary chunk, so they are inserted once and never overwritten
//     (DO NOTHING), preserving the value an earlier pass persisted while the
//     prior sample still existed.
//
// Only buckets with bucket_start >= emitSince are emitted (zero = all). Zero-
// traffic buckets are dropped (the chart treats a missing bucket as zero).
func emitFromAccs(buckets map[entityHourKey]*hourAcc, earliest map[int64]time.Time, idCol string, emitSince time.Time) (overwrite, keepIfNew []map[string]any) {
	for k, acc := range buckets {
		if !emitSince.IsZero() && k.bucket.Before(emitSince) {
			continue
		}
		up := int64(math.Round(acc.up))
		down := int64(math.Round(acc.down))
		total := int64(math.Round(acc.total))
		if up == 0 && down == 0 && total == 0 {
			continue
		}
		row := map[string]any{
			idCol:          k.id,
			"bucket_start": k.bucket,
			"up_bytes":     up,
			"down_bytes":   down,
			"total_bytes":  total,
		}
		if e, ok := earliest[k.id]; ok && !e.After(k.bucket) {
			overwrite = append(overwrite, row)
		} else {
			keepIfNew = append(keepIfNew, row)
		}
	}
	return overwrite, keepIfNew
}

// ---- user-level rollup ----------------------------------------------------

type userRawRow struct {
	UserID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

func (s *Service) rollupUser(ctx context.Context, cutoff, emitSince time.Time) (int64, error) {
	var raws []userRawRow
	q := s.db.WithContext(ctx).
		Table("traffic_snapshots").
		Select("user_id, up_bytes, down_bytes, total_bytes, captured_at")
	q = withRecentRawWindow(q, emitSince)
	if err := q.Find(&raws).Error; err != nil {
		return 0, err
	}
	if len(raws) == 0 {
		return 0, nil
	}
	samples := make([]rawSample, len(raws))
	for i, r := range raws {
		samples[i] = rawSample{entityID: r.UserID, upBytes: r.UpBytes, downBytes: r.DownBytes, totalBytes: r.TotalBytes, capturedAt: r.CapturedAt}
	}
	buckets, earliest := accumulate(samples, cutoff, s.heartbeat)
	overwrite, keepIfNew := emitFromAccs(buckets, earliest, "user_id", emitSince)
	return s.upsertSplit(ctx, "traffic_snapshots_hourly", []string{"user_id", "bucket_start"}, overwrite, keepIfNew)
}

// ---- node-level rollup ----------------------------------------------------

type nodeRawRow struct {
	NodeID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

func (s *Service) rollupNode(ctx context.Context, cutoff, emitSince time.Time) (int64, error) {
	var raws []nodeRawRow
	q := s.db.WithContext(ctx).
		Table("node_traffic_snapshots").
		Select("node_id, up_bytes, down_bytes, total_bytes, captured_at")
	q = withRecentRawWindow(q, emitSince)
	if err := q.Find(&raws).Error; err != nil {
		return 0, err
	}
	if len(raws) == 0 {
		return 0, nil
	}
	samples := make([]rawSample, len(raws))
	for i, r := range raws {
		samples[i] = rawSample{entityID: r.NodeID, upBytes: r.UpBytes, downBytes: r.DownBytes, totalBytes: r.TotalBytes, capturedAt: r.CapturedAt}
	}
	buckets, earliest := accumulate(samples, cutoff, s.heartbeat)
	overwrite, keepIfNew := emitFromAccs(buckets, earliest, "node_id", emitSince)
	return s.upsertSplit(ctx, "node_traffic_snapshots_hourly", []string{"node_id", "bucket_start"}, overwrite, keepIfNew)
}

// withRecentRawWindow bounds a raw-table query to the window RollupRecent emits
// (buckets >= emitSince) plus a look-back so the oldest emitted bucket's
// left-boundary segment (the sample before its :00, up to maxInterpGap earlier)
// is still fetched. captured_at is indexed (idx_traffic_captured /
// idx_node_traffic_captured), so this is a small range scan, not a full read of
// the 7-day raw window. RollupOnce passes a zero emitSince and keeps the full scan.
func withRecentRawWindow(q *gorm.DB, emitSince time.Time) *gorm.DB {
	if emitSince.IsZero() {
		return q
	}
	// The bound MUST be in the same wall-clock representation the driver stored
	// captured_at in (the process TZ), NOT forced to UTC: the SQLite driver
	// persists times as TZ-offset-bearing strings and compares them
	// lexicographically, so a UTC bound vs a local-offset stored string silently
	// excludes valid rows (e.g. "16:37-07:00" < "23:37+00:00" as strings). In
	// production the clock is pinned to UTC so this IS UTC; in a non-UTC dev/test
	// env it matches the local-offset rows. emitSince already carries the process
	// TZ (it comes from time.Now()). accumulate's cutoff + emitSince filters still
	// bound the result exactly. The 2h look-back (maxInterpGap of boundary context
	// + 1h margin) keeps the oldest emitted bucket's left-boundary segment in range
	// and absorbs minor string-ordering fuzz.
	return q.Where("captured_at >= ?", emitSince.Add(-2*time.Hour))
}

// upsertSplit writes the overwrite set (re-emit allowed) and the keepIfNew set
// (insert-once, never shrink) for one hourly table. See emitFromAccs for why the
// split exists.
func (s *Service) upsertSplit(ctx context.Context, table string, conflictCols []string, overwrite, keepIfNew []map[string]any) (int64, error) {
	var n int64
	c, err := s.upsert(ctx, table, conflictCols, overwrite, false)
	n += c
	if err != nil {
		return n, err
	}
	c, err = s.upsert(ctx, table, conflictCols, keepIfNew, true)
	n += c
	return n, err
}

// upsert pushes the batched rows through GORM's OnConflict clause so MySQL
// uses "ON DUPLICATE KEY UPDATE" and SQLite uses "ON CONFLICT(...) DO
// UPDATE" — both keyed by the unique-index columns supplied. Idempotent.
// keepOnConflict=true switches the conflict action to DO NOTHING / INSERT
// IGNORE so an existing row is preserved rather than overwritten with a
// re-computed value short by a now-pruned cross-boundary segment.
func (s *Service) upsert(ctx context.Context, table string, conflictCols []string, rows []map[string]any, keepOnConflict bool) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	conflict := make([]clause.Column, len(conflictCols))
	for i, c := range conflictCols {
		conflict[i] = clause.Column{Name: c}
	}
	onConflict := clause.OnConflict{Columns: conflict}
	if keepOnConflict {
		onConflict.DoNothing = true
	} else {
		onConflict.DoUpdates = clause.AssignmentColumns([]string{"up_bytes", "down_bytes", "total_bytes"})
	}
	res := s.db.WithContext(ctx).
		Table(table).
		Clauses(onConflict).
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
