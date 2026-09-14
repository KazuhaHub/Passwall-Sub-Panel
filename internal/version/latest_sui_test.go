package version

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Global release snapshots/transports are deliberately isolated rather than
// running these cases in parallel; no test requests the real GitHub API.
func isolateLatestSUI(t *testing.T) string {
	t.Helper()
	oldTag, oldClient, oldDir := LatestSUI(), httpClient, getCacheDir()
	latestSUIFetchMu.Lock()
	oldAt, oldTry, oldInflight, oldDone, oldErr := latestSUILastAt, latestSUILastTryAt, latestSUIInflight, latestSUIFlightDone, latestSUILastErr
	latestSUILastAt, latestSUILastTryAt, latestSUIInflight, latestSUIFlightDone, latestSUILastErr = time.Time{}, time.Time{}, false, nil, nil
	latestSUIFetchMu.Unlock()
	dir := t.TempDir()
	SetCacheDir(dir)
	SetLatestSUI("")
	t.Cleanup(func() {
		SetLatestSUI(oldTag)
		httpClient = oldClient
		SetCacheDir(oldDir)
		latestSUIFetchMu.Lock()
		latestSUILastAt, latestSUILastTryAt, latestSUIInflight, latestSUIFlightDone, latestSUILastErr = oldAt, oldTry, oldInflight, oldDone, oldErr
		latestSUIFetchMu.Unlock()
	})
	return dir
}

func TestLatestSUIReleaseWaitsForColdFlightWithoutCancellingItsOwner(t *testing.T) {
	isolateLatestSUI(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.6.2"}`))}, nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- RefreshLatestSUI(context.Background()) }()
	<-started
	// Metadata must wait for the cold background flight, not return an empty
	// tag immediately. The waiting request's deadline must not cancel its owner.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if tag, err := LatestSUIRelease(ctx); tag != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cold waiting metadata: tag=%q err=%v", tag, err)
	}
	close(release)
	if tag, err := LatestSUIRelease(context.Background()); tag != "v1.6.2" || err != nil {
		t.Errorf("completed shared metadata: tag=%q err=%v", tag, err)
	}
	if err := <-ownerDone; err != nil || calls.Load() != 1 {
		t.Fatalf("owner=%v GitHub calls=%d", err, calls.Load())
	}
}

func TestLatestSUIReleaseStartsBoundedColdFetchAndKeepsUsableCache(t *testing.T) {
	isolateLatestSUI(t)
	calls := 0
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > latestSUIFetchTimeout {
			t.Error("metadata request must use the eight-second total budget")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.6.2"}`))}, nil
	})}
	if tag, err := LatestSUIRelease(context.Background()); tag != "v1.6.2" || err != nil {
		t.Fatalf("cold metadata tag=%q err=%v", tag, err)
	}
	if tag, err := LatestSUIRelease(context.Background()); tag != "v1.6.2" || err != nil || calls != 1 {
		t.Fatalf("cached metadata tag=%q err=%v calls=%d", tag, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LatestSUIRelease(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metadata context: %v", err)
	}
}

func TestLatestSUIReleaseFailureIsNotAnEmptySuccessfulResult(t *testing.T) {
	isolateLatestSUI(t)
	calls := 0
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for range 2 {
		if tag, err := LatestSUIRelease(context.Background()); tag != "" || err == nil {
			t.Fatalf("unavailable metadata returned success: tag=%q err=%v", tag, err)
		}
	}
	if calls != 1 {
		t.Fatalf("failed metadata fetch was not throttled: %d requests", calls)
	}
	SetLatestSUI("v1.6.2")
	if tag, err := LatestSUIRelease(context.Background()); tag != "v1.6.2" || err != nil {
		t.Fatalf("last-known-good metadata lost after fetch failure: tag=%q err=%v", tag, err)
	}
}

func TestSUIUpdateAvailableRequiresANewerStableVersion(t *testing.T) {
	isolateLatestSUI(t)
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v1.6.1", "v1.6.2", true},
		{"1.6.2", "v1.6.2", false},
		{"v1.6.3", "v1.6.2", false},
		{"v1.6.2-beta.1", "v1.6.2", true},
		{"v1.7.0-beta.1", "v1.6.2", false},
		{"v1.6.1+build2", "v1.6.2", true},
		{"dev", "v1.6.2", false},
		{"unknown", "v1.6.2", false},
		{"", "v1.6.2", false},
		{"v1.6.1", "", false},
		{"v1.6.1", "invalid", false},
		{"v1.6.1", "v1.7.0-beta.1", false},
	} {
		SetLatestSUI(tc.latest)
		if got := IsSUIUpdateAvailable(tc.current); got != tc.want {
			t.Errorf("current=%q latest=%q: got %v want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestLatestSUIFetchSharesAndCachesOneOfficialStableSnapshot(t *testing.T) {
	dir := isolateLatestSUI(t)
	calls := 0
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != latestSUIURL || req.Method != http.MethodGet ||
			req.Header.Get("Accept") != "application/vnd.github+json" {
			t.Fatalf("unexpected request: %s %s headers=%v", req.Method, req.URL, req.Header)
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > latestSUIFetchTimeout {
			t.Fatal("release request must have a bounded deadline")
		}
		// Another refresh entering while this request is active must not make
		// its own request or wait for the flight it cannot influence.
		if err := RefreshLatestSUI(context.Background()); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.6.2","prerelease":false,"draft":false}`))}, nil
	})}
	if err := RefreshLatestSUI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := RefreshLatestSUI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || LatestSUI() != "v1.6.2" || LatestSUIRefreshAt().IsZero() || LatestSUIRefreshError() != nil {
		t.Fatalf("calls=%d tag=%q checked=%v error=%v", calls, LatestSUI(), LatestSUIRefreshAt(), LatestSUIRefreshError())
	}
	if _, err := os.Stat(filepath.Join(dir, latestSUICacheFile)); err != nil {
		t.Fatal(err)
	}
	SetLatestSUI("")
	if err := LoadLatestSUICache(); err != nil || LatestSUI() != "v1.6.2" {
		t.Fatalf("offline cache tag=%q error=%v", LatestSUI(), err)
	}
}

func TestLatestSUIFailedOrUnstableFetchKeepsSnapshotAndThrottlesRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"GitHub unavailable", "", http.StatusServiceUnavailable},
		{"invalid JSON", "{", http.StatusOK},
		{"empty tag", `{}`, http.StatusOK},
		{"unknown tag", `{"tag_name":"dev"}`, http.StatusOK},
		{"prerelease flag", `{"tag_name":"v1.6.3","prerelease":true}`, http.StatusOK},
		{"beta tag without flag", `{"tag_name":"v1.6.3-beta.1"}`, http.StatusOK},
		{"draft", `{"tag_name":"v1.6.3","draft":true}`, http.StatusOK},
		{"oversized", strings.Repeat(" ", (1<<20)+1), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateLatestSUI(t)
			SetLatestSUI("v1.6.2")
			if err := saveLatestSUICache("v1.6.2"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, latestSUICacheFile)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			httpClient = &http.Client{Transport: compatV4RoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			if err := RefreshLatestSUI(context.Background()); err == nil {
				t.Fatal("invalid/unavailable response must report an error")
			}
			if err := RefreshLatestSUI(context.Background()); err != nil || calls != 1 {
				t.Fatalf("failed fetch retry was not throttled: calls=%d err=%v", calls, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) || LatestSUI() != "v1.6.2" ||
				LatestSUIRefreshError() == nil || !LatestSUIRefreshAt().IsZero() {
				t.Fatalf("failure corrupted snapshot: tag=%q error=%v cacheError=%v", LatestSUI(), LatestSUIRefreshError(), err)
			}
		})
	}
}

func TestLatestSUICacheDoesNotTurnUnstableDataIntoAStableHint(t *testing.T) {
	dir := isolateLatestSUI(t)
	if err := LoadLatestSUICache(); err != nil {
		t.Fatal(err)
	}
	SetLatestSUI("v1.6.2")
	if err := saveLatestSUICache("v1.7.0-beta.1"); err != nil {
		t.Fatal(err)
	}
	if err := LoadLatestSUICache(); err == nil || LatestSUI() != "v1.6.2" {
		t.Fatalf("invalid cache changed snapshot: tag=%q error=%v", LatestSUI(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, latestSUICacheFile)); err != nil {
		t.Fatal(err)
	}
	SetCacheDir("")
	if err := LoadLatestSUICache(); err != nil {
		t.Fatal(err)
	}
}
