// Package operationgate drains live operations before an in-process backend
// switch. It is deliberately not a distributed lock: online migration requires
// the administrator to confirm that only this PSP instance uses the database.
package operationgate

import (
	"context"
	"errors"

	"golang.org/x/sync/semaphore"
)

const capacity int64 = 1 << 30

type Gate struct{ permits *semaphore.Weighted }
type contextKey struct{}

func New() *Gate { return &Gate{permits: semaphore.NewWeighted(capacity)} }

// Read covers BOTH upstream I/O and the subsequent database writes. Nested
// calls reuse the same admission instead of deadlocking behind a waiting writer.
func (g *Gate) Read(ctx context.Context) (context.Context, func(), error) {
	if g == nil || ctx.Value(contextKey{}) == g {
		return ctx, func() {}, nil
	}
	if err := g.permits.Acquire(ctx, 1); err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, contextKey{}, g), func() { g.permits.Release(1) }, nil
}

func (g *Gate) RunRead(ctx context.Context, fn func(context.Context) error) error {
	ctx, release, err := g.Read(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

func (g *Gate) Exclusive(ctx context.Context, fn func(context.Context) error) error {
	if g == nil {
		return errors.New("online backend coordination is unavailable")
	}
	if ctx.Value(contextKey{}) == g {
		return errors.New("cannot upgrade an admitted operation")
	}
	if err := g.permits.Acquire(ctx, capacity); err != nil {
		return err
	}
	defer g.permits.Release(capacity)
	return fn(ctx)
}
