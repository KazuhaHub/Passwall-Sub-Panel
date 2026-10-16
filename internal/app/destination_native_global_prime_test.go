//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Each owner has one serial worker and keeps the same reverse lane order.
// Waiting for a different owner's packet must not stop this owner advancing.
// The caller supplies the original shared fault deadline, not a per-lane budget.
func nativePrimeGlobalByteOwners(ctx context.Context, owners int, send func(context.Context, int, int) error, frozen func(int, int) bool) error {
	if ctx == nil || owners < 1 || owners > 65 || send == nil || frozen == nil {
		return errors.New("global byte priming scope invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	phase, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var failed sync.Once
	var firstErr error
	fail := func(err error) { failed.Do(func() { firstErr = err; cancel() }) }
	for owner := range owners {
		workers.Add(1)
		go func() {
			defer workers.Done()
			poll := time.NewTicker(100 * time.Millisecond)
			defer poll.Stop()
			for _, kind := range []int{3, 2, 1, 0} {
				if err := phase.Err(); err != nil {
					fail(err)
					return
				}
				if err := send(phase, owner, kind); err != nil {
					fail(err)
					return
				}
				for {
					if err := phase.Err(); err != nil {
						fail(err)
						return
					}
					if frozen(owner, kind) {
						break
					}
					select {
					case <-phase.Done():
						fail(phase.Err())
						return
					case <-poll.C:
					}
				}
			}
		}()
	}
	workers.Wait()
	return firstErr
}

func TestNativeGlobalBytePrimingKeepsIndependentOwnerOrderAndDeadline(t *testing.T) {
	t.Run("independent_owner_order", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		var mu sync.Mutex
		var calls [2][]int
		slow, fast, entered := make(chan struct{}), make(chan struct{}), make(chan struct{})
		done, exited := make(chan error, 1), make(chan struct{})
		go func() {
			defer close(exited)
			done <- nativePrimeGlobalByteOwners(ctx, 2, func(ctx context.Context, owner, kind int) error {
				if owner == 1 && kind == 3 {
					select {
					case <-entered:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				mu.Lock()
				calls[owner] = append(calls[owner], kind)
				if owner == 0 && kind == 3 {
					close(entered)
				}
				mu.Unlock()
				return nil
			}, func(owner, kind int) bool {
				if owner == 0 && kind == 3 {
					select {
					case <-slow:
						return true
					default:
						return false
					}
				}
				if owner == 1 && kind == 0 {
					close(fast)
				}
				return true
			})
		}()
		t.Cleanup(func() { cancel(); <-exited })
		select {
		case <-fast:
		case <-ctx.Done():
			t.Fatal("one owner's first packet blocked another owner's reverse lane sequence")
		}
		mu.Lock()
		valid := slices.Equal(calls[0], []int{3}) && slices.Equal(calls[1], []int{3, 2, 1, 0})
		mu.Unlock()
		if !valid {
			t.Fatal("per-owner order changed or an owner advanced before its own packet froze")
		}
		select {
		case <-done:
			t.Fatal("priming completed before every original owner was ready")
		default:
		}
		close(slow)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(calls[0], []int{3, 2, 1, 0}) {
			t.Fatal("the delayed owner lost a lane or changed its order")
		}
	})

	t.Run("all_65_owners", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		var mu sync.Mutex
		var calls [65][]int
		active, peak, arrived := 0, 0, 0
		gate := make(chan struct{})
		err := nativePrimeGlobalByteOwners(ctx, 65, func(ctx context.Context, owner, kind int) error {
			mu.Lock()
			calls[owner] = append(calls[owner], kind)
			active++
			peak = max(peak, active)
			if kind == 3 {
				arrived++
				if arrived == 65 {
					close(gate)
				}
			}
			mu.Unlock()
			defer func() { mu.Lock(); active--; mu.Unlock() }()
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func(int, int) bool { return true })
		if err != nil || active != 0 || peak != 65 || arrived != 65 {
			t.Fatal("original 65-owner concurrency, completion or request ownership changed")
		}
		for _, owner := range calls {
			if !slices.Equal(owner, []int{3, 2, 1, 0}) {
				t.Fatal("priming omitted, duplicated or reordered an original request")
			}
		}
	})

	t.Run("send_failure_joins_workers", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		want := errors.New("owned priming send failure")
		entered := make(chan struct{})
		var joined atomic.Bool
		var reads atomic.Int64
		err := nativePrimeGlobalByteOwners(ctx, 2, func(ctx context.Context, owner, kind int) error {
			if owner == 0 {
				select {
				case <-entered:
					return want
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			close(entered)
			<-ctx.Done()
			joined.Store(true)
			return ctx.Err()
		}, func(int, int) bool { reads.Add(1); return true })
		if !errors.Is(err, want) || !joined.Load() || reads.Load() != 0 || ctx.Err() != nil {
			t.Fatal("send failure was hidden, left a worker running or cancelled the shared outer deadline")
		}
	})

	t.Run("unfrozen_deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		var wrongLane atomic.Int64
		err := nativePrimeGlobalByteOwners(ctx, 3, func(_ context.Context, owner, kind int) error {
			if kind != 3 {
				wrongLane.Add(1)
			}
			return nil
		}, func(int, int) bool { return false })
		if !errors.Is(err, context.DeadlineExceeded) || wrongLane.Load() != 0 {
			t.Fatal("a missing original frozen packet bypassed or extended the shared deadline")
		}
	})

	t.Run("cancelled_before_start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var calls atomic.Int64
		err := nativePrimeGlobalByteOwners(ctx, 65, func(context.Context, int, int) error { calls.Add(1); return nil }, func(int, int) bool { calls.Add(1); return true })
		if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatal("cancelled priming still sent or accepted a packet")
		}
	})

	t.Run("invalid_scope", func(t *testing.T) {
		send := func(context.Context, int, int) error { t.Error("invalid priming sent a request"); return nil }
		frozen := func(int, int) bool { t.Error("invalid priming read a packet"); return true }
		for _, owners := range []int{-1, 0, 66} {
			if err := nativePrimeGlobalByteOwners(t.Context(), owners, send, frozen); err == nil {
				t.Fatal("out-of-range owner count accepted")
			}
		}
		//lint:ignore SA1012 Deliberately verify that the private helper rejects a missing priming context.
		if nativePrimeGlobalByteOwners(nil, 65, send, frozen) == nil {
			t.Fatal("missing priming context accepted")
		}
		if nativePrimeGlobalByteOwners(t.Context(), 65, nil, frozen) == nil || nativePrimeGlobalByteOwners(t.Context(), 65, send, nil) == nil {
			t.Fatal("missing priming callbacks accepted")
		}
	})
}
