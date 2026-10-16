//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeLoadTraffic struct {
	cancel   context.CancelFunc
	exited   chan struct{}
	duration time.Duration
	err      error
}

// Observation stays on the test goroutine: a slow control probe or process
// sample cannot stop this fixed open-loop traffic clock. Finish closes the
// sender queue and joins its workers before wait/stop returns. The real caller
// retains 3000 ticks, 110 offers per tick and 100ms spacing; smaller schedules
// below are only unit vectors, never runtime acceptance evidence.
func nativeStartLoadTraffic(parent context.Context, start time.Time, ticks, perTick int, interval time.Duration, offer func(context.Context, int), finish func()) (*nativeLoadTraffic, error) {
	if parent == nil || start.IsZero() || ticks < 1 || ticks > 3000 || perTick < 1 || perTick > 110 || interval <= 0 || interval > 100*time.Millisecond || offer == nil || finish == nil {
		return nil, errors.New("native load traffic scope invalid")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	traffic := &nativeLoadTraffic{cancel: cancel, exited: make(chan struct{})}
	go func() {
		defer close(traffic.exited)
		defer finish()
		wait := func(until time.Time) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if delay := time.Until(until); delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
				}
			}
			return ctx.Err()
		}
		traffic.err = func() error {
			for tick := 0; tick < ticks; tick++ {
				if err := wait(start.Add(time.Duration(tick) * interval)); err != nil {
					return err
				}
				for n := 0; n < perTick; n++ {
					if err := ctx.Err(); err != nil {
						return err
					}
					offer(ctx, tick*perTick+n)
				}
			}
			return wait(start.Add(time.Duration(ticks) * interval))
		}()
		// Measure the actual traffic window before the sender drain and any
		// delayed observation. Neither operation extends the delivery buckets.
		traffic.duration = time.Since(start)
	}()
	return traffic, nil
}

func (t *nativeLoadTraffic) wait() (time.Duration, error) {
	<-t.exited
	return t.duration, t.err
}

func (t *nativeLoadTraffic) stop() {
	t.cancel()
	<-t.exited
}

func TestNativeLoadTrafficScheduleKeepsClockAndOwnedCleanup(t *testing.T) {
	t.Run("traffic_finishes_while_observer_is_blocked", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		var offered []int
		var finished atomic.Int64
		start := time.Now()
		traffic, err := nativeStartLoadTraffic(ctx, start, 4, 3, time.Millisecond, func(_ context.Context, seq int) {
			offered = append(offered, seq)
		}, func() { finished.Add(1) })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(traffic.stop)
		// This test goroutine stands in for a blocked synchronous observer. It
		// does no production or scheduling work while traffic must complete.
		select {
		case <-traffic.exited:
		case <-ctx.Done():
			t.Fatal("observer wait held up the independent traffic clock")
		}
		elapsed, err := traffic.wait()
		if err != nil || elapsed < 4*time.Millisecond || finished.Load() != 1 || !reflect.DeepEqual(offered, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}) {
			t.Fatal("traffic changed its complete event set, duration or finish ownership")
		}
	})

	t.Run("finish_precedes_wait_and_stop_is_repeatable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		entered, release, waited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		traffic, err := nativeStartLoadTraffic(ctx, time.Now(), 1, 1, time.Millisecond, func(context.Context, int) {}, func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { once.Do(func() { close(release) }); traffic.stop(); <-waited })
		go func() { defer close(waited); _, _ = traffic.wait() }()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("traffic did not reach its owned finish")
		}
		select {
		case <-waited:
			t.Fatal("wait returned before sender cleanup completed")
		default:
		}
		once.Do(func() { close(release) })
		select {
		case <-waited:
		case <-ctx.Done():
			t.Fatal("wait did not join completed cleanup")
		}
		traffic.stop()
		traffic.stop()
	})

	t.Run("cancel_joins_without_cancelling_parent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		entered := make(chan struct{})
		var calls, finishes atomic.Int64
		var joined atomic.Bool
		traffic, err := nativeStartLoadTraffic(ctx, time.Now(), 3000, 110, 100*time.Millisecond, func(ctx context.Context, _ int) {
			calls.Add(1)
			close(entered)
			<-ctx.Done()
			joined.Store(true)
		}, func() {
			if joined.Load() {
				finishes.Add(1)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(traffic.stop)
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("traffic did not start its first offer")
		}
		traffic.stop()
		_, err = traffic.wait()
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 || finishes.Load() != 1 || ctx.Err() != nil {
			t.Fatal("stop left a producer alive, skipped finish or cancelled the outer observation context")
		}
	})

	t.Run("fixed_start_is_not_reset_for_overdue_ticks", func(t *testing.T) {
		var offered []int
		start := time.Now().Add(-time.Second)
		traffic, err := nativeStartLoadTraffic(t.Context(), start, 2, 2, time.Millisecond, func(_ context.Context, seq int) { offered = append(offered, seq) }, func() {})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(traffic.stop)
		elapsed, err := traffic.wait()
		if err != nil || elapsed < time.Second || !reflect.DeepEqual(offered, []int{0, 1, 2, 3}) {
			t.Fatal("late scheduling reset the original clock or trimmed planned requests")
		}
	})

	t.Run("invalid_or_cancelled_scope_does_not_start", func(t *testing.T) {
		var calls atomic.Int64
		offer := func(context.Context, int) { calls.Add(1) }
		finish := func() { calls.Add(1) }
		for _, cfg := range []struct {
			ticks, perTick int
			interval       time.Duration
		}{{0, 1, time.Millisecond}, {3001, 1, time.Millisecond}, {1, 0, time.Millisecond}, {1, 111, time.Millisecond}, {1, 1, 0}, {1, 1, 101 * time.Millisecond}} {
			if traffic, err := nativeStartLoadTraffic(t.Context(), time.Now(), cfg.ticks, cfg.perTick, cfg.interval, offer, finish); err == nil || traffic != nil {
				t.Fatal("invalid schedule started a producer")
			}
		}
		//lint:ignore SA1012 Verify that a missing parent never starts traffic.
		if traffic, err := nativeStartLoadTraffic(nil, time.Now(), 1, 1, time.Millisecond, offer, finish); err == nil || traffic != nil {
			t.Fatal("missing parent started a producer")
		}
		for _, missing := range []int{0, 1, 2} {
			start, send, done := time.Now(), offer, finish
			switch missing {
			case 0:
				start = time.Time{}
			case 1:
				send = nil
			case 2:
				done = nil
			}
			if traffic, err := nativeStartLoadTraffic(t.Context(), start, 1, 1, time.Millisecond, send, done); err == nil || traffic != nil {
				t.Fatal("incomplete schedule started a producer")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if traffic, err := nativeStartLoadTraffic(ctx, time.Now(), 1, 1, time.Millisecond, offer, finish); !errors.Is(err, context.Canceled) || traffic != nil || calls.Load() != 0 {
			t.Fatal("cancelled or invalid schedule invoked callbacks")
		}
	})
}
