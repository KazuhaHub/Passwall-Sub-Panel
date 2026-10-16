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
	return nativePrimeGlobalByteOwnersWithReady(ctx, owners, send, frozen, nil)
}

// A frozen-lane notification is bounded metadata, not an extra request queue.
// Its single feeder may wait for bulk queue space without holding up any
// owner's next original prime. Return only after both priming and feeding join.
func nativePrimeGlobalByteOwnersWithQueuedReady(ctx context.Context, owners int, send func(context.Context, int, int) error, frozen func(int, int) bool, feed func(context.Context, int, int) error) error {
	if ctx == nil || owners < 1 || owners > 65 || send == nil || frozen == nil || feed == nil {
		return errors.New("global byte queued priming scope invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	phase, cancel := context.WithCancel(ctx)
	defer cancel()
	type lane struct{ owner, kind int }
	ready := make(chan lane, owners*4)
	exited := make(chan struct{})
	var failed sync.Once
	var firstErr error
	fail := func(err error) { failed.Do(func() { firstErr = err; cancel() }) }
	go func() {
		defer close(exited)
		for next := range ready {
			if err := phase.Err(); err != nil {
				fail(err)
				return
			}
			if err := feed(phase, next.owner, next.kind); err != nil {
				fail(err)
				return
			}
		}
	}()
	err := nativePrimeGlobalByteOwnersWithReady(phase, owners, send, frozen, func(ctx context.Context, owner, kind int) error {
		select {
		case ready <- lane{owner, kind}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		fail(err)
	}
	close(ready) // every serial owner has joined, so no notification can race close
	<-exited
	if err := phase.Err(); err != nil {
		fail(err)
	}
	return firstErr
}

// Ready can feed actual traffic behind a lane's frozen original packet while
// other lanes/owners still wait for their first packet. It runs once per lane,
// in the same serial owner worker, under the original shared fault deadline.
func nativePrimeGlobalByteOwnersWithReady(ctx context.Context, owners int, send func(context.Context, int, int) error, frozen func(int, int) bool, ready func(context.Context, int, int) error) error {
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
						if ready != nil {
							if err := ready(phase, owner, kind); err != nil {
								fail(err)
								return
							}
						}
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
	t.Run("queued_ready_does_not_hold_serial_primes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		entered, release, allSent, exited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		var once sync.Once
		var sent atomic.Int64
		var calls [65]int
		var frozen [65][4]atomic.Bool
		var fed [65][4]int
		go func() {
			defer close(exited)
			done <- nativePrimeGlobalByteOwnersWithQueuedReady(ctx, 65, func(_ context.Context, owner, kind int) error {
				if kind != 3-calls[owner] {
					return errors.New("queued notification reordered original primes")
				}
				calls[owner]++
				if sent.Add(1) == 260 {
					close(allSent)
				}
				return nil
			}, func(owner, kind int) bool {
				frozen[owner][kind].Store(true)
				return true
			}, func(ctx context.Context, owner, kind int) error {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if !frozen[owner][kind].Load() {
					return errors.New("bulk feeder used an unfrozen lane")
				}
				fed[owner][kind]++
				return nil
			})
		}()
		t.Cleanup(func() { cancel(); <-exited })
		for _, stage := range []<-chan struct{}{entered, allSent} {
			select {
			case <-stage:
			case <-ctx.Done():
				t.Fatal("blocked bulk feeder held up the original 260 serial owner primes")
			}
		}
		select {
		case <-done:
			t.Fatal("priming returned before its owned bulk feeder joined")
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		for owner := range 65 {
			if calls[owner] != 4 || fed[owner] != [4]int{1, 1, 1, 1} {
				t.Fatal("queued readiness omitted or duplicated an original lane")
			}
		}
	})

	t.Run("queued_ready_failure_cancels_and_joins", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		entered := make(chan struct{})
		var active, feeds atomic.Int64
		boom := errors.New("owned queued feeder failure")
		err := nativePrimeGlobalByteOwnersWithQueuedReady(ctx, 2, func(ctx context.Context, owner, kind int) error {
			active.Add(1)
			defer active.Add(-1)
			if owner == 0 {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			}
			select {
			case <-entered:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func(int, int) bool { return true }, func(context.Context, int, int) error {
			feeds.Add(1)
			return boom
		})
		if !errors.Is(err, boom) || active.Load() != 0 || feeds.Load() != 1 || ctx.Err() != nil {
			t.Fatal("feeder failure lost its cause, leaked a prime or cancelled the parent")
		}
	})

	t.Run("queued_ready_cancel_joins_blocked_feeder", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		limit, stop := context.WithTimeout(t.Context(), 2*time.Second)
		defer stop()
		entered, feederExited, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			defer close(exited)
			done <- nativePrimeGlobalByteOwnersWithQueuedReady(ctx, 65, func(context.Context, int, int) error { return nil }, func(int, int) bool { return true }, func(ctx context.Context, _, _ int) error {
				defer close(feederExited)
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			})
		}()
		t.Cleanup(func() { cancel(); <-exited })
		select {
		case <-entered:
		case <-limit.Done():
			t.Fatal("queued feeder never started")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("queued feeder lost cancellation")
			}
		case <-limit.Done():
			t.Fatal("cancellation did not join queued priming and its blocked feeder")
		}
		select {
		case <-feederExited:
		default:
			t.Fatal("queued priming returned with its feeder still running")
		}
	})

	t.Run("queued_ready_invalid_scope_does_not_start", func(t *testing.T) {
		var calls atomic.Int64
		send := func(context.Context, int, int) error { calls.Add(1); return nil }
		frozen := func(int, int) bool { calls.Add(1); return true }
		for _, owners := range []int{0, 66} {
			if err := nativePrimeGlobalByteOwnersWithQueuedReady(t.Context(), owners, send, frozen, send); err == nil {
				t.Fatal("invalid queued owner count started work")
			}
		}
		//lint:ignore SA1012 Verify that a missing parent never starts a feeder.
		if err := nativePrimeGlobalByteOwnersWithQueuedReady(nil, 1, send, frozen, send); err == nil {
			t.Fatal("missing queued parent started work")
		}
		if nativePrimeGlobalByteOwnersWithQueuedReady(t.Context(), 1, nil, frozen, send) == nil || nativePrimeGlobalByteOwnersWithQueuedReady(t.Context(), 1, send, nil, send) == nil || nativePrimeGlobalByteOwnersWithQueuedReady(t.Context(), 1, send, frozen, nil) == nil {
			t.Fatal("missing queued callback started work")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := nativePrimeGlobalByteOwnersWithQueuedReady(ctx, 1, send, frozen, send); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatal("invalid or cancelled queued scope invoked callbacks")
		}
	})

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

	t.Run("ready_pipelines_only_frozen_owner_lanes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		type lane struct{ owner, kind int }
		ready := make(chan lane, 8)
		done := make(chan error, 1)
		var sent [2][4]atomic.Bool
		go func() {
			defer close(exited)
			done <- nativePrimeGlobalByteOwnersWithReady(ctx, 2, func(ctx context.Context, owner, kind int) error {
				if owner == 1 && kind == 3 {
					select {
					case <-entered:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				sent[owner][kind].Store(true)
				if owner == 0 && kind == 3 {
					close(entered)
				}
				return nil
			}, func(owner, kind int) bool {
				if owner == 0 && kind == 3 {
					select {
					case <-release:
					default:
						return false
					}
				}
				return sent[owner][kind].Load()
			}, func(ctx context.Context, owner, kind int) error {
				if !sent[owner][kind].Load() {
					return errors.New("ready preceded the original request")
				}
				select {
				case ready <- lane{owner, kind}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		t.Cleanup(func() { cancel(); <-exited })
		read := func(owner int) {
			t.Helper()
			for _, kind := range []int{3, 2, 1, 0} {
				select {
				case got := <-ready:
					if got != (lane{owner, kind}) {
						t.Fatal("ready traffic bypassed a frozen packet, repeated a lane or changed its owner order")
					}
				case <-ctx.Done():
					t.Fatal("ready lanes waited for another owner's first packet")
				}
			}
		}
		read(1)
		select {
		case <-done:
			t.Fatal("ready traffic bypassed an unprepared owner")
		default:
		}
		close(release)
		read(0)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ready_failure_cancels_and_joins", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		want := errors.New("owned ready traffic failure")
		entered := make(chan struct{})
		var joined atomic.Bool
		err := nativePrimeGlobalByteOwnersWithReady(ctx, 2, func(context.Context, int, int) error { return nil }, func(int, int) bool { return true }, func(ctx context.Context, owner, kind int) error {
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
		})
		if !errors.Is(err, want) || !joined.Load() || ctx.Err() != nil {
			t.Fatal("ready failure was hidden, left a worker running or reset the shared deadline")
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
