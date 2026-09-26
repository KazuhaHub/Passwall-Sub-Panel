package risk

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
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
