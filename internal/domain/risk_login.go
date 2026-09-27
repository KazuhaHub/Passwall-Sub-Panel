package domain

import (
	"sort"
	"strings"
)

// login_country: a panel login from a country the account has not been seen
// in.
//
// This is an account-takeover tripwire, not a sharing detector. A proxy
// subscription needs no panel login — the client fetches /sub/<token> — so
// an account holder signs in rarely: to check a quota, reset a password,
// enrol 2FA. That makes a login from a new country rare and pointed, and it
// says nothing about who is using the proxy.
//
//   - A login is PLACED when no address rule set it aside and the geo
//     database named its country. Logins from PSP's own nodes and relays,
//     internal ranges and the ignore list are skipped, and so is any login
//     from a country one of PSP's LANDING nodes is in: an account holder's
//     browser often reaches the panel through their own proxy, and its
//     egress is the landing's.
//   - A country is KNOWN when an earlier placed login came from it, or the
//     account's subscription fetches established it (on min_days of the
//     week's days). The fetches are where the clients actually are, so a
//     subscriber who never signed in from home is not accused the first
//     time they do.
//   - A recent login (the last hold days, RiskLoginHoldDays by default) is
//     JUDGED once the account has the warm-up's earlier placed logins
//     (RiskLoginWarmupLogins by default); before that there is nothing to
//     call it new against. Both are per-group policy (risk.login_warmup_logins,
//     risk.login_hold_days). A judged login from a country
//     that is not known is an EVENT, and any event flags the account. Each
//     new country is one event — its second login finds it known — and the
//     flag decays as the event leaves the recent days.
//
// Known errors. A CDN-fronted node's hostname resolves to the CDN, so the
// CDN's country is the "node country" and the VPS's own egress is not: a
// login through that tunnel can read as new (remedy: the ignore list, or
// fetches from that country). Relays are deliberately not node countries —
// relays usually sit in the account holder's own country, and skipping it
// would skip every login from home.
//
// Evidence never carries an address: country codes, times and methods.

// The login_country defaults: how many earlier placed logins a login needs
// before it is judged, how many days an event keeps the account flagged, and
// how far back the login log is read. Each is now a setting whose shipped
// value is this constant — the first two per group (RiskPolicy), the
// lookback fleet-wide (RiskRuntime, at most RiskLoginLookbackMaxDays).
const (
	RiskLoginWarmupLogins = 3
	RiskLoginHoldDays     = 7
	RiskLoginLookbackDays = 90
)

// Evidence caps: what one row stores and the admin table draws. The verdict
// is computed over every login; only the listing is bounded.
const (
	RiskEvidenceMaxKnown  = 12
	RiskEvidenceMaxEvents = 5
)

// The skip reasons a login carries besides AddressExclusion's own
// (internal, listed, infra): a country one of PSP's landing nodes is in, and
// an address no database placed (or no rule could read).
const (
	LoginSkipNodeCountry = "node_country"
	LoginSkipUnplaced    = "unplaced"
)

// LoginSighting is one successful panel login, as the evaluator reads it.
type LoginSighting struct {
	// AtMS is when it happened (unix ms).
	AtMS int64
	// CC is the upper-case country code of its address, "" when not
	// placed. Method is how the account signed in (local, saml, oidc,
	// passkey).
	CC, Method string
	// Skip is why the login says nothing about where the account holder
	// is: "" (it does), AddressExclusion's internal / listed / infra, or
	// LoginSkipNodeCountry / LoginSkipUnplaced.
	Skip string
}

// LoginCountryPolicy is what login_country judges with: the signal's own
// switch, the account's group geo policy, whose scope (off only) and
// exemption it reuses, and the group's two thresholds. The geo tolerances do
// not apply — one new country is the event.
type LoginCountryPolicy struct {
	Off bool
	Geo GeoAnomalyPolicy
	// WarmupLogins is how many earlier placed logins a login needs before
	// it is judged; HoldDays how many days an event keeps the account
	// flagged. 0 (or negative) is "never configured" and judges with
	// RiskLoginWarmupLogins / RiskLoginHoldDays — never with a warm-up of
	// nothing, which would judge an account's first login ever, or a hold of
	// no days. The worker hands over RiskPolicy's values, already clamped.
	WarmupLogins, HoldDays int
}

// LoginCountryInput is one account's logins over the lookback.
type LoginCountryInput struct {
	NowMS int64
	// LookbackDays is how far back the logins reach: the configured
	// risk.login_lookback_days, or the auth-event retention when that is
	// shorter. Clamped to 1..RiskLoginLookbackMaxDays; 0 means the default
	// RiskLoginLookbackDays.
	LookbackDays int
	GeoAvailable bool
	Logins       []LoginSighting
	// Known is the countries the account's subscription fetches established
	// (EstablishedCountries over the fetch window).
	Known []string
}

// LoginCountryEvidence is what the admin UI draws for login_country. The
// field names are a wire contract: the SPA reads them from rows as they were
// stored. Every slice is present, empty rather than null.
type LoginCountryEvidence struct {
	V            int `json:"v"`
	LookbackDays int `json:"lookback_days"`
	HoldDays     int `json:"hold_days"`
	Warmup       int `json:"warmup"`
	// Logins counts every login in the lookback, skipped or not; Recent
	// those in the last HoldDays; Judged the recent placed ones that had
	// Warmup earlier placed logins.
	Logins int `json:"logins"`
	Recent int `json:"recent"`
	Judged int `json:"judged"`
	// Skipped counts the lookback's logins by why they were set aside.
	Skipped LoginSkips `json:"skipped"`
	// Known is the countries a new login is measured against now — the
	// fetches' and every placed login's in the lookback — less the
	// countries of the events listed below. Sorted; at most
	// RiskEvidenceMaxKnown.
	Known []string `json:"known"`
	// Events are the logins from a new country, newest first; at most
	// RiskEvidenceMaxEvents.
	Events []LoginEventEvidence `json:"events"`
}

// LoginSkips counts skipped logins by reason.
type LoginSkips struct {
	Infra       int `json:"infra"`
	Internal    int `json:"internal"`
	Listed      int `json:"listed"`
	NodeCountry int `json:"node_country"`
	Unplaced    int `json:"unplaced"`
}

// LoginEventEvidence is one login from a new country.
type LoginEventEvidence struct {
	CC     string `json:"cc"`
	AtMS   int64  `json:"at_ms"`
	Method string `json:"method"`
}

// loginDayMS is one day in milliseconds. Login times are instants, so the
// hold and the lookback are counted in whole 24-hour spans back from the
// run's clock, not in calendar days.
const loginDayMS = int64(24 * 60 * 60 * 1000)

// EvaluateLoginCountry judges one account's panel logins. The branches,
// first match wins:
//
//  1. Off → disabled / signal_off, no evidence.
//  2. the group's geo scope is off → disabled / scope_off, no evidence.
//     (Scope country leaves it on: countries are what it judges.)
//  3. AllowAnywhere → exempt / allow_anywhere, no evidence.
//  4. no login in the last hold days, skipped or not → idle /
//     no_recent_logins, no evidence.
//  5. no geo database → unknown / geo_unavailable.
//  6. no recent login was placed → unknown / unplaced.
//  7. any event → flagged / new_country.
//  8. no recent placed login had the warm-up behind it → unknown / learning.
//  9. otherwise clean / known_countries.
//
// "Cannot tell" is never clean. Only logins inside the lookback count —
// neither older ones (the store's bound is only a coarse pre-filter) nor
// ones stamped after NowMS (a clock step, or a login during the run; the
// next run has it). "Earlier" is strictly earlier: two logins in the same
// millisecond are not each other's history.
func EvaluateLoginCountry(p LoginCountryPolicy, in LoginCountryInput) (RiskVerdict, *LoginCountryEvidence) {
	geo := p.Geo.sanitized()
	switch {
	case p.Off:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	case geo.Scope == GeoScopeOff:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeScopeOff}, nil
	case geo.AllowAnywhere:
		return RiskVerdict{State: GeoStateExempt, Code: RiskCodeAllowAnywhere}, nil
	}

	lookback := in.LookbackDays
	if lookback <= 0 {
		lookback = RiskLoginLookbackDays
	}
	lookback = min(lookback, RiskLoginLookbackMaxDays)
	warmup, hold := p.WarmupLogins, p.HoldDays
	if warmup <= 0 {
		warmup = RiskLoginWarmupLogins
	}
	if hold <= 0 {
		hold = RiskLoginHoldDays
	}
	from := in.NowMS - int64(lookback)*loginDayMS
	recentFrom := in.NowMS - int64(hold)*loginDayMS

	// The evidence records the numbers this verdict was judged with — the
	// group's, not the shipped ones — so the admin's sentence reads them.
	ev := &LoginCountryEvidence{
		V:            RiskEvidenceVersion,
		LookbackDays: lookback,
		HoldDays:     hold,
		Warmup:       warmup,
		Known:        []string{},
		Events:       []LoginEventEvidence{},
	}
	// Count every login in the lookback, and keep the placed ones with
	// their country normalised: a database answering "jp" is not a second
	// country beside "JP".
	var placed []LoginSighting
	for _, l := range in.Logins {
		if l.AtMS < from || l.AtMS > in.NowMS {
			continue
		}
		ev.Logins++
		if l.AtMS >= recentFrom {
			ev.Recent++
		}
		l.CC = strings.ToUpper(strings.TrimSpace(l.CC))
		switch {
		case l.Skip == "" && l.CC != "":
			placed = append(placed, l)
		case l.Skip == AddressExcludedInfra:
			ev.Skipped.Infra++
		case l.Skip == AddressExcludedInternal:
			ev.Skipped.Internal++
		case l.Skip == AddressExcludedListed:
			ev.Skipped.Listed++
		case l.Skip == LoginSkipNodeCountry:
			ev.Skipped.NodeCountry++
		default:
			// No reason and no country, or a reason this build does not
			// know: nothing placed it, and it must never read as placed.
			ev.Skipped.Unplaced++
		}
	}
	switch {
	case ev.Recent == 0:
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoRecentLogins}, nil
	case !in.GeoAvailable:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeGeoUnavailable}, ev
	}
	// Oldest first: each login is judged against the ones before it.
	sort.SliceStable(placed, func(i, j int) bool { return placed[i].AtMS < placed[j].AtMS })

	fetched := map[string]bool{}
	for _, cc := range in.Known {
		if cc = strings.ToUpper(strings.TrimSpace(cc)); cc != "" {
			fetched[cc] = true
		}
	}
	// prior holds the countries of the placed logins strictly before the
	// one being judged, and priors how many there were. Logins sharing a
	// millisecond are judged against the same history, then all join it.
	prior := map[string]bool{}
	priors, recentPlaced := 0, 0
	eventCC := map[string]bool{}
	var events []LoginEventEvidence
	for i := 0; i < len(placed); {
		j := i
		for j < len(placed) && placed[j].AtMS == placed[i].AtMS {
			j++
		}
		for _, l := range placed[i:j] {
			if l.AtMS < recentFrom {
				continue
			}
			recentPlaced++
			if priors < warmup {
				continue // learning: too little history to call anything new
			}
			ev.Judged++
			// One new country, one event — even for two logins from it in
			// the same millisecond.
			if !prior[l.CC] && !fetched[l.CC] && !eventCC[l.CC] {
				eventCC[l.CC] = true
				events = append(events, LoginEventEvidence{CC: l.CC, AtMS: l.AtMS, Method: l.Method})
			}
		}
		for _, l := range placed[i:j] {
			prior[l.CC] = true
		}
		priors += j - i
		i = j
	}

	// Known, as it stands now: the fetches' countries and every placed
	// login's, less the countries listed as events — an event's own
	// country is shown as the event until it decays, and known after.
	known := map[string]bool{}
	for cc := range fetched {
		known[cc] = true
	}
	for cc := range prior {
		known[cc] = true
	}
	for cc := range eventCC {
		delete(known, cc)
	}
	for cc := range known {
		ev.Known = append(ev.Known, cc)
	}
	sort.Strings(ev.Known)
	ev.Known = ev.Known[:min(len(ev.Known), RiskEvidenceMaxKnown)]
	sort.SliceStable(events, func(i, j int) bool { return events[i].AtMS > events[j].AtMS })
	ev.Events = append(ev.Events, events[:min(len(events), RiskEvidenceMaxEvents)]...)

	switch {
	case recentPlaced == 0:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeUnplaced}, ev
	case len(events) > 0:
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeNewCountry}, ev
	case ev.Judged == 0:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeLearning}, ev
	}
	return RiskVerdict{State: GeoStateClean, Code: RiskCodeKnownCountries}, ev
}
