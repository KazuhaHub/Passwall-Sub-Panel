package destlist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func refreshMetricValue(result string) int64 {
	for _, counter := range metrics.Take().Counters {
		if counter.Name == "psp_dest_list_refresh_total{result="+result+"}" {
			return counter.Value
		}
	}
	return 0
}

type metricsFailingRefreshStore struct{ DefinitionStore }

func (s metricsFailingRefreshStore) CommitListRefresh(context.Context, domain.DestList, domain.DestListRefresh, time.Time) error {
	return domain.ErrUnavailable
}

func TestListRefreshMetricsOnlyCountCommittedFilteringAndNotPreviews(t *testing.T) {
	s, store := newListService(t)
	fixture := strings.Replace(geoFixture, "domain:legacy.example.com:@cn:@ads", "domain:com:@cn:@ads", 1)
	var downloads atomic.Int32
	s.cache.fetcher.client.Transport = cacheFixtureTransport(fixture, geoChecksum(fixture), &downloads)
	store.lists[1] = domain.DestList{ID: 1, Kind: domain.DestListGeosite, GeositeCategory: "finance"}
	before := refreshMetricValue("broad")
	if err := s.RefreshList(t.Context(), 1); err != nil || refreshMetricValue("broad") != before+1 {
		t.Fatal("successfully filtered category not counted")
	}
	current, err := store.GetList(t.Context(), 1)
	if err != nil || current.ParseReport == nil || current.ParseReport.IgnoredBroad != 1 || strings.Contains(string(current.Entries), "domain:com\n") {
		t.Fatal("category filtering fixture retained broad entry")
	}
	if _, err := s.Preview(t.Context(), current); err != nil || refreshMetricValue("broad") != before+1 {
		t.Fatal("preview counted as refresh")
	}
	s.store = metricsFailingRefreshStore{DefinitionStore: store}
	failed := refreshMetricValue("failed")
	if err := s.RefreshList(t.Context(), 1); !errors.Is(err, domain.ErrUnavailable) || refreshMetricValue("failed") != failed+1 || refreshMetricValue("broad") != before+1 {
		t.Fatal("failed commit counted as successfully handled broad entries")
	}
}
func TestListRefreshMetricsDescribeCompletedContentAndFailedAttempts(t *testing.T) {
	s, store := newListService(t)
	store.lists[1] = domain.DestList{ID: 1, Kind: domain.DestListRemote, SourceURL: "https://rules.example.com/list"}
	body := "example.com\n"
	var failure error
	s.fetcher.client.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
		if failure != nil {
			return nil, failure
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	for _, want := range []string{"updated", "unchanged", "broad", "failed"} {
		before := refreshMetricValue(want)
		if want == "broad" {
			body += "domain:com\n"
		}
		if want == "failed" {
			failure = errors.New("fixture download failed")
		}
		err := s.RefreshList(t.Context(), 1)
		if (err != nil) != (want == "failed" || want == "broad") || refreshMetricValue(want) != before+1 {
			t.Fatalf("refresh metric omitted outcome %s", want)
		}
		if want == "broad" {
			current, _ := store.GetList(t.Context(), 1)
			if string(current.Entries) != "domain:example.com\n" {
				t.Fatal("remote broad rejection replaced usable content")
			}
		}
	}
}
