package auth

import (
	"context"
	"errors"
	"testing"
	"time"
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

// With no durable store the service must behave exactly as it did before the
// table existed: process-local protection, still catching a double submit.
func TestAssertionAlreadyConsumed_NoStoreUsesMemoryCache(t *testing.T) {
	s := &SAMLService{}
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	if s.assertionAlreadyConsumed(context.Background(), "a1", exp, now) {
		t.Fatal("first submission reported as replay")
	}
	if !s.assertionAlreadyConsumed(context.Background(), "a1", exp, now.Add(time.Second)) {
		t.Fatal("second submission not detected without a store")
	}
}

// The durable store is authoritative for IDs this process never saw — the
// restart / second-instance case.
func TestAssertionAlreadyConsumed_StoreCatchesUnseenID(t *testing.T) {
	store := &fakeReplayStore{seen: true}
	s := &SAMLService{}
	s.SetReplayStore(store)
	now := time.Now()

	// Memory cache is empty for this ID, so only the store can reject it.
	if !s.assertionAlreadyConsumed(context.Background(), "never-seen-here", now.Add(5*time.Minute), now) {
		t.Fatal("store-reported replay was not honoured")
	}
	if store.calls != 1 {
		t.Fatalf("store consulted %d times, want 1", store.calls)
	}
}

// A store error must degrade to the in-memory answer — never weaker than the
// pre-existing behaviour, and never a hard lockout of all SSO logins.
func TestAssertionAlreadyConsumed_StoreErrorFallsBackToMemory(t *testing.T) {
	store := &fakeReplayStore{err: errors.New("db down")}
	s := &SAMLService{}
	s.SetReplayStore(store)
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	// First submission still admitted: a DB blip must not lock everyone out.
	if s.assertionAlreadyConsumed(context.Background(), "a2", exp, now) {
		t.Fatal("first submission rejected while the store was erroring")
	}
	// ...but the process-local cache still catches the replay.
	if !s.assertionAlreadyConsumed(context.Background(), "a2", exp, now.Add(time.Second)) {
		t.Fatal("replay slipped through while the store was erroring — fallback is weaker than the old behaviour")
	}
}

// Even when the store reports "not seen" (for example its row was swept), a
// same-process double submit must still be caught by the memory cache.
func TestAssertionAlreadyConsumed_MemoryCatchesWhatStoreMisses(t *testing.T) {
	store := &fakeReplayStore{seen: false}
	s := &SAMLService{}
	s.SetReplayStore(store)
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	if s.assertionAlreadyConsumed(context.Background(), "a3", exp, now) {
		t.Fatal("first submission reported as replay")
	}
	if !s.assertionAlreadyConsumed(context.Background(), "a3", exp, now.Add(time.Second)) {
		t.Fatal("memory cache did not catch a replay the store missed")
	}
}
