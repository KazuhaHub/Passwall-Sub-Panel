package risk

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
)

// The fetch window: every subscription fetch of the last few panel-local
// days, folded per account into which client fetched from which address on
// which days. It is read once per run, in one streamed pass, and lives only
// for that run — the raw rows (and their addresses) are never kept beyond
// it, and nothing derived from an address is stored except the provinces
// and countries the evidence names.

// scanBatch is how many sub_logs rows one ScanSince batch carries: a week of
// fetches can be hundreds of thousands of rows, and a batch this size keeps
// what one pass holds to a few megabytes.
const scanBatch = 5000

// windowLabelRunes caps a client string used as its own label, as the
// evidence stores it.
const windowLabelRunes = 64

// fetchWindow is one run's view of the fetch log.
type fetchWindow struct {
	// days is how many days the window holds (bit i of a day mask = day i,
	// 0 = the oldest); start is day 0's panel-local date.
	days  int
	start string
	// users holds every listed account that fetched in the window.
	users map[int64]*userWindow
}

// userWindow is one account's fetches: which addresses, and for each
// (address, client) pair the days it was seen on. An account has one only
// when it fetched on a window day.
type userWindow struct {
	ips        map[string]struct{}
	cells      map[windowCell]uint8
	identities map[string]*identityAgg
	// fetches counts the account's fetches in the window, with an address
	// or without; withHWID how many of them declared a device.
	fetches, withHWID int
}

// windowCell is one client at one raw address.
type windowCell struct{ ip, identity string }

// identityAgg describes one client of one account for the evidence.
type identityAgg struct {
	kind  string // "hwid" (declared a device id) or "ua" (known by its client string)
	label string
	hwid4 string
	// labelMS is when the fetch the label came from happened (unix ms):
	// rows arrive in id order, which is not quite time order, and the label
	// shown is the newest one declared.
	labelMS int64
	// days is every window day the client fetched on, from any address or
	// none: the device count reads it. (sub_spread reads days per place,
	// from the cells, where an excluded address says nothing.)
	days uint8
	// client is the newest client name its fetches were detected as, by
	// fetch time like the label, and clientMS when; lastMS is its newest
	// fetch of all.
	client           string
	clientMS, lastMS int64
}

// windowDays is how many days the fetch window holds: a week, or the
// sub-log retention when that is shorter — the rows before it are gone, and
// a week-long window would read those days as days without fetches.
func windowDays(retention int) int {
	if retention > 0 && retention < domain.RiskWindowDays {
		return retention
	}
	return domain.RiskWindowDays
}

// readWindow streams the fetch log from the first window day's local
// midnight and folds every row of a listed account into its window.
//
// Days are panel-local calendar days, cut in Go on each row's instant: the
// store's bound is only a lower bound, and a row stamped after now (a clock
// step) belongs to no window day. Rows of accounts the user list does not
// hold are skipped entirely — they are not judged, and they must not count
// toward another account's shared exit either.
func (s *Service) readWindow(ctx context.Context, r *refresh) (*fetchWindow, error) {
	w := &fetchWindow{days: windowDays(r.global.SubLogRetentionDays), users: map[int64]*userWindow{}}
	y, m, d := r.now.In(r.loc).Date()
	day0 := time.Date(y, m, d-(w.days-1), 0, 0, 0, 0, r.loc)
	w.start = paneltz.DateString(day0, r.loc)

	listed := make(map[int64]bool, len(r.users))
	for _, u := range r.users {
		listed[u.ID] = true
	}
	err := s.d.SubLogs.ScanSince(ctx, day0, scanBatch, func(rows []domain.SubLog) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The slice is the store's and is reused for the next batch: only
		// values are copied out of it.
		for i := range rows {
			if listed[rows[i].UserID] {
				w.add(&rows[i], day0, r.loc)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// add folds one fetch into its account's window.
func (w *fetchWindow) add(row *domain.SubLog, day0 time.Time, loc *time.Location) {
	day := domain.CivilDaysBetween(day0, row.AccessedAt, loc)
	if day < 0 || day >= w.days {
		return
	}
	uw := w.users[row.UserID]
	if uw == nil {
		uw = &userWindow{
			ips:        map[string]struct{}{},
			cells:      map[windowCell]uint8{},
			identities: map[string]*identityAgg{},
		}
		w.users[row.UserID] = uw
	}
	ip := strings.TrimSpace(row.IP)

	// A declared device id is the client; without one, the exact client
	// string is. That errs toward linking: two people on the same app and
	// version are one client here, which can hide a spread but never
	// invents one.
	key, kind := "u:"+row.UA, "ua"
	if row.DeviceID != "" {
		key, kind = "d:"+row.DeviceID, "hwid"
	}
	at := row.AccessedAt.UnixMilli()
	agg := uw.identities[key]
	if agg == nil {
		agg = &identityAgg{kind: kind, lastMS: at}
		if kind == "hwid" {
			agg.hwid4 = truncateRunes(row.DeviceID, 4)
		} else {
			agg.label = truncateRunes(row.UA, windowLabelRunes)
		}
		uw.identities[key] = agg
	}
	uw.fetches++
	if kind == "hwid" {
		uw.withHWID++
	}
	agg.days |= 1 << day
	agg.lastMS = max(agg.lastMS, at)
	// The newest label and client name, by fetch time; a later fetch that
	// declared none (or was not recognised) does not erase them.
	if kind == "hwid" && row.DeviceLabel != "" && (agg.label == "" || at >= agg.labelMS) {
		agg.label, agg.labelMS = row.DeviceLabel, at
	}
	if row.ClientType != "" && (agg.client == "" || at >= agg.clientMS) {
		agg.client, agg.clientMS = row.ClientType, at
	}
	// A row with no address still says the account fetched; it just has
	// no source to place.
	if ip != "" {
		uw.ips[ip] = struct{}{}
		uw.cells[windowCell{ip: ip, identity: key}] |= 1 << day
	}
}

// truncateRunes cuts s to at most n characters, never inside one.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
