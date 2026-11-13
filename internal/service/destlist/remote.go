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

// MaxRemoteBytes is the largest body a remote list may have. Over it the
// fetch FAILS; it never truncates (geo/update.go's reader does, and a
// truncated list is a different list that happens to parse).
const MaxRemoteBytes = 16 << 20

// remoteTimeout caps one fetch end to end.
const remoteTimeout = 60 * time.Second

const maxRemoteURLBytes = 1024

// ErrInsecureURL is a list URL that is not https (HTTP 400
// dest_list_insecure_url). It is a validation error too.
var ErrInsecureURL = fmt.Errorf("%w: dest list url must be https", domain.ErrValidation)

// Doer is the slice of *http.Client a fetch needs, so tests can stand in for
// the network.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// ValidateRemoteURL checks a list URL: https only, a host, no embedded
// credentials (they would end up in logs and in the stored row), and within
// the column's width.
func ValidateRemoteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxRemoteURLBytes {
		return nil, fmt.Errorf("%w: dest list url is empty or over %d bytes", domain.ErrValidation, maxRemoteURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: dest list url does not parse", domain.ErrValidation)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, ErrInsecureURL
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: dest list url has no host", domain.ErrValidation)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: dest list url must not carry credentials", domain.ErrValidation)
	}
	return u, nil
}

// newSafeClient is the SSRF-refusing client every fetch of an admin-supplied
// URL goes through (CLAUDE.md: outbound HTTP goes through safehttp), with a
// redirect policy that never leaves https.
func newSafeClient() *http.Client {
	c := safehttp.NewClient(remoteTimeout)
	c.CheckRedirect = refuseInsecureRedirect
	return c
}

func refuseInsecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("stopped after 5 redirects")
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return fmt.Errorf("refusing a redirect to %s", req.URL.Scheme)
	}
	return nil
}

// fetchBody GETs rawURL and returns the whole body, or an error: a non-2xx
// answer, a body over limit (a *TooLargeError of kind "remote_bytes"), a
// transport failure. It reads limit+1 bytes to tell "exactly limit" from
// "more", and refuses on an advertised Content-Length without reading.
func fetchBody(ctx context.Context, d Doer, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/plain, application/yaml, */*")
	resp, err := d.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, &TooLargeError{Kind: "remote_bytes", Used: int(min(resp.ContentLength, int64(1<<31-1))), Limit: int(limit)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, &TooLargeError{Kind: "remote_bytes", Used: len(body), Limit: int(limit)}
	}
	return body, nil
}
