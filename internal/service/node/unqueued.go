package node

import "fmt"

// errUnqueuedPush describes the one case where "push failed, so it was queued
// for retry" is not true: the push failed AND the retry could not be recorded.
//
// Returning nil there is the failure shape this repository keeps producing —
// the caller is told the change is in effect while nothing reached the panel
// and nothing ever will. A log line is not a retry. When the enqueue SUCCEEDS,
// nil remains correct: the work is durably queued and will converge.
//
// The local row is already committed and every operation guarded this way is
// idempotent, so the caller's correct response is to repeat the same call. The
// message says that, because an error that does not say what to do next gets
// read as "it broke, leave it alone" — which would leave the panel diverged.
func errUnqueuedPush(op string, pushErr, taskErr error) error {
	return fmt.Errorf(
		"%s: the panel was not updated (%v) and the retry could not be queued (%v); "+
			"the change is saved locally — repeat this action to converge the panel",
		op, pushErr, taskErr)
}
