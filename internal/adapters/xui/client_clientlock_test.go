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
	// Unique baseURL so the process-global per-(backend,email) lock map doesn't
	// collide with other tests in this package.
	c := &Client{baseURL: "test://lockunit"}

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

	t.Run("two *Clients on the SAME backend share the lock (duplicate-panel fix)", func(t *testing.T) {
		// The exact production topology: one 3X-UI server registered as two PSP
		// panels → two distinct *Client instances. They MUST serialize writes to
		// the same client, or they race on the backend's client_inbounds.
		c1 := &Client{baseURL: "test://dup-backend"}
		c2 := &Client{baseURL: "test://dup-backend"}
		var inCrit, maxConc int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			cl := c1
			if i%2 == 1 {
				cl = c2
			}
			wg.Add(1)
			go func(c *Client) {
				defer wg.Done()
				unlock := c.lockClientEmail("dup@psp.local")
				defer unlock()
				n := atomic.AddInt32(&inCrit, 1)
				if n > atomic.LoadInt32(&maxConc) {
					atomic.StoreInt32(&maxConc, n)
				}
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt32(&inCrit, -1)
			}(cl)
		}
		wg.Wait()
		if maxConc != 1 {
			t.Fatalf("two *Clients on the same backend+email must serialize, got max concurrency %d", maxConc)
		}
	})

	t.Run("different backends do NOT block each other", func(t *testing.T) {
		cA := &Client{baseURL: "test://backendA"}
		cB := &Client{baseURL: "test://backendB"}
		start := make(chan struct{})
		var ready, peak int32
		var wg sync.WaitGroup
		for _, c := range []*Client{cA, cB} {
			wg.Add(1)
			go func(c *Client) {
				defer wg.Done()
				unlock := c.lockClientEmail("same@psp.local")
				defer unlock()
				atomic.AddInt32(&ready, 1)
				<-start
				atomic.AddInt32(&peak, 1)
			}(c)
		}
		for atomic.LoadInt32(&ready) < 2 {
			time.Sleep(time.Millisecond)
		}
		close(start)
		wg.Wait()
		if peak != 2 {
			t.Fatalf("distinct backends must not block on a shared email, got %d", peak)
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
