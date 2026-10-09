// Package destaudit ingests best-effort native destination telemetry separately
// from the config/roster control path.
package destaudit

import (
	"container/list"
	"encoding/json"
	"slices"
	"sync"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

type batchKey struct{ agentID, batchID string }

type queuedBatch struct {
	agentID      string
	panelID      int64
	receivedHour int64
	receivedAt   time.Time
	body         protocol.AuditObservation
	bytes        int
	canceled     bool // accessed only while the queue mutex is held
	cancelReason string
}

func (b *queuedBatch) key() batchKey { return batchKey{b.agentID, b.body.BatchID} }

type queueLimits struct{ perAgent, batches, bytes, recent int }
type auditLane struct {
	limits         queueLimits
	waiting        []*queuedBatch
	active         *queuedBatch
	agents         map[string]int
	batches, bytes int
	recent         map[batchKey]*list.Element
	order          *list.List
}

type batchQueue struct {
	mu       sync.Mutex
	lanes    map[string]*auditLane
	inflight map[batchKey]*queuedBatch
	slot     int
	closed   bool
	wake     chan struct{}
	admit    func(int64, protocol.AuditObservation) string // immutable; called under mu, memory only
}

var auditSlots = [...]string{"block", "block", "block", "block", "observe", "trial", "usage"}

func newBatchQueue() *batchQueue {
	q := &batchQueue{lanes: map[string]*auditLane{}, inflight: map[batchKey]*queuedBatch{}, wake: make(chan struct{}, 1)}
	for kind, limits := range map[string]queueLimits{
		"block": {4, 128, 32 << 20, 5000}, "observe": {1, 32, 8 << 20, 1000},
		"trial": {1, 32, 8 << 20, 1000}, "usage": {1, 64, 16 << 20, 3000},
	} {
		q.lanes[kind] = &auditLane{limits: limits, agents: map[string]int{}, recent: map[batchKey]*list.Element{}, order: list.New()}
	}
	return q
}

// offer never waits on storage or the collection gate. The short mutex covers
// deduplication and all reservations together; processing retains its quota.
func (q *batchQueue) offer(agentID string, panelID, receivedHour int64, body protocol.AuditObservation) string {
	return q.offerAt(agentID, panelID, receivedHour, time.UnixMilli(receivedHour).UTC(), body)
}

func (q *batchQueue) offerAt(agentID string, panelID, receivedHour int64, receivedAt time.Time, body protocol.AuditObservation) string {
	if agentID == "" || panelID <= 0 || receivedHour <= 0 || receivedHour%protocol.AuditHourMS != 0 || protocol.ValidateAuditObservation(body) != nil {
		return "invalid"
	}
	wire, err := json.Marshal(body)
	if err != nil || len(wire) > protocol.MaxAuditObservationBytes {
		return "invalid"
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return "closed"
	}
	if q.admit != nil {
		if reason := q.admit(panelID, body); reason != "" {
			return reason
		}
	}
	key := batchKey{agentID, body.BatchID}
	if _, found := q.inflight[key]; found {
		return "duplicate_batch"
	}
	// Batch identity excludes Kind, so a mutated retransmission cannot escape
	// either the in-flight set or the four bounded recent caches.
	for _, lane := range q.lanes {
		if item, found := lane.recent[key]; found {
			lane.order.MoveToFront(item)
			return "duplicate_batch"
		}
	}
	lane := q.lanes[body.Kind]
	if lane.batches >= lane.limits.batches || lane.agents[agentID] >= lane.limits.perAgent || len(wire) > lane.limits.bytes-lane.bytes {
		return "queue_full"
	}
	body.Hits = slices.Clone(body.Hits)
	body.Usage = slices.Clone(body.Usage)
	b := &queuedBatch{agentID: agentID, panelID: panelID, receivedHour: receivedHour, receivedAt: receivedAt, body: body, bytes: len(wire)}
	q.inflight[key] = b
	lane.waiting = append(lane.waiting, b)
	lane.batches++
	lane.bytes += b.bytes
	lane.agents[agentID]++
	q.signal()
	return "accepted"
}

func (q *batchQueue) offerReceived(agentID string, panelID int64, receivedAt time.Time, body protocol.AuditObservation) string {
	if receivedAt.IsZero() {
		return "invalid"
	}
	return q.offerAt(agentID, panelID, receivedAt.UTC().Truncate(time.Hour).UnixMilli(), receivedAt.UTC(), body)
}

func (q *batchQueue) isCanceled(b *queuedBatch) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return b.canceled || q.inflight[b.key()] != b
}

func (q *batchQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

func (q *batchQueue) cancellationReason(b *queuedBatch) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if b.cancelReason == "collect_off" {
		return "collect_off"
	}
	return "stale_collect_revision"
}

// next is called by exactly one worker, once per database chunk rather than
// once per batch. Each lane has at most one processing batch. Empty slots
// favour block; otherwise the next occupied slot receives the opportunity.
func (q *batchQueue) next() *queuedBatch {
	b, _ := q.nextWithCanceled()
	return b
}

// Return retired active payloads to the sole worker for loss accounting. No
// secondary queue retains them outside the original bounded reservations.
func (q *batchQueue) nextWithCanceled() (*queuedBatch, []*queuedBatch) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var retired []*queuedBatch
	for _, lane := range q.lanes {
		if lane.active != nil && lane.active.canceled {
			retired = append(retired, lane.active)
			q.release(lane.active)
			lane.active = nil
		}
	}
	available := func(kind string) bool { lane := q.lanes[kind]; return lane.active != nil || len(lane.waiting) > 0 }
	position := q.slot
	q.slot = (q.slot + 1) % len(auditSlots)
	kind := auditSlots[position]
	if !available(kind) {
		kind = ""
		if available("block") {
			kind = "block"
		} else {
			for offset := 1; offset < len(auditSlots); offset++ {
				candidate := auditSlots[(position+offset)%len(auditSlots)]
				if available(candidate) {
					kind = candidate
					break
				}
			}
		}
	}
	if kind == "" {
		return nil, retired
	}
	lane := q.lanes[kind]
	if lane.active == nil {
		lane.active = lane.waiting[0]
		lane.waiting[0] = nil
		lane.waiting = lane.waiting[1:]
	}
	return lane.active, retired
}

func (q *batchQueue) finish(b *queuedBatch) {
	if b == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.inflight[b.key()] != b {
		return
	}
	lane := q.lanes[b.body.Kind]
	if lane.active != b {
		return
	}
	lane.active = nil
	q.release(b)
}

// release requires q.mu. No LRU eviction can ever remove an in-flight key.
func (q *batchQueue) release(b *queuedBatch) {
	lane := q.lanes[b.body.Kind]
	key := b.key()
	delete(q.inflight, key)
	lane.batches--
	lane.bytes -= b.bytes
	lane.agents[b.agentID]--
	if lane.agents[b.agentID] == 0 {
		delete(lane.agents, b.agentID)
	}
	lane.recent[key] = lane.order.PushFront(key)
	for len(lane.recent) > lane.limits.recent {
		last := lane.order.Back()
		delete(lane.recent, last.Value.(batchKey))
		lane.order.Remove(last)
	}
}

func (q *batchQueue) stop() { q.mu.Lock(); q.closed = true; q.signal(); q.mu.Unlock() }
func (q *batchQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *batchQueue) pending() (batches, bytes int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, lane := range q.lanes {
		batches += lane.batches
		bytes += lane.bytes
	}
	return
}

// discardPanel removes waiting data immediately. The gate owner prevents a
// canceled processing batch from writing after a setting update succeeds;
// its reservation remains until the worker yields or finishes that chunk.
func (q *batchQueue) discardPanel(panelID int64) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	waiting, active := q.discardPanelLocked(panelID, "stale_collect_revision")
	return len(waiting) + active
}

// The control cache changes under this same lock, so admission cannot race
// between checking an old revision and reserving its queue slot.
func (q *batchQueue) discardPanelLocked(panelID int64, reason string) (waiting []*queuedBatch, active int) {
	for _, lane := range q.lanes {
		kept := lane.waiting[:0]
		for _, b := range lane.waiting {
			if b.panelID == panelID {
				q.release(b)
				waiting = append(waiting, b)
			} else {
				kept = append(kept, b)
			}
		}
		clear(lane.waiting[len(kept):])
		lane.waiting = kept
		if lane.active != nil && lane.active.panelID == panelID && !lane.active.canceled {
			lane.active.canceled = true
			lane.active.cancelReason = reason
			active++
		}
	}
	q.signal()
	return waiting, active
}
