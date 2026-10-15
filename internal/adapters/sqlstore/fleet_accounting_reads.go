package sqlstore

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Only callers admitted before a SQL read starts may share that read. Callers
// arriving during it join the next batch, whose SQL starts after their calls.
// This preserves current-read ordering across writes without retaining results,
// adding a TTL, or issuing a complete fleet query for every concurrent poll.
type fleetAccountingReads struct {
	mu      sync.Mutex
	running bool
	pending *fleetAccountingRead
}

type fleetAccountingRead struct {
	contexts []context.Context
	done     chan struct{}
	rows     []domain.PSPClientAccounting
	err      error
}

func (r *fleetAccountingReads) read(ctx context.Context, load func(context.Context) ([]domain.PSPClientAccounting, error)) ([]domain.PSPClientAccounting, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.pending == nil {
		r.pending = &fleetAccountingRead{done: make(chan struct{})}
	}
	batch := r.pending
	batch.contexts = append(batch.contexts, ctx)
	if !r.running {
		r.running = true
		go r.drain(load)
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-batch.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The stored result belongs only to this completed batch. Each caller
		// receives its own numeric values, so mutations cannot cross responses.
		return slices.Clone(batch.rows), batch.err
	}
}

func (r *fleetAccountingReads) drain(load func(context.Context) ([]domain.PSPClientAccounting, error)) {
	for {
		r.mu.Lock()
		batch := r.pending
		r.pending = nil
		if batch == nil {
			r.running = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()

		active := batch.contexts[:0]
		for _, ctx := range batch.contexts {
			if ctx.Err() == nil {
				active = append(active, ctx)
			}
		}
		if len(active) != 0 {
			batch.rows, batch.err = readFleetAccountingBatch(active, load)
			if batch.err != nil {
				batch.rows = nil
			}
		}
		batch.contexts = nil
		close(batch.done)
	}
}

func readFleetAccountingBatch(callers []context.Context, load func(context.Context) ([]domain.PSPClientAccounting, error)) ([]domain.PSPClientAccounting, error) {
	// One canceled caller must not cancel another caller's current read. Keep
	// context values for SQL tracing; cancel the query when all callers leave.
	ctx, cancel := context.WithCancel(context.WithoutCancel(callers[0]))
	defer cancel()
	var remaining atomic.Int64
	remaining.Store(int64(len(callers)))
	stops := make([]func() bool, len(callers))
	for i, caller := range callers {
		stops[i] = context.AfterFunc(caller, func() {
			if remaining.Add(-1) == 0 {
				cancel()
			}
		})
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	return load(ctx)
}
