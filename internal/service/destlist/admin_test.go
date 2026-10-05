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

func TestListQueueRefreshOwnsStatusUntilLifecycleWorkCompletes(t *testing.T) {
	s, store := newListService(t)
	store.lists[1] = domain.DestList{ID: 1, Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://example.test/rules", UpdatedAt: time.Now()}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("domain:example.test\n"))}, nil
	})
	var queued func(context.Context)
	if err := s.QueueRefresh(t.Context(), 1, func(_ string, work func(context.Context)) { queued = work }); err != nil || queued == nil || !s.IsRefreshing(1) {
		t.Fatal("queued refresh omitted lifecycle status")
	}
	finished := make(chan struct{})
	go func() { queued(t.Context()); close(finished) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not enter download")
	}
	if !s.IsRefreshing(1) {
		t.Fatal("in-flight download lost refreshing status")
	}
	once.Do(func() { close(release) })
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not drain")
	}
	list, err := store.GetList(t.Context(), 1)
	if err != nil || list.EntryCount != 1 || list.LastFetchedAt == nil || s.IsRefreshing(1) {
		t.Fatal("refresh status outlived or preceded persisted result")
	}
	if err := s.QueueRefresh(t.Context(), 1, nil); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing dispatcher accepted unowned background work")
	}
}

func TestListSaveAdmissionCoversDownloadAndValidationFailure(t *testing.T) {
	s, store := newListService(t)
	gate := operationgate.New()
	s.SetOperationGate(gate)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("domain:example.test\n"))}, nil
	})
	problem := errors.New("fleet validation failed")
	s.SetSaveValidator(func(context.Context, domain.DestList) error { return problem })
	list := domain.DestList{Name: "new source", Kind: domain.DestListRemote, SourceURL: "https://example.test/rules"}
	result := make(chan error, 1)
	go func() { result <- s.Save(t.Context(), &list, time.Time{}) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not enter download")
	}
	exclusive := make(chan struct{})
	go func() { _ = gate.Exclusive(t.Context(), func(context.Context) error { close(exclusive); return nil }) }()
	select {
	case <-exclusive:
		t.Fatal("backend switch crossed list save I/O")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := <-result; !errors.Is(err, problem) {
		t.Fatal("save did not preserve validation error")
	}
	select {
	case <-exclusive:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not release admission")
	}
	if store.saves != 0 || list.ID != 0 || list.LastFetchedAt != nil || len(list.Entries) != 0 {
		t.Fatal("failed validation changed caller or persisted definition")
	}
}
