package destaudit

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"slices"
	"time"
)

const workerChunkRows = 1000

// Events never carry destinations, accounts, raw SQL or driver errors.
type workerEvent struct {
	agentID, kind, outcome     string
	panelID, receivedHour      int64
	rows                       int
	persistedLoss              bool
	nodeDropped, nodeUnmatched uint64
}

type workerSummary struct{ batches, rows int }
type batchProgress struct {
	mapped           mappedBatch
	reserved, stored int
}

// Exactly one tracked background worker owns at most four mapped batches,
// one per active lane. Their original queue quota stays reserved throughout.
type ingestWorker struct {
	q        *batchQueue
	repo     ports.DestAuditRepo
	now      func() time.Time
	emit     func(workerEvent)
	progress map[*queuedBatch]*batchProgress
	forced   workerSummary
}

func newIngestWorker(q *batchQueue, repo ports.DestAuditRepo, now func() time.Time, emit func(workerEvent)) *ingestWorker {
	return &ingestWorker{q: q, repo: repo, now: now, emit: emit, progress: map[*queuedBatch]*batchProgress{}}
}

func auditHourInRange(hour int64, now time.Time) bool {
	return hour >= now.Add(-48*time.Hour).UnixMilli() && hour <= now.Add(time.Hour).UnixMilli()
}

func (w *ingestWorker) event(b *queuedBatch, outcome string, rows int) {
	if w.emit != nil {
		w.emit(workerEvent{agentID: b.agentID, kind: b.body.Kind, outcome: outcome, panelID: b.panelID, receivedHour: b.receivedHour, rows: rows})
	}
}

func (w *ingestWorker) committedLoss(b *queuedBatch, outcome string, rows int) {
	if w.emit != nil {
		w.emit(workerEvent{agentID: b.agentID, kind: b.body.Kind, outcome: outcome, panelID: b.panelID, receivedHour: b.receivedHour, rows: rows, persistedLoss: true})
	}
}

func (w *ingestWorker) failed(ctx context.Context, b *queuedBatch, rows int) {
	w.event(b, "ingest_error", rows)
	if ctx.Err() != nil {
		w.forced.batches++
		w.forced.rows += rows
	}
	w.finish(b)
}

func (w *ingestWorker) finish(b *queuedBatch) {
	delete(w.progress, b)
	w.q.finish(b)
}

func (w *ingestWorker) remaining(b *queuedBatch) int {
	if state := w.progress[b]; state != nil {
		return state.reserved - state.stored
	}
	return len(b.body.Hits) + len(b.body.Usage)
}

// Setting changes may release an active reservation before its next turn.
// Remove its mapped progress too so off/on cannot resurrect it or leak memory.
func (w *ingestWorker) discardCanceled() bool {
	changed := false
	for b := range w.progress {
		if w.q.isCanceled(b) {
			w.event(b, w.q.cancellationReason(b), w.remaining(b))
			w.finish(b)
			changed = true
		}
	}
	return changed
}

// One call provides one transaction opportunity, never a whole batch.
func (w *ingestWorker) step(ctx context.Context) bool {
	changed := w.discardCanceled()
	b, retired := w.q.nextWithCanceled()
	for _, canceled := range retired {
		w.event(canceled, w.q.cancellationReason(canceled), w.remaining(canceled))
		w.finish(canceled)
		changed = true
	}
	if b == nil {
		return changed
	}
	state := w.progress[b]
	if state == nil {
		w.begin(ctx, b)
		return true
	}
	if !auditHourInRange(b.body.Hour, w.now()) {
		w.event(b, "out_of_range", w.remaining(b))
		w.finish(b)
		return true
	}
	end := min(state.stored+workerChunkRows, state.reserved)
	chunk := domain.DestAuditChunk{AgentID: b.agentID, BatchID: b.body.BatchID, Kind: b.body.Kind, PanelID: b.panelID, CollectRevision: b.body.CollectRevision}
	if b.body.Kind == "usage" {
		chunk.Usage = state.mapped.usage[state.stored:end]
	} else {
		chunk.Hits = state.mapped.hits[state.stored:end]
	}
	// Current mode/revision are checked again under the repository gate inside
	// every transaction, including a setting change during this step.
	rejected, err := w.repo.WriteDestinationAuditChunk(ctx, chunk)
	if err != nil {
		w.failed(ctx, b, w.remaining(b))
		return true
	}
	if rejected != "" {
		w.event(b, boundedRejection(rejected), w.remaining(b))
		w.finish(b)
		return true
	}
	w.event(b, "stored", end-state.stored)
	state.stored = end
	if end == state.reserved {
		w.finish(b)
	}
	return true
}

func (w *ingestWorker) begin(ctx context.Context, b *queuedBatch) {
	now := w.now()
	known := map[int64]bool{}
	// Only existence is needed. Anonymous, counter-only and expired batches
	// never load any account data.
	if b.body.Kind != "trial" && auditHourInRange(b.body.Hour, now) {
		ids := make([]int64, 0, len(b.body.Hits)+len(b.body.Usage))
		for _, hit := range b.body.Hits {
			if id, err := hit.Subject.RowID(); err == nil {
				ids = append(ids, id)
			}
		}
		for _, hit := range b.body.Usage {
			if id, err := hit.Subject.RowID(); err == nil {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		for start := 0; start < len(ids); start += 200 {
			found, err := w.repo.ResolveDestinationAuditUsers(ctx, ids[start:min(start+200, len(ids))])
			if err != nil {
				w.failed(ctx, b, w.remaining(b))
				return
			}
			for id, present := range found {
				if present {
					known[id] = true
				}
			}
		}
	}
	mapped := mapBatch(b.panelID, b.body, known, w.now())
	batch := domain.DestAuditBatch{AgentID: b.agentID, BatchID: b.body.BatchID, Kind: b.body.Kind, PanelID: b.panelID, HourMS: b.body.Hour, ReceivedHourMS: b.receivedHour, ReceivedAt: b.receivedAt, CollectRevision: b.body.CollectRevision, Hits: mapped.hits, Usage: mapped.usage, Dropped: b.body.Dropped, Unmatched: b.body.Unmatched, Losses: mapped.losses}
	result, err := w.repo.BeginDestinationAudit(ctx, batch)
	if err != nil {
		rows := len(mapped.hits) + len(mapped.usage)
		for _, n := range mapped.losses {
			rows += int(n)
		}
		w.failed(ctx, b, rows)
		return
	}
	if result.Duplicate {
		w.event(b, "duplicate_batch", 0)
		w.finish(b)
		return
	}
	if result.Rejected != "" {
		w.event(b, boundedRejection(result.Rejected), len(b.body.Hits)+len(b.body.Usage))
		w.finish(b)
		return
	}
	rows := len(mapped.hits) + len(mapped.usage)
	if result.Reserved < 0 || result.Reserved > rows || result.Stored != min(result.Reserved, workerChunkRows) {
		w.event(b, "ingest_error", rows)
		w.finish(b)
		return
	}
	for _, reason := range []string{"unknown_subject", "out_of_range"} {
		if n := mapped.losses[reason]; n > 0 {
			w.committedLoss(b, reason, int(n))
		}
	}
	if rows > result.Reserved {
		w.committedLoss(b, "over_budget", rows-result.Reserved)
	}
	if w.emit != nil && (b.body.Dropped > 0 || b.body.Unmatched > 0) {
		w.emit(workerEvent{agentID: b.agentID, kind: b.body.Kind, outcome: "node_diagnostics", panelID: b.panelID, receivedHour: b.receivedHour, nodeDropped: b.body.Dropped, nodeUnmatched: b.body.Unmatched})
	}
	if result.Stored > 0 {
		w.event(b, "stored", result.Stored)
	}
	if result.Reserved == result.Stored {
		w.finish(b)
		return
	}
	w.progress[b] = &batchProgress{mapped: mapped, reserved: result.Reserved, stored: result.Stored}
}

func boundedRejection(reason string) string {
	switch reason {
	case "collect_off", "stale_collect_revision":
		return reason
	default:
		return "ingest_error"
	}
}

// Stop offers first and drain using a context independent of normal app
// cancellation. The shutdown deadline may cancel it and reports what remains.
func (w *ingestWorker) run(ctx context.Context) workerSummary {
	for {
		if ctx.Err() != nil {
			return w.abandon()
		}
		if w.step(ctx) {
			continue
		}
		if w.q.isClosed() {
			return workerSummary{}
		}
		select {
		case <-w.q.wake:
		case <-ctx.Done():
			return w.abandon()
		}
	}
}

func (w *ingestWorker) abandon() workerSummary {
	w.q.stop()
	w.discardCanceled()
	summary := w.forced
	w.forced = workerSummary{}
	for {
		b, retired := w.q.nextWithCanceled()
		for _, canceled := range retired {
			w.event(canceled, w.q.cancellationReason(canceled), w.remaining(canceled))
			w.finish(canceled)
		}
		if b == nil {
			break
		}
		rows := w.remaining(b)
		summary.batches++
		summary.rows += rows
		w.event(b, "ingest_error", rows)
		w.finish(b)
	}
	return summary
}
