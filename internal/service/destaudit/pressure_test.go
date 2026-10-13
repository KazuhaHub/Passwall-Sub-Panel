package destaudit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPressureRetainsActiveCanceledQuotaWithoutMutatingQueue(t *testing.T) {
	q := newBatchQueue()
	s := &Service{q: q}
	body := workerBody("block", 1, 1)
	if q.offer("private-owner", 9, body.Hour, body) != "accepted" {
		t.Fatal("fixture batch rejected")
	}
	wire, _ := json.Marshal(body)
	before := s.Pressure()
	if before[0].Waiting != 1 || before[0].Active != 0 || before[0].Batches != 1 || before[0].Bytes != len(wire) || before[0].Owners != 1 || before[0].MaxOwner != 1 {
		t.Fatal("reservation view missing actual canonical bytes")
	}
	active := q.next()
	q.discardPanel(9)
	view := s.Pressure()
	if view[0].Waiting != 0 || view[0].Active != 1 || view[0].Batches != 1 || view[0].Bytes != len(wire) {
		t.Fatal("canceled processing reservation disappeared before its worker yielded")
	}
	slot := q.slot
	for range 10 {
		if !reflect.DeepEqual(view, s.Pressure()) || q.slot != slot {
			t.Fatal("read advanced or released the queue")
		}
	}
	data, _ := json.Marshal(view)
	for _, private := range []string{"private-owner", "e00000.com", "usr_7", body.BatchID} {
		if strings.Contains(string(data), private) {
			t.Fatal("queue pressure exported private identity")
		}
	}
	view[0].Kind = "changed"
	if s.Pressure()[0].Kind != "block" {
		t.Fatal("pressure view aliases queue state")
	}
	q.finish(active)
	if s.Pressure()[0].Batches != 0 || s.Pressure()[0].Bytes != 0 || s.Pressure()[0].Owners != 0 {
		t.Fatal("finished batch retained a reservation")
	}
}

func TestPressurePreservesFourIndependentOriginalLimitsAndOwnerQuotas(t *testing.T) {
	q := newBatchQueue()
	s := &Service{q: q}
	for i, kind := range []string{"block", "observe", "trial", "usage"} {
		body := queueFixture(kind, i+1)
		if q.offer("first", 9, body.Hour, body) != "accepted" {
			t.Fatal("first owner rejected")
		}
		body.BatchID = queueFixture(kind, i+20).BatchID
		if q.offer("second", 9, body.Hour, body) != "accepted" {
			t.Fatal("second owner rejected")
		}
	}
	var batches, bytes, batchLimits, byteLimits int
	for i, lane := range s.Pressure() {
		if lane.Batches != 2 || lane.Owners != 2 || lane.MaxOwner != 1 || lane.OwnerLimit != []int{4, 1, 1, 1}[i] || lane.BatchLimit != []int{128, 32, 32, 64}[i] || lane.ByteLimit != []int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[i] {
			t.Fatal("lane or owner bound changed")
		}
		batches += lane.Batches
		bytes += lane.Bytes
		batchLimits += lane.BatchLimit
		byteLimits += lane.ByteLimit
	}
	count, size := q.pending()
	if batches != count || bytes != size || batchLimits != 256 || byteLimits != 64<<20 {
		t.Fatal("coherent pressure totals disagree with actual reservations")
	}
}
