package operationgate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExclusiveDrainsAndRespectsCancellation(t *testing.T) {
	g := New()
	ctx, release, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	timeout, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.Exclusive(timeout, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("exclusive crossed a live operation: %v called=%v", err, called)
	}
	if err := g.RunRead(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := g.Exclusive(ctx, func(context.Context) error { return nil }); err == nil {
		t.Fatal("nested upgrade must fail")
	}
	release()
	if err := g.Exclusive(context.Background(), func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatal(err)
	}
}
