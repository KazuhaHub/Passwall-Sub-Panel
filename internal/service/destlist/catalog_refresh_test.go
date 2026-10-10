package destlist

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCategoryQueuePublishesPendingBeforeDispatchAndCoalesces(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	s := NewService(nil, cache)
	var work func(context.Context)
	dispatched := 0
	dispatch := func(_ string, job func(context.Context)) {
		dispatched++
		work = job
		_, _, state, err := s.CategoryView(t.Context())
		if !state.Refreshing || state.LastError != "" || !errors.Is(err, domain.ErrUnavailable) {
			t.Fatal("pending status was not visible before dispatcher starts")
		}
	}
	for range 2 {
		if err := s.QueueCategoryRefresh(t.Context(), dispatch); err != nil {
			t.Fatal(err)
		}
	}
	if dispatched != 1 {
		t.Fatal("duplicate queued download", dispatched)
	}
	work(t.Context())
	categories, at, state, err := s.CategoryView(t.Context())
	if err != nil || len(categories) == 0 || at.IsZero() || state.Refreshing || state.LastError != "" || calls.Load() != 2 {
		t.Fatalf("completed download did not publish settled catalog: %+v %v", state, err)
	}
}

func TestCategoryQueueFailureRetainsCatalogAndAllowsRetry(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	s := NewService(nil, cache)
	if err := s.RefreshCategories(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, before, _, _ := s.CategoryView(t.Context())
	cache.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("upstream unavailable") })
	var work func(context.Context)
	dispatch := func(_ string, job func(context.Context)) { work = job }
	if err := s.QueueCategoryRefresh(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	work(t.Context())
	categories, at, state, err := s.CategoryView(t.Context())
	if err != nil || len(categories) == 0 || !at.Equal(before) || state.Refreshing || state.LastError == "" {
		t.Fatalf("failure lost old catalog or refresh result: %+v %v", state, err)
	}
	if err := s.QueueCategoryRefresh(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	_, _, state, _ = s.CategoryView(t.Context())
	if !state.Refreshing || state.LastError != "" {
		t.Fatal("retry retained old failure", state)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	work(canceled)
	_, _, state, _ = s.CategoryView(t.Context())
	if state.Refreshing || state.LastError == "" {
		t.Fatal("canceled lifecycle work stayed pending", state)
	}
	if err := s.QueueCategoryRefresh(t.Context(), nil); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("accepted unowned work")
	}
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := s.QueueCategoryRefresh(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	work(t.Context())
	_, _, state, err = s.CategoryView(t.Context())
	if err != nil || state.Refreshing || state.LastError != "" {
		t.Fatal("successful retry retained prior failure", state, err)
	}
}
