package destpolicy

import (
	"context"
	"errors"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

type ControlStore interface {
	PublicationStore
	SetPaused(context.Context, bool, time.Time) error
}

type Controls struct {
	store     ControlStore
	publisher *Publisher
	gate      *operationgate.Gate
}

func NewControls(store ControlStore) *Controls {
	return &Controls{store: store, publisher: NewPublisher(store, nil)}
}
func (s *Controls) SetOperationGate(gate *operationgate.Gate) { s.gate = gate }

type PausePublicationError struct {
	Paused bool
	Cause  error
}

func (e *PausePublicationError) Error() string { return "pause saved; publication unavailable" }
func (e *PausePublicationError) Unwrap() error { return e.Cause }

func (s *Controls) Publish(ctx context.Context) (PublicationResult, error) {
	if s == nil || s.store == nil {
		return PublicationResult{}, domain.ErrUnavailable
	}
	ctx, release, err := s.gate.Read(ctx)
	if err != nil {
		return PublicationResult{}, err
	}
	defer release()
	return s.publisher.PublishNow(ctx)
}

func (s *Controls) Pause(ctx context.Context, paused bool) (PublicationResult, error) {
	if s == nil || s.store == nil {
		return PublicationResult{}, domain.ErrUnavailable
	}
	ctx, release, err := s.gate.Read(ctx)
	if err != nil {
		return PublicationResult{}, err
	}
	defer release()
	if err := s.store.SetPaused(ctx, paused, time.Now().UTC()); err != nil {
		return PublicationResult{}, err
	}
	result, err := s.publisher.publishNow(ctx, metrics.DestPublishPause)
	var rejection *PublicationRejection
	if errors.As(err, &rejection) {
		return result, nil
	}
	if err != nil {
		return PublicationResult{}, &PausePublicationError{Paused: paused, Cause: err}
	}
	return result, nil
}
