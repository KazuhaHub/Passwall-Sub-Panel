package destaudit

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type lossTestStore struct {
	mu        sync.Mutex
	calls     []domain.DestAuditLossBatch
	committed map[string]bool
	total     int64
	write     func(domain.DestAuditLossBatch) error
}

func (s *lossTestStore) FlushDestinationAuditLoss(_ context.Context, batch domain.DestAuditLossBatch) error {
	s.mu.Lock()
	copy := batch
	copy.Losses = slices.Clone(batch.Losses)
	s.calls = append(s.calls, copy)
	s.mu.Unlock()
	if s.write != nil {
		if err := s.write(batch); err != nil {
			return err
		}
	}
	s.commit(copy)
	return nil
}
func (s *lossTestStore) commit(batch domain.DestAuditLossBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed == nil {
		s.committed = map[string]bool{}
	}
	if s.committed[batch.BatchID] {
		return
	}
	s.committed[batch.BatchID] = true
	for _, row := range batch.Losses {
		s.total += row.Rows
	}
}
func lossFixture(panel, rows int64) domain.DestAuditLoss {
	return domain.DestAuditLoss{PanelID: panel, HourMS: 1_800_000_000_000, Kind: "block", Reason: "queue_full", Rows: rows}
}
func lossTime() time.Time { return time.UnixMilli(1_800_000_000_010).UTC() }

func TestLossBufferAmbiguousCommitRetriesFrozenIncrementAndNewCountsSeparately(t *testing.T) {
	b := newLossBuffer()
	if !b.add(lossFixture(9, 7)) {
		t.Fatal("initial loss rejected")
	}
	s := &lossTestStore{}
	s.write = func(batch domain.DestAuditLossBatch) error {
		s.commit(batch)
		return errors.New("reply failed after commit")
	}
	if out, err := b.flush(t.Context(), s, lossTime()); err == nil || out.keys != 1 || out.rows != 7 || out.discarded {
		t.Fatalf("failed flush%+v %v", out, err)
	}
	if !b.add(lossFixture(9, 3)) {
		t.Fatal("new count for frozen key rejected")
	}
	s.write = nil
	if out, err := b.flush(t.Context(), s, lossTime().Add(time.Hour)); err != nil || out.rows != 7 {
		t.Fatalf("retry%+v %v", out, err)
	}
	if !reflect.DeepEqual(s.calls[0], s.calls[1]) {
		t.Fatal("failed batch identity, receipt time or increment changed")
	}
	if out, err := b.flush(t.Context(), s, lossTime().Add(2*time.Hour)); err != nil || out.rows != 3 {
		t.Fatalf("new increment%+v %v", out, err)
	}
	if s.calls[2].BatchID == s.calls[0].BatchID || s.total != 10 {
		t.Fatal("ambiguous commit duplicated counts")
	}
	if out, err := b.flush(t.Context(), s, lossTime()); err != nil || out.keys != 0 || len(s.calls) != 3 {
		t.Fatal("empty flush called storage")
	}
}

func TestLossBufferCapsAllPendingAndFrozenKeysAndFlushes200AtATime(t *testing.T) {
	b := newLossBuffer()
	for id := int64(1); id <= 10000; id++ {
		if !b.add(lossFixture(id, 1)) {
			t.Fatalf("capacity rejected%d", id)
		}
	}
	if b.add(lossFixture(10001, 1)) {
		t.Fatal("buffer exceeded 10000 keys")
	}
	s := &lossTestStore{write: func(domain.DestAuditLossBatch) error { return errors.New("storage unavailable") }}
	if out, err := b.flush(t.Context(), s, lossTime()); err == nil || out.keys != 200 || out.rows != 200 {
		t.Fatalf("bounded freeze%+v %v", out, err)
	}
	if b.add(lossFixture(10001, 1)) {
		t.Fatal("frozen keys no longer counted against capacity")
	}
	if !b.add(lossFixture(1, 4)) {
		t.Fatal("pending count for existing frozen key rejected")
	}
	s.write = nil
	if _, err := b.flush(t.Context(), s, lossTime()); err != nil {
		t.Fatal(err)
	}
	for id := int64(10001); id <= 10200; id++ {
		want := id < 10200 // key1 remains queued, so only199 slots were released.
		if got := b.add(lossFixture(id, 1)); got != want {
			t.Fatalf("capacity after ACK%d got%v", id, got)
		}
	}
	for i := 0; i < 60; i++ {
		out, err := b.flush(t.Context(), s, lossTime())
		if err != nil || out.keys > 200 {
			t.Fatalf("flush%+v %v", out, err)
		}
		if out.keys == 0 {
			break
		}
		if i == 59 {
			t.Fatal("buffer never drained")
		}
	}
	if s.total != 10203 {
		t.Fatalf("retained counts%d", s.total)
	}
}

func TestLossBufferAddsNeverWaitForDatabaseAndFlushesAreSerialized(t *testing.T) {
	b := newLossBuffer()
	if !b.add(lossFixture(1, 7)) {
		t.Fatal("initial loss rejected")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s := &lossTestStore{write: func(domain.DestAuditLossBatch) error {
		once.Do(func() { close(entered); <-release })
		return nil
	}}
	done := make(chan error, 2)
	go func() { _, err := b.flush(t.Context(), s, lossTime()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("flush did not enter storage")
	}
	added := make(chan bool, 1)
	go func() { added <- b.add(lossFixture(1, 5)) }()
	select {
	case ok := <-added:
		if !ok {
			t.Fatal("concurrent add rejected")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("add waited on database")
	}
	go func() { _, err := b.flush(t.Context(), s, lossTime()); done <- err }()
	close(release)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("serialized flush stuck")
		}
	}
	if s.total != 12 || len(s.calls) != 2 || s.calls[0].BatchID == s.calls[1].BatchID {
		t.Fatal("concurrent flush duplicated frozen increment")
	}
}

func TestLossBufferExpiryAndShutdownDiscardOnlyTheUnconfirmedIncrement(t *testing.T) {
	b := newLossBuffer()
	if !b.add(lossFixture(1, 7)) {
		t.Fatal("initial loss rejected")
	}
	s := &lossTestStore{write: func(domain.DestAuditLossBatch) error { return errors.New("storage down") }}
	if _, err := b.flush(t.Context(), s, lossTime()); err == nil {
		t.Fatal("fault not returned")
	}
	if !b.add(lossFixture(1, 5)) || !b.add(lossFixture(2, 11)) {
		t.Fatal("new losses rejected")
	}
	s.write = func(domain.DestAuditLossBatch) error { return domain.ErrDestAuditLossExpired }
	if out, err := b.flush(t.Context(), s, lossTime().Add(49*time.Hour)); !errors.Is(err, domain.ErrDestAuditLossExpired) || !out.discarded || out.keys != 1 || out.rows != 7 {
		t.Fatalf("expired increment%+v %v", out, err)
	}
	s.write = nil
	if out, err := b.flush(t.Context(), s, lossTime().Add(49*time.Hour)); err != nil || out.rows != 16 || out.keys != 2 {
		t.Fatalf("expiry lost new counts%+v %v", out, err)
	}
	if !b.add(lossFixture(1, 3)) || !b.add(lossFixture(2, 4)) {
		t.Fatal("shutdown losses rejected")
	}
	s.write = func(domain.DestAuditLossBatch) error { return errors.New("shutdown fault") }
	if _, err := b.flush(t.Context(), s, lossTime()); err == nil {
		t.Fatal("shutdown fault missing")
	}
	if !b.add(lossFixture(1, 5)) {
		t.Fatal("new shutdown count rejected")
	}
	if out := b.discard(); !out.discarded || out.keys != 2 || out.rows != 12 {
		t.Fatalf("undrained losses%+v", out)
	}
	if out := b.discard(); out.keys != 0 || out.rows != 0 {
		t.Fatal("discard counted twice")
	}
}

func TestLossBufferDefendsFrozenPayloadAndSaturatesRows(t *testing.T) {
	b := newLossBuffer()
	for _, bad := range []domain.DestAuditLoss{{}, {PanelID: 1, HourMS: lossFixture(1, 1).HourMS, Kind: "block", Reason: "node_dropped", Rows: 1}, {PanelID: 1, HourMS: lossFixture(1, 1).HourMS + 1, Kind: "block", Reason: "queue_full", Rows: 1}} {
		if b.add(bad) {
			t.Fatal("invalid loss accepted")
		}
	}
	if !b.add(lossFixture(1, math.MaxInt64)) || !b.add(lossFixture(1, 9)) {
		t.Fatal("saturated loss rejected")
	}
	s := &lossTestStore{write: func(batch domain.DestAuditLossBatch) error {
		batch.Losses[0].Rows = 2
		return errors.New("mutating adapter failure")
	}}
	if out, err := b.flush(t.Context(), s, lossTime()); err == nil || out.rows != math.MaxInt64 {
		t.Fatalf("saturation%+v %v", out, err)
	}
	s.write = nil
	if out, err := b.flush(t.Context(), s, lossTime()); err != nil || out.rows != math.MaxInt64 || s.calls[1].Losses[0].Rows != math.MaxInt64 {
		t.Fatal("adapter mutated frozen retry")
	}
}
