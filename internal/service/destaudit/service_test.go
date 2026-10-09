package destaudit

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type collectorStore struct {
	workerStore
	lossTestStore
	seed     []domain.DestAuditControl
	watch    func([]domain.DestAuditControl)
	watchErr error
	flush    func(context.Context, domain.DestAuditLossBatch) error
}

func (r *collectorStore) WatchDestinationAuditControls(_ context.Context, fn func([]domain.DestAuditControl)) error {
	if r.watchErr != nil {
		return r.watchErr
	}
	fn(slices.Clone(r.seed))
	r.watch = fn
	return nil
}
func (r *collectorStore) FlushDestinationAuditLoss(ctx context.Context, b domain.DestAuditLossBatch) error {
	if r.flush != nil {
		return r.flush(ctx, b)
	}
	return r.lossTestStore.FlushDestinationAuditLoss(ctx, b)
}
func collectorFixture(t *testing.T, r *collectorStore) (*Service, *[]Event) {
	t.Helper()
	if r.seed == nil {
		r.seed = []domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectHits, Revision: 1}}
	}
	events := []Event{}
	s, err := New(t.Context(), r, Options{Now: lossTime, Emit: func(e Event) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	return s, &events
}
func collectorRows(events []Event, outcome string) int64 {
	var rows int64
	for _, e := range events {
		if e.Outcome == outcome {
			rows += e.Rows
		}
	}
	return rows
}

func TestCollectorColdSeedPermissionsAndFailClosed(t *testing.T) {
	r := &collectorStore{seed: []domain.DestAuditControl{
		{PanelID: 9, Available: true, Collect: domain.AuditCollectHits, Revision: 1},
		{PanelID: 10, Available: true, Collect: domain.AuditCollectOff, Revision: 2},
		{PanelID: 11, Available: true, Collect: domain.AuditCollectHitsAndUsage, Revision: 3},
	}}
	s, events := collectorFixture(t, r)
	b := workerBody("block", 1, 2)
	s.Offer("a", 9, lossTime(), b)
	s.Offer("b", 10, lossTime(), b)
	s.Offer("c", 99, lossTime(), b)
	b.CollectRevision = 2
	s.Offer("d", 9, lossTime(), b)
	u := workerBody("usage", 2, 1)
	s.Offer("e", 9, lossTime(), u)
	u.CollectRevision = 3
	s.Offer("f", 11, lossTime(), u)
	if n, _ := s.q.pending(); n != 2 || r.watch == nil {
		t.Fatal("current permission was not seeded before admission")
	}
	if collectorRows(*events, "collect_off") != 5 || collectorRows(*events, "stale_collect_revision") != 2 {
		t.Fatalf("rejection rows%v", *events)
	}
	s.StopOffers()
	if got := s.Run(t.Context()); got != (Summary{}) {
		t.Fatalf("drain%+v", got)
	}
	if r.total != 7 || len(r.begins) != 2 {
		t.Fatalf("losses%d writes%d", r.total, len(r.begins))
	}
}

func TestCollectorFailedWarmDoesNotStartOrAdmit(t *testing.T) {
	r := &collectorStore{watchErr: errors.New("seed failed")}
	if s, err := New(t.Context(), r, Options{}); err == nil || s != nil {
		t.Fatal("failed seed produced a collector")
	}
	if len(r.writes) != 0 || len(r.calls) != 0 {
		t.Fatal("constructor started storage worker")
	}
}

func TestCollectorNoOpKeepsQueueAndOffOnNeverResurrects(t *testing.T) {
	r := &collectorStore{}
	s, events := collectorFixture(t, r)
	b := workerBody("block", 1, 3)
	s.Offer("a", 9, lossTime(), b)
	r.watch(slices.Clone(r.seed))
	if n, _ := s.q.pending(); n != 1 {
		t.Fatal("no-op cache notice discarded current data")
	}
	r.watch([]domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectOff, Revision: 2}})
	if n, _ := s.q.pending(); n != 0 || collectorRows(*events, "collect_off") != 3 {
		t.Fatal("off did not discard waiting data before returning")
	}
	r.watch([]domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectHits, Revision: 3}})
	s.Offer("a", 9, lossTime(), b)
	b.BatchID = "00000000000000000000000000000002"
	b.CollectRevision = 3
	s.Offer("a", 9, lossTime(), b)
	s.StopOffers()
	s.Run(t.Context())
	if len(r.begins) != 1 || r.begins[0].CollectRevision != 3 || collectorRows(*events, "stored") != 3 {
		t.Fatal("old generation resurrected after off/on")
	}
}

func TestCollectorChangedControlCancelsActiveAndPreservesOtherPanel(t *testing.T) {
	r := &collectorStore{}
	s, events := collectorFixture(t, r)
	r.watch([]domain.DestAuditControl{{PanelID: 10, Available: true, Collect: domain.AuditCollectHits, Revision: 1}})
	s.Offer("a", 9, lossTime(), workerBody("block", 1, 2501))
	s.Offer("b", 10, lossTime(), workerBody("observe", 2, 2))
	if !s.worker.step(t.Context()) {
		t.Fatal("missing first chunk")
	}
	r.watch([]domain.DestAuditControl{{PanelID: 9}})
	s.StopOffers()
	s.Run(t.Context())
	if len(r.writes) != 2 || collectorRows(*events, "stored") != 1002 || collectorRows(*events, "collect_off") != 1501 || len(s.worker.progress) != 0 {
		t.Fatalf("cancelled active data%v %v", r.writes, *events)
	}
	if r.total != 1501 {
		t.Fatal("cancellation loss omitted")
	}
}

func TestCollectorLossBufferDoesNotCountAlreadyCommittedMappingLossTwice(t *testing.T) {
	r := &collectorStore{workerStore: workerStore{unknown: map[int64]bool{999: true}, limit: 1}}
	s, events := collectorFixture(t, r)
	b := workerBody("block", 1, 3)
	b.Hits[0].Subject = "usr_999"
	s.Offer("a", 9, lossTime(), b)
	s.StopOffers()
	s.Run(t.Context())
	if len(r.begins) != 1 || r.begins[0].Losses["unknown_subject"] != 1 || collectorRows(*events, "unknown_subject") != 1 || collectorRows(*events, "over_budget") != 1 {
		t.Fatal("missing committed mapping losses")
	}
	if r.total != 0 || len(r.calls) != 0 {
		t.Fatal("already committed mapping/budget losses flushed twice")
	}
}

func TestCollectorFirstRollbackRetainsAllEstimatedMappingLoss(t *testing.T) {
	r := &collectorStore{workerStore: workerStore{unknown: map[int64]bool{999: true}, failAt: 1, writeErr: errors.New("private")}}
	s, events := collectorFixture(t, r)
	b := workerBody("block", 1, 3)
	b.Hits[0].Subject = "usr_999"
	s.Offer("a", 9, lossTime(), b)
	s.StopOffers()
	s.Run(t.Context())
	if r.total != 3 || collectorRows(*events, "ingest_error") != 3 {
		t.Fatal("first rollback dropped uncommitted mapping loss")
	}
}

func TestCollectorForcedShutdownReportsPayloadAndUnflushedLossKeys(t *testing.T) {
	r := &collectorStore{}
	s, _ := collectorFixture(t, r)
	s.Offer("a", 9, lossTime(), workerBody("block", 1, 3))
	s.Offer("b", 99, lossTime(), workerBody("observe", 2, 2))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := s.Run(ctx)
	if got != (Summary{Batches: 1, Rows: 3, LossKeys: 2, LossRows: 5}) || len(r.writes) != 0 || len(r.calls) != 0 {
		t.Fatalf("forced summary%+v", got)
	}
	if n, _ := s.q.pending(); n != 0 {
		t.Fatal("forced shutdown retained payload")
	}
}

func TestCollectorBlockedLossWriteDoesNotBlockOfferOrCacheUpdate(t *testing.T) {
	r := &collectorStore{}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.flush = func(ctx context.Context, b domain.DestAuditLossBatch) error {
		once.Do(func() { close(started) })
		select {
		case <-release:
			r.commit(b)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// No event receiver here: all reads occur after the worker joins.
	r.seed = []domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectHits, Revision: 1}}
	s, err := New(t.Context(), r, Options{Now: lossTime})
	if err != nil {
		t.Fatal(err)
	}
	s.Offer("x", 99, lossTime(), workerBody("block", 1, 1))
	done := make(chan Summary, 1)
	go func() { done <- s.Run(t.Context()) }()
	t.Cleanup(func() {
		once.Do(func() {})
		select {
		case <-release:
		default:
			close(release)
		}
		s.StopOffers()
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("loss worker never flushed")
	}
	admitted := make(chan struct{})
	go func() {
		s.Offer("a", 9, lossTime(), workerBody("block", 2, 1))
		r.watch([]domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectOff, Revision: 2}})
		close(admitted)
	}()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("Offer/cache waited for storage lock")
	}
	close(release)
	s.StopOffers()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not finish")
	}
	if len(r.writes) != 0 || r.total != 2 {
		t.Fatal("off allowed blocked pending payload or lost counts")
	}
}

func TestCollectorFullQueueLossUsesReceiptHourAndRetriesSameFrozenBatch(t *testing.T) {
	r := &collectorStore{}
	s, events := collectorFixture(t, r)
	s.Offer("a", 9, lossTime(), workerBody("trial", 1, 1))
	s.Offer("a", 9, lossTime().Add(time.Hour), workerBody("trial", 2, 2))
	if collectorRows(*events, "queue_full") != 2 {
		t.Fatal("queue full not counted in rows")
	}
	fail := true
	r.flush = func(_ context.Context, b domain.DestAuditLossBatch) error {
		r.calls = append(r.calls, b)
		r.commit(b)
		if fail {
			fail = false
			return errors.New("commit acknowledgement lost")
		}
		return nil
	}
	s.StopOffers()
	got := s.Run(t.Context())
	if got != (Summary{}) || len(r.calls) != 2 || !reflect.DeepEqual(r.calls[0], r.calls[1]) || r.calls[0].Losses[0].HourMS != lossTime().Add(time.Hour).Truncate(time.Hour).UnixMilli() || r.total != 2 {
		t.Fatalf("retry accounting%+v calls%v", got, r.calls)
	}
}
