package xui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLockClientEmail_SerializesSameEmail verifies the per-email write lock:
// two holders of the SAME email never overlap (3X-UI rejects concurrent
// same-client mutations), while DIFFERENT emails proceed in parallel.
func TestLockClientEmail_SerializesSameEmail(t *testing.T) {
	c := &Client{}

	t.Run("same email serializes", func(t *testing.T) {
		var inCrit int32
		var maxConc int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unlock := c.lockClientEmail("u1@psp.local")
				defer unlock()
				n := atomic.AddInt32(&inCrit, 1)
				if n > atomic.LoadInt32(&maxConc) {
					atomic.StoreInt32(&maxConc, n)
				}
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt32(&inCrit, -1)
			}()
		}
		wg.Wait()
		if maxConc != 1 {
			t.Fatalf("same-email max concurrency = %d, want 1 (lock must serialize)", maxConc)
		}
	})

	t.Run("different emails run in parallel", func(t *testing.T) {
		start := make(chan struct{})
		var ready, peak int32
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				unlock := c.lockClientEmail(string(rune('a'+idx)) + "@psp.local")
				defer unlock()
				atomic.AddInt32(&ready, 1)
				<-start // hold the lock until all are inside their own crit section
				atomic.AddInt32(&peak, 1)
			}(i)
		}
		// wait until all 4 acquired their (distinct) locks
		for atomic.LoadInt32(&ready) < 4 {
			time.Sleep(time.Millisecond)
		}
		close(start)
		wg.Wait()
		if peak != 4 {
			t.Fatalf("distinct-email holders that proceeded = %d, want 4 (no cross-email blocking)", peak)
		}
	})

	t.Run("empty email is a no-op lock", func(t *testing.T) {
		unlock := c.lockClientEmail("")
		unlock() // must not panic / deadlock
		unlock2 := c.lockClientEmail("")
		unlock2()
	})

	t.Run("lockClientEmails dedupes and never deadlocks on overlap", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			// overlapping sets with a duplicate within one call
			u1 := c.lockClientEmails([]string{"x@p", "y@p", "x@p", ""})
			u1()
			u2 := c.lockClientEmails([]string{"y@p", "x@p"})
			u2()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("lockClientEmails deadlocked on duplicate/overlapping emails")
		}
	})
}
