package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- fetch-window fakes --------------------------------------------------

type scanCall struct {
	since time.Time
	batch int
}

// fakeScanner hands out its rows whatever the bound — the service must cut
// the window by date itself — in batches of three through ONE reused slice,
// the way the store does, so a service that kept a batch would read rows
// overwritten by the next one. err is returned after every row was handed
// out; errAfter stops after that many batches instead.
type fakeScanner struct {
	rows     []domain.SubLog
	err      error
	errAfter int
	calls    []scanCall
}

func (f *fakeScanner) ScanSince(ctx context.Context, since time.Time, batch int, fn func([]domain.SubLog) error) error {
	f.calls = append(f.calls, scanCall{since, batch})
	buf := make([]domain.SubLog, 0, 3)
	for i, sent := 0, 0; i < len(f.rows); i += 3 {
		if f.errAfter > 0 && sent == f.errAfter {
			return f.err
		}
		buf = append(buf[:0], f.rows[i:min(i+3, len(f.rows))]...)
		if err := fn(buf); err != nil {
			return err
		}
		sent++
		for j := range buf {
			buf[j] = domain.SubLog{IP: "0.0.0.0", UA: "overwritten"}
		}
	}
	return f.err
}

// fakeGeo places addresses from a table and records every batch it was
// asked about.
type fakeGeo struct {
	places  map[string]domain.GeoLocation
	off     bool
	lookups [][]string
}

func (g *fakeGeo) Lookup(_ context.Context, ips []string) map[string]domain.GeoLocation {
	g.lookups = append(g.lookups, append([]string(nil), ips...))
	out := map[string]domain.GeoLocation{}
	for _, ip := range ips {
		if loc, ok := g.places[ip]; ok {
			out[ip] = loc
		}
	}
	return out
}

func (g *fakeGeo) Available(context.Context) bool { return !g.off }

// ---- fixtures ------------------------------------------------------------

// The places. Documentation ranges, so none of them is "internal".
const (
	ipHomeGD  = "203.0.113.10"  // CN / Guangdong
	ipHomeGD2 = "203.0.113.11"  // CN / Guangdong
	ipHomeGD3 = "203.0.113.12"  // CN / Guangdong
	ipHunan   = "198.51.100.20" // CN / Hunan
	ipOffice  = "198.51.100.99" // CN / Hunan: an office exit
	ipRelay   = "192.0.2.50"    // CN / Hunan: one of PSP's relays
	ipTokyo   = "192.0.2.80"    // JP / Tokyo
)

func spreadGeo() *fakeGeo {
	gd := domain.GeoLocation{CountryCode: "CN", Region: "Guangdong"}
	hn := domain.GeoLocation{CountryCode: "CN", Region: "Hunan"}
	return &fakeGeo{places: map[string]domain.GeoLocation{
		ipHomeGD: gd, ipHomeGD2: gd, ipHomeGD3: gd,
		ipHunan: hn, ipOffice: hn, ipRelay: hn,
		ipTokyo:            {CountryCode: "jp", Region: " Tokyo "},
		"2408:8207:1:2::a": gd,
	}}
}

// localDay is an instant on window day k (0 = 2026-09-19, 6 = today,
// 2026-09-25) in the panel's zone, Shanghai.
func localDay(t *testing.T, k, hour int) time.Time {
	t.Helper()
	return time.Date(2026, 9, 19+k, hour, 0, 0, 0, shanghai(t))
}

// everyDay is one fetch per window day.
func everyDay(t *testing.T, row func(at time.Time) domain.SubLog) []domain.SubLog {
	t.Helper()
	var out []domain.SubLog
	for k := range 7 {
		out = append(out, row(localDay(t, k, 9)))
	}
	return out
}

const phoneID = "0a1b2c3d4e5f6071"

// phone is the account holder's phone: it declares a device id.
func phone(uid int64, ip string) func(time.Time) domain.SubLog {
	return func(at time.Time) domain.SubLog {
		return domain.SubLog{UserID: uid, IP: ip, UA: "Happ/3.4.0", ClientType: "v2rayng", AccessedAt: at,
			DeviceID: phoneID, DeviceLabel: "Android 15 Pixel 9"}
	}
}

// client is a client known only by its client string.
func client(uid int64, ip, ua string) func(time.Time) domain.SubLog {
	return func(at time.Time) domain.SubLog {
		return domain.SubLog{UserID: uid, IP: ip, UA: ua, ClientType: "mihomo", AccessedAt: at}
	}
}

func rowsOf(parts ...[]domain.SubLog) []domain.SubLog {
	var out []domain.SubLog
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// newSpreadHarness is a harness with the fetch window wired: a seven-day
// retention, the geo table above, infrastructure loaded and empty.
func newSpreadHarness(users []*domain.User, rows []domain.SubLog) *harness {
	h := newHarness(users)
	h.settings.global.SubLogRetentionDays = 7
	h.scanner = &fakeScanner{rows: rows}
	h.geo = spreadGeo()
	h.infraLoaded = func() bool { return true }
	return h
}

// spreadRow is one account's saved sub_spread row with its evidence decoded.
func spreadRow(t *testing.T, h *harness, uid int64) (domain.RiskSignal, domain.SubSpreadEvidence) {
	t.Helper()
	r, ok := h.store.saved(t)[uid][domain.RiskKindSubSpread]
	if !ok {
		t.Fatalf("no sub_spread row saved for user %d", uid)
	}
	var ev domain.SubSpreadEvidence
	if r.Evidence != nil {
		if err := json.Unmarshal(r.Evidence, &ev); err != nil {
			t.Fatalf("user %d evidence %s: %v", uid, r.Evidence, err)
		}
	}
	return r, ev
}

func wantSpreadRow(t *testing.T, r domain.RiskSignal, state domain.GeoState, code domain.RiskCode) {
	t.Helper()
	if r.State != state || r.Code != code {
		t.Fatalf("user %d sub_spread = %s/%s, want %s/%s (evidence %s)", r.UserID, r.State, r.Code, state, code, r.Evidence)
	}
}

// ---- tests ---------------------------------------------------------------

// The window is judged per account: user 1's phone lives in Guangdong and
// another client fetches from Hunan every day, and nothing links them —
// flagged. User 2 fetched nothing: idle, with no evidence.
func TestRefresh_SubSpreadJudgesTheWindow(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)),
		everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
	))
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateFlagged, domain.RiskCodeSpread)
	if ev.Groups != 2 || ev.Country != "CN" || ev.WindowStart != "2026-09-19" || ev.WindowDays != 7 {
		t.Fatalf("evidence %+v, want 2 groups in CN over the 7 days from 2026-09-19", ev)
	}
	idle, _ := spreadRow(t, h, 2)
	wantSpreadRow(t, idle, domain.GeoStateIdle, domain.RiskCodeNoFetches)
	if idle.Evidence != nil {
		t.Fatalf("an account that fetched nothing has evidence %s", idle.Evidence)
	}
	if rows := h.store.saved(t); len(rows[1]) != 3 {
		t.Fatalf("user 1 rows %v, want usage_shift, devices and sub_spread", rows[1])
	}
	if len(h.scanner.calls) != 1 || h.scanner.calls[0].batch != scanBatch {
		t.Fatalf("scans %+v, want one in batches of %d", h.scanner.calls, scanBatch)
	}
	wantOutcome(t, before, "ok")
}

// An account an admin trusts is not judged on where it fetches from: the
// same week that flags the control reads exempt / trusted for it, with no
// evidence, like any exemption. Mutation: judge with the group's geo policy
// as loaded, and user 1 is flagged too.
func TestSubSpread_TrustedAccountIsExemptTrusted(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipOffice, "ClashX Pro/1.118.0")),
	))
	h.trust = &fakeTrust{ids: []int64{1}}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	control, _ := spreadRow(t, h, 2)
	wantSpreadRow(t, control, domain.GeoStateFlagged, domain.RiskCodeSpread)
	r, _ := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateExempt, domain.RiskCodeTrusted)
	if r.Evidence != nil {
		t.Fatalf("a trusted account's row has evidence %s", r.Evidence)
	}
}

// An office exit three accounts fetch from says nothing about where any of
// them is. It is set aside for all three, and each reads as its phone at
// home.
func TestRefresh_SharedExitAcrossThreeUsersIsExcluded(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipOffice, "office-proxy/1")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipOffice, "office-proxy/1")),
		everyDay(t, phone(3, ipHomeGD3)), everyDay(t, client(3, ipOffice, "office-proxy/1")),
	))
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid := int64(1); uid <= 3; uid++ {
		r, ev := spreadRow(t, h, uid)
		wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
		if ev.Excluded.Shared != 1 || ev.Coverage.Sources != 1 {
			t.Fatalf("user %d: excluded %+v, coverage %+v; want the office set aside as shared", uid, ev.Excluded, ev.Coverage)
		}
	}
}

// The shared-exit threshold is the fleet's geo_anomaly.shared_exit_min_users,
// the same knob the live check reads: an operator who lowers it to two
// declares that two accounts on one source are an exit, and the week of
// fetches must agree with the live verdict about it. With the shipped three,
// the same two accounts spread over two provinces
// (TestRefresh_FetchesOfUnknownUsersAreIgnored).
func TestSubSpread_SharedExitThresholdComesFromSettings(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipOffice, "office-proxy/1")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipOffice, "office-proxy/1")),
	))
	h.settings.global.GeoAnomalySharedExitMinUsers = 2
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid := int64(1); uid <= 2; uid++ {
		r, ev := spreadRow(t, h, uid)
		wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
		if ev.Excluded.Shared != 1 {
			t.Fatalf("user %d: excluded %+v, want the office set aside as shared at threshold 2", uid, ev.Excluded)
		}
	}
}

// A fetch arriving from one of PSP's own relays carries the relay's address,
// not the user's: set aside. Without the infrastructure test the same week
// reads as two provinces.
func TestRefresh_InfraAddressIsExcluded(t *testing.T) {
	rows := rowsOf(everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipRelay, "clash.meta/1.19.2")))
	h := newSpreadHarness(usersInGroups(0), rows)
	relay := netip.MustParseAddr(ipRelay)
	h.isInfra = func(a netip.Addr) bool { return a == relay }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.Excluded.Infra != 1 {
		t.Fatalf("excluded %+v, want the relay counted as infrastructure", ev.Excluded)
	}

	h = newSpreadHarness(usersInGroups(0), rows)
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ = spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateFlagged, domain.RiskCodeSpread)
}

// A phone rotates IPv6 privacy addresses inside its /64 every few hours. One
// /64 is one source, looked up once at its smallest member, and its days are
// the union of every member's.
func TestRefresh_IPv6PrivacyAddressesAreOneSource(t *testing.T) {
	var rows []domain.SubLog
	for k, ip := range []string{"2408:8207:1:2::c", "2408:8207:1:2::c", "2408:8207:1:2::b", "2408:8207:1:2::b", "2408:8207:1:2::a", "2408:8207:1:2::a", "2408:8207:1:2::c"} {
		rows = append(rows, phone(1, ip)(localDay(t, k, 20)))
	}
	h := newSpreadHarness(usersInGroups(0), rows)
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.Coverage.Sources != 1 || ev.Coverage.Placed != 1 {
		t.Fatalf("coverage %+v, want one placed source", ev.Coverage)
	}
	if len(ev.Provinces) != 1 || ev.Provinces[0].Days != 0x7f {
		t.Fatalf("provinces %+v, want Guangdong on all seven days", ev.Provinces)
	}
	if !reflect.DeepEqual(h.geo.lookups, [][]string{{"2408:8207:1:2::a"}}) {
		t.Fatalf("geo lookups %v, want one batch asking for the smallest member only", h.geo.lookups)
	}
}

// The place signals judge a whole week against the infrastructure set, so
// they wait until it has been built: judged against an empty set, every
// relayed account reads as fetching from the relay's province. The run says
// so (infra_pending), keeps the stored sub_spread rows, and still writes the
// signals that do not place anything.
func TestRefresh_PlaceKindsWaitForTheInfraSet(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
	))
	h.infraLoaded = func() bool { return false }
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	for uid := int64(1); uid <= 2; uid++ {
		if _, ok := rows[uid][domain.RiskKindSubSpread]; ok {
			t.Fatalf("user %d got a sub_spread row before the infrastructure set loaded", uid)
		}
		if _, ok := rows[uid][domain.RiskKindUsageShift]; !ok {
			t.Fatalf("user %d lost its usage_shift row to the infrastructure wait", uid)
		}
	}
	if len(h.geo.lookups) != 0 {
		t.Fatalf("placed %v while waiting for the infrastructure set", h.geo.lookups)
	}
	wantOutcome(t, before, "infra_pending")
}

// A fetch log that cannot be read to the end is not judged from the part
// that was: a week missing its last batches would read as fewer provinces or
// fewer days. The window's signals keep their rows, the rest are written,
// and the run is partial.
func TestRefresh_ScanErrorKeepsWindowKindsAndIsPartial(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(2, ipHunan, "ClashX Pro/1.118.0")),
	))
	h.scanner.err = errors.New("database is locked")
	h.scanner.errAfter = 2
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	for uid := int64(1); uid <= 2; uid++ {
		for _, kind := range []domain.RiskKind{domain.RiskKindSubSpread, domain.RiskKindDevices} {
			if _, ok := rows[uid][kind]; ok {
				t.Fatalf("user %d %s judged from a fetch window that failed half-way", uid, kind)
			}
		}
		if _, ok := rows[uid][domain.RiskKindUsageShift]; !ok {
			t.Fatalf("user %d lost its usage_shift row to the fetch-log failure", uid)
		}
	}
	wantOutcome(t, before, "partial")
}

// The window is seven panel-local days, or the sub-log retention when that
// is shorter — the rows before it are gone. Two days of logs cannot show a
// province on three: unknown, never clean. The scan starts at the first
// window day's local midnight.
func TestRefresh_WindowFollowsRetention(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
	))
	h.settings.global.SubLogRetentionDays = 2
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateUnknown, domain.RiskCodeRetentionShort)
	if ev.WindowDays != 2 || ev.RetentionDays != 2 || ev.MinDays != 3 || ev.WindowStart != "2026-09-24" {
		t.Fatalf("evidence %+v, want a 2-day window from 2026-09-24 under retention 2, min_days 3", ev)
	}
	if want := time.Date(2026, 9, 24, 0, 0, 0, 0, shanghai(t)); len(h.scanner.calls) != 1 || !h.scanner.calls[0].since.Equal(want) {
		t.Fatalf("scans %+v, want one from %s", h.scanner.calls, want)
	}

	// A retention of 0 keeps the logs forever: the full week.
	h = newSpreadHarness(usersInGroups(0), nil)
	h.settings.global.SubLogRetentionDays = 0
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 19, 0, 0, 0, 0, shanghai(t)); !h.scanner.calls[0].since.Equal(want) {
		t.Fatalf("scan from %s under retention 0, want %s", h.scanner.calls[0].since, want)
	}
}

// Day masks are panel-local calendar days. Shanghai's 2026-09-19 begins at
// 16:00 UTC the day before: a fetch at 15:59Z is the day before the window,
// one at 16:00Z is its first day. Today counts (day 6). A row from the
// future, or from before the window, is dropped whatever the store handed
// back.
func TestRefresh_DayMasksUsePanelTimezone(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	h := newSpreadHarness(usersInGroups(0), []domain.SubLog{
		phone(1, ipHomeGD)(at("2026-09-18T15:59:00Z")), // 09-18 23:59 local: before the window
		phone(1, ipHomeGD)(at("2026-09-18T16:00:00Z")), // 09-19 00:00 local: day 0
		phone(1, ipHomeGD)(at("2026-09-24T16:30:00Z")), // 09-25 00:30 local: day 6, today
		phone(1, ipHomeGD)(at("2026-09-25T16:00:00Z")), // 09-26 00:00 local: tomorrow
	})
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ev := spreadRow(t, h, 1)
	if len(ev.Provinces) != 1 || ev.Provinces[0].Days != 1|1<<6 {
		t.Fatalf("provinces %+v, want Guangdong on days 0 and 6 only (mask %d)", ev.Provinces, 1|1<<6)
	}
	if want := time.Date(2026, 9, 19, 0, 0, 0, 0, shanghai(t)); !h.scanner.calls[0].since.Equal(want) {
		t.Fatalf("scan from %s, want the local midnight %s", h.scanner.calls[0].since, want)
	}
}

// A window whose first day is a date without a midnight still starts on that
// date. Anchored at the resolved 00:00 — 23:00 the evening before — every
// day was counted from the day before: the evening's fetch became day 0, the
// first day's became day 1, and TODAY's fell off the end of the window, for
// every account, on every run of that day. Today counts (day 6). The scan may
// start early, since each row is still placed by its own date, but never
// after the first day's first instant: the store cuts exactly at the bound.
func TestRefresh_WindowStartsOnADateWithoutAMidnight(t *testing.T) {
	for _, g := range midnightGaps {
		t.Run(g.zone, func(t *testing.T) {
			loc := g.load(t)
			h := newSpreadHarness(usersInGroups(0), []domain.SubLog{
				phone(1, ipHomeGD)(g.at(loc, -1, 23, 30)), // the evening before: outside the window
				phone(1, ipHomeGD)(g.at(loc, 0, 1, 30)),   // just after the jump: day 0
				phone(1, ipHomeGD)(g.at(loc, 6, 10, 0)),   // today: day 6
			})
			h.settings.global.Timezone = g.zone
			h.now = g.at(loc, 6, 12, 0)
			if err := h.service().RefreshOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			_, ev := spreadRow(t, h, 1)
			if ev.WindowStart != g.date(0) {
				t.Fatalf("window start = %q, want %q", ev.WindowStart, g.date(0))
			}
			if len(ev.Provinces) != 1 || ev.Provinces[0].Days != 1|1<<6 {
				t.Fatalf("provinces %+v, want Guangdong on days 0 and 6 only (mask %d)", ev.Provinces, 1|1<<6)
			}
			first := dayStartOf(t, g.at(loc, 0, 12, 0), loc) // the jump, 01:00
			if len(h.scanner.calls) != 1 || h.scanner.calls[0].since.After(first) {
				t.Fatalf("scans %+v, want one from no later than %s", h.scanner.calls, first)
			}
		})
	}
}

// sub_spread reuses the group's own geo policy (V3-D3): a group that allows
// two provinces allows two groups of them, while the global tolerance of one
// still applies to everyone else.
func TestRefresh_GroupRegionToleranceReachesSubSpread(t *testing.T) {
	h := newSpreadHarness(usersInGroups(5, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipHunan, "ClashX Pro/1.118.0")),
	))
	wide := h.settings.global
	wide.GeoAnomalyMaxRegions = 2
	h.settings.groups = map[int64]ports.UISettings{5: wide}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.Tolerance != 2 {
		t.Fatalf("group 5 tolerance = %d, want its max_regions 2", ev.Tolerance)
	}
	r, ev = spreadRow(t, h, 2)
	wantSpreadRow(t, r, domain.GeoStateFlagged, domain.RiskCodeSpread)
	if ev.Tolerance != 1 {
		t.Fatalf("group 0 tolerance = %d, want the global 1", ev.Tolerance)
	}
}

// The group's switches reach the signal: risk.sub_spread_off, and a geo
// scope of "country" (provinces are not judged there). Both read disabled
// with no evidence.
func TestRefresh_GroupSwitchesReachSubSpread(t *testing.T) {
	h := newSpreadHarness(usersInGroups(5, 6), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipHunan, "ClashX Pro/1.118.0")),
	))
	off, country := h.settings.global, h.settings.global
	off.RiskSubSpreadOff = true
	country.GeoAnomalyScope = "country"
	h.settings.groups = map[int64]ports.UISettings{5: off, 6: country}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid, code := range map[int64]domain.RiskCode{1: domain.RiskCodeSignalOff, 2: domain.RiskCodeScopeCountry} {
		r, _ := spreadRow(t, h, uid)
		wantSpreadRow(t, r, domain.GeoStateDisabled, code)
		if r.Evidence != nil {
			t.Fatalf("user %d: disabled with evidence %s", uid, r.Evidence)
		}
	}
}

// The admin ignore list applies here as on the poll, and one bad line does
// not switch the good ones off.
func TestRefresh_IgnoreListApplies(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
	))
	h.settings.global.GeoAnomalyIgnoreAddresses = "not-an-address\n198.51.100.0/24 # the office"
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.Excluded.Listed != 1 {
		t.Fatalf("excluded %+v, want Hunan's source set aside as listed", ev.Excluded)
	}
}

// Fetches filed under an account the user list does not have (deleted
// mid-window, or a row the list skipped) are not judged — and they do not
// count toward a shared exit either: two accounts on one source is a
// household, and a ghost third must not turn it into an office.
func TestRefresh_FetchesOfUnknownUsersAreIgnored(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0), rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipOffice, "office-proxy/1")),
		everyDay(t, phone(2, ipHomeGD2)), everyDay(t, client(2, ipOffice, "office-proxy/1")),
		everyDay(t, client(99, ipOffice, "office-proxy/1")),
	))
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	if _, ok := rows[99]; ok {
		t.Fatalf("saved rows for an account that is not in the user list: %v", rows[99])
	}
	for uid := int64(1); uid <= 2; uid++ {
		r, ev := spreadRow(t, h, uid)
		wantSpreadRow(t, r, domain.GeoStateFlagged, domain.RiskCodeSpread)
		if ev.Excluded.Shared != 0 {
			t.Fatalf("user %d: excluded %+v, want nothing shared between two accounts", uid, ev.Excluded)
		}
	}
}

// Without a geo database nothing can be placed: unknown, not clean, and the
// resolver is not asked to look anything up. A deployment with no resolver
// wired reads the same.
func TestRefresh_NoGeoIsGeoUnavailable(t *testing.T) {
	rows := rowsOf(everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")))
	h := newSpreadHarness(usersInGroups(0), rows)
	h.geo.off = true
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateUnknown, domain.RiskCodeGeoUnavailable)
	if len(h.geo.lookups) != 0 {
		t.Fatalf("looked up %v with the database unavailable", h.geo.lookups)
	}
	if ev.Coverage.Sources != 2 {
		t.Fatalf("coverage %+v, want the two kept sources counted", ev.Coverage)
	}

	h = newSpreadHarness(usersInGroups(0), rows)
	h.geo = nil
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ = spreadRow(t, h, 1)
	wantSpreadRow(t, r, domain.GeoStateUnknown, domain.RiskCodeGeoUnavailable)
}

// A client that declares a device id is labelled by the newest label it
// declared — by when it was fetched, not by scan order, and a later fetch
// with no label does not erase it — and shown by a four-character prefix of
// its id. A client string is its own label, cut to 64 characters (not bytes).
func TestRefresh_SubSpreadLabelsClients(t *testing.T) {
	labelled := func(k int, label string) domain.SubLog {
		r := phone(1, ipHomeGD)(localDay(t, k, 9))
		r.DeviceLabel = label
		return r
	}
	long := strings.Repeat("客户端", 30) // 90 runes, 270 bytes
	h := newSpreadHarness(usersInGroups(0), []domain.SubLog{
		labelled(3, "Android 15 Pixel 9"),
		labelled(1, "Android 14 Pixel 9"),
		labelled(5, ""),
		client(1, ipHomeGD, long)(localDay(t, 2, 9)),
	})
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ev := spreadRow(t, h, 1)
	byKind := map[string]domain.SubIdentityEvidence{}
	for _, id := range ev.Identities {
		byKind[id.Kind] = id
	}
	if got := byKind["hwid"]; got.Label != "Android 15 Pixel 9" || got.HWID4 != phoneID[:4] || got.Days != 1<<1|1<<3|1<<5 {
		t.Fatalf("hwid client = %+v, want the newest label, prefix %q, days 1, 3 and 5", got, phoneID[:4])
	}
	if got := byKind["ua"]; got.Label != strings.Repeat("客户端", 30)[:len("客户端")*21+len("客")] {
		t.Fatalf("ua client label = %q (%d runes), want the first 64 runes", got.Label, len([]rune(got.Label)))
	}
}

// Nothing the worker saves carries an address. The evidence names provinces,
// countries, client labels and a four-character device prefix; every input
// address, the /24 and /64 around it, and the full device id stay out — of
// sub_spread's evidence and of devices'.
func TestRefresh_SavedEvidenceHasNoInputAddress(t *testing.T) {
	rows := rowsOf(
		everyDay(t, phone(1, ipHomeGD)), everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
		everyDay(t, client(1, ipTokyo, "clash.meta/1.19.2")),
		everyDay(t, phone(2, "2408:8207:1:2::a")), everyDay(t, client(2, ipRelay, "clash.meta/1.19.2")),
		everyDay(t, client(2, ipOffice, "office-proxy/1")), everyDay(t, client(3, ipOffice, "office-proxy/1")),
		everyDay(t, client(4, ipOffice, "office-proxy/1")), everyDay(t, client(4, "10.0.0.7", "lan/1")),
	)
	h := newSpreadHarness(usersInGroups(0, 0, 0, 0), rows)
	relay := netip.MustParseAddr(ipRelay)
	h.isInfra = func(a netip.Addr) bool { return a == relay }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	leaks := []string{phoneID, "203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24", "2408:8207:1:2::/64", "2408:8207:1"}
	for _, r := range rows {
		leaks = append(leaks, r.IP)
	}
	saved := map[domain.RiskKind]int{}
	for _, kinds := range h.store.saved(t) {
		for _, r := range kinds {
			if r.Kind == domain.RiskKindUsageShift || r.Evidence == nil {
				continue
			}
			saved[r.Kind]++
			for _, leak := range leaks {
				if strings.Contains(string(r.Evidence), leak) {
					t.Fatalf("user %d %s evidence carries %q: %s", r.UserID, r.Kind, leak, r.Evidence)
				}
			}
		}
	}
	if want := map[domain.RiskKind]int{domain.RiskKindSubSpread: 4, domain.RiskKindDevices: 4}; !reflect.DeepEqual(saved, want) {
		t.Fatalf("checked rows with evidence %v, want %v", saved, want)
	}
}

// Each province carries its region's ISO code from the lookups of the
// sources placed there, normalized, and the smallest valid one across every
// source and client: a database that codes one home address and not the
// other still names the province. The phone fetches from two Guangdong
// addresses, only one of them coded; the worker merges its cells in map
// order, so the week is judged on twenty fresh harnesses and must name
// Guangdong every time. The code is display only — the province is still
// keyed by its name (domain TestSubSpread_RegionCodeNeverChangesTheVerdict).
func TestRefresh_SubSpreadProvincesCarryTheRegionCode(t *testing.T) {
	for run := range 20 {
		h := newSpreadHarness(usersInGroups(0), rowsOf(
			everyDay(t, phone(1, ipHomeGD)),
			everyDay(t, phone(1, ipHomeGD2)),
			everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
		))
		for ip, rc := range map[string]string{ipHomeGD: "GD", ipHomeGD2: "", ipHunan: "hn"} {
			g := h.geo.places[ip]
			g.RegionCode = rc
			h.geo.places[ip] = g
		}
		if err := h.service().RefreshOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		r, ev := spreadRow(t, h, 1)
		wantSpreadRow(t, r, domain.GeoStateFlagged, domain.RiskCodeSpread)
		got := map[string]string{}
		for _, p := range ev.Provinces {
			got[p.Region] = p.RC
		}
		if want := map[string]string{"Guangdong": "GD", "Hunan": "HN"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: province codes %v, want %v (evidence %s)", run, got, want, r.Evidence)
		}
	}
}

// Coordinates never reach a saved row (I2). Since authcore v0.5.0 every
// lookup the worker reads carries the network's latitude, longitude and
// accuracy radius; none of the place kinds stores them — not sub_spread's
// provinces, not devices', not login_country's events. Each place is given
// its own six-decimal coordinates, so a leak of any one of them shows, in
// full or cut to its first six characters.
func TestRefresh_SavedEvidenceHasNoCoordinate(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0, 0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day))...)
	ips := make([]string, 0, len(h.geo.places))
	for ip := range h.geo.places {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	var needles []string
	for i, ip := range ips {
		lat, err := strconv.ParseFloat(fmt.Sprintf("%d.%06d", 20+i, 543121+i), 64)
		if err != nil {
			t.Fatal(err)
		}
		lon, err := strconv.ParseFloat(fmt.Sprintf("%d.%06d", 110+i, 57861+i), 64)
		if err != nil {
			t.Fatal(err)
		}
		g := h.geo.places[ip]
		g.Latitude, g.Longitude, g.AccuracyRadiusKm = lat, lon, 20+i
		h.geo.places[ip] = g
		for _, v := range []float64{lat, lon} {
			s := strconv.FormatFloat(v, 'f', -1, 64)
			needles = append(needles, s, s[:6])
		}
	}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	checked := map[domain.RiskKind]int{}
	var all strings.Builder
	for _, kinds := range h.store.saved(t) {
		for _, r := range kinds {
			if r.Kind == domain.RiskKindUsageShift || r.Evidence == nil {
				continue
			}
			checked[r.Kind]++
			all.Write(r.Evidence)
			for _, needle := range needles {
				if strings.Contains(string(r.Evidence), needle) {
					t.Fatalf("user %d %s evidence carries the coordinate %q: %s", r.UserID, r.Kind, needle, r.Evidence)
				}
			}
			for _, key := range []string{"latitude", "longitude", "accuracy"} {
				if strings.Contains(strings.ToLower(string(r.Evidence)), key) {
					t.Fatalf("user %d %s evidence carries %q: %s", r.UserID, r.Kind, key, r.Evidence)
				}
			}
		}
	}
	for _, kind := range []domain.RiskKind{domain.RiskKindSubSpread, domain.RiskKindDevices, domain.RiskKindLoginCountry} {
		if checked[kind] == 0 {
			t.Fatalf("checked no %s row with evidence (checked %v): the guard is looking at nothing", kind, checked)
		}
	}
	// Not vacuous: the places the coordinates belong to are there.
	for _, want := range []string{`"Guangdong"`, `"JP"`} {
		if !strings.Contains(all.String(), want) {
			t.Fatalf("saved evidence lacks %s: %s", want, all.String())
		}
	}
}
