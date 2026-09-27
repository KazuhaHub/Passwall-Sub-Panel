package domain

import (
	"encoding/json"
	"testing"
)

// reasonFixture is one EvaluateGeo branch, pinned by the exact English it
// produced before reason codes existed. The fixtures reach every sentence
// EvaluateGeo can write — twice where the tier changes the sentence (an over
// tier at country or city; a latch with or without its tier) — so the table
// doubles as the list of branches the reason codes must name.
type reasonFixture struct {
	name   string
	p      GeoAnomalyPolicy
	o      GeoObservation
	prev   GeoStreak
	reason string
	code   GeoReasonCode
	tier   GeoTier
}

// reasonFixtures builds the table fresh for each test so no test can mutate
// a slice another one reads. The policy is DefaultGeoPolicy (scope city,
// tolerances 1/1/2, flag after 3, clear after 6, ratio 0.5) unless a case
// says otherwise, and every observation has lookup available unless it is
// the case about lookup being off.
func reasonFixtures() []reasonFixture {
	def := DefaultGeoPolicy()
	return []reasonFixture{
		{
			name:   "scope off",
			p:      pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeOff }),
			o:      GeoObservation{GeoAvailable: true},
			reason: "location checks are switched off for this account",
			code:   GeoWhyDisabled,
		},
		{
			name:   "allow anywhere",
			p:      pol(func(p *GeoAnomalyPolicy) { p.AllowAnywhere = true }),
			o:      GeoObservation{GeoAvailable: true},
			reason: "this account is allowed to connect from anywhere",
			code:   GeoWhyExempt,
		},
		{
			name:   "idle with stale window addresses",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Stale: 2},
			reason: "no concurrent connections; 2 address(es) seen earlier in the upstream window",
			code:   GeoWhyIdleStale,
		},
		{
			name:   "idle",
			p:      def,
			o:      GeoObservation{GeoAvailable: true},
			reason: "no live connections",
			code:   GeoWhyIdleNone,
		},
		{
			name:   "every source excluded",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Excluded: GeoExcluded{Shared: 1, Infra: 2, Internal: 1}},
			reason: "all 4 concurrent address(es) are excluded (shared 1, listed 0, infrastructure 2, internal 1); no conclusion drawn",
			code:   GeoWhyUnknownExcluded,
		},
		{
			name:   "lookup off",
			p:      def,
			o:      GeoObservation{GeoAvailable: false, Unplaced: 2},
			reason: "location lookup unavailable; no conclusion drawn",
			code:   GeoWhyUnknownGeoOff,
		},
		{
			name:   "too few placed",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Places: []string{"JP"}, Placed: 1, Unplaced: 2},
			reason: "only 1 of 3 addresses could be located (50% required)",
			code:   GeoWhyUnknownLowRatio,
		},
		{
			name:   "suspect at the country tier",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Places: []string{"DE", "JP"}, Placed: 2},
			reason: "in 2 countries at once ([DE JP]); tolerance is 1, 1 of 3 checks so far",
			code:   GeoWhySuspect,
			tier:   GeoTierCountry,
		},
		{
			name: "suspect at the city tier",
			p:    def,
			o: GeoObservation{GeoAvailable: true, Places: []string{"JP"}, Placed: 3,
				RegionSpread: 1, RegionCountry: "JP", Regions: []string{"JP/Kanto"},
				CitySpread: 3, CityCountry: "JP", Cities: []string{"JP/Chiba", "JP/Tokyo", "JP/Yokohama"}},
			reason: "in 3 cities of JP at once ([JP/Chiba JP/Tokyo JP/Yokohama]); tolerance is 2, 1 of 3 checks so far",
			code:   GeoWhySuspect,
			tier:   GeoTierCity,
		},
		{
			name: "flag sustained at the region tier",
			p:    def,
			o: GeoObservation{GeoAvailable: true, Places: []string{"CN"}, Placed: 2,
				RegionSpread: 2, RegionCountry: "CN", Regions: []string{"CN/Guangdong", "CN/Hunan"}, CitySpread: 2},
			prev:   GeoStreak{Over: 2},
			reason: "in 2 regions of CN at once ([CN/Guangdong CN/Hunan]); tolerance is 1, sustained for 3 of 3 checks",
			code:   GeoWhyFlaggedSustained,
			tier:   GeoTierRegion,
		},
		{
			name:   "latched flag clearing, raised at the region tier",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Places: []string{"CN"}, Placed: 1, RegionSpread: 1},
			prev:   GeoStreak{Flagged: true, Tier: GeoTierRegion},
			reason: "within tolerance for 1 of the 6 checks needed to clear; flagged at the region tier",
			code:   GeoWhyFlaggedClearing,
			tier:   GeoTierRegion,
		},
		{
			// A latch an older build stored without its tier.
			name:   "latched flag clearing, tier unknown",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Places: []string{"CN"}, Placed: 1, RegionSpread: 1},
			prev:   GeoStreak{Flagged: true},
			reason: "within tolerance for 1 of the 6 checks needed to clear",
			code:   GeoWhyFlaggedClearing,
		},
		{
			name:   "connected, nothing placed",
			p:      pol(func(p *GeoAnomalyPolicy) { p.MinPlacedRatio = 0 }),
			o:      GeoObservation{GeoAvailable: true, Unplaced: 1},
			reason: "connected, but no address could be placed",
			code:   GeoWhyCleanUnplaced,
		},
		{
			name:   "within tolerance",
			p:      def,
			o:      GeoObservation{GeoAvailable: true, Places: []string{"JP"}, Placed: 2, RegionSpread: 1, CitySpread: 2},
			reason: "within tolerance: 1 country(ies) [JP], 1 region(s) and 2 city(ies) in the widest country; tolerances 1/1/2 (scope city)",
			code:   GeoWhyCleanWithin,
		},
	}
}

// The English Reason is what logs, the audit trail and every SPA older than
// the localized one read, and it is what a newer SPA falls back to for a row
// it cannot localize. Adding a machine-readable code beside it must not move
// a single byte of it, so each branch's sentence is pinned exactly.
func TestEvaluateGeo_EnglishReasonIsUnchanged(t *testing.T) {
	for _, f := range reasonFixtures() {
		t.Run(f.name, func(t *testing.T) {
			if got := EvaluateGeo(f.p, f.o, f.prev).Reason; got != f.reason {
				t.Fatalf("reason = %q\nwant     %q", got, f.reason)
			}
		})
	}
}

// whySnapshot is what every Why must carry besides its code and tier: the
// policy as EvaluateGeo actually judged with it, after sanitizing.
func whySnapshot(p GeoAnomalyPolicy) GeoWhy {
	p = p.sanitized()
	return GeoWhy{Scope: p.Scope, Tol: p.FlagTolerances(), FlagAfter: p.FlagAfterPolls,
		ClearAfter: p.ClearAfterPolls, MinPlacedRatio: p.MinPlacedRatio}
}

// A UI localizes a verdict from its code, not by pattern-matching the English
// sentence, so every branch has to name itself — and the tier it talks about,
// which is the over tier while a streak runs and the tier that raised the flag
// while a latch clears. Each branch also carries the policy it was judged
// against, not only the branches whose sentence prints numbers: a branch
// that forgot it would render a localized sentence with zeros in it.
func TestEvaluateGeo_EveryBranchSetsItsCode(t *testing.T) {
	for _, f := range reasonFixtures() {
		t.Run(f.name, func(t *testing.T) {
			why := EvaluateGeo(f.p, f.o, f.prev).Why
			if why.Code != f.code || why.Tier != f.tier {
				t.Fatalf("why = %q/%q, want %q/%q", why.Code, why.Tier, f.code, f.tier)
			}
			want := whySnapshot(f.p)
			want.Code, want.Tier = f.code, f.tier
			if why != want {
				t.Fatalf("why = %+v\nwant  %+v (the sanitized policy the verdict was judged with)", why, want)
			}
		})
	}
}

// The policy recorded is the one JUDGED with, which is the sanitized one. A
// stored 0 tolerance, a 0 streak and an unreadable scope are repaired before
// judging (a region tolerance of 0 would flag everyone at home), and a
// snapshot of the raw values would tell an admin the account was judged
// against numbers nothing ever used.
func TestEvaluateGeo_WhySnapshotsTheSanitizedPolicy(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) {
		p.MaxRegions = 0
		p.FlagAfterPolls = 0
		p.Scope = "bogus"
	})
	why := EvaluateGeo(p, GeoObservation{}, GeoStreak{}).Why
	if why.Tol.Regions != 1 || why.FlagAfter != 1 || why.Scope != GeoScopeCountry {
		t.Fatalf("why = %+v, want regions tolerance 1, flag after 1, scope country", why)
	}
	want := GeoWhy{Code: GeoWhyIdleNone, Scope: GeoScopeCountry, Tol: GeoTolerances{Countries: 1, Regions: 1, Cities: 2},
		FlagAfter: 1, ClearAfter: 6, MinPlacedRatio: 0.5}
	if why != want {
		t.Fatalf("why = %+v\nwant  %+v", why, want)
	}
}

// AllGeoReasonCodes is the list the SPA's locale keys are checked against. A
// code no branch produces is a key nobody sees; a branch whose code is not
// listed renders as the stored English forever. The fixtures reach every
// branch, so the two sets must be the same.
func TestAllGeoReasonCodesIsExactlyTheBranches(t *testing.T) {
	reached := map[GeoReasonCode]bool{}
	for _, f := range reasonFixtures() {
		reached[EvaluateGeo(f.p, f.o, f.prev).Why.Code] = true
	}
	listed := map[GeoReasonCode]bool{}
	for _, c := range AllGeoReasonCodes() {
		if listed[c] {
			t.Fatalf("AllGeoReasonCodes lists %q twice", c)
		}
		listed[c] = true
	}
	for c := range reached {
		if !listed[c] {
			t.Fatalf("a branch produced %q, which AllGeoReasonCodes does not list (listed %v)", c, AllGeoReasonCodes())
		}
	}
	for c := range listed {
		if !reached[c] {
			t.Fatalf("AllGeoReasonCodes lists %q, which no branch produced (reached %v)", c, reached)
		}
	}
}

// The field names are a wire contract: the admin SPA reads evidence.why
// straight from the API, and the stored evidence outlives the build that
// wrote it. GeoTolerances had no JSON tags while nothing serialized it, so
// without them "tol" would travel as {"Countries":1,...}. A Why with no tier
// omits the key rather than sending "".
func TestGeoWhy_JSONIsTheShapeTheSPAReads(t *testing.T) {
	two := GeoObservation{GeoAvailable: true, Places: []string{"DE", "JP"}, Placed: 2}
	raw, err := json.Marshal(EvaluateGeo(DefaultGeoPolicy(), two, GeoStreak{}).Why)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"suspect","tier":"country","scope":"city","tol":{"countries":1,"regions":1,"cities":2},"flag_after":3,"clear_after":6,"min_placed_ratio":0.5}`
	if string(raw) != want {
		t.Fatalf("why = %s\nwant  %s", raw, want)
	}
	raw, err = json.Marshal(EvaluateGeo(DefaultGeoPolicy(), GeoObservation{}, GeoStreak{}).Why)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"code":"idle_none","scope":"city","tol":{"countries":1,"regions":1,"cities":2},"flag_after":3,"clear_after":6,"min_placed_ratio":0.5}`
	if string(raw) != want {
		t.Fatalf("why = %s\nwant  %s", raw, want)
	}
}
