package destaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func (q *batchQueue) setTestLimits(kind string, limits queueLimits) {
	q.lanes[kind].limits = limits // before any concurrent use
}

func queueFixture(kind string, id int) protocol.AuditObservation {
	return protocol.AuditObservation{BatchID: fmt.Sprintf("%032x", id), Kind: kind, Hour: 1_800_000_000_000, CollectRevision: 1, Dropped: 1}
}

func requireOffer(t *testing.T, q *batchQueue, agent string, panel int64, kind string, id int, want string) {
	t.Helper()
	if got := q.offer(agent, panel, 1_800_000_000_000, queueFixture(kind, id)); got != want {
		t.Fatalf("offer %s/%s/%d = %s; want %s", agent, kind, id, got, want)
	}
}

func TestQueueReservesEachKindAndCountsProcessing(t *testing.T) {
	q := newBatchQueue()
	for i := 1; i <= 4; i++ {
		requireOffer(t, q, "a", 1, "block", i, "accepted")
	}
	requireOffer(t, q, "a", 1, "block", 5, "queue_full")
	for i, kind := range []string{"observe", "trial", "usage"} {
		requireOffer(t, q, "a", 1, kind, 10+i, "accepted")
		requireOffer(t, q, "a", 1, kind, 20+i, "queue_full")
	}
	active := q.next()
	if active == nil || active.body.Kind != "block" {
		t.Fatal("block reservation missing")
	}
	requireOffer(t, q, "a", 1, "block", 5, "queue_full")
	if count, _ := q.pending(); count != 7 {
		t.Fatalf("processing escaped quota: %d", count)
	}
	q.finish(active)
	requireOffer(t, q, "a", 1, "block", 5, "accepted")
}

func TestQueueGlobalLimitsCannotBorrowOtherKinds(t *testing.T) {
	q := newBatchQueue()
	for _, tc := range []struct {
		kind  string
		count int
	}{{"block", 128}, {"observe", 32}, {"trial", 32}, {"usage", 64}} {
		for i := 1; i <= tc.count; i++ {
			requireOffer(t, q, fmt.Sprintf("%s-%d", tc.kind, i), 1, tc.kind, i, "accepted")
		}
		requireOffer(t, q, "extra-"+tc.kind, 1, tc.kind, 999, "queue_full")
	}
	if n, _ := q.pending(); n != 256 {
		t.Fatalf("want total reservation256, got%d", n)
	}
}

func TestQueueCanonicalByteQuotaAndRejectedRetry(t *testing.T) {
	q := newBatchQueue()
	b := queueFixture("block", 1)
	wire, _ := json.Marshal(b)
	q.setTestLimits("block", queueLimits{4, 128, len(wire) * 2, 5000})
	requireOffer(t, q, "a", 1, "block", 1, "accepted")
	requireOffer(t, q, "a", 1, "block", 2, "accepted")
	requireOffer(t, q, "b", 1, "block", 3, "queue_full")
	a := q.next()
	if a == nil {
		t.Fatal("missing first batch")
	}
	if n, bytes := q.pending(); n != 2 || bytes != len(wire)*2 {
		t.Fatalf("quota n=%d bytes=%d", n, bytes)
	}
	q.finish(a)
	requireOffer(t, q, "b", 1, "block", 3, "accepted")
}

func TestQueueDedupKeepsInflightOutsideRecentLRU(t *testing.T) {
	q := newBatchQueue()
	q.setTestLimits("block", queueLimits{4, 128, 32 << 20, 2})
	requireOffer(t, q, "pending", 1, "block", 1, "accepted")
	active := q.next()
	if active == nil {
		t.Fatal("no active batch")
	}
	// Churn another lane while the first batch remains processing.
	q.setTestLimits("observe", queueLimits{1, 32, 8 << 20, 2})
	for i := 2; i <= 7; i++ {
		requireOffer(t, q, "churn", 2, "observe", i, "accepted")
		var b *queuedBatch
		for j := 0; j < 7; j++ {
			candidate := q.next()
			if candidate != nil && candidate.body.Kind == "observe" {
				b = candidate
				break
			}
		}
		if b == nil {
			t.Fatal("lower lane starved")
		}
		q.finish(b)
	}
	requireOffer(t, q, "pending", 1, "block", 1, "duplicate_batch")
	// Identity is agent+batch, even if a peer changes Kind on retransmission.
	requireOffer(t, q, "pending", 1, "usage", 1, "duplicate_batch")
	q.finish(active)
	requireOffer(t, q, "pending", 1, "trial", 1, "duplicate_batch")
	requireOffer(t, q, "different-agent", 1, "block", 1, "accepted")
	requireOffer(t, q, "churn", 2, "observe", 2, "accepted") // bounded recent evicted
	requireOffer(t, q, "churn", 2, "observe", 7, "duplicate_batch")
}

func TestQueueYieldsBetweenChunksAndGivesEmptySlotsToBlock(t *testing.T) {
	q := newBatchQueue()
	for i, kind := range []string{"block", "observe", "trial", "usage"} {
		requireOffer(t, q, "a", 1, kind, i+1, "accepted")
	}
	want := []string{"block", "block", "block", "block", "observe", "trial", "usage", "block", "block", "block", "block", "observe", "trial", "usage"}
	var firstBlock *queuedBatch
	for _, kind := range want {
		b := q.next()
		if b == nil || b.body.Kind != kind {
			t.Fatalf("chunk scheduling want%s got%v", kind, b)
		}
		if kind == "block" {
			if firstBlock == nil {
				firstBlock = b
			}
			if firstBlock != b {
				t.Fatal("more than one active batch per lane")
			}
		}
	}
	q.finish(q.next()) // block
	for _, kind := range []string{"observe", "trial", "usage"} {
		for j := 0; j < 7; j++ {
			b := q.next()
			if b != nil && b.body.Kind == kind {
				q.finish(b)
				break
			}
		}
	}
	requireOffer(t, q, "b", 1, "block", 100, "accepted")
	for i := 0; i < 14; i++ {
		b := q.next()
		if b == nil || b.body.Kind != "block" {
			t.Fatal("empty slot did not go to block")
		}
	}
}

func TestQueueOwnsRowsAndStopsWithoutDroppingPending(t *testing.T) {
	q := newBatchQueue()
	b := queueFixture("block", 1)
	b.Dropped = 0
	b.Hits = []protocol.AuditHit{{Hour: b.Hour, RuleID: "p1", Action: "block", Subject: "usr_7", Dest: "example.com", Port: 443, Count: 1, FirstMS: b.Hour + 1, LastMS: b.Hour + 2}}
	if q.offer("a", 1, b.Hour, b) != "accepted" {
		t.Fatal("fixture rejected")
	}
	b.Hits[0].Dest = "mutated.example"
	q.stop()
	requireOffer(t, q, "b", 1, "block", 2, "closed")
	next := q.next()
	if next == nil || next.body.Hits[0].Dest != "example.com" {
		t.Fatal("queued rows borrowed or shutdown discarded them")
	}
	q.finish(next)
	if n, bytes := q.pending(); n != 0 || bytes != 0 {
		t.Fatalf("quota leak after drain %d/%d", n, bytes)
	}
}

func TestQueueConcurrentDuplicateAndFullRejectAreAtomic(t *testing.T) {
	q := newBatchQueue()
	var mu sync.Mutex
	results := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			result := q.offer("a", 1, 1_800_000_000_000, queueFixture("usage", 1))
			mu.Lock()
			results[result]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if results["accepted"] != 1 || results["duplicate_batch"] != 99 {
		t.Fatalf("concurrent duplicate: %v", results)
	}
	q2 := newBatchQueue()
	results = map[string]int{}
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			result := q2.offer("a", 1, 1_800_000_000_000, queueFixture("trial", i+1))
			mu.Lock()
			results[result]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if results["accepted"] != 1 || results["queue_full"] != 99 {
		t.Fatalf("concurrent full: %v", results)
	}
}

func TestQueueDiscardsPanelPendingAndInvalidInput(t *testing.T) {
	q := newBatchQueue()
	requireOffer(t, q, "a", 1, "block", 1, "accepted")
	requireOffer(t, q, "b", 2, "observe", 2, "accepted")
	if n := q.discardPanel(1); n != 1 {
		t.Fatalf("removed%d want1", n)
	}
	if n, _ := q.pending(); n != 1 {
		t.Fatal("panel discard crossed panel boundary")
	}
	requireOffer(t, q, "a", 1, "block", 1, "duplicate_batch")
	invalid := queueFixture("private-kind", 3)
	if q.offer("a", 1, invalid.Hour, invalid) != "invalid" {
		t.Fatal("peer-controlled kind accepted")
	}
	invalid = queueFixture("block", 4)
	invalid.BatchID = strings.Repeat("x", 32)
	if q.offer("a", 1, invalid.Hour, invalid) != "invalid" {
		t.Fatal("malformed identity accepted")
	}
}

func TestQueueDiscardProcessingRemainsBoundedUntilWorkerYields(t *testing.T) {
	q := newBatchQueue()
	requireOffer(t, q, "a", 1, "trial", 1, "accepted")
	active := q.next()
	if active == nil {
		t.Fatal("no processing batch")
	}
	if q.discardPanel(1) != 1 || q.discardPanel(1) != 0 {
		t.Fatal("processing discard counted more than once")
	}
	if n, _ := q.pending(); n != 1 {
		t.Fatal("processing escaped reservation before yielding")
	}
	requireOffer(t, q, "a", 1, "trial", 2, "queue_full")
	if q.next() != nil {
		t.Fatal("canceled processing returned for next chunk")
	}
	q.finish(active) // stale completion must not release twice
	if n, bytes := q.pending(); n != 0 || bytes != 0 {
		t.Fatal("canceled processing reservation leaked")
	}
	requireOffer(t, q, "a", 1, "trial", 1, "duplicate_batch")
	requireOffer(t, q, "a", 1, "trial", 2, "accepted")
}
