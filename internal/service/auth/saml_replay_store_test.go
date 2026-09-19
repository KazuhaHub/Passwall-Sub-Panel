package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fakeReplayStore struct {
	seen  bool
	err   error
	calls int
}

func (f *fakeReplayStore) SeenOrAdd(context.Context, string, time.Time, time.Time) (bool, error) {
	f.calls++
	return f.seen, f.err
}

func (f *fakeReplayStore) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

func TestAssertNotConsumed_FirstSightingIsAdmitted(t *testing.T) {
	store := &fakeReplayStore{seen: false}
	s := &SAMLService{}
	s.SetReplayStore(store)

	if err := s.assertNotConsumed(context.Background(), "a1", time.Now().Add(5*time.Minute), time.Now()); err != nil {
		t.Fatalf("first sighting refused: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store consulted %d times, want 1", store.calls)
	}
}

// The durable store is authoritative for IDs this process never saw — the
// restart / second-instance case.
func TestAssertNotConsumed_StoreCatchesUnseenID(t *testing.T) {
	store := &fakeReplayStore{seen: true}
	s := &SAMLService{}
	s.SetReplayStore(store)

	err := s.assertNotConsumed(context.Background(), "never-seen-here", time.Now().Add(5*time.Minute), time.Now())
	if err == nil {
		t.Fatal("store-reported replay was not honoured")
	}
	if !errors.Is(err, ErrSAMLAssertionReplayed) {
		t.Fatalf("a replay should be reported as one, got %v", err)
	}
}

// Supersedes TestAssertionAlreadyConsumed_StoreErrorFallsBackToMemory
// (ADR 0023 item 3). ADR 0036 D1 flips that decision: accepting a login asserts
// the assertion has not been used before, and a process-local answer cannot
// support that assertion across a restart or a second instance — which is the
// whole reason the durable set exists.
//
// The in-process cache is therefore gone from the decision path entirely, so
// this is not a "slower fallback", it is a refusal.
func TestAssertNotConsumed_StoreErrorRefusesInsteadOfFallingBackToMemory(t *testing.T) {
	store := &fakeReplayStore{err: errors.New("db down")}
	s := &SAMLService{}
	s.SetReplayStore(store)

	err := s.assertNotConsumed(context.Background(), "a2", time.Now().Add(5*time.Minute), time.Now())
	if err == nil {
		t.Fatal("a store failure admitted the login; the replay question could not be answered")
	}
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("a store failure must be classifiable as unavailable, got %v", err)
	}
}

// Supersedes TestAssertionAlreadyConsumed_NoStoreUsesMemoryCache and
// TestAssertionAlreadyConsumed_MemoryCatchesWhatStoreMisses (ADR 0023 item 4).
// A missing store is an assembly error, not a silent degradation: with no
// durable set there is no answer that survives a restart, so there is no safe
// answer at all.
func TestAssertNotConsumed_MissingStoreRefuses(t *testing.T) {
	s := &SAMLService{} // deliberately not wired

	err := s.assertNotConsumed(context.Background(), "a3", time.Now().Add(5*time.Minute), time.Now())
	if err == nil {
		t.Fatal("a login was admitted with no durable replay store configured")
	}
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("a missing store must be classifiable as unavailable, got %v", err)
	}
}

// A replay and a storage fault mean opposite things and must stay separable:
// one is an attack to alert on, the other is an outage to fix, and an operator
// seeing "replay" for both would eventually stop trusting the signal.
func TestAssertNotConsumed_ReplayIsDistinctFromStorageFailure(t *testing.T) {
	replay := &SAMLService{}
	replay.SetReplayStore(&fakeReplayStore{seen: true})
	err := replay.assertNotConsumed(context.Background(), "a4", time.Now().Add(5*time.Minute), time.Now())
	if err == nil || errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("a replayed assertion must not look like a storage outage: %v", err)
	}

	outage := &SAMLService{}
	outage.SetReplayStore(&fakeReplayStore{err: errors.New("db down")})
	err = outage.assertNotConsumed(context.Background(), "a4", time.Now().Add(5*time.Minute), time.Now())
	if err == nil || errors.Is(err, ErrSAMLAssertionReplayed) {
		t.Fatalf("a storage outage must not look like a replay: %v", err)
	}
}
