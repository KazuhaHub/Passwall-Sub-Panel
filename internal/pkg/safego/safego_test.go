package safego

import (
	"sync"
	"testing"
	"time"
)

func TestGoRecoversPanic(t *testing.T) {
	// If recover misses the panic, the test process crashes — passing
	// this test IS the assertion.
	done := make(chan struct{})
	Go("test.panic", func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish in time")
	}
}

func TestGoTrackedReleasesWaitGroupOnPanic(t *testing.T) {
	var wg sync.WaitGroup
	GoTracked(&wg, "test.tracked-panic", func() {
		panic("boom")
	})
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("wg.Wait did not return after panicking worker — recover or Done() leak")
	}
}

func TestGoTrackedRunsNormally(t *testing.T) {
	var wg sync.WaitGroup
	hit := make(chan struct{})
	GoTracked(&wg, "test.tracked-normal", func() {
		close(hit)
	})
	<-hit
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("WaitGroup did not drain")
	}
}

func TestGoTrackedNilWGAccepted(t *testing.T) {
	// nil WG is documented as tolerated — used by tests that don't
	// care about draining.
	done := make(chan struct{})
	GoTracked(nil, "test.nil-wg", func() {
		close(done)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker with nil wg did not run")
	}
}
