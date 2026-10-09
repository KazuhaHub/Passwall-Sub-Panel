package destpolicy

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func TestManualPublicationReturnsCommittedRejectionWithQuotaDetails(t *testing.T) {
	_, store, p := publicationFixture()
	detail := domain.DestPublishError{Kind: "regexps", Used: 129, Limit: 128}
	p.build = func(domain.DestDefinitions) ([]byte, error) { return nil, &DefinitionError{Detail: detail} }
	result, err := p.PublishNow(t.Context())
	var rejection *PublicationRejection
	if !errors.As(err, &rejection) || rejection.Detail != detail || result.PublishError == nil || *result.PublishError != detail || result.Generation != 3 || result.PublishedGeneration != 2 || store.published != 0 || store.issues != 1 {
		t.Fatal("manual validation failure lost quota details or falsely reported publication")
	}
	if err := p.EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal("runtime validation failure blocked node control")
	}
}

func TestManualPublicationRetriesCASWithFreshDefinitionsAndBoundsContention(t *testing.T) {
	_, store, p := publicationFixture()
	store.beforePublish = func() {
		store.defs.State.Generation++
		store.beforePublish = nil
	}
	result, err := p.PublishNow(t.Context())
	if err != nil || result.PublishedGeneration != 4 || result.Generation != 4 || store.reads != 2 || store.published != 1 {
		t.Fatal("manual CAS retry did not rebuild and commit the current generation")
	}
	_, store, p = publicationFixture()
	store.beforePublish = func() { store.defs.State.Generation++ }
	if _, err := p.PublishNow(t.Context()); !errors.Is(err, domain.ErrConflict) || store.reads != 3 || store.published != 0 || store.defs.State.PublishedGeneration != 2 {
		t.Fatal("manual publication hid or failed to bound repeated CAS contention")
	}
}

func TestManualPublicationDoesNotRetryReadErrorsAsCommitConflicts(t *testing.T) {
	_, store, p := publicationFixture()
	store.readError = domain.ErrConflict
	if _, err := p.PublishNow(t.Context()); !errors.Is(err, domain.ErrConflict) || store.reads != 1 {
		t.Fatal("read failure became a publication CAS retry")
	}
	if err := p.EnsurePublished(t.Context(), 60, true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("runtime publication masked a definition read failure")
	}
}

func TestPausedCompilerDoesNotFabricatePolicyWhenPriorSnapshotIsCorrupt(t *testing.T) {
	c, _, _, agent, snapshot, base := cachedCompilerFixture(t)
	store := c.definitions.(*cachedPublicationStore)
	store.state.Generation, store.state.Paused = 2, true
	store.snapshot.Body = []byte(`{"schema":9,"generation":1}`)
	candidate, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if !errors.Is(err, domain.ErrUnavailable) || candidate.Policy != nil || candidate.Mint.Kind != "" {
		t.Fatal("saved pause turned a corrupt prior snapshot into a fabricated candidate")
	}
}

func TestPublicationMetricsCountOnlyWinningCommitsAndRecordedRejections(t *testing.T) {
	manual := metrics.DestPolicyPublishTotal.With(metrics.DestPublishManual)
	before := manual.Value()
	_, store, p := publicationFixture()
	store.beforePublish = func() { store.defs.State.Generation++; store.beforePublish = nil }
	if _, err := p.PublishNow(t.Context()); err != nil || manual.Value() != before+1 {
		t.Fatal("a lost publication CAS counted as a committed manual publication")
	}
	if _, err := p.PublishNow(t.Context()); err != nil || manual.Value() != before+1 {
		t.Fatal("an unchanged manual request counted another publication")
	}
	_, store, p = publicationFixture()
	p.build = func(domain.DestDefinitions) ([]byte, error) {
		return nil, &DefinitionError{Detail: domain.DestPublishError{Kind: "rules", Used: 8193, Limit: 8192}}
	}
	rejected := metrics.DestPolicyPublishRejectedTotal.Value()
	store.errorWriteError = domain.ErrConflict
	if _, err := p.PublishNow(t.Context()); !errors.Is(err, domain.ErrConflict) || metrics.DestPolicyPublishRejectedTotal.Value() != rejected {
		t.Fatal("stale validation errors counted as current publication rejections")
	}
	store.errorWriteError = nil
	if _, err := p.PublishNow(t.Context()); err == nil || metrics.DestPolicyPublishRejectedTotal.Value() != rejected+1 || manual.Value() != before+1 {
		t.Fatal("committed validation failure was counted as successful publication")
	}
	for _, tc := range []struct {
		trigger string
		delay   time.Duration
	}{{metrics.DestPublishDebounced, time.Minute}, {metrics.DestPublishMaxWait, 0}} {
		now, s, publisher := publicationFixture()
		last, first := now.Add(-tc.delay), now.Add(-6*time.Minute)
		s.defs.State.LastWriteAt, s.defs.State.FirstUnpublishedAt = &last, &first
		counter := metrics.DestPolicyPublishTotal.With(tc.trigger)
		previous := counter.Value()
		if err := publisher.EnsurePublished(t.Context(), 60, false); err != nil || counter.Value() != previous+1 {
			t.Fatal("runtime publication used the wrong bounded trigger")
		}
	}
}
