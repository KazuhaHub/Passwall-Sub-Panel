package sharedclient

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type cleanupOwnership struct {
	ports.OwnershipRepo
	entries []*domain.XUIClientEntry
	err     error
	calls   int
}

func (r *cleanupOwnership) ListByUser(ctx context.Context, _ int64) ([]*domain.XUIClientEntry, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.entries, r.err
}

type cleanupClientRead struct {
	ports.PSPClientRepo
	err   error
	calls int
}

func (r *cleanupClientRead) ListByUser(context.Context, int64) ([]*domain.PSPClient, error) {
	r.calls++
	return nil, r.err
}

func TestDeleteLegacyReadsCurrentOwnershipBeforeReplacementCoverage(t *testing.T) {
	coverageErr := errors.New("current replacement coverage unavailable")
	clients := &cleanupClientRead{err: coverageErr}
	owned := &cleanupOwnership{}
	service := New(clients, nil, nil)
	service.SetOwnershipRepo(owned)
	// Empty ownership needs no replacement/panel read or write. Nil panel and
	// node repositories ensure the no-work path cannot touch those boundaries.
	result, err := service.DeleteLegacyForUser(t.Context(), 7)
	if err != nil || result != (CleanupResult{}) || clients.calls != 0 || owned.calls != 1 {
		t.Fatal("empty legacy ownership unnecessarily read replacement coverage")
	}
	// A later legacy row must not be hidden by a cached empty result. Coverage
	// remains mandatory, and a failed read must never remove the ownership row.
	owned.entries = []*domain.XUIClientEntry{{ID: 1, PanelID: 10, InboundID: 20}}
	result, err = service.DeleteLegacyForUser(t.Context(), 7)
	if !errors.Is(err, coverageErr) || result != (CleanupResult{}) || clients.calls != 1 || owned.calls != 2 {
		t.Fatal("new legacy ownership bypassed current replacement coverage")
	}
	owned.entries = nil
	owned.err = errors.New("legacy ownership unavailable")
	result, err = service.DeleteLegacyForUser(t.Context(), 7)
	if !errors.Is(err, owned.err) || result != (CleanupResult{}) || clients.calls != 1 || owned.calls != 3 {
		t.Fatal("ownership failure was treated as empty cleanup")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err = service.DeleteLegacyForUser(ctx, 7)
	if !errors.Is(err, context.Canceled) || result != (CleanupResult{}) || clients.calls != 1 || owned.calls != 4 {
		t.Fatal("cancelled ownership lookup performed replacement work")
	}
}
