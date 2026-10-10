package destlist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type listStore struct {
	mu             sync.Mutex
	lists          map[int64]domain.DestList
	saves, commits int
}

func (s *listStore) GetList(_ context.Context, id int64) (domain.DestList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.lists[id]
	if !ok {
		return v, domain.ErrNotFound
	}
	return v, nil
}
func (s *listStore) ListRefreshTargets(context.Context) ([]domain.DestList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r []domain.DestList
	for _, v := range s.lists {
		if v.Kind != domain.DestListCustom {
			r = append(r, v)
		}
	}
	return r, nil
}
func (s *listStore) SaveList(_ context.Context, v *domain.DestList, expected, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.lists[v.ID]; ok && !old.UpdatedAt.Equal(expected) {
		return domain.ErrConflict
	}
	if v.ID == 0 {
		v.ID = int64(len(s.lists) + 1)
	}
	s.saves++
	v.UpdatedAt = now
	s.lists[v.ID] = *v
	return nil
}
func (s *listStore) CommitListRefresh(_ context.Context, c domain.DestList, result domain.DestListRefresh, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.lists[c.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if !old.UpdatedAt.Equal(c.UpdatedAt) || old.SourceURL != c.SourceURL || old.GeositeCategory != c.GeositeCategory || old.GeositeAttrs != c.GeositeAttrs {
		return domain.ErrConflict
	}
	s.commits++
	old.LastError = result.LastError
	old.UpdatedAt = now
	if result.LastError == "" {
		old.Entries = result.Entries
		old.EntryCount = result.EntryCount
		old.RegexpCount = result.RegexpCount
		old.ContentSHA256 = result.ContentSHA256
		old.ParseReport = result.ParseReport
		old.LastFetchedAt = &now
	}
	s.lists[c.ID] = old
	return nil
}
func newListService(t *testing.T) (*Service, *listStore) {
	t.Helper()
	store := &listStore{lists: map[int64]domain.DestList{}}
	return NewService(store, NewGeositeCache(t.TempDir())), store
}

func TestListPreviewDoesNotWriteOrDownloadMissingCatalog(t *testing.T) {
	s, store := newListService(t)
	p, err := s.Preview(t.Context(), domain.DestList{Kind: domain.DestListCustom, SourceText: []byte("#comment\nExample.COM\ndomain:com\n")})
	if err != nil || p.Parsed.EntryCount != 1 || p.Parsed.Report.IgnoredBroad != 1 || store.saves != 0 || store.commits != 0 {
		t.Fatalf("preview wrong/wrote definition: %+v / %v", p, err)
	}
	var calls atomic.Int32
	s.cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if _, err := s.Preview(t.Context(), domain.DestList{Kind: domain.DestListGeosite, GeositeCategory: "finance"}); !errors.Is(err, domain.ErrUnavailable) || calls.Load() != 0 {
		t.Fatalf("missing category auto-downloaded: %v calls=%d", err, calls.Load())
	}
	if err := s.cache.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err = s.Preview(t.Context(), domain.DestList{Kind: domain.DestListGeosite, GeositeCategory: "finance", GeositeAttrs: "cn,ads"})
	if err != nil || p.Parsed.EntryCount != 2 || calls.Load() != 2 {
		t.Fatalf("preview changed attribute semantics/downloaded: %+v / %v", p, err)
	}
}

func TestListSaveCustomPreservesOriginalTextAndReport(t *testing.T) {
	s, store := newListService(t)
	source := "#comment\nExample.COM\ndomain:com\n"
	list := domain.DestList{Name: "custom", Kind: domain.DestListCustom, SourceText: []byte(source)}
	if err := s.Save(t.Context(), &list, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if string(list.SourceText) != source || string(list.Entries) != "domain:example.com\n" || list.ParseReport == nil || list.ParseReport.IgnoredBroad != 1 || store.saves != 1 {
		t.Fatalf("source/report lost: %+v", list)
	}
	list.Kind = domain.DestListRemote
	list.SourceURL = "https://rules.example.com/list"
	if err := s.Save(t.Context(), &list, list.UpdatedAt); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("existing list type mutable: %v", err)
	}
}

func TestListRemoteDownloadFailureCanSavePendingButBroadInputCannot(t *testing.T) {
	s, store := newListService(t)
	s.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	list := domain.DestList{Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/list?token=secret"}
	if err := s.Save(t.Context(), &list, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if list.LastFetchedAt != nil || list.EntryCount != 0 || list.ParseReport != nil || list.LastError == "" || strings.Contains(list.LastError, "secret") {
		t.Fatalf("failed first fetch falsely ready: %+v", list)
	}
	s.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("example.com\ncom\n")), Header: http.Header{}}, nil
	})
	bad := domain.DestList{Name: "broad", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/broad"}
	if err := s.Save(t.Context(), &bad, time.Time{}); !errors.Is(err, domain.ErrValidation) || store.saves != 1 {
		t.Fatalf("broad remote persisted: %v", err)
	}
}

func TestListRefreshEmptyCategoryPreservesUsableReportAndEntries(t *testing.T) {
	s, store := newListService(t)
	version := time.UnixMilli(1791000000000).UTC()
	report := &domain.DestParseReport{Accepted: 1}
	store.lists[1] = domain.DestList{ID: 1, Name: "wide", Kind: domain.DestListGeosite, GeositeCategory: "wide", UpdatedAt: version, LastFetchedAt: &version, Entries: []byte("domain:example.com\n"), ContentSHA256: "old", EntryCount: 1, ParseReport: report}
	var calls atomic.Int32
	s.cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := s.RefreshList(t.Context(), 1); err == nil || err.Error() != "dest_list_empty_after_filter" {
		t.Fatalf("empty selection accepted: %v", err)
	}
	v, _ := store.GetList(t.Context(), 1)
	if string(v.Entries) != "domain:example.com\n" || v.ContentSHA256 != "old" || v.ParseReport != report || !v.LastFetchedAt.Equal(version) || v.LastError != "dest_list_empty_after_filter" {
		t.Fatalf("usable list overwritten: %+v", v)
	}
}

func TestListSlowRefreshCannotCommitAfterSourceEditOrDeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "delete"}[deleted], func(t *testing.T) {
			s, store := newListService(t)
			version := time.UnixMilli(1791000000000).UTC()
			store.lists[1] = domain.DestList{ID: 1, Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/old", UpdatedAt: version}
			s.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				store.mu.Lock()
				if deleted {
					delete(store.lists, 1)
				} else {
					v := store.lists[1]
					v.SourceURL = "https://rules.example.com/new"
					v.UpdatedAt = version.Add(time.Millisecond)
					store.lists[1] = v
				}
				store.mu.Unlock()
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("example.com"))}, nil
			})
			err := s.RefreshList(t.Context(), 1)
			want := domain.ErrConflict
			if deleted {
				want = domain.ErrNotFound
			}
			if !errors.Is(err, want) || store.commits != 0 {
				t.Fatalf("old source committed: %v commits=%d", err, store.commits)
			}
		})
	}
}

func TestListRefreshRoundDownloadsCatalogOnceAndObservesHours(t *testing.T) {
	s, store := newListService(t)
	now := time.UnixMilli(1791000000000).UTC()
	previous := now.Add(-24 * time.Hour)
	s.now = func() time.Time { return now }
	for id := int64(1); id <= 2; id++ {
		store.lists[id] = domain.DestList{ID: id, Name: "finance", Kind: domain.DestListGeosite, GeositeCategory: "finance", UpdatedAt: previous, LastFetchedAt: &previous}
	}
	var calls atomic.Int32
	s.cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := s.RefreshDue(t.Context(), 168); err != nil || calls.Load() != 0 {
		t.Fatalf("not-due refreshed: %v calls=%d", err, calls.Load())
	}
	if err := s.RefreshDue(t.Context(), 6); err != nil || calls.Load() != 2 || store.commits != 2 {
		t.Fatalf("round didn't share catalog/read interval: %v calls=%d commits=%d", err, calls.Load(), store.commits)
	}
	if err := s.RefreshDue(t.Context(), 6); err != nil || calls.Load() != 2 {
		t.Fatalf("immediate repeat downloaded: %v calls=%d", err, calls.Load())
	}
}

func TestListFailedRoundsAreBoundedAndChangedSourceRetries(t *testing.T) {
	s, store := newListService(t)
	now := time.UnixMilli(1791000000000).UTC()
	s.now = func() time.Time { return now }
	store.lists[1] = domain.DestList{ID: 1, Name: "pending", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/old", UpdatedAt: now}
	var calls atomic.Int32
	s.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := s.RefreshDue(t.Context(), 24); err == nil || calls.Load() != 1 {
		t.Fatalf("failure not recorded: %v calls=%d", err, calls.Load())
	}
	if err := s.RefreshDue(t.Context(), 24); err != nil || calls.Load() != 1 {
		t.Fatalf("failed source hammered: %v calls=%d", err, calls.Load())
	}
	store.mu.Lock()
	v := store.lists[1]
	v.SourceURL = "https://rules.example.com/new"
	v.UpdatedAt = v.UpdatedAt.Add(time.Millisecond)
	store.lists[1] = v
	store.mu.Unlock()
	if err := s.RefreshDue(t.Context(), 24); err == nil || calls.Load() != 2 {
		t.Fatalf("new source suppressed by old failure: %v calls=%d", err, calls.Load())
	}
	now = now.Add(6 * time.Hour)
	if err := s.RefreshDue(t.Context(), 6); err == nil || calls.Load() != 3 {
		t.Fatalf("hot shorter interval ignored: %v calls=%d", err, calls.Load())
	}
}

func TestListEmptyRemoteRefreshRetainsSuccessfulContent(t *testing.T) {
	s, store := newListService(t)
	version := time.UnixMilli(1791000000000).UTC()
	report := &domain.DestParseReport{Accepted: 1}
	store.lists[1] = domain.DestList{ID: 1, Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/list", UpdatedAt: version, LastFetchedAt: &version, Entries: []byte("domain:example.com\n"), EntryCount: 1, ContentSHA256: "old", ParseReport: report}
	s.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("#comment\n"))}, nil
	})
	if err := s.RefreshList(t.Context(), 1); err == nil || err.Error() != "dest_list_empty" {
		t.Fatalf("empty remote committed: %v", err)
	}
	v, _ := store.GetList(t.Context(), 1)
	if v.EntryCount != 1 || v.ContentSHA256 != "old" || v.ParseReport != report || !v.LastFetchedAt.Equal(version) || v.LastError != "dest_list_empty" {
		t.Fatalf("old usable content lost: %+v", v)
	}
}
