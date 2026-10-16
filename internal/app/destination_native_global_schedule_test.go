//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeGlobalTraffic struct {
	cancel context.CancelFunc
	exited chan struct{}
	err    error
}

func nativeGlobalTrafficDue(start time.Time, tick int) (time.Time, bool) {
	if start.IsZero() || tick < 0 || tick >= 1200 {
		return time.Time{}, false
	}
	return start.Add(time.Duration(tick) * 119 * time.Second / 1199), true
}

// The global entrypoint has exactly65 owners,1200 original ticks and four lanes
// plus one control per owner every tenth tick. No duration/rate/owner override
// is available. Finish closes the original queue and joins the original workers
// even on cancellation; process observers cannot hold this producer's clock.
func nativeStartGlobalTraffic(parent context.Context, start time.Time, offer func(context.Context, int, int, int), finish func()) (*nativeGlobalTraffic, error) {
	if parent == nil || start.IsZero() || offer == nil || finish == nil {
		return nil, errors.New("global traffic scope invalid")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	traffic := &nativeGlobalTraffic{cancel: cancel, exited: make(chan struct{})}
	go func() {
		defer close(traffic.exited)
		defer finish()
		traffic.err = func() error {
			for tick := 0; tick < 1200; tick++ {
				due, _ := nativeGlobalTrafficDue(start, tick)
				if err := nativeDayWait(ctx, due); err != nil {
					return err
				}
				for owner := 0; owner < 65; owner++ {
					for kind := 0; kind < 4; kind++ {
						if err := ctx.Err(); err != nil {
							return err
						}
						offer(ctx, owner, kind, tick)
					}
					if tick%10 == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
						offer(ctx, owner, 4, tick/10)
					}
				}
			}
			return nativeDayWait(ctx, start.Add(120*time.Second))
		}()
	}()
	return traffic, nil
}

func (t *nativeGlobalTraffic) wait() error { <-t.exited; return t.err }
func (t *nativeGlobalTraffic) stop()       { t.cancel(); <-t.exited }

func TestNativeGlobalTrafficSchedulePreservesOriginalOwnersOrderAndClock(t *testing.T) {
	t.Run("all65owners_fourlanes_control_order_and_original_deadlines", func(t *testing.T) {
		start := time.Now().Add(-121 * time.Second)
		var delivered [65][4]int
		var controls [65]int
		var users [200]int
		var offers, finished atomic.Int64
		traffic, err := nativeStartGlobalTraffic(t.Context(), start, func(_ context.Context, owner, kind, seq int) {
			tick := seq
			if kind == 4 {
				tick = seq * 10
			}
			perOwner := 4
			if tick%10 == 0 {
				perOwner = 5
			}
			// Independent flat offset counts all control ticks strictly before
			// this tick, then the original per-owner lane/control order.
			want := 65*(4*tick+(tick+9)/10) + owner*perOwner + kind
			if int(offers.Add(1)-1) != want {
				t.Error("global event order changed")
			}
			if kind == 4 {
				controls[owner]++
			} else {
				delivered[owner][kind]++
				if kind == 2 {
					users[seq%200]++
				}
			}
		}, func() { finished.Add(1) })
		nativeMust(t, err, "start complete global schedule unit vector")
		t.Cleanup(traffic.stop)
		nativeMust(t, traffic.wait(), "join complete global schedule")
		if offers.Load() != 319800 || finished.Load() != 1 {
			t.Fatal("global original event set or cleanup incomplete")
		}
		for owner := range delivered {
			for _, n := range delivered[owner] {
				if n != 1200 {
					t.Fatal("owner lane lost original event")
				}
			}
			if controls[owner] != 120 {
				t.Fatal("owner control count changed")
			}
		}
		for _, n := range users {
			if n != 390 {
				t.Fatal("original200-user distribution changed")
			}
		}
		for _, tick := range []int{0, 1, 10, 1190, 1199} {
			due, valid := nativeGlobalTrafficDue(start, tick)
			if !valid || !due.Equal(start.Add(time.Duration(tick)*119*time.Second/1199)) {
				t.Fatal("original enqueue deadline reset")
			}
		}
	})
	t.Run("producer_finishes_while_process_observer_is_blocked", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		var calls atomic.Int64
		traffic, err := nativeStartGlobalTraffic(ctx, time.Now().Add(-121*time.Second), func(context.Context, int, int, int) { calls.Add(1) }, func() {})
		nativeMust(t, err, "start independent global producer")
		t.Cleanup(traffic.stop)
		// This goroutine performs no observation or queue work while the
		// producer completes. Past anchors are short unit vectors only.
		select {
		case <-traffic.exited:
		case <-ctx.Done():
			t.Fatal("blocked observer held global producer")
		}
		if calls.Load() != 319800 || traffic.wait() != nil {
			t.Fatal("blocked observer lost original events")
		}
	})
	t.Run("finish_joins_before_wait_and_stop_is_repeatable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		entered, release, waited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		traffic, err := nativeStartGlobalTraffic(ctx, time.Now().Add(-121*time.Second), func(context.Context, int, int, int) {}, func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		nativeMust(t, err, "start joined global finish")
		t.Cleanup(func() { once.Do(func() { close(release) }); traffic.stop(); <-waited })
		go func() { defer close(waited); _ = traffic.wait() }()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("global finish never entered")
		}
		select {
		case <-waited:
			t.Fatal("global wait returned before worker cleanup")
		default:
		}
		once.Do(func() { close(release) })
		select {
		case <-waited:
		case <-ctx.Done():
			t.Fatal("global worker finish not joined")
		}
		traffic.stop()
		traffic.stop()
	})
	t.Run("cancel_during_offer_joins_without_cancelling_parent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		entered := make(chan struct{})
		var calls, finished atomic.Int64
		traffic, err := nativeStartGlobalTraffic(ctx, time.Now().Add(-121*time.Second), func(ctx context.Context, _, _, _ int) {
			if calls.Add(1) == 1 {
				close(entered)
			}
			<-ctx.Done()
		}, func() { finished.Add(1) })
		nativeMust(t, err, "start cancel vector")
		t.Cleanup(traffic.stop)
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("global offer never entered")
		}
		traffic.stop()
		if !errors.Is(traffic.wait(), context.Canceled) || calls.Load() != 1 || finished.Load() != 1 || ctx.Err() != nil {
			t.Fatal("global cancellation escaped ownership or offered after cancellation")
		}
	})
	t.Run("invalid_and_precancelled_scope_never_starts", func(t *testing.T) {
		offer := func(context.Context, int, int, int) { t.Error("invalid global producer started") }
		finish := func() { t.Error("invalid global cleanup ran") }
		for i := range 4 {
			parent, start, o, f := t.Context(), time.Now(), offer, finish
			switch i {
			case 0:
				parent = nil
			case 1:
				start = time.Time{}
			case 2:
				o = nil
			case 3:
				f = nil
			}
			if task, err := nativeStartGlobalTraffic(parent, start, o, f); err == nil || task != nil {
				t.Fatal("invalid global schedule accepted")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if task, err := nativeStartGlobalTraffic(ctx, time.Now(), offer, finish); !errors.Is(err, context.Canceled) || task != nil {
			t.Fatal("precancelled global schedule started")
		}
		for _, tick := range []int{-1, 1200} {
			if _, valid := nativeGlobalTrafficDue(time.Now(), tick); valid {
				t.Fatal("global tick outside original window accepted")
			}
		}
		if _, valid := nativeGlobalTrafficDue(time.Time{}, 0); valid {
			t.Fatal("zero original clock accepted")
		}
	})
}
