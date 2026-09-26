package domain

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// loginNow is the run's clock in the login_country fixtures.
var loginNow = time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)

// ago is an instant d before loginNow, as unix ms.
func ago(d time.Duration) int64 { return loginNow.Add(-d).UnixMilli() }

const day = 24 * time.Hour

// placedAt is a login placed in country cc, d ago.
func placedAt(cc string, d time.Duration) LoginSighting {
	return LoginSighting{AtMS: ago(d), CC: cc, Method: string(AuthMethodLocal)}
}

// skippedAt is a login set aside for reason, d ago. cc is what its address
// would have placed it in; a skipped login must say nothing whatever it is.
func skippedAt(reason, cc string, d time.Duration) LoginSighting {
	return LoginSighting{AtMS: ago(d), CC: cc, Method: string(AuthMethodLocal), Skip: reason}
}

// homeLogins is an account's settled history: n logins from China, spread
// over the month before the recent days.
func homeLogins(n int) []LoginSighting {
	out := make([]LoginSighting, n)
	for i := range out {
		out[i] = placedAt("CN", time.Duration(10+i)*day)
	}
	return out
}

// loginPolicy is the shipped policy: the signal on, the default geo policy.
func loginPolicy() LoginCountryPolicy {
	return LoginCountryPolicy{Geo: DefaultGeoPolicy()}
}

// loginInput is a 90-day lookback with the geo database available and no
// country established by the fetches.
func loginInput(logins ...LoginSighting) LoginCountryInput {
	return LoginCountryInput{
		NowMS: loginNow.UnixMilli(), LookbackDays: RiskLoginLookbackDays, GeoAvailable: true,
		Logins: logins,
	}
}

func wantLogin(t *testing.T, v RiskVerdict, state GeoState, code RiskCode) {
	t.Helper()
	if v.State != state || v.Code != code {
		t.Fatalf("verdict = %s/%s, want %s/%s", v.State, v.Code, state, code)
	}
}

func mustLoginEvidence(t *testing.T, ev *LoginCountryEvidence) *LoginCountryEvidence {
	t.Helper()
	if ev == nil {
		t.Fatal("no evidence for a verdict that judged something")
	}
	return ev
}

func eventCountries(ev *LoginCountryEvidence) []string {
	out := []string{}
	for _, e := range ev.Events {
		out = append(out, e.CC)
	}
	return out
}

// With fewer than three earlier placed logins there is nothing to call a
// country new against: the account's first logins ever, from anywhere, are
// learning. The third earlier login is what makes the fourth judged.
func TestLoginCountry_LearningBeforeThreePriorLogins(t *testing.T) {
	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(2), placedAt("JP", day))...))
	wantLogin(t, v, GeoStateUnknown, RiskCodeLearning)
	if ev = mustLoginEvidence(t, ev); ev.Judged != 0 || len(ev.Events) != 0 || ev.Warmup != RiskLoginWarmupLogins {
		t.Fatalf("judged %d, events %+v, warmup %d; want nothing judged against a warm-up of %d",
			ev.Judged, ev.Events, ev.Warmup, RiskLoginWarmupLogins)
	}

	v, ev = EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), placedAt("JP", day))...))
	wantLogin(t, v, GeoStateFlagged, RiskCodeNewCountry)
	if ev = mustLoginEvidence(t, ev); ev.Judged != 1 {
		t.Fatalf("judged %d with three earlier logins, want 1", ev.Judged)
	}
}

// After the warm-up, a login from a country no earlier login came from and
// the fetches never established is flagged — the account-takeover shape —
// and the event names the country, when, and how the account signed in.
func TestLoginCountry_NewCountryAfterWarmupIsFlagged(t *testing.T) {
	jp := placedAt("JP", 2*day)
	jp.Method = string(AuthMethodOIDC)
	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), jp, placedAt("CN", day))...))
	wantLogin(t, v, GeoStateFlagged, RiskCodeNewCountry)
	ev = mustLoginEvidence(t, ev)
	want := []LoginEventEvidence{{CC: "JP", AtMS: jp.AtMS, Method: "oidc"}}
	if !reflect.DeepEqual(ev.Events, want) {
		t.Fatalf("events = %+v, want %+v", ev.Events, want)
	}
	if ev.Recent != 2 || ev.Judged != 2 || ev.Logins != 5 {
		t.Fatalf("logins %d, recent %d, judged %d; want 5, 2 and 2", ev.Logins, ev.Recent, ev.Judged)
	}
	if !reflect.DeepEqual(ev.Known, []string{"CN"}) {
		t.Fatalf("known = %v, want [CN]: the event's own country is listed as the event, not as known", ev.Known)
	}
}

// The subscription fetches are where the account's clients actually are. A
// country they established is known even if no earlier login came from it:
// a subscriber who never signed in from their holiday flat is not accused
// the first time they do.
func TestLoginCountry_SubscriptionCountryCountsAsKnown(t *testing.T) {
	in := loginInput(append(homeLogins(3), placedAt("JP", day))...)
	in.Known = []string{"JP"}
	v, ev := EvaluateLoginCountry(loginPolicy(), in)
	wantLogin(t, v, GeoStateClean, RiskCodeKnownCountries)
	if ev = mustLoginEvidence(t, ev); !reflect.DeepEqual(ev.Known, []string{"CN", "JP"}) || ev.Judged != 1 {
		t.Fatalf("known %v, judged %d; want [CN JP] and 1", ev.Known, ev.Judged)
	}
}

// A flag holds for seven days and then decays: the event login is no longer
// recent, and its country is known from then on. The boundary is inclusive —
// exactly seven days ago is still recent.
func TestLoginCountry_FlagDecaysAfterSevenDays(t *testing.T) {
	hold := time.Duration(RiskLoginHoldDays) * day
	v, _ := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), placedAt("JP", hold), placedAt("CN", day))...))
	wantLogin(t, v, GeoStateFlagged, RiskCodeNewCountry)

	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), placedAt("JP", hold+time.Millisecond), placedAt("CN", day))...))
	wantLogin(t, v, GeoStateClean, RiskCodeKnownCountries)
	if ev = mustLoginEvidence(t, ev); !reflect.DeepEqual(ev.Known, []string{"CN", "JP"}) || len(ev.Events) != 0 {
		t.Fatalf("known %v, events %+v; want JP known once its event decayed, and no event", ev.Known, ev.Events)
	}
}

// One new country is one event. Its second login finds it known — the first
// one is an earlier login from it — so a week abroad is one event, not one
// per sign-in. A second new country is a second event, listed newest first.
func TestLoginCountry_SecondLoginFromTheSameNewCountryIsNotASecondEvent(t *testing.T) {
	first, second := placedAt("JP", 3*day), placedAt("JP", 2*day)
	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), second, first)...))
	wantLogin(t, v, GeoStateFlagged, RiskCodeNewCountry)
	ev = mustLoginEvidence(t, ev)
	if len(ev.Events) != 1 || ev.Events[0].AtMS != first.AtMS || ev.Judged != 2 {
		t.Fatalf("events %+v, judged %d; want one event, the first JP login, and both judged", ev.Events, ev.Judged)
	}

	_, ev = EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), first, second, placedAt("US", day))...))
	if got := eventCountries(mustLoginEvidence(t, ev)); !reflect.DeepEqual(got, []string{"US", "JP"}) {
		t.Fatalf("event countries = %v, want [US JP] (newest first)", got)
	}
}

// A skipped login says nothing about where the account holder is: it is not
// an earlier login a later one is measured against, and it is never an
// event, whatever country its address was in. Only the counts record it.
func TestLoginCountry_SkippedLoginsAreNeitherPriorsNorEvents(t *testing.T) {
	logins := append(homeLogins(2),
		skippedAt(AddressExcludedInfra, "CN", 20*day),
		skippedAt(AddressExcludedInternal, "", 21*day),
		skippedAt(AddressExcludedListed, "US", 22*day),
		skippedAt(LoginSkipNodeCountry, "JP", 2*day), // through the account's own proxy
		skippedAt(LoginSkipUnplaced, "", 2*day),
		placedAt("CN", day),
	)
	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(logins...))
	// Two placed logins before the recent one, not seven: still learning.
	wantLogin(t, v, GeoStateUnknown, RiskCodeLearning)
	ev = mustLoginEvidence(t, ev)
	want := LoginSkips{Infra: 1, Internal: 1, Listed: 1, NodeCountry: 1, Unplaced: 1}
	if ev.Skipped != want || len(ev.Events) != 0 || ev.Logins != 8 || ev.Recent != 3 {
		t.Fatalf("skipped %+v, events %+v, logins %d, recent %d; want %+v, no event, 8 and 3",
			ev.Skipped, ev.Events, ev.Logins, ev.Recent, want)
	}
	if !reflect.DeepEqual(ev.Known, []string{"CN"}) {
		t.Fatalf("known = %v, want [CN]: a skipped login makes no country known", ev.Known)
	}
	// An unrecognised reason, or a login with no reason and no country, is
	// counted as unplaced — never read as placed.
	v, ev = EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3),
		skippedAt("unparseable", "JP", day), LoginSighting{AtMS: ago(day)})...))
	wantLogin(t, v, GeoStateUnknown, RiskCodeUnplaced)
	if ev = mustLoginEvidence(t, ev); ev.Skipped.Unplaced != 2 {
		t.Fatalf("skipped %+v, want both odd logins counted as unplaced", ev.Skipped)
	}
}

// Logins older than the lookback are not read as history, and a login
// stamped after the run's clock is not in it yet (the next run sees it).
// The lookback is the auth-event retention when that is shorter, clamped to
// 1..90; 0 means the full 90 days.
func TestLoginCountry_LookbackBoundsTheLogins(t *testing.T) {
	logins := append(homeLogins(3), placedAt("CN", 40*day), placedAt("JP", day))
	for i := range 3 {
		logins[i].AtMS = ago(time.Duration(50+i) * day)
	}
	in := loginInput(logins...)
	in.LookbackDays = 45
	v, ev := EvaluateLoginCountry(loginPolicy(), in)
	wantLogin(t, v, GeoStateUnknown, RiskCodeLearning)
	if ev = mustLoginEvidence(t, ev); ev.LookbackDays != 45 || ev.Logins != 2 {
		t.Fatalf("lookback %d, logins %d; want 45 and the two logins inside it", ev.LookbackDays, ev.Logins)
	}

	for lookback, want := range map[int]int{0: 90, -3: 90, 120: 90, 1: 1} {
		in.LookbackDays = lookback
		_, ev = EvaluateLoginCountry(loginPolicy(), in)
		if ev = mustLoginEvidence(t, ev); ev.LookbackDays != want {
			t.Fatalf("lookback %d read as %d, want %d", lookback, ev.LookbackDays, want)
		}
	}

	in = loginInput(append(homeLogins(3), LoginSighting{AtMS: loginNow.Add(time.Minute).UnixMilli(), CC: "JP"})...)
	v, _ = EvaluateLoginCountry(loginPolicy(), in)
	wantLogin(t, v, GeoStateIdle, RiskCodeNoRecentLogins)
}

// No login in the last seven days — skipped ones included — is idle: there
// is nothing recent to judge, and no evidence outlives the week. Most
// accounts read this way; a subscription needs no panel login.
func TestLoginCountry_NoRecentLoginIsIdle(t *testing.T) {
	for _, logins := range [][]LoginSighting{nil, homeLogins(5)} {
		v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(logins...))
		wantLogin(t, v, GeoStateIdle, RiskCodeNoRecentLogins)
		if ev != nil {
			t.Fatalf("evidence %+v for an account with no recent login, want none", ev)
		}
	}
}

// Recent logins that all came through a node, an internal range or an
// address nobody could place say nothing: unknown, never clean.
func TestLoginCountry_OnlySkippedRecentIsUnknownUnplaced(t *testing.T) {
	v, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3),
		skippedAt(LoginSkipNodeCountry, "JP", day), skippedAt(AddressExcludedInfra, "CN", 2*day))...))
	wantLogin(t, v, GeoStateUnknown, RiskCodeUnplaced)
	if ev = mustLoginEvidence(t, ev); ev.Recent != 2 || ev.Judged != 0 || ev.Skipped.NodeCountry != 1 || ev.Skipped.Infra != 1 {
		t.Fatalf("recent %d, judged %d, skipped %+v; want 2 recent, none judged, both skips counted", ev.Recent, ev.Judged, ev.Skipped)
	}
}

// The signal's own switch, and a group geo scope of "off", read disabled
// with no evidence — the switch first. Scope "country" does not switch it
// off: countries are exactly what it judges.
func TestLoginCountry_ScopeOffIsDisabled(t *testing.T) {
	flagged := loginInput(append(homeLogins(3), placedAt("JP", day))...)
	p := loginPolicy()
	p.Geo.Scope = GeoScopeOff
	v, ev := EvaluateLoginCountry(p, flagged)
	wantLogin(t, v, GeoStateDisabled, RiskCodeScopeOff)
	if ev != nil {
		t.Fatalf("evidence %+v under scope off, want none", ev)
	}
	p.Off = true
	v, ev = EvaluateLoginCountry(p, flagged)
	wantLogin(t, v, GeoStateDisabled, RiskCodeSignalOff)
	if ev != nil {
		t.Fatalf("evidence %+v with the signal off, want none", ev)
	}
	p = loginPolicy()
	p.Geo.Scope = GeoScopeCountry
	v, _ = EvaluateLoginCountry(p, flagged)
	wantLogin(t, v, GeoStateFlagged, RiskCodeNewCountry)
}

// An account allowed to connect from anywhere is exempt, with no evidence.
func TestLoginCountry_AllowAnywhereIsExempt(t *testing.T) {
	p := loginPolicy()
	p.Geo.AllowAnywhere = true
	v, ev := EvaluateLoginCountry(p, loginInput(append(homeLogins(3), placedAt("JP", day))...))
	wantLogin(t, v, GeoStateExempt, RiskCodeAllowAnywhere)
	if ev != nil {
		t.Fatalf("evidence %+v for an exempt account, want none", ev)
	}
}

// Without a geo database no login can be placed: unknown, not clean — and
// not idle either, when there were recent logins to judge.
func TestLoginCountry_GeoUnavailableIsUnknown(t *testing.T) {
	in := loginInput(append(homeLogins(3), placedAt("JP", day))...)
	in.GeoAvailable = false
	v, ev := EvaluateLoginCountry(loginPolicy(), in)
	wantLogin(t, v, GeoStateUnknown, RiskCodeGeoUnavailable)
	if ev = mustLoginEvidence(t, ev); ev.Recent != 1 || len(ev.Events) != 0 {
		t.Fatalf("recent %d, events %+v; want the recent login counted and no event", ev.Recent, ev.Events)
	}
}

// The evidence names countries, times and sign-in methods — never an
// address, whatever the input carried.
func TestLoginCountry_EvidenceCarriesNoAddress(t *testing.T) {
	in := loginInput(append(homeLogins(3), placedAt("JP", day), placedAt("US", 2*day),
		skippedAt(AddressExcludedInfra, "CN", day), skippedAt(LoginSkipNodeCountry, "DE", day))...)
	in.Known = []string{"CN", "SG"}
	_, ev := EvaluateLoginCountry(loginPolicy(), in)
	raw, err := json.Marshal(mustLoginEvidence(t, ev))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	strs := 0
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			strs++
			if _, err := netip.ParseAddr(x); err == nil {
				t.Errorf("evidence carries an address: %q", x)
			}
			if _, err := netip.ParsePrefix(x); err == nil {
				t.Errorf("evidence carries a network: %q", x)
			}
		}
	}
	walk(doc)
	if strs == 0 {
		t.Fatal("walked no strings: the evidence is empty")
	}
}

// What one row stores is bounded — at most 12 known countries and 5 events,
// the newest — and every list is present, empty rather than null. Country
// codes are compared upper-cased, so a database answering "jp" is not a
// second country beside "JP".
func TestLoginCountry_EvidenceIsBoundedAndOrdered(t *testing.T) {
	logins := homeLogins(3)
	for i, cc := range []string{"AT", "BE", "CH", "DE", "ES", "FR", "GB"} {
		logins = append(logins, placedAt(cc, time.Duration(i+1)*time.Hour))
	}
	in := loginInput(logins...)
	in.Known = []string{"AU", "BR", "CA", "DK", "EE", "FI", "GR", "HU", "IE", "IS", "IT", "LU", "MT"}
	_, ev := EvaluateLoginCountry(loginPolicy(), in)
	ev = mustLoginEvidence(t, ev)
	if got := eventCountries(ev); !reflect.DeepEqual(got, []string{"AT", "BE", "CH", "DE", "ES"}) {
		t.Fatalf("events = %v, want the five newest (AT is an hour ago)", got)
	}
	if len(ev.Known) != RiskEvidenceMaxKnown || !sort.StringsAreSorted(ev.Known) || ev.Known[0] != "AU" {
		t.Fatalf("known = %v, want the first %d sorted", ev.Known, RiskEvidenceMaxKnown)
	}

	in = loginInput(placedAt("cn", 30*day), placedAt(" CN ", 20*day), placedAt("Cn", 10*day), placedAt("CN", day))
	v, ev := EvaluateLoginCountry(loginPolicy(), in)
	wantLogin(t, v, GeoStateClean, RiskCodeKnownCountries)
	if ev = mustLoginEvidence(t, ev); !reflect.DeepEqual(ev.Known, []string{"CN"}) || ev.Events == nil {
		t.Fatalf("known %v, events %#v; want [CN] and an empty list", ev.Known, ev.Events)
	}
	_, ev = EvaluateLoginCountry(loginPolicy(), loginInput(skippedAt(LoginSkipUnplaced, "", day)))
	if ev = mustLoginEvidence(t, ev); ev.Known == nil || ev.Events == nil {
		t.Fatalf("a nil list in %+v", ev)
	}
}

// The evidence's JSON field names are a wire contract: the SPA reads them
// from rows written by older builds too.
func TestLoginCountry_EvidenceShape(t *testing.T) {
	_, ev := EvaluateLoginCountry(loginPolicy(), loginInput(append(homeLogins(3), placedAt("JP", day))...))
	raw, err := json.Marshal(mustLoginEvidence(t, ev))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	wantKeys := func(what string, obj map[string]json.RawMessage, keys ...string) {
		t.Helper()
		var got []string
		for k := range obj {
			got = append(got, k)
		}
		sort.Strings(got)
		sort.Strings(keys)
		if !reflect.DeepEqual(got, keys) {
			t.Fatalf("%s keys = %v, want %v", what, got, keys)
		}
	}
	wantKeys("evidence", doc, "v", "lookback_days", "hold_days", "warmup", "logins", "recent", "judged",
		"skipped", "known", "events")
	var skipped map[string]json.RawMessage
	if err := json.Unmarshal(doc["skipped"], &skipped); err != nil {
		t.Fatal(err)
	}
	wantKeys("skipped", skipped, "infra", "internal", "listed", "node_country", "unplaced")
	var events []map[string]json.RawMessage
	if err := json.Unmarshal(doc["events"], &events); err != nil || len(events) != 1 {
		t.Fatalf("events %s: %v", doc["events"], err)
	}
	wantKeys("event", events[0], "cc", "at_ms", "method")
	if ev.V != RiskEvidenceVersion || ev.HoldDays != RiskLoginHoldDays || ev.Warmup != RiskLoginWarmupLogins ||
		ev.LookbackDays != RiskLoginLookbackDays {
		t.Fatalf("v %d, hold %d, warmup %d, lookback %d", ev.V, ev.HoldDays, ev.Warmup, ev.LookbackDays)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("evidence has a null: %s", raw)
	}
}

// loginFixtures reaches every code login_country can return, one fixture
// each.
func loginFixtures() []struct {
	p  LoginCountryPolicy
	in LoginCountryInput
} {
	flagged := func() LoginCountryInput { return loginInput(append(homeLogins(3), placedAt("JP", day))...) }
	with := func(f func(*LoginCountryPolicy)) LoginCountryPolicy { p := loginPolicy(); f(&p); return p }
	change := func(f func(*LoginCountryInput)) LoginCountryInput { in := flagged(); f(&in); return in }
	return []struct {
		p  LoginCountryPolicy
		in LoginCountryInput
	}{
		{with(func(p *LoginCountryPolicy) { p.Off = true }), flagged()},
		{with(func(p *LoginCountryPolicy) { p.Geo.Scope = GeoScopeOff }), flagged()},
		{with(func(p *LoginCountryPolicy) { p.Geo.AllowAnywhere = true }), flagged()},
		{loginPolicy(), loginInput(homeLogins(3)...)},
		{loginPolicy(), change(func(in *LoginCountryInput) { in.GeoAvailable = false })},
		{loginPolicy(), change(func(in *LoginCountryInput) { in.Logins[3].Skip = LoginSkipNodeCountry })},
		{loginPolicy(), change(func(in *LoginCountryInput) { in.Logins = in.Logins[1:] })},
		{loginPolicy(), flagged()},
		{loginPolicy(), change(func(in *LoginCountryInput) { in.Known = []string{"JP"} })},
	}
}

// AllRiskCodes()[login_country] is the list the SPA's locale keys are checked
// against, so it must be exactly the codes this evaluator can return.
func TestLoginCountry_CodesAreExactlyAllRiskCodes(t *testing.T) {
	reached := map[RiskCode]bool{}
	for _, f := range loginFixtures() {
		v, _ := EvaluateLoginCountry(f.p, f.in)
		reached[v.Code] = true
	}
	listed := map[RiskCode]bool{}
	for _, c := range AllRiskCodes()[RiskKindLoginCountry] {
		listed[c] = true
	}
	if !reflect.DeepEqual(reached, listed) {
		t.Fatalf("login_country codes reached %v, AllRiskCodes lists %v", reached, listed)
	}
}
