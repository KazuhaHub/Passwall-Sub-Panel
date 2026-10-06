package destpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

type PublicationStore interface {
	State(context.Context) (domain.DestPolicyState, error)
	ReadDefinitions(context.Context) (domain.DestDefinitions, error)
	Publish(context.Context, int64, int64, []byte, time.Time) error
	RecordPublishError(context.Context, int64, int64, domain.DestPublishError, time.Time) error
}
type SnapshotBuilder func(domain.DestDefinitions) ([]byte, error)
type Publisher struct {
	store PublicationStore
	build SnapshotBuilder
	now   func() time.Time
}

func NewPublisher(store PublicationStore, build SnapshotBuilder) *Publisher {
	if build == nil {
		build = BuildDefinitionSnapshot
	}
	return &Publisher{store: store, build: build, now: time.Now}
}

type PublicationResult struct {
	Generation, PublishedGeneration int64
	Paused                          bool
	PublishError                    *domain.DestPublishError
}

type PublicationRejection struct{ Detail domain.DestPublishError }

func (e *PublicationRejection) Error() string { return (&DefinitionError{Detail: e.Detail}).Error() }
func (e *PublicationRejection) Unwrap() error { return domain.ErrValidation }

type publicationCASConflict struct{ cause error }

func (e *publicationCASConflict) Error() string { return e.cause.Error() }
func (e *publicationCASConflict) Unwrap() error { return e.cause }

func publicationResult(state domain.DestPolicyState) PublicationResult {
	result := PublicationResult{Generation: state.Generation, PublishedGeneration: state.PublishedGeneration, Paused: state.Paused}
	if state.PublishError != nil {
		issue := *state.PublishError
		result.PublishError = &issue
	}
	return result
}

// Runtime publication keeps validation/CAS failures from blocking node control.
// Manual publication instead reports the outcome of the actual committed attempt.
func (p *Publisher) EnsurePublished(ctx context.Context, minSeconds int, force bool) error {
	trigger := ""
	if force {
		trigger = metrics.DestPublishPause
	}
	_, err := p.publishAttempt(ctx, minSeconds, force, trigger)
	var rejection *PublicationRejection
	var conflict *publicationCASConflict
	if errors.As(err, &rejection) || errors.As(err, &conflict) {
		return nil
	}
	return err
}

func (p *Publisher) PublishNow(ctx context.Context) (PublicationResult, error) {
	return p.publishNow(ctx, metrics.DestPublishManual)
}

func (p *Publisher) publishNow(ctx context.Context, trigger string) (PublicationResult, error) {
	// Forced publication does not depend on a debounce-settings read.
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return PublicationResult{}, err
		}
		result, err := p.publishAttempt(ctx, 30, true, trigger)
		var conflict *publicationCASConflict
		if !errors.As(err, &conflict) {
			return result, err
		}
	}
	return PublicationResult{}, domain.ErrConflict
}

func (p *Publisher) publishAttempt(ctx context.Context, minSeconds int, force bool, trigger string) (PublicationResult, error) {
	if p == nil || p.store == nil || p.build == nil {
		return PublicationResult{}, domain.ErrUnavailable
	}
	if minSeconds < 30 || minSeconds > 3600 {
		return PublicationResult{}, invalid("policy_apply_min_seconds")
	}
	state, err := p.store.State(ctx)
	if err != nil {
		return PublicationResult{}, err
	}
	now := p.now().UTC()
	due, err := publicationDue(state, time.Duration(minSeconds)*time.Second, now, force)
	if err != nil || !due {
		return publicationResult(state), err
	}
	defs, err := p.store.ReadDefinitions(ctx)
	if err != nil {
		return PublicationResult{}, err
	}
	// The cheap state read may have become stale. A write between it and the
	// consistent read must get its own debounce window and publication CAS.
	due, err = publicationDue(defs.State, time.Duration(minSeconds)*time.Second, now, force)
	if err != nil || !due {
		return publicationResult(defs.State), err
	}
	body, err := p.build(defs)
	if err == nil {
		body = bytes.TrimSpace(body)
		if !json.Valid(body) || len(body) == 0 || body[0] != '{' {
			err = invalid("snapshot.body")
		}
	}
	if err != nil {
		if !errors.Is(err, domain.ErrValidation) {
			return PublicationResult{}, err
		}
		issue := domain.DestPublishError{Kind: "invalid", Field: "definitions"}
		var typed *DefinitionError
		if errors.As(err, &typed) {
			issue = typed.Detail
		}
		// Invalid definitions retain the prior publication; they must not
		// block the control response or put every node into LKG fallback.
		err = p.store.RecordPublishError(ctx, defs.State.Generation, defs.State.PublishedGeneration, issue, now)
		if errors.Is(err, domain.ErrConflict) {
			return PublicationResult{}, &publicationCASConflict{cause: err}
		}
		if err != nil {
			return PublicationResult{}, err
		}
		result := publicationResult(defs.State)
		result.PublishError = &issue
		metrics.DestPolicyPublishRejectedTotal.Inc()
		return result, &PublicationRejection{Detail: issue}
	}
	err = p.store.Publish(ctx, defs.State.Generation, defs.State.PublishedGeneration, body, now)
	if errors.Is(err, domain.ErrConflict) {
		return PublicationResult{}, &publicationCASConflict{cause: err}
	}
	if err != nil {
		return PublicationResult{}, err
	}
	if trigger == "" {
		trigger = metrics.DestPublishDebounced
		if now.Sub(*defs.State.LastWriteAt) < time.Duration(minSeconds)*time.Second {
			trigger = metrics.DestPublishMaxWait
		}
	}
	metrics.DestPolicyPublishTotal.With(trigger).Inc()
	return PublicationResult{Generation: defs.State.Generation, PublishedGeneration: defs.State.Generation, Paused: defs.State.Paused}, nil
}

func publicationDue(state domain.DestPolicyState, delay time.Duration, now time.Time, force bool) (bool, error) {
	if state.Generation < 0 || state.PublishedGeneration < 0 || state.PublishedGeneration > state.Generation {
		return false, fmt.Errorf("%w: invalid destination publication state", domain.ErrUnavailable)
	}
	if state.Generation == state.PublishedGeneration {
		return false, nil
	}
	if state.LastWriteAt == nil || state.FirstUnpublishedAt == nil {
		return false, fmt.Errorf("%w: missing destination publication times", domain.ErrUnavailable)
	}
	return force || now.Sub(*state.LastWriteAt) >= delay || now.Sub(*state.FirstUnpublishedAt) >= 5*delay, nil
}

type DefinitionError struct{ Detail domain.DestPublishError }

func (e *DefinitionError) Error() string {
	if e.Detail.Kind == "invalid" {
		return "dest_policy_invalid: " + e.Detail.Field
	}
	return "dest_policy_over_limit: " + e.Detail.Kind
}
func (e *DefinitionError) Unwrap() error { return domain.ErrValidation }
