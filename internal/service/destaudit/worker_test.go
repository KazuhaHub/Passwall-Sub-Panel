package destaudit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type workerWrite struct {
	kind string
	rows int
}
type workerStore struct {
	writes             []workerWrite
	begins             []domain.DestAuditBatch
	chunks             []domain.DestAuditChunk
	queries            [][]int64
	unknown            map[int64]bool
	queryErr, writeErr error
	failAt, limit      int
	rejected           string
	duplicate          bool
	afterBegin         func()
}

func (s *workerStore) ResolveDestinationAuditUsers(_ context.Context, ids []int64) (map[int64]bool, error) {
	s.queries = append(s.queries, slices.Clone(ids))
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	out := map[int64]bool{}
	for _, id := range ids {
		if !s.unknown[id] {
			out[id] = true
		}
	}
	return out, nil
}
func (s *workerStore) BeginDestinationAudit(_ context.Context, b domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	s.begins = append(s.begins, b)
	rows := len(b.Hits) + len(b.Usage)
	reserved := rows
	if s.limit > 0 {
		reserved = min(rows, s.limit)
	}
	stored := min(reserved, 1000)
	s.writes = append(s.writes, workerWrite{b.Kind, stored})
	if s.failAt == len(s.writes) {
		return domain.DestAuditBegin{}, s.writeErr
	}
	if s.afterBegin != nil {
		s.afterBegin()
	}
	return domain.DestAuditBegin{Duplicate: s.duplicate, Rejected: s.rejected, Reserved: reserved, Stored: stored}, nil
}
func (s *workerStore) WriteDestinationAuditChunk(_ context.Context, b domain.DestAuditChunk) (string, error) {
	s.chunks = append(s.chunks, b)
	s.writes = append(s.writes, workerWrite{b.Kind, len(b.Hits) + len(b.Usage)})
	if s.failAt == len(s.writes) {
		return "", s.writeErr
	}
	return s.rejected, nil
}

func workerBody(kind string, id, rows int) protocol.AuditObservation {
	b := queueFixture(kind, id)
	for i := 0; i < rows; i++ {
		dest := fmt.Sprintf("e%05d.com", i)
		if kind == "usage" {
			b.Usage = append(b.Usage, protocol.AuditUsage{Hour: b.Hour, Subject: "usr_7", Site: dest, Count: 1})
		} else {
			action, subject, port, rule := "block", "usr_7", uint16(443), "p12"
			if kind != "block" {
				action = "observe"
			}
			if kind == "trial" {
				subject = ""
				port = 0
				rule = "g12"
			}
			b.Hits = append(b.Hits, mappingHit(rule, action, subject, dest, port, 1, 10))
		}
	}
	return b
}
func workerFixture(t *testing.T, s *workerStore) (*ingestWorker, *[]workerEvent, *time.Time) {
	t.Helper()
	now := time.UnixMilli(1_800_000_000_000 + 123456).UTC()
	events := []workerEvent{}
	w := newIngestWorker(newBatchQueue(), s, func() time.Time { return now }, func(e workerEvent) { events = append(events, e) })
	return w, &events, &now
}
func offerWorker(t *testing.T, w *ingestWorker, body protocol.AuditObservation) {
	t.Helper()
	if err := protocol.ValidateAuditObservation(body); err != nil {
		t.Fatal(err)
	}
	if got := w.q.offer("agent", 9, body.Hour, body); got != "accepted" {
		t.Fatalf("offer %s", got)
	}
}
func drainWorker(t *testing.T, w *ingestWorker) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if !w.step(t.Context()) {
			return
		}
	}
	t.Fatal("worker did not finish bounded fixture")
}
func requireEmptyWorker(t *testing.T, w *ingestWorker) {
	t.Helper()
	if n, b := w.q.pending(); n != 0 || b != 0 {
		t.Fatalf("worker leaked quota batches%d bytes%d", n, b)
	}
}
func eventRows(events []workerEvent, outcome string) int {
	rows := 0
	for _, e := range events {
		if e.outcome == outcome {
			rows += e.rows
		}
	}
	return rows
}

func TestWorkerSchedulesEveryChunkAndPreservesFourLanes(t *testing.T) {
	s := &workerStore{}
	w, events, _ := workerFixture(t, s)
	for i, kind := range []string{"block", "observe", "trial", "usage"} {
		offerWorker(t, w, workerBody(kind, i+1, 4001))
	}
	for i := 0; i < 7; i++ {
		if !w.step(t.Context()) {
			t.Fatal("missing chunk")
		}
	}
	want := []workerWrite{{"block", 1000}, {"block", 1000}, {"block", 1000}, {"block", 1000}, {"observe", 1000}, {"trial", 1000}, {"usage", 1000}}
	if !reflect.DeepEqual(s.writes, want) {
		t.Fatalf("chunk schedule%v", s.writes)
	}
	drainWorker(t, w)
	requireEmptyWorker(t, w)
	if eventRows(*events, "stored") != 16004 {
		t.Fatalf("stored rows%v", *events)
	}
	for _, chunk := range s.chunks {
		if len(chunk.Hits)+len(chunk.Usage) > 1000 {
			t.Fatal("oversized transaction")
		}
	}
	if len(s.queries) != 3 {
		t.Fatalf("queries%d; trial must not query accounts", len(s.queries))
	}
}

func TestWorkerMergesBeforeBudgetAndKeepsLossUnits(t *testing.T) {
	s := &workerStore{unknown: map[int64]bool{999: true}}
	w, events, _ := workerFixture(t, s)
	b := workerBody("block", 1, 0)
	b.Dropped = 3
	b.Unmatched = 4
	b.Hits = []protocol.AuditHit{mappingHit("p12x1", "block", "usr_7", "example.com", 443, 3, 10), mappingHit("p12x2", "block", "usr_7", "example.com", 443, 4, 20), mappingHit("p12", "block", "usr_999", "example.com", 443, 999, 30)}
	offerWorker(t, w, b)
	drainWorker(t, w)
	if len(s.begins) != 1 {
		t.Fatal("missing first transaction")
	}
	got := s.begins[0]
	if len(got.Hits) != 1 || got.Hits[0].Count != 7 || got.Hits[0].Source != "p12" || got.Losses["unknown_subject"] != 1 || got.Dropped != 3 || got.Unmatched != 4 {
		t.Fatalf("mapped transaction%+v", got)
	}
	if eventRows(*events, "unknown_subject") != 1 || eventRows(*events, "stored") != 1 {
		t.Fatal("rows confused with events")
	}
	requireEmptyWorker(t, w)
}

func TestWorkerKeepsExactFirstReceiptAndFrozenBudgetHour(t *testing.T) {
	s := &workerStore{}
	w, _, now := workerFixture(t, s)
	if got := w.q.offerReceived("agent", 9, *now, workerBody("block", 1, 1001)); got != "accepted" {
		t.Fatal(got)
	}
	received := *now
	*now = now.Add(2 * time.Hour)
	drainWorker(t, w)
	if len(s.begins) != 1 || !s.begins[0].ReceivedAt.Equal(received) || s.begins[0].ReceivedHourMS != received.UTC().Truncate(time.Hour).UnixMilli() {
		t.Fatal("first receipt replaced with processing clock or hour floor")
	}
}

func TestWorkerResolvesSortedDistinctIDsInBoundedQueries(t *testing.T) {
	s := &workerStore{}
	w, _, _ := workerFixture(t, s)
	b := workerBody("observe", 1, 451)
	for i := range b.Hits {
		b.Hits[i].Subject = protocol.SubjectKey(fmt.Sprintf("usr_%d", 450-i%450))
	}
	offerWorker(t, w, b)
	drainWorker(t, w)
	if len(s.queries) != 3 || len(s.queries[0]) != 200 || len(s.queries[1]) != 200 || len(s.queries[2]) != 50 {
		t.Fatalf("queries%v", s.queries)
	}
	var ids []int64
	for _, query := range s.queries {
		ids = append(ids, query...)
	}
	if len(ids) != 450 || ids[0] != 1 || ids[449] != 450 || !slices.IsSorted(ids) {
		t.Fatal("unbounded, duplicate or unsorted IDs")
	}
}

func TestWorkerFailureStopsRemainingChunksAndReleasesQuota(t *testing.T) {
	for _, fail := range []int{1, 2} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s := &workerStore{failAt: fail, writeErr: errors.New("private destination and account must never escape")}
			w, events, _ := workerFixture(t, s)
			b := workerBody("block", 1, 2501)
			offerWorker(t, w, b)
			drainWorker(t, w)
			if len(s.writes) != fail || eventRows(*events, "ingest_error") != 2501-(fail-1)*1000 {
				t.Fatalf("failure scope writes%v events%v", s.writes, *events)
			}
			requireEmptyWorker(t, w)
			if got := w.q.offer("agent", 9, b.Hour, b); got != "duplicate_batch" {
				t.Fatal("failed completed batch retried from recent cache")
			}
		})
	}
}

func TestWorkerQueryFailureDoesNotWriteOrExposeError(t *testing.T) {
	s := &workerStore{queryErr: errors.New("secret.sql.raw-value")}
	w, events, _ := workerFixture(t, s)
	offerWorker(t, w, workerBody("block", 1, 5))
	drainWorker(t, w)
	if len(s.writes) != 0 || eventRows(*events, "ingest_error") != 5 {
		t.Fatalf("query failure writes%v events%v", s.writes, *events)
	}
	requireEmptyWorker(t, w)
}

func TestWorkerBudgetOnlyWritesReservedRows(t *testing.T) {
	s := &workerStore{limit: 1501}
	w, events, _ := workerFixture(t, s)
	offerWorker(t, w, workerBody("block", 1, 2501))
	drainWorker(t, w)
	if !reflect.DeepEqual(s.writes, []workerWrite{{"block", 1000}, {"block", 501}}) || eventRows(*events, "stored") != 1501 || eventRows(*events, "over_budget") != 1000 {
		t.Fatalf("budget writes%v events%v", s.writes, *events)
	}
	requireEmptyWorker(t, w)
}

func TestWorkerAgeBeforeMappingAndBetweenChunks(t *testing.T) {
	t.Run("already_expired", func(t *testing.T) {
		s := &workerStore{}
		w, events, now := workerFixture(t, s)
		*now = now.Add(49 * time.Hour)
		offerWorker(t, w, workerBody("block", 1, 2501))
		drainWorker(t, w)
		if len(s.queries) != 0 || len(s.begins) != 1 || len(s.begins[0].Hits) != 0 || s.begins[0].Losses["out_of_range"] != 2501 || eventRows(*events, "out_of_range") != 2501 {
			t.Fatalf("expired handling begins%d queries%d events%v", len(s.begins), len(s.queries), *events)
		}
		requireEmptyWorker(t, w)
	})
	t.Run("expires_after_first", func(t *testing.T) {
		s := &workerStore{}
		w, events, now := workerFixture(t, s)
		s.afterBegin = func() { *now = now.Add(49 * time.Hour) }
		offerWorker(t, w, workerBody("block", 1, 2501))
		drainWorker(t, w)
		if len(s.writes) != 1 || eventRows(*events, "stored") != 1000 || eventRows(*events, "out_of_range") != 1501 {
			t.Fatalf("late expiry%v %v", s.writes, *events)
		}
		requireEmptyWorker(t, w)
	})
}

func TestWorkerReplayOrCurrentPermissionRejectsWholeBatch(t *testing.T) {
	for _, reason := range []string{"duplicate_batch", "collect_off", "stale_collect_revision"} {
		t.Run(reason, func(t *testing.T) {
			s := &workerStore{rejected: reason, duplicate: reason == "duplicate_batch"}
			w, events, _ := workerFixture(t, s)
			offerWorker(t, w, workerBody("block", 1, 2501))
			drainWorker(t, w)
			if len(s.chunks) != 0 || eventRows(*events, "stored") != 0 {
				t.Fatal("replay or forbidden chunk written")
			}
			if reason != "duplicate_batch" && eventRows(*events, reason) != 2501 {
				t.Fatalf("rejected scope%v", *events)
			}
			if reason == "duplicate_batch" && (len(*events) != 1 || (*events)[0].outcome != reason || (*events)[0].rows != 0) {
				t.Fatalf("replay double counted%v", *events)
			}
			requireEmptyWorker(t, w)
		})
	}
}

func TestWorkerCancellationCannotRetainMappedProgress(t *testing.T) {
	s := &workerStore{}
	w, events, _ := workerFixture(t, s)
	s.afterBegin = func() { w.q.discardPanel(9) }
	offerWorker(t, w, workerBody("block", 1, 2501))
	drainWorker(t, w)
	if len(s.writes) != 1 || eventRows(*events, "stale_collect_revision") != 1501 {
		t.Fatalf("cancelled progress%v %v", s.writes, *events)
	}
	requireEmptyWorker(t, w)
	if w.step(t.Context()) {
		t.Fatal("cancelled progress resurrected")
	}
}

func TestWorkerStopsOffersThenDrainsAndCountsForcedTermination(t *testing.T) {
	t.Run("drain", func(t *testing.T) {
		s := &workerStore{}
		w, events, _ := workerFixture(t, s)
		offerWorker(t, w, workerBody("block", 1, 2501))
		w.q.stop()
		if got := w.q.offer("agent", 9, 1_800_000_000_000, workerBody("observe", 2, 1)); got != "closed" {
			t.Fatal("shutdown admitted batch")
		}
		if got := w.run(t.Context()); got.batches != 0 || got.rows != 0 {
			t.Fatal("normal drain reported loss")
		}
		if eventRows(*events, "stored") != 2501 {
			t.Fatal("shutdown did not drain")
		}
		requireEmptyWorker(t, w)
	})
	t.Run("forced", func(t *testing.T) {
		s := &workerStore{}
		w, events, _ := workerFixture(t, s)
		offerWorker(t, w, workerBody("block", 1, 3))
		offerWorker(t, w, workerBody("observe", 2, 2))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if got := w.run(ctx); got.batches != 2 || got.rows != 5 {
			t.Fatalf("undrained summary%+v", got)
		}
		if len(s.writes) != 0 || eventRows(*events, "ingest_error") != 5 {
			t.Fatal("forced termination lost accounting")
		}
		requireEmptyWorker(t, w)
	})
}

func TestWorkerWakesForNewBatchAndStopsWhenQueueDrained(t *testing.T) {
	s := &workerStore{}
	w, _, _ := workerFixture(t, s)
	done := make(chan workerSummary, 1)
	go func() { done <- w.run(t.Context()) }()
	offerWorker(t, w, workerBody("block", 1, 3))
	w.q.stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker missed wake or shutdown")
	}
	if len(s.writes) != 1 {
		t.Fatal("worker did not ingest queued data")
	}
	requireEmptyWorker(t, w)
}
