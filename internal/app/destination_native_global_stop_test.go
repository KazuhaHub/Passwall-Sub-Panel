//go:build destination_acceptance && linux

package app

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type nativeGlobalStopTarget struct {
	signal func()
	kill   func()
	done   <-chan error
}

type nativeGlobalStopResult struct {
	err    error
	forced bool
	reaped bool
}

// Every daemon gets its original grace interval concurrently. Waiting for one
// daemon must not postpone another's shutdown, and a broken pipe must not keep
// the failed test from saving its original evidence indefinitely.
func nativeStopGlobalDaemons(targets []nativeGlobalStopTarget, grace, reap time.Duration) ([]nativeGlobalStopResult, error) {
	if len(targets) > 65 || grace <= 0 || reap <= 0 {
		return nil, errors.New("global cleanup scope invalid")
	}
	for _, target := range targets {
		if target.signal == nil || target.kill == nil || target.done == nil {
			return nil, errors.New("global cleanup target invalid")
		}
	}
	results := make([]nativeGlobalStopResult, len(targets))
	for _, target := range targets {
		target.signal()
	}
	graceTimer := time.NewTimer(grace)
	defer graceTimer.Stop()
	for i, target := range targets {
		select {
		case err := <-target.done:
			results[i].err, results[i].reaped = err, true
		case <-graceTimer.C:
			goto force
		}
	}
	return results, nil

force:
	for i, target := range targets {
		if results[i].reaped {
			continue
		}
		select {
		case err := <-target.done:
			results[i].err, results[i].reaped = err, true
		default:
			target.kill()
			results[i].forced = true
		}
	}
	reapTimer := time.NewTimer(reap)
	defer reapTimer.Stop()
	for i, target := range targets {
		if results[i].reaped {
			continue
		}
		select {
		case err := <-target.done:
			results[i].err, results[i].reaped = err, true
		case <-reapTimer.C:
			return results, nil
		}
	}
	return results, nil
}

func TestNativeGlobalDaemonCleanupBroadcastsAndBoundsWaits(t *testing.T) {
	t.Run("all_65_signaled_before_wait", func(t *testing.T) {
		all, cancel, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var signals, kills atomic.Int64
		targets := make([]nativeGlobalStopTarget, 65)
		var done [65]chan error
		for i := range targets {
			done[i] = make(chan error, 1)
			targets[i] = nativeGlobalStopTarget{func() {
				if signals.Add(1) == 65 {
					close(all)
				}
			}, func() { kills.Add(1) }, done[i]}
		}
		go func() {
			defer close(exited)
			select {
			case <-all:
				for _, result := range done {
					result <- nil
				}
			case <-cancel:
			}
		}()
		t.Cleanup(func() { close(cancel); <-exited })
		results, err := nativeStopGlobalDaemons(targets, time.Second, time.Second)
		if err != nil || len(results) != 65 || signals.Load() != 65 || kills.Load() != 0 {
			t.Fatal("cleanup did not broadcast to the complete started cohort")
		}
		for _, result := range results {
			if !result.reaped || result.forced || result.err != nil {
				t.Fatal("one daemon's wait prevented another daemon's graceful exit")
			}
		}
	})
	t.Run("kill_only_unfinished_and_keep_errors", func(t *testing.T) {
		original, killed := errors.New("owned graceful exit error"), errors.New("owned forced exit error")
		first, blocked, last := make(chan error, 1), make(chan error, 1), make(chan error, 1)
		first <- original
		var kills [3]int
		targets := []nativeGlobalStopTarget{
			{func() {}, func() { kills[0]++ }, first},
			{func() {}, func() { kills[1]++; blocked <- killed }, blocked},
			{func() { last <- nil }, func() { kills[2]++ }, last},
		}
		results, err := nativeStopGlobalDaemons(targets, 20*time.Millisecond, time.Second)
		if err != nil || len(results) != 3 || kills != [3]int{0, 1, 0} {
			t.Fatal("cleanup killed an already-finished daemon or missed a blocked daemon")
		}
		if results[0].err != original || results[0].forced || !results[0].reaped || results[1].err != killed || !results[1].forced || !results[1].reaped || results[2].err != nil || results[2].forced || !results[2].reaped {
			t.Fatal("cleanup changed an original wait error or failed to join the forced exit")
		}
	})
	t.Run("unreapable_wait_is_bounded", func(t *testing.T) {
		done := make(chan error, 1)
		var killed atomic.Int64
		finished, exited := make(chan []nativeGlobalStopResult, 1), make(chan struct{})
		go func() {
			defer close(exited)
			results, _ := nativeStopGlobalDaemons([]nativeGlobalStopTarget{{func() {}, func() { killed.Add(1) }, done}}, 20*time.Millisecond, 20*time.Millisecond)
			finished <- results
		}()
		t.Cleanup(func() { done <- nil; <-exited })
		select {
		case results := <-finished:
			if len(results) != 1 || !results[0].forced || results[0].reaped || killed.Load() != 1 {
				t.Fatal("unreaped daemon was reported as a clean exit")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("forced cleanup still waited indefinitely")
		}
	})
	t.Run("empty_and_invalid_scope", func(t *testing.T) {
		if results, err := nativeStopGlobalDaemons(nil, time.Second, time.Second); err != nil || len(results) != 0 {
			t.Fatal("cleanup could not handle failure before any daemon started")
		}
		for _, targets := range [][]nativeGlobalStopTarget{make([]nativeGlobalStopTarget, 66), {{}}} {
			if _, err := nativeStopGlobalDaemons(targets, time.Second, time.Second); err == nil {
				t.Fatal("cleanup accepted an invalid owned target set")
			}
		}
		if _, err := nativeStopGlobalDaemons(nil, 0, time.Second); err == nil {
			t.Fatal("cleanup accepted a missing grace deadline")
		}
	})
}
