package domain

import (
	"math"
	"slices"
	"sort"
	"strings"
)

// sub_spread: subscription fetches from provinces that no single client links
// together, across the days of a week.
//
// The concurrent-location verdict (v2) reads the proxy's live connections,
// which a relay hides: every user behind a relay connects from the relay. A
// subscription fetch usually goes straight from the client to the panel, so
// its source is where the client is, relay or not. What it measures is
// different too — not "two places at once" but "places that recur, and that
// nothing ties together":
//
//   - A PROVINCE is (country, region). A source placed only to its country
//     forms none (v2's rule: without a region there is nothing to tell
//     apart).
//   - A province is ESTABLISHED when fetches came from it on min_days of the
//     window's days. One day is a trip; three of seven is a habit.
//   - A CLIENT (identity) is a declared device id, or without one the exact
//     client string. One client seen in two provinces, on any days, LINKS
//     them: the same device travelled, or refreshes both at home and on its
//     carrier. Linked provinces form one GROUP, transitively.
//   - More groups holding an established province than the group's region
//     tolerance (geo_anomaly.max_regions, V3-D3) is flagged; more groups
//     when brief provinces are counted too is suspect.
//
// Only ONE country is judged: the one with the most established provinces.
// A fetch the client routes through its own tunnel leaves at the landing's
// egress abroad, so "home country plus the landing's country" is the
// ordinary picture of a relayed user, and concurrent use abroad is v2's
// business anyway. Other countries are shown as context, never judged, and
// provinces are never folded across countries.
//
// Known errors, both pinned by tests. A false positive: a phone whose SIM is
// from another province geolocates there even at home, and if it never
// refreshes on the home WiFi nothing links it to the router (remedy: a higher
// max_regions for that group, or risk.sub_spread_off). A false negative: two
// people on the same app and version are one client string, so their
// provinces link.
//
// Evidence never carries an address: provinces, countries, day masks, client
// labels and a four-character device-id prefix. The identity key is an
// in-memory handle and is never rendered.

// Evidence caps: what one row stores and the admin table draws. The verdict
// is computed over everything; only the listing is bounded.
const (
	RiskEvidenceMaxProvinces  = 12
	RiskEvidenceMaxIdentities = 10
	RiskEvidenceMaxForeign    = 6
)

// SubSpreadPolicy is what sub_spread judges with. It has no knob of its own
// beyond the switch and min_days: the tolerance, scope, exemption and placed
// ratio are the group's concurrent-location policy (V3-D3), so an admin who
// tuned one tuned both.
type SubSpreadPolicy struct {
	Off bool
	// Geo is the account's group policy. Scope off disables the signal;
	// scope country disables it too (this signal is about provinces, and the
	// admin said countries only). MaxRegions is the tolerance.
	Geo GeoAnomalyPolicy
	// MinDays is how many window days make a province established; clamped
	// to 1..RiskWindowDays.
	MinDays int
}

// SubPlaceSighting is one client at one place, over the window: which days
// (bit i = window day i, 0 = oldest) it fetched from a source placed there.
type SubPlaceSighting struct {
	// Identity is the client's key ("d:<device id>" or "u:<client string>"):
	// opaque here, and never rendered.
	Identity string
	// CC is the upper-case country code, "" when the source could not be
	// placed; Region is "" when it was placed to its country only.
	CC, Region string
	Days       uint8
}

// SubIdentity describes one client for the evidence. Kind is "hwid" (it
// declared a device id; HWID4 is that id's first four characters) or "ua"
// (known only by its client string).
type SubIdentity struct{ Key, Kind, Label, HWID4 string }

// SubSpreadInput is one account's fetch window after address hygiene and
// placement.
type SubSpreadInput struct {
	// WindowDays is how many days the window holds: 7, or the sub-log
	// retention when that is shorter. RetentionDays is sub_log_retention_days
	// as stored (0 = never pruned), shown to explain a short window.
	WindowDays, RetentionDays int
	// WindowStart is the panel-local date of window day 0, so the UI can
	// label the day masks without knowing the panel's zone.
	WindowStart string
	// Fetched: the account fetched at least once in the window.
	Fetched bool
	// Sources is how many sources were kept after exclusion (an IPv6 /64 is
	// one source); Placed how many of them resolved to a country, and
	// RegionKnown how many of those to a region too.
	Sources, Placed, RegionKnown int
	Excluded                     GeoExcluded
	GeoAvailable                 bool
	Sightings                    []SubPlaceSighting
	// Identities describes the clients the sightings name. A client missing
	// here still links provinces; it is only left out of the listing.
	Identities []SubIdentity
}

// SubSpreadEvidence is what the admin UI draws for sub_spread. The field
// names are a wire contract: the SPA reads them from rows as they were
// stored. Every slice is present, empty rather than null.
type SubSpreadEvidence struct {
	V           int    `json:"v"`
	WindowDays  int    `json:"window_days"`
	WindowStart string `json:"window_start"`
	// RetentionDays is left out when the logs are never pruned.
	RetentionDays int `json:"retention_days,omitempty"`
	MinDays       int `json:"min_days"`
	MinPlacedPct  int `json:"min_placed_pct"`
	Tolerance     int `json:"tolerance"`
	// Country is the judged country; "" when nothing reached judging.
	Country string `json:"country"`
	// Groups counts the groups holding an established province, GroupsAll
	// every group; Groups ≤ GroupsAll always.
	Groups    int `json:"groups"`
	GroupsAll int `json:"groups_all"`
	// Provinces are the judged country's, in group order; at most
	// RiskEvidenceMaxProvinces.
	Provinces []SubProvince `json:"provinces"`
	// Identities are the clients that touched a listed province, most-linking
	// first; at most RiskEvidenceMaxIdentities.
	Identities []SubIdentityEvidence `json:"identities"`
	// Foreign is every other placed country, context only; at most
	// RiskEvidenceMaxForeign.
	Foreign  []SubForeign `json:"foreign"`
	Excluded GeoExcluded  `json:"excluded"`
	Coverage SubCoverage  `json:"coverage"`
}

// SubProvince is one province of the judged country.
type SubProvince struct {
	CC          string `json:"cc"`
	Region      string `json:"region"`
	Days        uint8  `json:"days"`
	Established bool   `json:"established"`
	// Group is the province's group number, 1..groups_all.
	Group int `json:"group"`
}

// SubIdentityEvidence is one client: its days across the whole window
// (wherever it fetched from) and the listed provinces it was seen in.
type SubIdentityEvidence struct {
	Kind  string `json:"kind"` // hwid | ua
	Label string `json:"label"`
	HWID4 string `json:"hwid4,omitempty"`
	Days  uint8  `json:"days"`
	// Provinces are indexes into the evidence's Provinces, ascending.
	Provinces []int `json:"provinces"`
}

// SubForeign is a placed country other than the judged one.
type SubForeign struct {
	CC   string `json:"cc"`
	Days uint8  `json:"days"`
}

// SubCoverage is how much of the window could be placed.
type SubCoverage struct {
	Sources     int `json:"sources"`
	Placed      int `json:"placed"`
	RegionKnown int `json:"region_known"`
}

// EvaluateSubSpread judges one account's fetch window. The branches, first
// match wins:
//
//  1. Off → disabled / signal_off, no evidence.
//  2. scope off → disabled / scope_off; scope country (or an unrecognised
//     scope, which v2 reads as country) → disabled / scope_country.
//  3. allow_anywhere → exempt / allow_anywhere, no evidence.
//  4. nothing fetched → idle / no_fetches, no evidence.
//  5. WindowDays < min_days → unknown / retention_short: the logs are not
//     kept long enough for anything to recur that often.
//  6. no source left after exclusion → unknown / all_excluded.
//  7. no geo database → unknown / geo_unavailable.
//  8. placed/sources below min_placed_ratio → unknown / low_placed.
//  9. no source placed to a region → unknown / no_regions.
//  10. groups > tolerance → flagged / spread; groups_all > tolerance →
//     suspect / spread_building; otherwise clean / within.
//
// "Cannot tell" is never clean: every guard from 5 on is unknown.
func EvaluateSubSpread(p SubSpreadPolicy, in SubSpreadInput) (RiskVerdict, *SubSpreadEvidence) {
	geo := p.Geo.sanitized()
	minDays := min(max(p.MinDays, 1), RiskWindowDays)
	switch {
	case p.Off:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	case geo.Scope == GeoScopeOff:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeScopeOff}, nil
	case geo.Scope == GeoScopeCountry:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeScopeCountry}, nil
	case geo.AllowAnywhere:
		return RiskVerdict{State: GeoStateExempt, Code: RiskCodeAllowAnywhere}, nil
	case !in.Fetched:
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoFetches}, nil
	}

	// Exclusions and coverage are counts the window always has, so every
	// verdict from here on carries them: an admin reading "unknown" sees
	// what was set aside and what could be placed, whichever guard stopped.
	ev := &SubSpreadEvidence{
		V:             RiskEvidenceVersion,
		WindowDays:    in.WindowDays,
		WindowStart:   in.WindowStart,
		RetentionDays: max(in.RetentionDays, 0),
		MinDays:       minDays,
		MinPlacedPct:  int(math.Round(geo.MinPlacedRatio * 100)),
		Tolerance:     geo.MaxRegions,
		Provinces:     []SubProvince{},
		Identities:    []SubIdentityEvidence{},
		Foreign:       []SubForeign{},
		Excluded:      in.Excluded,
		Coverage:      SubCoverage{Sources: in.Sources, Placed: in.Placed, RegionKnown: in.RegionKnown},
	}
	switch {
	case in.WindowDays < minDays:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeRetentionShort}, ev
	case in.Sources <= 0:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeAllExcluded}, ev
	case !in.GeoAvailable:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeGeoUnavailable}, ev
	case float64(in.Placed)/float64(in.Sources) < geo.MinPlacedRatio:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeLowPlaced}, ev
	}

	provinces := map[subPlace]uint8{}
	countries := map[string]uint8{}
	for _, s := range in.Sightings {
		if s.CC == "" || s.Days == 0 {
			continue
		}
		countries[s.CC] |= s.Days
		if s.Region != "" {
			provinces[subPlace{s.CC, s.Region}] |= s.Days
		}
	}
	if len(provinces) == 0 {
		ev.Foreign = foreignCountries(countries, "")
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeNoRegions}, ev
	}

	country := judgedCountry(provinces, minDays)
	ev.Country = country
	ev.Foreign = foreignCountries(countries, country)
	groups := groupProvinces(country, provinces, in.Sightings, minDays)
	ev.Groups, ev.GroupsAll = groups.established, groups.all
	ev.Provinces = groups.listed
	ev.Identities = identityEvidence(in, country, ev.Provinces)

	switch {
	case ev.Groups > geo.MaxRegions:
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeSpread}, ev
	case ev.GroupsAll > geo.MaxRegions:
		return RiskVerdict{State: GeoStateSuspect, Code: RiskCodeSpreadBuilding}, ev
	}
	return RiskVerdict{State: GeoStateClean, Code: RiskCodeWithin}, ev
}

// EstablishedCountries is the countries an account's fetches came from on at
// least minDays of the window's days — any province, or none known (a
// country-only source still says which country). Sorted. minDays is clamped
// to 1..RiskWindowDays like sub_spread's own.
func EstablishedCountries(s []SubPlaceSighting, minDays int) []string {
	minDays = min(max(minDays, 1), RiskWindowDays)
	days := map[string]uint8{}
	for _, x := range s {
		if x.CC != "" {
			days[x.CC] |= x.Days
		}
	}
	out := []string{}
	for cc, mask := range days {
		if DayCount(mask) >= minDays {
			out = append(out, cc)
		}
	}
	sort.Strings(out)
	return out
}

// subPlace is a province: a region inside its country.
type subPlace struct{ cc, region string }

// judgedCountry picks the one country sub_spread judges: the most
// established provinces, then the most provinces, then the smaller code — so
// a proxy's scatter of one-day egress regions abroad never outweighs the
// provinces an account actually lives in, and the choice is stable.
func judgedCountry(provinces map[subPlace]uint8, minDays int) string {
	type tally struct{ established, all int }
	by := map[string]*tally{}
	for pl, mask := range provinces {
		t := by[pl.cc]
		if t == nil {
			t = &tally{}
			by[pl.cc] = t
		}
		t.all++
		if DayCount(mask) >= minDays {
			t.established++
		}
	}
	best := ""
	for cc, t := range by {
		if best == "" {
			best = cc
			continue
		}
		b := by[best]
		if t.established > b.established ||
			(t.established == b.established && (t.all > b.all || (t.all == b.all && cc < best))) {
			best = cc
		}
	}
	return best
}

// provinceGroups is the judged country's provinces grouped by the clients
// that link them.
type provinceGroups struct {
	established, all int
	// listed is every province with its group, in listing order, capped.
	listed []SubProvince
}

// groupProvinces runs one union-find over the judged country's provinces:
// every client unites every province of the country it was seen in, on any
// day. A link may pass through a province that is not established itself —
// two clients meeting once in a province still belong together, which errs
// toward silence. Groups are numbered established-first, each by its
// smallest established region name, then the rest by their smallest region
// name, so a stable week numbers the same way every hour.
func groupProvinces(country string, provinces map[subPlace]uint8, sightings []SubPlaceSighting, minDays int) provinceGroups {
	var regions []string
	for pl := range provinces {
		if pl.cc == country {
			regions = append(regions, pl.region)
		}
	}
	sort.Strings(regions)
	index := make(map[string]int, len(regions))
	parent := make([]int, len(regions))
	for i, r := range regions {
		index[r] = i
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	first := map[string]int{} // client → a province it was seen in
	for _, s := range sightings {
		if s.CC != country || s.Region == "" || s.Days == 0 {
			continue
		}
		i := index[s.Region]
		j, ok := first[s.Identity]
		if !ok {
			first[s.Identity] = i
			continue
		}
		if a, b := find(i), find(j); a != b {
			parent[b] = a
		}
	}

	established := func(r string) bool { return DayCount(provinces[subPlace{country, r}]) >= minDays }
	type component struct {
		hasEst   bool
		firstEst string // smallest established region
		first    string // smallest region
		numbered int
	}
	comps := map[int]*component{}
	for _, r := range regions { // ascending, so the first seen is the smallest
		root := find(index[r])
		c := comps[root]
		if c == nil {
			c = &component{first: r}
			comps[root] = c
		}
		if established(r) && !c.hasEst {
			c.hasEst, c.firstEst = true, r
		}
	}
	ordered := make([]*component, 0, len(comps))
	for _, c := range comps {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.hasEst != b.hasEst {
			return a.hasEst
		}
		if a.hasEst {
			return a.firstEst < b.firstEst
		}
		return a.first < b.first
	})
	var g provinceGroups
	for n, c := range ordered {
		c.numbered = n + 1
		if c.hasEst {
			g.established++
		}
	}
	g.all = len(ordered)

	g.listed = make([]SubProvince, 0, len(regions))
	for _, r := range regions {
		mask := provinces[subPlace{country, r}]
		g.listed = append(g.listed, SubProvince{
			CC: country, Region: r, Days: mask, Established: established(r),
			Group: comps[find(index[r])].numbered,
		})
	}
	sort.Slice(g.listed, func(i, j int) bool {
		a, b := g.listed[i], g.listed[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Established != b.Established {
			return a.Established
		}
		if da, db := DayCount(a.Days), DayCount(b.Days); da != db {
			return da > db
		}
		return a.Region < b.Region
	})
	if len(g.listed) > RiskEvidenceMaxProvinces {
		g.listed = g.listed[:RiskEvidenceMaxProvinces]
	}
	return g
}

// identityEvidence lists the clients that touched a listed province: most
// listed provinces first, then most days, then hwid before ua, then label.
// Each client's days are its whole window, wherever it fetched from. A client
// the input does not describe links provinces but is not listed.
func identityEvidence(in SubSpreadInput, country string, listed []SubProvince) []SubIdentityEvidence {
	at := make(map[string]int, len(listed))
	for i, p := range listed {
		at[p.Region] = i
	}
	days := map[string]uint8{}
	touched := map[string]map[int]bool{}
	for _, s := range in.Sightings {
		if s.Days == 0 {
			continue
		}
		days[s.Identity] |= s.Days
		if s.CC != country || s.Region == "" {
			continue
		}
		if i, ok := at[s.Region]; ok {
			if touched[s.Identity] == nil {
				touched[s.Identity] = map[int]bool{}
			}
			touched[s.Identity][i] = true
		}
	}
	type row struct {
		key string
		ev  SubIdentityEvidence
	}
	var rows []row
	seen := map[string]bool{}
	for _, id := range in.Identities {
		if seen[id.Key] || len(touched[id.Key]) == 0 {
			continue
		}
		seen[id.Key] = true
		idx := make([]int, 0, len(touched[id.Key]))
		for i := range touched[id.Key] {
			idx = append(idx, i)
		}
		slices.Sort(idx)
		ev := SubIdentityEvidence{Kind: id.Kind, Label: id.Label, Days: days[id.Key], Provinces: idx}
		if id.Kind == "hwid" {
			ev.HWID4 = id.HWID4
		}
		rows = append(rows, row{id.Key, ev})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].ev, rows[j].ev
		if len(a.Provinces) != len(b.Provinces) {
			return len(a.Provinces) > len(b.Provinces)
		}
		if da, db := DayCount(a.Days), DayCount(b.Days); da != db {
			return da > db
		}
		if ah, bh := a.Kind == "hwid", b.Kind == "hwid"; ah != bh {
			return ah
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		if a.HWID4 != b.HWID4 {
			return a.HWID4 < b.HWID4
		}
		// The key never leaves this function; it only makes two
		// otherwise identical rows order the same way every hour.
		return strings.Compare(rows[i].key, rows[j].key) < 0
	})
	out := make([]SubIdentityEvidence, 0, min(len(rows), RiskEvidenceMaxIdentities))
	for _, r := range rows {
		if len(out) == RiskEvidenceMaxIdentities {
			break
		}
		out = append(out, r.ev)
	}
	return out
}

// foreignCountries is every placed country but the judged one, most days
// first, capped.
func foreignCountries(countries map[string]uint8, judged string) []SubForeign {
	out := make([]SubForeign, 0, len(countries))
	for cc, mask := range countries {
		if cc != judged {
			out = append(out, SubForeign{CC: cc, Days: mask})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if da, db := DayCount(out[i].Days), DayCount(out[j].Days); da != db {
			return da > db
		}
		return out[i].CC < out[j].CC
	})
	if len(out) > RiskEvidenceMaxForeign {
		out = out[:RiskEvidenceMaxForeign]
	}
	return out
}
