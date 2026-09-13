package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

func TestDetachedDispatcherOwnsAdmissionUntilItsLastWrite(t *testing.T) {
	gate := operationgate.New()
	var wg sync.WaitGroup
	dispatcher := &asyncDispatcher{ctx: context.Background(), wg: &wg, gate: gate}
	started, finishIO, written := make(chan struct{}), make(chan struct{}), make(chan struct{})
	dispatcher.Go("test.old-backend-response", func(ctx context.Context) {
		close(started)
		<-finishIO
		// A nested service does not deadlock upgrading/reacquiring admission.
		if err := gate.RunRead(ctx, func(context.Context) error { close(written); return nil }); err != nil {
			t.Error(err)
		}
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.Exclusive(ctx, func(context.Context) error { t.Error("switch crossed a detached response before DB write"); return nil }); err == nil {
		t.Fatal("exclusive did not wait")
	}
	close(finishIO)
	wg.Wait()
	<-written
	if err := gate.Exclusive(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
