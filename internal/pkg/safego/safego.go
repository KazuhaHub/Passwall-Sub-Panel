// Package safego launches goroutines with a panic-recovery shield and
// optional WaitGroup tracking.
//
// Every background loop, async push, and fan-out worker in the panel
// runs here so a single unhandled nil-deref or map race in a 3X-UI
// response can't take the whole process down. The recovered panic is
// logged with the goroutine's identifying name plus a stack snapshot.
//
// Passing a *sync.WaitGroup lets App.Shutdown wait for in-flight
// workers to finish — Add(1) happens BEFORE the goroutine starts so
// Wait() never races a not-yet-incremented counter.
package safego

import (
	"runtime/debug"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Go runs fn in a new goroutine. Any panic is caught, logged, and
// swallowed — the caller never sees it. The name string appears in the
// log line so we can tell which worker died.
func Go(name string, fn func()) {
	go func() {
		defer recoverAndLog(name)
		fn()
	}()
}

// GoTracked is Go plus WaitGroup tracking. The caller's wg.Wait()
// blocks until fn returns OR a panic is recovered — whichever happens
// first. Returning nil wg is tolerated and behaves identically to Go.
func GoTracked(wg *sync.WaitGroup, name string, fn func()) {
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer recoverAndLog(name)
		fn()
	}()
}

// Run executes fn synchronously with the same recover shield. Useful
// when the caller already manages its own goroutine (e.g. wraps work
// inside an existing sync.WaitGroup body) but still wants the panic
// safety net.
func Run(name string, fn func()) {
	defer recoverAndLog(name)
	fn()
}

// Recover is meant to be deferred at the top of an existing goroutine
// body. Use it when retrofitting recover into code that already
// orchestrates its own sync.WaitGroup / semaphore / channel plumbing:
//
//	go func() {
//	    defer safego.Recover("name")
//	    defer wg.Done()
//	    ...
//	}()
//
// `defer wg.Done()` should still come after Recover so the recover
// shield fires first when stack unwinds.
func Recover(name string) {
	recoverAndLog(name)
}

func recoverAndLog(name string) {
	r := recover()
	if r == nil {
		return
	}
	log.Error("goroutine panic recovered",
		"worker", name,
		"panic", r,
		"stack", string(debug.Stack()))
}
