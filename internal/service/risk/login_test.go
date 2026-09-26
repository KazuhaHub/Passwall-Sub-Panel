package risk

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- login-log fake --------------------------------------------------------

// fakeLogins serves the authentication-event log the way the repository
// does: filtered by outcome and by the keyset cursor (id > AfterID), ordered
// by the sort it is asked for (its default otherwise: newest first), and
// paged by offset with the repository's cap — a page size above 200 is
// served as 200, as applyPagination does. The since bound is NOT applied,
// like the scanner's: the service must cut the lookback itself. afterPage
// runs once each read has been served, with the number of that read, so a
// test can land a new login, or prune old ones, between two pages.
type fakeLogins struct {
	events    []*domain.AuthEvent
	err       error
	calls     []ports.AuthEventFilter
	afterPage func(read int)
	lastID    int64
	// ignoreCursor serves every read as if AfterID were 0: a store that
	// lost the cursor.
	ignoreCursor bool
}

func (f *fakeLogins) List(_ context.Context, filter ports.AuthEventFilter) ([]*domain.AuthEvent, int64, error) {
	f.calls = append(f.calls, filter)
	if f.err != nil {
		return nil, 0, f.err
	}
	var match []*domain.AuthEvent
	for _, e := range f.events {
		if !f.ignoreCursor && filter.AfterID > 0 && e.ID <= filter.AfterID {
			continue
		}
		if filter.Outcome == "" || string(e.Outcome) == filter.Outcome {
			match = append(match, e)
		}
	}
	if filter.SortBy == "id" && filter.SortDir == "asc" {
		sort.SliceStable(match, func(i, j int) bool { return match[i].ID < match[j].ID })
	} else {
		sort.SliceStable(match, func(i, j int) bool {
			if !match[i].At.Equal(match[j].At) {
				return match[i].At.After(match[j].At)
			}
			return match[i].ID > match[j].ID
		})
	}
	total := int64(len(match))
	if filter.PageSize > 0 {
		size := min(filter.PageSize, 200)
		from := min((max(filter.Page, 1)-1)*size, len(match))
		match = match[from:min(from+size, len(match))]
	}
	out := append([]*domain.AuthEvent(nil), match...)
	if f.afterPage != nil {
		f.afterPage(len(f.calls))
	}
	return out, total, nil
}

// add appends events, numbering them after every one ever added: ids only
// grow, and a pruned id is never reused.
func (f *fakeLogins) add(events ...*domain.AuthEvent) {
	for _, e := range events {
		f.lastID++
		e.ID = f.lastID
		f.events = append(f.events, e)
	}
}

// deleteBefore drops the events older than cutoff, as the hourly
// retention prune does (AuthEventRepo.DeleteBefore), and says how many.
func (f *fakeLogins) deleteBefore(cutoff time.Time) int {
	kept := f.events[:0]
	for _, e := range f.events {
		if !e.At.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	n := len(f.events) - len(kept)
	f.events = kept
	return n
}

// ---- fixtures ----------------------------------------------------------------

// More places, beside the fetch window's (subspread_test.go).
const (
	ipLandingJP = "192.0.2.90"    // JP / Osaka: one of PSP's landing nodes
	ipTokyo2    = "192.0.2.81"    // JP / Tokyo
	ipTokyo3    = "192.0.2.82"    // JP / Tokyo
	ipLoginUS   = "198.51.100.40" // US / California
)

func loginGeo() *fakeGeo {
	g := spreadGeo()
	tokyo := domain.GeoLocation{CountryCode: "JP", Region: "Tokyo"}
	g.places[ipLandingJP] = domain.GeoLocation{CountryCode: "JP", Region: "Osaka"}
	g.places[ipTokyo2], g.places[ipTokyo3] = tokyo, tokyo
	g.places[ipLoginUS] = domain.GeoLocation{CountryCode: "US", Region: "California"}
	return g
}

// signIn is a successful local login of account uid from ip, d before the
// run.
func signIn(uid int64, ip string, d time.Duration) *domain.AuthEvent {
	return &domain.AuthEvent{UserID: uid, UPN: "u@example.test", Method: domain.AuthMethodLocal,
		Outcome: domain.AuthOutcomeSuccess, IP: ip, UA: "Mozilla/5.0", At: refreshNow.Add(-d)}
}

// settledAt is account uid's history: three logins from ip, 10..12 days
// ago — before the recent days, inside the lookback.
func settledAt(uid int64, ip string) []*domain.AuthEvent {
	return []*domain.AuthEvent{signIn(uid, ip, 10*day), signIn(uid, ip, 11*day), signIn(uid, ip, 12*day)}
}

const day = 24 * time.Hour

// newLoginHarness is a fetch-window harness with the login log wired: every
// account in users (at most three) fetches from its own home in Guangdong
// every day, so China is established for all of them. Three accounts on one
// address would be a shared exit, and establish nothing.
func newLoginHarness(t *testing.T, users []*domain.User, events ...*domain.AuthEvent) *harness {
	t.Helper()
	homes := []string{ipHomeGD, ipHomeGD2, ipHomeGD3}
	if len(users) > len(homes) {
		t.Fatalf("%d accounts, but only %d homes", len(users), len(homes))
	}
	var rows []domain.SubLog
	for i, u := range users {
		rows = append(rows, everyDay(t, phone(u.ID, homes[i]))...)
	}
	h := newSpreadHarness(users, rows)
	h.geo = loginGeo()
	h.logins = &fakeLogins{}
	h.logins.add(events...)
	return h
}

// loginRow is one account's saved login_country row with its evidence
// decoded.
func loginRow(t *testing.T, h *harness, uid int64) (domain.RiskSignal, domain.LoginCountryEvidence) {
	t.Helper()
	r, ok := h.store.saved(t)[uid][domain.RiskKindLoginCountry]
	if !ok {
		t.Fatalf("no login_country row saved for user %d", uid)
	}
	var ev domain.LoginCountryEvidence
	if r.Evidence != nil {
		if err := json.Unmarshal(r.Evidence, &ev); err != nil {
			t.Fatalf("user %d evidence %s: %v", uid, r.Evidence, err)
		}
	}
	return r, ev
}

func wantLoginRow(t *testing.T, r domain.RiskSignal, state domain.GeoState, code domain.RiskCode) {
	t.Helper()
	if r.State != state || r.Code != code {
		t.Fatalf("user %d login_country = %s/%s, want %s/%s (evidence %s)", r.UserID, r.State, r.Code, state, code, r.Evidence)
	}
}

func eventCCs(ev domain.LoginCountryEvidence) []string {
	out := []string{}
	for _, e := range ev.Events {
		out = append(out, e.CC)
	}
	return out
}

// ---- tests -------------------------------------------------------------------

// The login log is judged per account. User 1 signed in from home three
// times, then from Tokyo: a new country, flagged. User 2 never signed in —
// a failed attempt from abroad is not a login — and reads idle with no
// evidence. Unresolved attempts (no account) and logins of accounts the user
// list does not hold are ignored. The log is read successes only, in id
// order, 200 rows a page, from at least 90 days back.
func TestRefresh_LoginCountryJudgesTheLogins(t *testing.T) {
	failed := signIn(2, ipLoginUS, day)
	failed.Outcome = domain.AuthOutcomeFailure
	h := newLoginHarness(t, usersInGroups(0, 0), append(settledAt(1, ipHomeGD),
		signIn(1, ipTokyo, day), failed, signIn(0, ipLoginUS, day), signIn(99, ipLoginUS, day))...)
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateFlagged, domain.RiskCodeNewCountry)
	if got := eventCCs(ev); !reflect.DeepEqual(got, []string{"JP"}) || ev.Logins != 4 || ev.Recent != 1 || ev.Judged != 1 {
		t.Fatalf("events %v, logins %d, recent %d, judged %d; want [JP], 4, 1 and 1", got, ev.Logins, ev.Recent, ev.Judged)
	}
	if ev.Events[0].AtMS != refreshNow.Add(-day).UnixMilli() || ev.Events[0].Method != "local" {
		t.Fatalf("event %+v, want the Tokyo login's time and method", ev.Events[0])
	}
	idle, _ := loginRow(t, h, 2)
	wantLoginRow(t, idle, domain.GeoStateIdle, domain.RiskCodeNoRecentLogins)
	if idle.Evidence != nil {
		t.Fatalf("an account that never signed in has evidence %s", idle.Evidence)
	}
	if _, ok := h.store.saved(t)[99]; ok {
		t.Fatal("saved rows for an account that is not in the user list")
	}
	if len(h.logins.calls) != 1 {
		t.Fatalf("read the login log %d times, want one short page", len(h.logins.calls))
	}
	c := h.logins.calls[0]
	if c.Outcome != string(domain.AuthOutcomeSuccess) || c.SortBy != "id" || c.SortDir != "asc" || c.PageSize != 200 || c.Page != 1 || c.AfterID != 0 {
		t.Fatalf("login read %+v, want successes by id ascending, 200 a page from the first id", c)
	}
	if c.Since == nil || c.Since.After(refreshNow.Add(-90*day)) {
		t.Fatalf("login read since %v, want a bound at or before 90 days back", c.Since)
	}
	wantOutcome(t, before, "ok")
}

// An account holder's browser often reaches the panel through their own
// proxy, and its egress is the landing node's. A login from a country one of
// PSP's landing nodes is in is set aside — even from another address there —
// and the landing addresses are looked up in the same batch as the logins.
// Without the landing set the same login is a new country.
func TestRefresh_LoginFromALandingCountryIsSkipped(t *testing.T) {
	events := append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, 2*day), signIn(1, ipHomeGD, day))
	h := newLoginHarness(t, usersInGroups(0), events...)
	h.landing = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(ipLandingJP)} }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)
	if ev.Skipped.NodeCountry != 1 || ev.Judged != 1 || len(ev.Events) != 0 {
		t.Fatalf("skipped %+v, judged %d, events %+v; want the Tokyo login skipped as a node country", ev.Skipped, ev.Judged, ev.Events)
	}
	if len(h.geo.lookups) != 2 {
		t.Fatalf("geo lookups %v, want the window's batch and one for the logins", h.geo.lookups)
	}
	batch := h.geo.lookups[1]
	sort.Strings(batch)
	if want := []string{ipLandingJP, ipTokyo, ipHomeGD}; !reflect.DeepEqual(batch, sortedCopy(want)) {
		t.Fatalf("login lookup batch %v, want the landing and each distinct login address once: %v", batch, sortedCopy(want))
	}

	h = newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, 2*day), signIn(1, ipHomeGD, day))...)
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ = loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateFlagged, domain.RiskCodeNewCountry)
}

// Relays are infrastructure — a login arriving FROM a relay is set aside —
// but their countries are not node countries. The owner's relays sit in the
// account holders' own country; skipping it would skip every login from
// home. So with a relay in China and the only landing in Japan, a login from
// a Chinese address is judged.
func TestRefresh_LoginFromARelayCountryIsJudged(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD),
		signIn(1, ipHunan, day), signIn(1, ipRelay, 2*day))...)
	relay := netip.MustParseAddr(ipRelay)
	h.isInfra = func(a netip.Addr) bool { return a == relay }
	h.landing = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(ipLandingJP)} }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)
	if ev.Judged != 1 || ev.Skipped.Infra != 1 || ev.Skipped.NodeCountry != 0 {
		t.Fatalf("judged %d, skipped %+v; want the Hunan login judged and only the relay's own address skipped", ev.Judged, ev.Skipped)
	}
	for _, ip := range h.geo.lookups[1] {
		if ip == ipRelay {
			t.Fatalf("looked up the relay's own address %v: an infrastructure login needs no country", h.geo.lookups[1])
		}
	}
}

// The login log is read as far back as it is kept: 90 days, or the
// auth-event retention when that is shorter (and 90 when it is 0, "keep
// forever", or longer). Logins before the lookback are not history, whatever
// the store hands back.
func TestRefresh_LookbackFollowsAuthEventRetention(t *testing.T) {
	events := func() []*domain.AuthEvent {
		return []*domain.AuthEvent{
			signIn(1, ipHomeGD, 40*day), signIn(1, ipHomeGD, 41*day), signIn(1, ipHomeGD, 42*day),
			signIn(1, ipHomeGD, 20*day), signIn(1, ipTokyo, day),
		}
	}
	for retention, want := range map[int]struct {
		lookback int
		code     domain.RiskCode
	}{
		30:  {30, domain.RiskCodeLearning},   // one earlier login inside 30 days
		0:   {90, domain.RiskCodeNewCountry}, // keep forever: the full 90 days
		200: {90, domain.RiskCodeNewCountry},
		89:  {89, domain.RiskCodeNewCountry},
	} {
		h := newLoginHarness(t, usersInGroups(0), events()...)
		h.settings.global.AuthEventRetentionDays = retention
		if err := h.service().RefreshOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		r, ev := loginRow(t, h, 1)
		if r.Code != want.code || ev.LookbackDays != want.lookback {
			t.Fatalf("retention %d: %s/%s over %d days, want %s over %d", retention, r.State, r.Code, ev.LookbackDays, want.code, want.lookback)
		}
		if since := h.logins.calls[0].Since; since == nil || since.After(refreshNow.Add(-time.Duration(want.lookback)*day)) {
			t.Fatalf("retention %d: read since %v, want at or before %d days back", retention, since, want.lookback)
		}
	}
}

// The repository serves at most 200 rows a page, so a loop that asked for
// more and stopped on a short page would read one page and stop. Every
// login is read. Ascending ids keep the pages stable while a login lands
// between two of them — newest-first paging would read one row twice and
// never see the new one. Each read is the first page after the last id
// the one before it served: a keyset walk, never page n by offset.
func TestRefresh_LoginPagesAreAllRead(t *testing.T) {
	var events []*domain.AuthEvent
	for i := range 450 {
		events = append(events, signIn(1, ipHomeGD, time.Duration(i+2)*4*time.Hour))
	}
	h := newLoginHarness(t, usersInGroups(0), events...)
	h.logins.afterPage = func(read int) {
		if read == 1 {
			h.logins.add(signIn(1, ipTokyo, time.Minute))
		}
	}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	if ev.Logins != 451 {
		t.Fatalf("read %d logins over %d pages, want all 451", ev.Logins, len(h.logins.calls))
	}
	wantLoginRow(t, r, domain.GeoStateFlagged, domain.RiskCodeNewCountry)
	if len(h.logins.calls) != 3 {
		t.Fatalf("read %d pages, want 3 (200, 200, 51)", len(h.logins.calls))
	}
	// Ids 1..450, then the new login as 451: the reads start after 0, 200
	// and 400.
	for i, c := range h.logins.calls {
		if after := int64(i * 200); c.Page != 1 || c.AfterID != after || c.PageSize > 200 || c.SortBy != "id" || c.SortDir != "asc" {
			t.Fatalf("read %d = %+v, want the first page after id %d, at most 200 by id ascending", i, c, after)
		}
	}
}

// The hourly retention prune deletes the logins older than the retention
// while a run may be between two pages, and those are the lowest ids of the
// read: the store's bound is a day wider than the lookback, so the band the
// prune cuts lies inside it. Paged by offset, the second page would then
// start that many rows late, and the rows the prune moved up past the
// boundary would never be read — here the one earlier login from Japan,
// which would turn today's login from Japan into a new country: an
// accusation from missing data. Each page is read after the last id of the
// one before, so every login that survives the prune is read.
func TestRefresh_LoginPagesSurviveARetentionPruneBetweenThem(t *testing.T) {
	var events []*domain.AuthEvent
	// 50 logins from the band the prune cuts: older than the 90-day
	// lookback, younger than the store's bound a day before it.
	for i := range 50 {
		events = append(events, signIn(1, ipHomeGD, 90*day+time.Duration(50-i)*10*time.Minute))
	}
	// 400 logins from home, oldest first, before the recent days; the 170th
	// of them (id 220, on the rows page 2 by offset would skip) is the one
	// earlier login from Tokyo.
	for i := range 400 {
		ip := ipHomeGD
		if i == 169 {
			ip = ipTokyo
		}
		events = append(events, signIn(1, ip, 8*day+time.Duration(400-i)*3*time.Hour))
	}
	h := newLoginHarness(t, usersInGroups(0), append(events, signIn(1, ipTokyo, day))...)
	pruned := 0
	h.logins.afterPage = func(read int) {
		if read == 1 {
			pruned = h.logins.deleteBefore(refreshNow.Add(-90 * day))
		}
	}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if pruned != 50 {
		t.Fatalf("the prune deleted %d logins between the pages, want the 50 of the band", pruned)
	}
	r, ev := loginRow(t, h, 1)
	if ev.Logins != 401 {
		t.Fatalf("judged %d logins of the lookback over %d reads, want all 401 that survived the prune", ev.Logins, len(h.logins.calls))
	}
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)
	if got := strings.Join(ev.Known, ","); got != "CN,JP" {
		t.Fatalf("known countries %s, want CN,JP: the earlier login from Japan was read", got)
	}
}

// The known countries come from the subscription fetches, so a login is
// judged only with the fetch window read in full: with a window that failed
// half-way China would not be known, and every account signing in from home
// would read as new. The kind keeps its previous row, and the run is
// partial.
func TestRefresh_LoginCountryWaitsForTheFetchWindow(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipHomeGD, day))...)
	svc := h.service()
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)

	h.scanner.err = errors.New("database is locked")
	h.scanner.errAfter = 2
	before := outcomes()
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.saved(t)[1][domain.RiskKindLoginCountry]; ok {
		t.Fatal("login_country judged although the fetch window failed half-way")
	}
	if _, ok := h.store.saved(t)[1][domain.RiskKindUsageShift]; !ok {
		t.Fatal("the fetch-window failure cost the usage_shift row too")
	}
	wantOutcome(t, before, "partial")
}

// Logins are placed against the infrastructure set too — a login through a
// relay carries the relay's address — so the kind waits for the set like
// sub_spread does, keeping its rows.
func TestRefresh_LoginCountryWaitsForTheInfraSet(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day))...)
	h.infraLoaded = func() bool { return false }
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.saved(t)[1][domain.RiskKindLoginCountry]; ok {
		t.Fatal("login_country judged before the infrastructure set loaded")
	}
	if len(h.logins.calls) != 0 {
		t.Fatalf("read the login log %d times for a kind that was skipped", len(h.logins.calls))
	}
	wantOutcome(t, before, "infra_pending")
}

// A login log that cannot be read costs login_country its rows this run —
// the previous ones stay — and nothing else: the fetch signals are written,
// and the run is partial.
func TestRefresh_LoginLogUnreadableKeepsItsRowsAndIsPartial(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0))
	h.logins.err = errors.New("auth_events locked")
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)[1]
	if _, ok := rows[domain.RiskKindLoginCountry]; ok {
		t.Fatal("login_country judged from a login log that could not be read")
	}
	for _, kind := range []domain.RiskKind{domain.RiskKindUsageShift, domain.RiskKindDevices, domain.RiskKindSubSpread} {
		if _, ok := rows[kind]; !ok {
			t.Fatalf("the login-log failure cost the %s row too", kind)
		}
	}
	wantOutcome(t, before, "partial")
}

// The walk ends on a short page, so a store that served a full page and
// then did not move past it — one that lost the cursor — would keep the
// run in the loop until shutdown, holding the read side of the operation
// gate all the while. A full page that does not pass the cursor is a login
// log that cannot be read: the kind keeps its previous rows, the run is
// partial, and the store is asked no third time.
func TestRefresh_LoginLogThatDoesNotAdvanceIsUnreadable(t *testing.T) {
	var events []*domain.AuthEvent
	for i := range 250 {
		events = append(events, signIn(1, ipHomeGD, time.Duration(i+2)*4*time.Hour))
	}
	h := newLoginHarness(t, usersInGroups(0), events...)
	h.logins.ignoreCursor = true
	// A backstop for a walk with no end: every read after the second fails.
	h.logins.afterPage = func(read int) {
		if read == 2 {
			h.logins.err = errors.New("read past the backstop")
		}
	}
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.logins.calls); n != 2 {
		t.Fatalf("read the login log %d times, want 2: the second read served the first page again", n)
	}
	if _, ok := h.store.saved(t)[1][domain.RiskKindLoginCountry]; ok {
		t.Fatal("login_country judged from a login log that did not advance")
	}
	wantOutcome(t, before, "partial")
}

// A country the account's fetches established — on the GROUP's min_days of
// the week — is known to its logins. Tokyo on every day is established;
// Tokyo on two days is not, under the default three, and the same login is a
// new country.
func TestRefresh_SubscriptionCountriesAreKnownToLogins(t *testing.T) {
	events := []*domain.AuthEvent{}
	for uid := int64(1); uid <= 3; uid++ {
		events = append(events, settledAt(uid, ipHomeGD)...)
		events = append(events, signIn(uid, ipTokyo, day))
	}
	h := newLoginHarness(t, usersInGroups(0, 0, 5), events...)
	h.scanner.rows = append(h.scanner.rows, everyDay(t, client(1, ipTokyo, "clash.meta/1.19.2"))...)
	for uid, ip := range map[int64]string{2: ipTokyo2, 3: ipTokyo3} {
		for _, k := range []int{2, 4} {
			h.scanner.rows = append(h.scanner.rows, client(uid, ip, "clash.meta/1.19.2")(localDay(t, k, 9)))
		}
	}
	lenient := h.settings.global
	lenient.RiskMinDays = 2
	h.settings.groups = map[int64]ports.UISettings{5: lenient}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)
	if !reflect.DeepEqual(ev.Known, []string{"CN", "JP"}) {
		t.Fatalf("user 1 known = %v, want [CN JP]", ev.Known)
	}
	r, _ = loginRow(t, h, 2)
	wantLoginRow(t, r, domain.GeoStateFlagged, domain.RiskCodeNewCountry)
	r, _ = loginRow(t, h, 3) // group 5: two days establish a country
	wantLoginRow(t, r, domain.GeoStateClean, domain.RiskCodeKnownCountries)
}

// The group's switches reach the signal: risk.login_country_off, and a geo
// scope of "off". Both read disabled with no evidence. An exempt group
// (allow_anywhere) reads exempt.
func TestRefresh_GroupSwitchesReachLoginCountry(t *testing.T) {
	var events []*domain.AuthEvent
	for uid := int64(1); uid <= 3; uid++ {
		events = append(events, settledAt(uid, ipHomeGD)...)
		events = append(events, signIn(uid, ipTokyo, day))
	}
	h := newLoginHarness(t, usersInGroups(5, 6, 7), events...)
	off, scopeOff, anywhere := h.settings.global, h.settings.global, h.settings.global
	off.RiskLoginCountryOff = true
	scopeOff.GeoAnomalyScope = "off"
	anywhere.GeoAnomalyAllowAnywhere = true
	h.settings.groups = map[int64]ports.UISettings{5: off, 6: scopeOff, 7: anywhere}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid, want := range map[int64][2]string{
		1: {string(domain.GeoStateDisabled), string(domain.RiskCodeSignalOff)},
		2: {string(domain.GeoStateDisabled), string(domain.RiskCodeScopeOff)},
		3: {string(domain.GeoStateExempt), string(domain.RiskCodeAllowAnywhere)},
	} {
		r, _ := loginRow(t, h, uid)
		wantLoginRow(t, r, domain.GeoState(want[0]), domain.RiskCode(want[1]))
		if r.Evidence != nil {
			t.Fatalf("user %d: %s with evidence %s", uid, r.Code, r.Evidence)
		}
	}
}

// Without a geo database no login can be placed: unknown, not clean, and no
// login address is looked up.
func TestRefresh_LoginCountryWithoutGeoIsUnknown(t *testing.T) {
	h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day))...)
	h.geo.off = true
	h.landing = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(ipLandingJP)} }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ := loginRow(t, h, 1)
	wantLoginRow(t, r, domain.GeoStateUnknown, domain.RiskCodeGeoUnavailable)
	if len(h.geo.lookups) != 0 {
		t.Fatalf("looked up %v with the database unavailable", h.geo.lookups)
	}
}

// Nothing login_country saves carries an address: every login address, the
// landing's and the relay's stay out of the evidence.
func TestRefresh_LoginEvidenceHasNoInputAddress(t *testing.T) {
	events := append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day), signIn(1, ipRelay, day),
		signIn(1, "10.0.0.7", day), signIn(1, ipLoginUS, 2*day), signIn(1, "2408:8207:1:2::a", 3*day), signIn(1, "not-an-ip", day))
	h := newLoginHarness(t, usersInGroups(0), events...)
	relay := netip.MustParseAddr(ipRelay)
	h.isInfra = func(a netip.Addr) bool { return a == relay }
	h.landing = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(ipLandingJP)} }
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := loginRow(t, h, 1)
	if ev.Judged == 0 || ev.Skipped.Infra != 1 || ev.Skipped.Internal != 1 || ev.Skipped.Unplaced != 1 {
		t.Fatalf("evidence %s: want logins judged and the relay, LAN and unparseable ones skipped", r.Evidence)
	}
	for _, e := range events {
		if strings.Contains(string(r.Evidence), e.IP) {
			t.Fatalf("login_country evidence carries %q: %s", e.IP, r.Evidence)
		}
	}
	for _, leak := range []string{ipLandingJP, "2408:8207:1", "203.0.113.0/24", "Mozilla"} {
		if strings.Contains(string(r.Evidence), leak) {
			t.Fatalf("login_country evidence carries %q: %s", leak, r.Evidence)
		}
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
