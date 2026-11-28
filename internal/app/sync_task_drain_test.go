package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A tick used to run one batch of 20 per processor and then wait 30 seconds, so
// a backlog of 500 tasks took more than 12 minutes to clear after a panel came
// back. A tick now keeps taking batches while they come back full and clean.

type batchScript struct {
	results []batchResult
	calls   int
}

type batchResult struct {
	ran, failed int
	err         error
}

func (b *batchScript) run(context.Context, int) (int, int, error) {
	r := b.results[b.calls]
	b.calls++
	return r.ran, r.failed, r.err
}

func TestDrainDueTasks(t *testing.T) {
	boom := errors.New("list due failed")
	full, short, failing := batchResult{ran: 20}, batchResult{ran: 7}, batchResult{ran: 20, failed: 1}
	for _, c := range []struct {
		name    string
		results []batchResult
		calls   int
		err     error
	}{
		{"an empty queue is one batch", []batchResult{{}}, 1, nil},
		{"a short batch ends the tick", []batchResult{short, full}, 1, nil},
		{"full clean batches keep going until a short one", []batchResult{full, full, short, full}, 3, nil},
		// The panel is still down: every further batch would wait on it again.
		{"a batch with a failure ends the tick", []batchResult{full, failing, full}, 2, nil},
		{"an error ends the tick and is returned", []batchResult{full, {err: boom}, full}, 2, boom},
	} {
		t.Run(c.name, func(t *testing.T) {
			script := &batchScript{results: c.results}
			err := drainDueTasks(context.Background(), 20, time.Hour, time.Now, script.run)
			if !errors.Is(err, c.err) || (c.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if script.calls != c.calls {
				t.Fatalf("ran %d batches, want %d", script.calls, c.calls)
			}
		})
	}
}

// The drain yields when its time is up, so the loop's other duties (and the
// shutdown check) come round even while the queue never runs dry.
func TestDrainDueTasksStopsAtItsBudget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	script := &batchScript{results: []batchResult{{ran: 20}, {ran: 20}, {ran: 20}, {ran: 20}}}
	clock := func() time.Time { now = now.Add(10 * time.Second); return now }
	if err := drainDueTasks(context.Background(), 20, 15*time.Second, clock, script.run); err != nil {
		t.Fatal(err)
	}
	// The clock reads 10s at the start, then 20s and 30s after the batches:
	// 10s gone after the first (go on), 20s after the second (stop).
	if script.calls != 2 {
		t.Fatalf("ran %d batches inside a 15s budget of 10s batches, want 2", script.calls)
	}
}

func TestDrainDueTasksStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	script := &batchScript{results: []batchResult{{ran: 20}, {ran: 20}}}
	err := drainDueTasks(ctx, 20, time.Hour, time.Now, func(c context.Context, n int) (int, int, error) {
		cancel()
		return script.run(c, n)
	})
	if err != nil || script.calls != 1 {
		t.Fatalf("a cancelled drain must stop after the batch in flight (calls=%d err=%v)", script.calls, err)
	}
}
