// Package sourceguard is the panel's side of the source guard (ADR 0044): the
// source sets of relay lines, the Cloudflare ranges a CDN line defaults to, and
// what a landing that can enforce them is sent.
package sourceguard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Where Cloudflare publishes its ranges.
const (
	CloudflareIPv4URL = "https://www.cloudflare.com/ips-v4"
	CloudflareIPv6URL = "https://www.cloudflare.com/ips-v6"
)

// Where the current ranges come from.
const (
	SourceBuiltin = "builtin"
	SourceFetched = "fetched"
)

const (
	fetchTimeout = 15 * time.Second
	// maxBody bounds one list; Cloudflare's are well under a kilobyte.
	maxBody = 16 << 10
	// A list that is much smaller or larger than what Cloudflare publishes is
	// more likely an error page or a hijack than a real list.
	minRanges = 5
	maxRanges = 500
	// RefreshEvery is how often the lists are fetched.
	RefreshEvery = 24 * time.Hour
)

// CloudflareStore keeps the last good fetch.
type CloudflareStore interface {
	LoadCloudflare(ctx context.Context) (domain.CloudflareRanges, bool, error)
	SaveCloudflare(ctx context.Context, ranges domain.CloudflareRanges) error
}

// Ranges is what a CDN line is checked against, and where it came from.
type Ranges struct {
	CIDRs     []string
	Source    string
	FetchedAt time.Time
	// BuiltinDate is when the built-in snapshot was taken.
	BuiltinDate string
}

// Cloudflare keeps Cloudflare's published ranges current: the last good fetch,
// or the built-in snapshot until there is one.
type Cloudflare struct {
	store   CloudflareStore
	enabled func(ctx context.Context) bool
	client  *http.Client
	v4, v6  string
	now     func() time.Time

	mu     sync.RWMutex
	loaded bool
	cur    Ranges
}

// NewCloudflare returns the keeper. enabled says whether auto-fetch is on (nil
// is on); store may be nil, and the fetched list then lives only in memory.
func NewCloudflare(store CloudflareStore, enabled func(ctx context.Context) bool) *Cloudflare {
	return &Cloudflare{
		store: store, enabled: enabled,
		client: &http.Client{Timeout: fetchTimeout},
		v4:     CloudflareIPv4URL, v6: CloudflareIPv6URL, now: time.Now,
	}
}

// WithSource points the keeper at other URLs and another client; for tests.
func (c *Cloudflare) WithSource(client *http.Client, v4, v6 string) *Cloudflare {
	if client != nil {
		c.client = client
	}
	c.v4, c.v6 = v4, v6
	return c
}

func (c *Cloudflare) builtin() Ranges {
	return Ranges{CIDRs: slices.Clone(BuiltinCloudflare), Source: SourceBuiltin, BuiltinDate: BuiltinCloudflareDate}
}

// Current returns the ranges in force. It never fails: a store that cannot be
// read leaves the built-in snapshot. With auto-fetch turned off the built-in
// snapshot is what is in force, not the last list fetched: the administrator
// chose to stop following Cloudflare, and a list that nothing refreshes would
// go stale without anyone knowing.
func (c *Cloudflare) Current(ctx context.Context) Ranges {
	if c.enabled != nil && !c.enabled(ctx) {
		return c.builtin()
	}
	c.mu.RLock()
	if c.loaded {
		defer c.mu.RUnlock()
		return c.cur
	}
	c.mu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.cur
	}
	c.cur = c.builtin()
	if c.store != nil {
		stored, ok, err := c.store.LoadCloudflare(ctx)
		switch {
		case err != nil:
			// Not cached: the next call reads again, and until then the snapshot
			// stands in.
			log.Warn("cloudflare ranges: stored list unreadable; using the built-in snapshot", "err", err)
			return c.cur
		case ok && validRanges(stored.CIDRs) == nil:
			c.cur = Ranges{CIDRs: stored.CIDRs, Source: SourceFetched, FetchedAt: stored.FetchedAt, BuiltinDate: BuiltinCloudflareDate}
		}
	}
	c.loaded = true
	return c.cur
}

// Refresh fetches both lists and, when they pass validation, stores them as the
// current ranges. On any failure nothing changes and the error is returned.
func (c *Cloudflare) Refresh(ctx context.Context) error {
	v4, err := c.fetch(ctx, c.v4)
	if err != nil {
		return fmt.Errorf("ips-v4: %w", err)
	}
	v6, err := c.fetch(ctx, c.v6)
	if err != nil {
		return fmt.Errorf("ips-v6: %w", err)
	}
	all := append(slices.Clone(v4), v6...)
	if err := validRanges(all); err != nil {
		return err
	}
	// One network, one spelling, in one order: the set a landing compares is the
	// same bytes whenever the content is the same.
	all, err = domain.NormalizeSourceCIDRs(all)
	if err != nil {
		return err
	}
	fetched := domain.CloudflareRanges{CIDRs: all, FetchedAt: c.now().UTC()}
	if c.store != nil {
		if err := c.store.SaveCloudflare(ctx, fetched); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}
	c.mu.Lock()
	c.cur = Ranges{CIDRs: all, Source: SourceFetched, FetchedAt: fetched.FetchedAt, BuiltinDate: BuiltinCloudflareDate}
	c.loaded = true
	c.mu.Unlock()
	return nil
}

// fetch reads one list with a plain GET (nothing identifying the deployment is
// sent) and returns its prefixes.
func (c *Cloudflare) fetch(ctx context.Context, url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Go's client adds its default User-Agent; blank it so none is sent.
	req.Header.Set("User-Agent", "")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, errors.New("response too large")
	}
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// validRanges holds the lists to what Cloudflare publishes: canonical prefixes
// of both families, in a plausible number.
func validRanges(cidrs []string) error {
	if len(cidrs) < minRanges || len(cidrs) > maxRanges {
		return fmt.Errorf("%d ranges, want %d..%d", len(cidrs), minRanges, maxRanges)
	}
	var v4, v6 bool
	for _, raw := range cidrs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return fmt.Errorf("%q: %w", raw, err)
		}
		if prefix.Masked() != prefix || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" || prefix.String() != raw {
			return fmt.Errorf("%q is not a canonical prefix", raw)
		}
		if prefix.Bits() == 0 {
			// A list that allows everything defeats the guard.
			return fmt.Errorf("%q matches every address", raw)
		}
		if prefix.Addr().Is4() {
			v4 = true
		} else {
			v6 = true
		}
	}
	if !v4 || !v6 {
		return errors.New("a list of one family only")
	}
	return nil
}

// Run refreshes the ranges once a day until ctx ends: after firstDelay, then
// every RefreshEvery (a restart does not fetch again within a day of the last
// fetch). Auto-fetch is read before every attempt, so turning it
// off takes effect on the next one. A failed fetch is logged and the previous
// ranges stay.
func (c *Cloudflare) Run(ctx context.Context, firstDelay time.Duration) {
	// A list fetched within the last day is not fetched again by a restart.
	if cur := c.Current(ctx); cur.Source == SourceFetched {
		if wait := RefreshEvery - c.now().Sub(cur.FetchedAt); wait > firstDelay {
			firstDelay = wait
		}
	}
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if c.enabled == nil || c.enabled(ctx) {
			if err := c.Refresh(ctx); err != nil && ctx.Err() == nil {
				log.Warn("cloudflare ranges: fetch failed; the previous ranges stay", "err", err)
			} else if err == nil {
				log.Info("cloudflare ranges refreshed", "ranges", len(c.Current(ctx).CIDRs))
			}
		}
		timer.Reset(RefreshEvery)
	}
}
