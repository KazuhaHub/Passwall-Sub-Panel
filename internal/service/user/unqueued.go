package user

import "fmt"

// errUnqueuedPush describes the one case where "the push failed, so it was
// queued for retry" is not true: the push failed AND the retry could not be
// recorded.
//
// Returning nil there is the failure shape this repository keeps producing.
// It bites hardest on this package's paths, because they are the ones that cut
// somebody off: PSP's own row is committed, so the user drops out of future
// subscription fetches — but the panel-side client is what actually refuses a
// connection, and an already-configured client keeps working until that lands.
// So the account looks disabled and is not, with nothing pending and nothing
// to notice it. A log line is not a retry.
//
// When the enqueue SUCCEEDS, nil stays correct: the work is durable and the
// sync-task loop converges it.
//
// The local row is already written and every operation guarded this way is
// idempotent, so the caller's correct response is to repeat the same call. The
// message says that — an error that does not say what to do next reads as "it
// broke, do not touch it", which would leave the account uncut.
func errUnqueuedPush(op string, pushErr, taskErr error) error {
	return fmt.Errorf(
		"%s: the panel was not updated (%v) and the retry could not be queued (%v); "+
			"the change is saved locally — repeat this action to converge the panel",
		op, pushErr, taskErr)
}
