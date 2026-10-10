package destpolicy

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type cachedPublicationStore struct {
	PublicationStore
	mu       sync.Mutex
	state    domain.DestPolicyState
	snapshot domain.DestPolicySnapshot
	reads    int
	err      error
}

func (s *cachedPublicationStore) State(context.Context) (domain.DestPolicyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}
func (s *cachedPublicationStore) PublishedState(context.Context) (domain.DestPolicyState, domain.DestPolicySnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.state, s.snapshot, true, s.err
}
func cacheSnapshot(t *testing.T, generation int64) domain.DestPolicySnapshot {
	t.Helper()
	body, err := BuildDefinitionSnapshot(domain.DestDefinitions{State: domain.DestPolicyState{Generation: generation, PublishedGeneration: generation}})
	if err != nil {
		t.Fatal(err)
	}
	return domain.DestPolicySnapshot{Generation: generation, Body: body}
}

func TestPublicationCacheSharesColdLoadsAndRetainsLivePause(t *testing.T) {
	store := &cachedPublicationStore{state: domain.DestPolicyState{Generation: 1, PublishedGeneration: 1}, snapshot: cacheSnapshot(t, 1)}
	c := &Compiler{definitions: store}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			defs, state, err := c.publishedDefinitions(t.Context())
			if err != nil || defs.State.Generation != 1 || state.PublishedGeneration != 1 {
				t.Errorf("concurrent snapshot: defs=%+v state=%+v / %v", defs.State, state, err)
			}
		})
	}
	wg.Wait()
	if store.reads != 1 {
		t.Fatalf("cold loads were not shared: %d", store.reads)
	}
	store.state.Generation, store.state.Paused = 2, true
	defs, state, err := c.publishedDefinitions(t.Context())
	if err != nil || defs.State.Paused || !state.Paused || state.Generation != 2 || store.reads != 1 {
		t.Fatalf("cached body hid live pause: defs=%+v state=%+v reads=%d / %v", defs.State, state, store.reads, err)
	}
}

func TestPublicationCacheDoesNotMaskChangedGenerationReadFailure(t *testing.T) {
	store := &cachedPublicationStore{state: domain.DestPolicyState{Generation: 1, PublishedGeneration: 1}, snapshot: cacheSnapshot(t, 1)}
	c := &Compiler{definitions: store}
	if _, _, err := c.publishedDefinitions(t.Context()); err != nil {
		t.Fatal(err)
	}
	store.state.Generation, store.state.PublishedGeneration = 2, 2
	store.err = domain.ErrUnavailable
	if defs, _, err := c.publishedDefinitions(t.Context()); !errors.Is(err, domain.ErrUnavailable) || defs.State.Generation != 0 {
		t.Fatalf("old snapshot masked new read failure: %+v / %v", defs.State, err)
	}
	store.err = nil
	store.snapshot = cacheSnapshot(t, 2)
	defs, _, err := c.publishedDefinitions(t.Context())
	if err != nil || defs.State.Generation != 2 || store.reads != 3 {
		t.Fatalf("failed load poisoned cache: %+v reads=%d / %v", defs.State, store.reads, err)
	}
}
