// Package paneltz centralizes "what timezone does the panel use for
// system-level time math" lookups. The settings table stores the IANA
// name; every place that needs a calendar-day boundary for monthly /
// quarterly traffic rolls, user expire_at math, or the default zone
// for admin-side chart bucketing pulls it through here.
//
// User-facing views (subscription page, /user/me dashboard) intentionally
// stay on the browser's timezone and don't go through this package.
package paneltz

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// locationCache memoises time.LoadLocation results. Go's stdlib re-parses
// the zoneinfo table on every call — at admin dashboard / sub render
// scale that's hundreds of redundant parses per second. *time.Location
// is immutable once obtained, so an unbounded map is safe; the key set
// is bounded by the IANA tz database size (~600 entries max). Empty
// string and unparseable values resolve to time.Local and are cached
// under their literal key so the negative path is also fast.
var locationCache sync.Map // map[string]*time.Location

// Location resolves the configured panel timezone. Falls back to
// time.Local when the settings repo is nil, the load errors out, the
// configured value is blank, or it's unparseable — matching pre-tz
// behavior so existing installs keep working unchanged.
func Location(ctx context.Context, settings ports.SettingsReader) *time.Location {
	if settings == nil {
		return time.Local
	}
	cfg, err := settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return time.Local
	}
	return LocationOf(cfg.Timezone)
}

// LocationOf resolves an IANA timezone name to a *time.Location, falling
// back to time.Local on blank or unparseable input. Use this when you
// already hold the settings value (e.g. inside a DTO mapper that just
// loaded settings) to avoid a second repo round-trip.
func LocationOf(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.Local
	}
	if cached, ok := locationCache.Load(tz); ok {
		return cached.(*time.Location)
	}
	loc := time.Local
	if l, err := time.LoadLocation(tz); err == nil {
		loc = l
	}
	locationCache.Store(tz, loc)
	return loc
}

// EndOfDay parses a "2006-01-02" calendar date and returns the instant at
// 23:59:59 of that day in loc. This is the canonical "an admin picked a
// date" → absolute-instant conversion: the date is interpreted in the
// panel timezone, never the browser's or the 3X-UI server's, so the same
// pick yields the same cutoff regardless of where anyone is. The returned
// instant is timezone-independent (it's an instant); only the calendar
// day it represents was resolved against loc.
func EndOfDay(dateStr string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateStr), loc)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, loc), nil
}

// DateString formats an instant as the "2006-01-02" calendar day it falls
// on in loc — the inverse of EndOfDay for display/prefill. Round-trips:
// DateString(EndOfDay(s, loc), loc) == s for any valid s.
func DateString(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02")
}

// Now returns time.Now() in the configured panel timezone.
func Now(ctx context.Context, settings ports.SettingsRepo) time.Time {
	return time.Now().In(Location(ctx, settings))
}

// defaultPanelConcurrency is the fallback fan-out cap used when the
// admin hasn't configured one. 8 is comfortable for the typical 1-5
// panel deployment and large enough that single-panel installs (where
// concurrency is a no-op anyway) don't notice.
const defaultPanelConcurrency = 8

// maxPanelConcurrencyCeiling clamps a misconfigured very-high value so
// a typo in the settings UI ("80" instead of "8") can't slam 3X-UI
// with 80 simultaneous HTTP requests.
const maxPanelConcurrencyCeiling = 64

// ResolveMaxPanelConcurrency turns the raw settings integer into the
// usable concurrency cap for traffic poll / reconcile fan-out: <= 0
// falls back to the default, values above the ceiling clamp down.
// Centralized so both PollOnce and RunOnce agree on bounds.
func ResolveMaxPanelConcurrency(raw int) int {
	if raw <= 0 {
		return defaultPanelConcurrency
	}
	if raw > maxPanelConcurrencyCeiling {
		return maxPanelConcurrencyCeiling
	}
	return raw
}

// Validate checks that a tz string is something this binary's tzdata can
// resolve. Empty is allowed (it means "use server local"). Used by the
// admin settings PUT handler to reject saves the browser offered but Go
// can't actually load — otherwise the save succeeds silently and
// Location's fallback path leaves the admin thinking their pick stuck.
func Validate(tz string) error {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return nil
	}
	_, err := time.LoadLocation(tz)
	return err
}
