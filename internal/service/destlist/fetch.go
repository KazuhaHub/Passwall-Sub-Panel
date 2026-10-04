package destlist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
)

type FetchResult struct {
	Parsed     Parsed
	HTTPStatus int
	Bytes      int
}

type Fetcher struct{ client *http.Client }

func NewFetcher() *Fetcher {
	client := safehttp.NewClient(time.Minute)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !strings.EqualFold(req.URL.Scheme, "https") {
			return &FetchError{Reason: "redirect_refused"}
		}
		return nil
	}
	return &Fetcher{client: client}
}

// FetchError never retains net/http's URL-bearing error text. It is persisted
// in last_error, so URL credentials and query tokens must not reach Error().
type FetchError struct {
	Reason string
	Status int
	cause  error
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("dest_list_fetch_failed: HTTP %d", e.Status)
	}
	return "dest_list_fetch_failed: " + e.Reason
}
func (e *FetchError) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return domain.ErrUnavailable
}

func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" {
		return &Error{Code: "dest_list_insecure_url"}
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return &Error{Code: "dest_list_insecure_url"}
	}
	return nil
}

func (f *Fetcher) Fetch(ctx context.Context, raw string) (FetchResult, error) {
	body, status, err := f.download(ctx, raw, MaxRemoteBytes)
	if err != nil {
		return FetchResult{HTTPStatus: status}, err
	}
	parsed, err := ParseRemote(body)
	if err != nil {
		return FetchResult{HTTPStatus: status, Bytes: len(body)}, err
	}
	return FetchResult{Parsed: parsed, HTTPStatus: status, Bytes: len(body)}, nil
}

func (f *Fetcher) download(ctx context.Context, raw string, limit int64) ([]byte, int, error) {
	if err := validateRemoteURL(raw); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, 0, &Error{Code: "dest_list_insecure_url"}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		failure := &FetchError{Reason: "request_failed"}
		if errors.Is(err, context.Canceled) {
			failure.cause = context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			failure.cause = context.DeadlineExceeded
		}
		return nil, 0, failure
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, &FetchError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, &FetchError{Reason: "read_failed"}
	}
	if int64(len(body)) > limit {
		return nil, resp.StatusCode, &Error{Code: "dest_list_too_large"}
	}
	return body, resp.StatusCode, nil
}
