package destaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestAdmissionPressureCapturesEachRejectedCanonicalCandidateBeforeLaterQueueChanges(t *testing.T) {
	for _, kind := range []string{"block", "observe", "trial", "usage"} {
		t.Run(kind, func(t *testing.T) {
			q := newBatchQueue()
			body := workerBody(kind, 1, 1)
			wire, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			// Small test geometry exercises the decision boundary. This is not
			// the separate real-daemon, original-budget byte-pressure gate.
			limits := q.lanes[kind].limits
			limits.bytes = 3*len(wire) - 1
			q.setTestLimits(kind, limits)
			offer := func(owner string, id int) (string, AdmissionPressure) {
				candidate := workerBody(kind, id, 1)
				return q.offerReceivedWithPressure(owner, 9, lossTime(), candidate)
			}
			for i := 1; i <= 2; i++ {
				if outcome, pressure := offer(fmt.Sprintf("private-owner-%d", i), i); outcome != "accepted" || pressure != (AdmissionPressure{}) {
					t.Fatal("accepted admission acquired rejection evidence")
				}
			}
			outcome, witness := offer("private-fresh-owner", 3)
			want := AdmissionPressure{CandidateBytes: len(wire), Batches: 2, Bytes: 2 * len(wire), OwnerBatches: 0, BatchLimit: limits.batches, ByteLimit: limits.bytes, OwnerLimit: limits.perAgent}
			if outcome != "queue_full" || witness != want || witness.Batches >= witness.BatchLimit || witness.CandidateBytes <= witness.ByteLimit-witness.Bytes {
				t.Fatal("byte rejection was not bound to its exact admission state")
			}
			if outcome, pressure := offer("private-owner-1", 1); outcome != "duplicate_batch" || pressure != (AdmissionPressure{}) {
				t.Fatal("duplicate changed precedence or acquired capacity evidence")
			}
			data, err := json.Marshal(witness)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private", "e00000.com", "usr_7", body.BatchID} {
				if strings.Contains(string(data), private) {
					t.Fatal("admission pressure exported private payload or identity")
				}
			}
			active := q.next()
			if active == nil {
				t.Fatal("lost an accepted reservation")
			}
			q.finish(active)
			if count, size := q.pending(); count != 1 || size != len(wire) || witness != want {
				t.Fatal("later queue changes rewrote an earlier admission witness")
			}
			if outcome, pressure := offer("private-fresh-owner", 3); outcome != "accepted" || pressure != (AdmissionPressure{}) {
				t.Fatal("rejected candidate was deduplicated or changed on retry")
			}
		})
	}
}

func TestAdmissionPressureKeepsOwnerBatchAndPermissionRejectionsDistinct(t *testing.T) {
	for _, boundary := range []string{"owner", "batch", "permission", "invalid", "closed"} {
		t.Run(boundary, func(t *testing.T) {
			q := newBatchQueue()
			body := workerBody("trial", 1, 1)
			if q.offerReceived("owner", 9, lossTime(), body) != "accepted" {
				t.Fatal("missing initial reservation")
			}
			owner, want := "owner", "queue_full"
			body.BatchID = queueFixture("trial", 2).BatchID
			switch boundary {
			case "batch":
				limits := q.lanes["trial"].limits
				limits.batches = 1
				q.setTestLimits("trial", limits)
				owner = "fresh"
			case "permission":
				q.admit = func(int64, protocol.AuditObservation) string { return "stale_collect_revision" }
				want = "stale_collect_revision"
			case "invalid":
				body.BatchID = "invalid"
				want = "invalid"
			case "closed":
				q.mu.Lock()
				q.closed = true
				q.mu.Unlock()
				want = "closed"
			}
			outcome, pressure := q.offerReceivedWithPressure(owner, 9, lossTime(), body)
			if outcome != want {
				t.Fatal("admission outcome changed")
			}
			if want != "queue_full" {
				if pressure != (AdmissionPressure{}) {
					t.Fatal("non-capacity rejection claimed a capacity witness")
				}
				return
			}
			if pressure.CandidateBytes <= 0 || pressure.CandidateBytes > pressure.ByteLimit-pressure.Bytes || boundary == "owner" && pressure.OwnerBatches != pressure.OwnerLimit || boundary == "batch" && (pressure.Batches != pressure.BatchLimit || pressure.OwnerBatches != 0) {
				t.Fatal("owner or batch rejection falsely appeared byte-exclusive")
			}
		})
	}
}

func TestCollectorAdmissionPressurePreservesConcurrentDecisionsAndDurableLossRows(t *testing.T) {
	r := &collectorStore{seed: []domain.DestAuditControl{{PanelID: 9, Available: true, Collect: domain.AuditCollectHitsAndUsage, Revision: 1}}}
	events := make(chan Event, 256)
	var s *Service
	service, err := New(t.Context(), r, Options{Now: lossTime, Emit: func(event Event) {
		// The callback must run after releasing the reservation mutex; it can
		// inspect current pressure without changing the saved admission state.
		_ = s.Pressure()
		events <- event
	}})
	if err != nil {
		t.Fatal(err)
	}
	s = service
	body := workerBody("trial", 1, 1)
	wire, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	limits := s.q.lanes["trial"].limits
	limits.bytes = 3*len(wire) + len(wire)/2
	s.q.setTestLimits("trial", limits)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range 100 {
		workers.Go(func() {
			<-start
			s.Offer(fmt.Sprintf("owner-%d", i), 9, lossTime(), workerBody("trial", i+1, 1))
		})
	}
	close(start)
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("admission callback blocked queue readers")
	}
	var accepted, rejected int
	for range 100 {
		event := <-events
		switch event.Outcome {
		case "accepted":
			accepted++
			if event.Admission != (AdmissionPressure{}) {
				t.Fatal("accepted event claimed a rejection witness")
			}
		case "queue_full":
			rejected++
			want := AdmissionPressure{CandidateBytes: len(wire), Batches: 3, Bytes: 3 * len(wire), OwnerBatches: 0, BatchLimit: limits.batches, ByteLimit: limits.bytes, OwnerLimit: limits.perAgent}
			if event.Admission != want || event.PanelID != 9 || event.Kind != "trial" || event.Rows != 1 || event.ReceivedHour != body.Hour {
				t.Fatal("concurrent event changed its admission or loss accounting")
			}
		default:
			t.Fatal("unexpected concurrent admission outcome")
		}
	}
	if accepted != 3 || rejected != 97 {
		t.Fatal("admission witness changed capacity decisions")
	}
	s.StopOffers()
	if summary := s.Run(t.Context()); summary != (Summary{}) || r.total != 97 || len(r.begins) != 3 {
		t.Fatal("rejection witness changed durable loss rows or accepted batch drain")
	}
}
