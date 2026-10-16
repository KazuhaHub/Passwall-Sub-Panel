package user

import (
	"context"
	"sync"
)

type groupMemberResyncRun struct{ dirty bool }

// Group changes can arrive from many independent node receipts. Keep one
// background membership pass per group, without delaying its first dispatch.
// Work reads current persisted members and definitions each time; no membership
// result or completed run is cached. A notification received after that read
// starts requires a subsequent full pass, even if the current pass fails.
type groupMemberResyncs struct {
	mu     sync.Mutex
	active map[int64]*groupMemberResyncRun
}

func (q *groupMemberResyncs) schedule(groupID int64, dispatch func(func(context.Context)), work func(context.Context)) {
	q.mu.Lock()
	if current := q.active[groupID]; current != nil {
		current.dirty = true
		q.mu.Unlock()
		return
	}
	current := &groupMemberResyncRun{}
	if q.active == nil {
		q.active = make(map[int64]*groupMemberResyncRun)
	}
	q.active[groupID] = current
	q.mu.Unlock()
	dispatch(func(ctx context.Context) {
		// Cancellation and recovered background panics must not leave a group
		// permanently scheduled. A later request may already own a new run.
		defer func() {
			q.mu.Lock()
			if q.active[groupID] == current {
				delete(q.active, groupID)
			}
			q.mu.Unlock()
		}()
		for {
			q.mu.Lock()
			current.dirty = false
			q.mu.Unlock()
			work(ctx)
			q.mu.Lock()
			again := current.dirty && ctx.Err() == nil
			if !again {
				// Release under the same lock that receives notifications: a
				// change at completion either marks this pass or starts a new one.
				delete(q.active, groupID)
			}
			q.mu.Unlock()
			if !again {
				return
			}
		}
	})
}
