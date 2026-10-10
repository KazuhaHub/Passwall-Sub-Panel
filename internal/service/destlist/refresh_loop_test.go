package destlist

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestListRefreshLoopTracksShutdownAndHotSettings(t *testing.T) {
	s, _ := newListService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	hours := 168
	rounds := make(chan int, 8)
	read := func(context.Context) (int, error) { mu.Lock(); v := hours; mu.Unlock(); rounds <- v; return v, nil }
	s.Start(ctx, &wg, read, func(err error) { t.Errorf("refresh failed: %v", err) })
	s.Start(ctx, &wg, read, nil) // lifecycle registration must not spawn twice
	select {
	case v := <-rounds:
		if v != 168 {
			t.Fatalf("initial setting wrong: %d", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop not started")
	}
	mu.Lock()
	hours = 6
	mu.Unlock()
	s.NotifySettingsChanged()
	select {
	case v := <-rounds:
		if v != 6 {
			t.Fatalf("hot setting not reread: %d", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hot update did not wake loop")
	}
	cancel()
	stopped := make(chan struct{})
	go func() { wg.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("tracked loop did not stop")
	}
	select {
	case <-rounds:
		t.Fatal("duplicate loop running")
	default:
	}
}

func TestListRefreshLoopSurvivesSettingsFailure(t *testing.T) {
	s, _ := newListService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	problem := errors.New("settings unavailable")
	var mu sync.Mutex
	failed := true
	rounds := make(chan struct{}, 4)
	read := func(context.Context) (int, error) {
		mu.Lock()
		v := failed
		mu.Unlock()
		rounds <- struct{}{}
		if v {
			return 0, problem
		}
		return 24, nil
	}
	reported := make(chan error, 1)
	s.Start(ctx, &wg, read, func(err error) { reported <- err })
	select {
	case err := <-reported:
		if !errors.Is(err, problem) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("settings failure not reported")
	}
	<-rounds
	mu.Lock()
	failed = false
	mu.Unlock()
	s.NotifySettingsChanged()
	select {
	case <-rounds:
	case <-time.After(3 * time.Second):
		t.Fatal("failure killed loop")
	}
	cancel()
	wg.Wait()
}
