package destlist

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

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/sync/singleflight"
)

func cacheFixtureTransport(body string, checksum []byte, calls *atomic.Int32) http.RoundTripper {
	return fetchTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		data := []byte(body)
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			data = checksum
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})
}

func TestGeositeCacheStartsUnavailableRefreshesAndRestoresWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	cache := NewGeositeCache(dir)
	if c, stamp, err := cache.Cached(); c != nil || !stamp.IsZero() || !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing cache ready: %+v %v %v", c, stamp, err)
	}
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, stamp, err := cache.Cached()
	if err != nil || stamp.IsZero() || c == nil || calls.Load() != 2 {
		t.Fatalf("refresh not committed: %v %v calls=%d", stamp, err, calls.Load())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "destlists", "dlc_plain.yml"))
	if err != nil || string(raw) != geoFixture {
		t.Fatalf("bad disk cache: %v", err)
	}
	restored := NewGeositeCache(dir)
	c, _, err = restored.Cached()
	if err != nil || c == nil {
		t.Fatalf("restart lost verified cache: %v", err)
	}
	if p, err := c.Select("finance", []string{"cn"}); err != nil || p.EntryCount != 3 {
		t.Fatalf("restored selection changed: %+v / %v", p, err)
	}
}

func TestGeositeCacheFailedRefreshPreservesMemoryDiskAndTimestamp(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, stamp, _ := cache.Cached()
	for _, tt := range []struct {
		body     string
		checksum []byte
	}{
		{geoFixture, geoChecksum("different")},
		{"lists: [", geoChecksum("lists: [")},
		{strings.Repeat("x", MaxRemoteBytes+1), geoChecksum("oversize")},
		{geoFixture, bytes.Repeat([]byte("x"), 4097)},
	} {
		cache.fetcher.client.Transport = cacheFixtureTransport(tt.body, tt.checksum, &calls)
		if err := cache.Refresh(context.Background()); err == nil {
			t.Fatal("bad refresh accepted")
		}
		now, nowStamp, err := cache.Cached()
		raw, fileErr := os.ReadFile(cache.path)
		if err != nil || old != now || !stamp.Equal(nowStamp) || fileErr != nil || string(raw) != geoFixture || cache.LastError() == "" {
			t.Fatalf("failed refresh corrupted usable cache: %v %v", err, fileErr)
		}
	}
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := cache.Refresh(context.Background()); err != nil || cache.LastError() != "" {
		t.Fatalf("success did not clear error: %v", err)
	}
}

func TestGeositeCacheAtomicReplacementFailurePreservesMemory(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, stamp, _ := cache.Cached()
	blocked := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cache.path = blocked
	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("directory target silently accepted")
	}
	c, nowStamp, err := cache.Cached()
	if err != nil || c != old || !stamp.Equal(nowStamp) {
		t.Fatal("memory replaced before atomic disk commit")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(blocked), ".dlc-*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary file leaked: %v / %v", files, err)
	}
}

func TestGeositeCacheRefreshCoalescesAndReadsStayAvailable(t *testing.T) {
	cache := NewGeositeCache(t.TempDir())
	var calls atomic.Int32
	cache.fetcher.client.Transport = cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	cache.fetcher.client.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, ".sha256sum") {
			close(entered)
			<-release
		}
		return cacheFixtureTransport(geoFixture, geoChecksum(geoFixture), &calls).RoundTrip(r)
	})
	first := cache.beginRefresh(context.Background())
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh never started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cache.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter blocked/accepted: %v", err)
	}
	ready := make(chan error, 1)
	go func() { _, _, err := cache.Cached(); ready <- err }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("network I/O held catalog lock")
	}
	followers := make([]<-chan singleflight.Result, 7)
	for i := range followers {
		followers[i] = cache.beginRefresh(context.Background())
	}
	release <- struct{}{}
	if outcome := <-first; outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	for _, result := range followers {
		if outcome := <-result; outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("refresh downloaded independently: %d", calls.Load())
	}
}

func TestGeositeCacheCorruptionIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destlists", "dlc_plain.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("lists: ["), 0600); err != nil {
		t.Fatal(err)
	}
	cache := NewGeositeCache(dir)
	if c, _, err := cache.Cached(); c != nil || !errors.Is(err, domain.ErrUnavailable) || cache.LastError() == "" {
		t.Fatalf("corrupt cache usable/unreported: %v", err)
	}
}
