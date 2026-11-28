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
//   - Three passes: RollupOnce re-emits EVERY bucket from a full raw scan (the
//     backfill). RollupBeforePrune is the hourly cleanup's rollup-before-prune
//     pass: RollupOnce the first time, afterwards only what changed since the
//     previous pass. RollupRecent emits
//     only buckets in the last few hours from a bounded raw window
//     (withRecentRawWindow) — called every traffic poll to keep "today" live
//     without re-scanning/re-writing the whole 7-day raw set each cycle.
package rollup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
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

	// lastComplete is when the last RollupBeforePrune pass that succeeded
	// started (zero until the first one). See RollupBeforePrune.
	mu           sync.Mutex
	lastComplete time.Time
	// lastFull is when the last full pass that succeeded started.
	lastFull time.Time
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
// re-emitting EVERY bucket from a full raw scan. Idempotent (upsert). It is the
// backfill: RollupBeforePrune runs it as the first pass of a process.
func (s *Service) RollupOnce(ctx context.Context) error {
	return s.rollup(ctx, time.Time{})
}

// RollupBeforePrune is the hourly cleanup loop's rollup-before-prune pass.
//
// The first pass of a process is RollupOnce: the backfill, which also covers
// whatever happened while the process was down. Every later pass emits only the
// buckets since the previous pass that SUCCEEDED, less a margin, from a raw
// window bounded the same way (withRecentRawWindow).
//
// Why that is enough: raw rows are only ever appended, with captured_at = the
// poll's own clock. A pass that started at T has emitted every bucket its
// samples reach. A sample that arrives after T changes a bucket only through
// the segment from the sample before it, and a segment longer than the
// heartbeat is dropped, so nothing before hourFloor(T - heartbeat) can change
// again. Buckets older than that are final and were written by an earlier pass.
//
// The full pass every hour read all raw rows of the retention window into
// memory and rewrote every bucket: about 100k rows today, tens of millions at a
// few thousand users, and on SQLite it holds the only connection meanwhile.
//
// The mark moves only when a pass succeeds, so after any number of failed
// passes the next one still starts from the last good one. A clock that went
// backwards makes the mark meaningless and the pass is a full one again, and
// so is one pass a day (fullPassEvery).
//
// A bucket at the old edge of the bounded window may have no sample at or
// before its :00 inside that window. The window reaches back at least one
// heartbeat, so nothing the bucket depends on is missing and its value is
// overwritten like any other (emitFromAccs, exact). Only a window that starts
// too close to the 7-day prune falls back to inserting such a bucket once.
func (s *Service) RollupBeforePrune(ctx context.Context) error {
	started := s.now()
	s.mu.Lock()
	last, lastFull := s.lastComplete, s.lastFull
	s.mu.Unlock()

	var emitSince time.Time // zero = every bucket
	full := last.IsZero() || last.After(started) || started.Sub(lastFull) >= fullPassEvery
	if !full {
		emitSince = last.Add(-beforePruneMargin(s.heartbeat))
	}
	if err := s.rollup(ctx, emitSince); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastComplete = started
	if full {
		s.lastFull = started
	}
	s.mu.Unlock()
	return nil
}

// fullPassEvery is how often RollupBeforePrune is a full pass again. The
// bounded passes are exact by argument (see emitFromAccs); the full pass is the
// corrector for whatever the argument does not cover: a snapshot committed
// more than an hour after it was stamped, a window too old to be exact. Raw
// rows live for 7 days, so each bucket gets several of these before its rows
// are pruned, at a twenty-fourth of what the full pass every hour used to cost.
const fullPassEvery = 24 * time.Hour

// beforePruneMargin is how far behind the previous pass RollupBeforePrune
// starts emitting. A sample that arrives after that pass changes buckets from
// the hour its segment starts in: the segment is at most one heartbeat long,
// that hour starts at most an hour before it, and one more hour covers a
// sample stamped before the pass started but committed after it (the poll
// writes its snapshots in one batch at its end). Never less than the window
// the per-poll pass keeps fresh.
func beforePruneMargin(heartbeat time.Duration) time.Duration {
	if m := heartbeat + 2*time.Hour; m > rollupRecentEmitWindow {
		return m
	}
	return rollupRecentEmitWindow
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
//
// exact says the pass read a bounded window that is known to hold every sample
// its buckets depend on (windowIsExact). Then no bucket is left-incomplete:
// the look-back is at least one heartbeat, so a sample missing from before a
// bucket's :00 could only have formed a segment longer than the heartbeat,
// which every pass drops anyway. Without this the first hour after a long
// silence (a user back after two days) was inserted once from its first five
// minutes and never grew, and by the time a full pass came round the prune
// could have removed the old sample that would have let it correct the value.
func emitFromAccs(buckets map[entityHourKey]*hourAcc, earliest map[int64]time.Time, idCol string, emitSince time.Time, exact bool) (overwrite, keepIfNew []map[string]any) {
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
		if e, ok := earliest[k.id]; exact || (ok && !e.After(k.bucket)) {
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
	q = s.withRecentRawWindow(q, emitSince)
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
	overwrite, keepIfNew := emitFromAccs(buckets, earliest, "user_id", emitSince, s.windowIsExact(emitSince, cutoff))
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
	q = s.withRecentRawWindow(q, emitSince)
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
	overwrite, keepIfNew := emitFromAccs(buckets, earliest, "node_id", emitSince, s.windowIsExact(emitSince, cutoff))
	return s.upsertSplit(ctx, "node_traffic_snapshots_hourly", []string{"node_id", "bucket_start"}, overwrite, keepIfNew)
}

// withRecentRawWindow bounds a raw-table query to the window a bounded pass
// emits (buckets >= emitSince) plus a look-back, so that the oldest emitted
// bucket still has the sample before its :00 in hand. captured_at is indexed
// (idx_traffic_captured / idx_node_traffic_captured), so this is a range scan,
// not a full read of the 7-day raw window. A zero emitSince keeps the full scan.
//
// The look-back has two parts.
//
// The segment that crosses a bucket's :00 is at most one heartbeat long, so its
// left sample lies at most heartbeat + 1h before the bucket's end; a fixed 2h
// was too short for any poll cadence above 48 minutes, and the bucket was then
// computed without that segment and kept short (it is insert-once when its
// left sample is out of sight).
//
// On SQLite the comparison is not one of instants. The driver stores a time as
// text with its zone offset and compares text, so a row reads as early or as
// late as its wall clock, whatever instant it is. The two raw tables are not
// even stamped alike: user snapshots carry the panel's timezone, node
// snapshots and this bound the process's. A row stamped at -07:00 reads seven
// hours earlier than a UTC bound and fell out of the window, so with the panel
// on a zone behind the process no user bucket was ever written by a bounded
// pass. Offsets span -12h to +14h, so the bound is lowered by the widest
// distance two of them can have. The extra rows cost a little reading and
// nothing else: accumulate and emitFromAccs work on instants and bound the
// result exactly. PostgreSQL and MySQL compare instants and need none of it.
func (s *Service) withRecentRawWindow(q *gorm.DB, emitSince time.Time) *gorm.DB {
	if emitSince.IsZero() {
		return q
	}
	from := emitSince.Add(-s.lookBack())
	if s.db.Dialector.Name() != "sqlite" {
		return q.Where("captured_at >= ?", from)
	}
	// Two steps on SQLite. The text bound, lowered by the zone slack, is what
	// the index can serve. datetime() then reads each candidate as an instant
	// (it understands the stored offset) and drops the day of rows the slack
	// let in, so they are not loaded: at a few thousand users that day is over
	// a million rows on every poll. A value datetime() cannot read is kept;
	// accumulate's own filters are the exact ones either way.
	return q.Where("captured_at >= ?", from.Add(-sqliteZoneSlack)).
		Where("(datetime(captured_at) IS NULL OR datetime(captured_at) >= ?)", from.UTC().Format("2006-01-02 15:04:05"))
}

// sqliteZoneSlack is the widest distance between two zone offsets (-12h, +14h).
const sqliteZoneSlack = 26 * time.Hour

// exactWindowMax is how far back a bounded window may start and still be taken
// as holding every sample its buckets depend on. Raw snapshots are pruned after
// 7 days (app's hourly cleanup); a window that starts well inside that has lost
// nothing to the prune. One that reaches further back, after days of failed
// passes, may be missing the sample before its oldest bucket, and falls back to
// the insert-once rule for left-incomplete buckets.
const exactWindowMax = 72 * time.Hour

//
// It is the window's START that has to be inside retained data, so the
// look-back counts: at a poll cadence of days the heartbeat alone reaches past
// the prune.
func (s *Service) windowIsExact(emitSince, now time.Time) bool {
	return !emitSince.IsZero() && now.Sub(emitSince.Add(-s.lookBack())) <= exactWindowMax
}

// lookBack is how far before emitSince a bounded pass reads raw rows; see
// withRecentRawWindow.
func (s *Service) lookBack() time.Duration {
	if lb := s.heartbeat + time.Hour; lb > 2*time.Hour {
		return lb
	}
	return 2 * time.Hour
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
	onConflict := onConflictClause(s.db.Dialector.Name(), conflictCols, keepOnConflict)
	res := s.db.WithContext(ctx).
		Table(table).
		Clauses(onConflict).
		CreateInBatches(rows, 500)
	return res.RowsAffected, res.Error
}

// onConflictClause builds the per-dialect conflict action for the hourly upsert.
// keepOnConflict=false → overwrite the up/down/total counters with the recomputed
// values. keepOnConflict=true → keep the existing row (insert-once, never shrink).
//
// The "keep" action is dialect-specific. SQLite/Postgres render
// OnConflict.DoNothing as a native "DO NOTHING". MySQL, however, renders it as an
// EMPTY "ON DUPLICATE KEY UPDATE" with no assignments — invalid SQL that fails with
// "Error 1064 ... near ”" (observed in production on a MySQL deployment). For
// MySQL we instead emit a no-op self-assignment of a conflict-key column
// ("`col`=`col`"): a valid "keep the existing row" that touches no data column.
// Verified via offline DryRun SQL generation across dialects (see the test).
func onConflictClause(dialect string, conflictCols []string, keepOnConflict bool) clause.OnConflict {
	conflict := make([]clause.Column, len(conflictCols))
	for i, c := range conflictCols {
		conflict[i] = clause.Column{Name: c}
	}
	oc := clause.OnConflict{Columns: conflict}
	if !keepOnConflict {
		oc.DoUpdates = clause.AssignmentColumns([]string{"up_bytes", "down_bytes", "total_bytes"})
		return oc
	}
	if dialect == "mysql" && len(conflictCols) > 0 {
		oc.DoUpdates = clause.Assignments(map[string]any{
			conflictCols[0]: clause.Column{Name: conflictCols[0]},
		})
	} else {
		oc.DoNothing = true
	}
	return oc
}

// hourFloor truncates t to the start of its containing UTC hour. The
// rollup pipeline canonicalises everything to UTC; per-TZ query bucketing
// happens at read time.
func hourFloor(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
}
