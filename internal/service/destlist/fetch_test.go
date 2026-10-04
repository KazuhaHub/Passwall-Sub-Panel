package destlist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRejectsInsecureAndMalformedURLsBeforeDial(t *testing.T) {
	for _, raw := range []string{"http://example.com/list", "ftp://example.com/list", "https://", "not a URL", "https://example.com/%zz"} {
		if _, err := NewFetcher().Fetch(t.Context(), raw); err == nil {
			t.Fatalf("invalid URL accepted: %s", raw)
		}
	}
}

func TestFetchUsesSafeHTTPToRefuseLoopbackAndDoesNotExposeURLSecrets(t *testing.T) {
	var contacted atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted.Store(true)
		_, _ = w.Write([]byte("example.com"))
	}))
	defer server.Close()
	_, err := NewFetcher().Fetch(t.Context(), server.URL+"/list?token=do-not-log-me")
	if err == nil || contacted.Load() || strings.Contains(err.Error(), "do-not-log-me") {
		t.Fatalf("unsafe dial or credential-bearing error: contacted=%v err=%v", contacted.Load(), err)
	}
}

func TestFetchBoundsBodyAndDoesNotAcceptPartialOversizeContent(t *testing.T) {
	f := NewFetcher()
	// Tests replace only the transport; production URL/redirect policy remains.
	f.client.Transport = fetchTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("#", MaxRemoteBytes+1))), Request: req}, nil
	})
	p, err := f.Fetch(t.Context(), "https://rules.example.com/list")
	if err == nil || !strings.Contains(err.Error(), "dest_list_too_large") || len(p.Parsed.Entries) != 0 {
		t.Fatalf("truncated remote body accepted: %+v / %v", p, err)
	}
}

func TestFetchPreservesStatusSizeAndCanonicalContent(t *testing.T) {
	f := NewFetcher()
	text := "payload:\n  - '+.Example.com'\n"
	f.client.Transport = fetchTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(text)), Request: req}, nil
	})
	p, err := f.Fetch(t.Context(), "https://rules.example.com/list")
	if err != nil || string(p.Parsed.Entries) != "domain:example.com\n" || p.HTTPStatus != 200 || p.Bytes != len(text) || f.client.Timeout != time.Minute {
		t.Fatalf("fetch result wrong: %+v / %v", p, err)
	}
}

func TestFetchRefusesHTTPRedirectsAndBoundsHTTPSRedirects(t *testing.T) {
	for _, target := range []string{"http://rules.example.com/list", "https://rules.example.com/list"} {
		f := NewFetcher()
		var requests atomic.Int64
		f.client.Transport = fetchTransport(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})
		_, err := f.Fetch(t.Context(), "https://rules.example.com/list?token=secret-value")
		if err == nil || requests.Load() > 10 || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("redirect policy failed: requests=%d / %v", requests.Load(), err)
		}
		if strings.HasPrefix(target, "http:") && requests.Load() != 1 {
			t.Fatalf("downgrade request sent: %d", requests.Load())
		}
	}
}

func TestFetchNon2xxAndReadErrorsReturnNoCandidate(t *testing.T) {
	for _, status := range []int{404, 503} {
		f := NewFetcher()
		f.client.Transport = fetchTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("example.com")), Request: req}, nil
		})
		p, err := f.Fetch(t.Context(), "https://rules.example.com/list")
		if err == nil || len(p.Parsed.Entries) != 0 {
			t.Fatalf("HTTP %d accepted: %+v / %v", status, p, err)
		}
	}
	f := NewFetcher()
	f.client.Transport = fetchTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: errorReader{}, Request: req}, nil
	})
	if p, err := f.Fetch(t.Context(), "https://rules.example.com/list"); err == nil || len(p.Parsed.Entries) != 0 {
		t.Fatalf("read failure returned content: %+v / %v", p, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewFetcher().Fetch(ctx, "https://rules.example.com/list"); err == nil {
		t.Fatal("canceled request accepted")
	}
}

type fetchTransport func(*http.Request) (*http.Response, error)

func (f fetchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("injected body read failure") }
func (errorReader) Close() error             { return nil }
