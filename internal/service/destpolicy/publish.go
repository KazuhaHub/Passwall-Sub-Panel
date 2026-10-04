package destpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
func (p *Publisher) EnsurePublished(ctx context.Context, minSeconds int, force bool) error {
	if p.store == nil || p.build == nil {
		return domain.ErrUnavailable
	}
	if minSeconds < 30 || minSeconds > 3600 {
		return invalid("policy_apply_min_seconds")
	}
	state, err := p.store.State(ctx)
	if err != nil {
		return err
	}
	now := p.now().UTC()
	due, err := publicationDue(state, time.Duration(minSeconds)*time.Second, now, force)
	if err != nil || !due {
		return err
	}
	defs, err := p.store.ReadDefinitions(ctx)
	if err != nil {
		return err
	}
	// The cheap state read may have become stale. A write between it and the
	// consistent read must get its own debounce window and publication CAS.
	due, err = publicationDue(defs.State, time.Duration(minSeconds)*time.Second, now, force)
	if err != nil || !due {
		return err
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
			return err
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
			return nil
		}
		return err
	}
	err = p.store.Publish(ctx, defs.State.Generation, defs.State.PublishedGeneration, body, now)
	if errors.Is(err, domain.ErrConflict) {
		return nil
	}
	return err
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
