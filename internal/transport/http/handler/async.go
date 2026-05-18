package handler

import "context"

// AsyncDispatcher is the structural shape every handler uses for
// post-response background work (email notifications, audit fan-out,
// best-effort fire-and-forget). The concrete implementation lives in
// internal/app — defined here as a local interface so the handler
// package doesn't need to import transport/http (cyclic) just to grab
// the same shape.
//
// nil-tolerant: handlers built without a dispatcher (e.g. unit tests
// for pure HTTP logic) fall back to synchronous behaviour or skip the
// async step.
type AsyncDispatcher interface {
	Context() context.Context
	Go(name string, fn func(ctx context.Context))
}
