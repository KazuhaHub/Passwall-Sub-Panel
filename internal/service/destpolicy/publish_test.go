package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type publicationStore struct {
	defs                          domain.DestDefinitions
	reads, published, issues      int
	issue                         *domain.DestPublishError
	beforeRead                    func()
	beforePublish                 func()
	publishError, errorWriteError error
	readError                     error
}

func (s *publicationStore) State(context.Context) (domain.DestPolicyState, error) {
	return s.defs.State, nil
}
func (s *publicationStore) ReadDefinitions(context.Context) (domain.DestDefinitions, error) {
	s.reads++
	if s.beforeRead != nil {
		s.beforeRead()
	}
	if s.readError != nil {
		return domain.DestDefinitions{}, s.readError
	}
	return s.defs, nil
}
func (s *publicationStore) Publish(_ context.Context, g, previous int64, body []byte, _ time.Time) error {
	if s.beforePublish != nil {
		s.beforePublish()
	}
	if s.publishError != nil {
		return s.publishError
	}
	if s.defs.State.Generation != g || s.defs.State.PublishedGeneration != previous {
		return domain.ErrConflict
	}
	s.published++
	s.defs.State.PublishedGeneration = g
	s.issue = nil
	return nil
}
func (s *publicationStore) RecordPublishError(_ context.Context, g, previous int64, issue domain.DestPublishError, _ time.Time) error {
	if s.errorWriteError != nil {
		return s.errorWriteError
	}
	if s.defs.State.Generation != g || s.defs.State.PublishedGeneration != previous {
		return domain.ErrConflict
	}
	s.issues++
	s.issue = &issue
	return nil
}
func publicationFixture() (time.Time, *publicationStore, *Publisher) {
	now := time.UnixMilli(1791000000000).UTC()
	last := now.Add(-30 * time.Second)
	first := now.Add(-time.Minute)
	store := &publicationStore{defs: domain.DestDefinitions{State: domain.DestPolicyState{Generation: 3, PublishedGeneration: 2, LastWriteAt: &last, FirstUnpublishedAt: &first}}}
	p := NewPublisher(store, func(d domain.DestDefinitions) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"generation":%d}`, d.State.Generation)), nil
	})
	p.now = func() time.Time { return now }
	return now, store, p
}

func TestPublisherTrailingDebounceAndForce(t *testing.T) {
	now, store, p := publicationFixture()
	if err := p.EnsurePublished(t.Context(), 60, false); err != nil || store.reads != 0 || store.published != 0 {
		t.Fatalf("published during debounce: %+v / %v", store, err)
	}
	last := now.Add(-60 * time.Second)
	store.defs.State.LastWriteAt = &last
	if err := p.EnsurePublished(t.Context(), 60, false); err != nil || store.reads != 1 || store.published != 1 {
		t.Fatalf("trailing edge not published: %+v / %v", store, err)
	}
	if err := p.EnsurePublished(t.Context(), 60, false); err != nil || store.reads != 1 || store.published != 1 {
		t.Fatalf("idle loop reread definitions: %+v / %v", store, err)
	}
	_, store, p = publicationFixture()
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 1 {
		t.Fatalf("force ignored: %+v / %v", store, err)
	}
}

func TestPublisherMaximumWaitSurvivesRecentWrites(t *testing.T) {
	now, store, p := publicationFixture()
	first := now.Add(-300 * time.Second)
	store.defs.State.FirstUnpublishedAt = &first
	if err := p.EnsurePublished(t.Context(), 60, false); err != nil || store.published != 1 {
		t.Fatalf("continuous edits starved publication: %+v / %v", store, err)
	}
}

func TestPublisherRechecksDebounceInsideConsistentRead(t *testing.T) {
	now, store, p := publicationFixture()
	past := now.Add(-90 * time.Second)
	store.defs.State.LastWriteAt = &past
	store.beforeRead = func() { recent := now; store.defs.State.Generation++; store.defs.State.LastWriteAt = &recent }
	if err := p.EnsurePublished(t.Context(), 60, false); err != nil || store.published != 0 {
		t.Fatalf("new write published using old due state: %+v / %v", store, err)
	}
}

func TestPublisherCASConflictKeepsPriorPublicationAndRetriesNextRound(t *testing.T) {
	_, store, p := publicationFixture()
	store.beforePublish = func() { store.defs.State.Generation++; store.beforePublish = nil }
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 0 || store.defs.State.PublishedGeneration != 2 {
		t.Fatalf("stale body published/blocked sync: %+v / %v", store, err)
	}
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 1 || store.defs.State.PublishedGeneration != 4 {
		t.Fatalf("didn't retry fresh generation: %+v / %v", store, err)
	}
}

func TestPublisherInvalidDefinitionRecordsTypedErrorWithoutPublishing(t *testing.T) {
	_, store, p := publicationFixture()
	p.build = func(domain.DestDefinitions) ([]byte, error) {
		return nil, &DefinitionError{Detail: domain.DestPublishError{Kind: "regexps", Used: 257, Limit: 256}}
	}
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 0 || store.issues != 1 || store.issue == nil || store.issue.Kind != "regexps" || store.defs.State.PublishedGeneration != 2 {
		t.Fatalf("invalid candidate published/blocked control: %+v / %v", store, err)
	}
	store.errorWriteError = domain.ErrConflict
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.issues != 1 {
		t.Fatalf("stale issue not discarded: %+v / %v", store, err)
	}
}

func TestPublisherStorageFailureDoesNotBecomeInvalidDefinition(t *testing.T) {
	_, store, p := publicationFixture()
	problem := errors.New("snapshot write failed")
	store.publishError = problem
	if err := p.EnsurePublished(t.Context(), 60, true); !errors.Is(err, problem) || store.issues != 0 {
		t.Fatalf("storage failure blamed on definition: %+v / %v", store, err)
	}
}

func TestPublisherRefusesMissingDependenciesAndInvalidTimes(t *testing.T) {
	if err := NewPublisher(nil, nil).EnsurePublished(t.Context(), 60, true); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("nil dependencies not unavailable: %v", err)
	}
	_, store, p := publicationFixture()
	store.defs.State.LastWriteAt = nil
	if err := p.EnsurePublished(t.Context(), 60, false); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("invalid persisted state treated as empty: %v", err)
	}
}

func TestPublisherDefaultBuilderChecksDefinitionsBeforePublish(t *testing.T) {
	now, store, _ := publicationFixture()
	p := NewPublisher(store, nil)
	p.now = func() time.Time { return now }
	store.defs.Policies = []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}
	store.defs.Policies[0].Inline.Ports = "70000"
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 0 || store.issue == nil || store.issue.Kind != "invalid" || store.issue.Field != "rule.p1" {
		t.Fatalf("invalid definitions published: %+v / %v", store, err)
	}
	store.defs.Policies[0].Inline.Ports = "443"
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil || store.published != 1 || store.issue != nil {
		t.Fatalf("fixed definition not published: %+v / %v", store, err)
	}
}
