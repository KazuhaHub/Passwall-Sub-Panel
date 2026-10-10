package destlist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

type drainingListStore struct {
	listStore
	stage                      string
	entered, canceled, release chan struct{}
}

func (s *drainingListStore) wait(ctx context.Context) error {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return ctx.Err()
}

func (s *drainingListStore) ListRefreshTargets(ctx context.Context) ([]domain.DestList, error) {
	if s.stage == "targets" {
		return nil, s.wait(ctx)
	}
	return s.listStore.ListRefreshTargets(ctx)
}

func (s *drainingListStore) CommitListRefresh(ctx context.Context, list domain.DestList, result domain.DestListRefresh, now time.Time) error {
	if s.stage == "commit" {
		return s.wait(ctx)
	}
	return s.listStore.CommitListRefresh(ctx, list, result, now)
}

func waitRefreshSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func TestListRefreshLoopDrainsInFlightWorkOnCancellation(t *testing.T) {
	for _, stage := range []string{"targets", "commit"} {
		t.Run(stage, func(t *testing.T) {
			store := &drainingListStore{listStore: listStore{lists: map[int64]domain.DestList{1: {ID: 1, Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/list"}}}, stage: stage, entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(store.release) }) }
			defer release()
			service := NewService(store, nil)
			gate := operationgate.New()
			service.SetOperationGate(gate)
			service.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("domain:example.com\n"))}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var wg sync.WaitGroup
			service.Start(ctx, &wg, nil, nil)
			waitRefreshSignal(t, store.entered, "refresh work did not start")
			cancel()
			waitRefreshSignal(t, store.canceled, "in-flight work did not receive cancellation")
			gateCtx, gateCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			err := gate.Exclusive(gateCtx, func(context.Context) error { t.Error("exclusive switch crossed canceled database cleanup"); return nil })
			gateCancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("exclusive switch did not wait for cleanup: %v", err)
			}
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
				t.Error("tracked worker drained before its in-flight database operation exited")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			waitRefreshSignal(t, done, "tracked worker did not drain after in-flight operation exited")
			if err := gate.Exclusive(t.Context(), func(context.Context) error { return nil }); err != nil {
				t.Fatalf("refresh kept admission after draining: %v", err)
			}
		})
	}
}

func TestListRefreshWaitsForExclusiveOperationBeforeIO(t *testing.T) {
	service, store := newListService(t)
	store.lists[1] = domain.DestList{ID: 1, Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/list"}
	service.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Error("download crossed exclusive operation")
		return nil, errors.New("unexpected request")
	})
	gate := operationgate.New()
	service.SetOperationGate(gate)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = gate.Exclusive(t.Context(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	defer func() { close(release); waitRefreshSignal(t, done, "exclusive operation did not finish") }()
	waitRefreshSignal(t, entered, "exclusive operation did not start")
	for _, refresh := range []func(context.Context) error{func(ctx context.Context) error { return service.RefreshDue(ctx, 24) }, func(ctx context.Context) error { return service.RefreshList(ctx, 1) }} {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		err := refresh(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refresh failed to wait for exclusive admission: %v", err)
		}
	}
}

func TestGeositeRefreshDrainsCanceledDownload(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	cache.fetcher.client.Transport = fetchTransport(func(request *http.Request) (*http.Response, error) {
		close(entered)
		<-request.Context().Done()
		close(canceled)
		<-release
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = cache.Refresh(ctx); close(done) }()
	waitRefreshSignal(t, entered, "catalog download did not start")
	cancel()
	waitRefreshSignal(t, canceled, "catalog download did not receive cancellation")
	select {
	case <-done:
		t.Error("refresh returned before canceled download exited")
	case <-time.After(100 * time.Millisecond):
	}
	finish()
	waitRefreshSignal(t, done, "refresh did not exit after download drained")
}
