package domain

// GeoRecord is one user's stored concurrent-location state: the streak that
// makes the next verdict stable, plus the verdict that streak produced.
//
// Kept together deliberately. An operator deciding whether to act on somebody's
// account needs the reason, not just the flag — "flagged" with no "because they
// were in 2 places for 3 consecutive checks, and the tolerance is 1" is not a
// basis for touching an account. Storing the two in separate places would let
// them come from different cycles with no way to tell.
type GeoRecord struct {
	UserID int64
	Streak GeoStreak
	State  GeoState
	Reason string
	// Places are the COUNTRIES the verdict was drawn from. v1 stored the
	// scope's opaque places here (e.g. "JP/Kanto"); a legacy row shows that
	// until its next judgement.
	Places []string
	// LiveIPs is how many distinct addresses the upstream still remembered
	// for this user (its whole retention window) — the number the admin
	// table has always shown, not the number judged.
	LiveIPs int
	// Concurrent is the number judged: the sources that were live at poll
	// time and not excluded (Placed + Unplaced). Next to LiveIPs it shows how
	// much of the window was only memory.
	Concurrent int
	// Excluded is how many live sources were set aside (shared exits, the
	// ignore list, infrastructure, internal ranges); Evidence breaks it down.
	Excluded int
	// Evidence is what the verdict was drawn from, in a form safe to store
	// and show: places and counts, never an address.
	Evidence GeoEvidence
	// Complete is false when a panel holding this user's clients could not be
	// read, which makes LiveIPs a FLOOR rather than a total. It travels with
	// the verdict because a reader who sees only the number would take a
	// partial count for a clean bill of health.
	Complete bool
	// UpdatedAtMS is when the poll last judged this user. A latched flag from
	// three weeks ago and one from two minutes ago mean different things.
	UpdatedAtMS int64
}

const (
	// GeoEvidenceMaxSpots bounds the spots kept per verdict. The evidence is
	// written per user per poll; a user with a hundred sources must not make
	// the row a hundred entries long.
	GeoEvidenceMaxSpots = 12
	// GeoEvidenceVersion is written into every evidence value. 0 means a
	// legacy row that has no evidence at all, which a reader must render as
	// "not recorded", not as "nothing found".
	GeoEvidenceVersion = 1
)

// GeoCoverage is how much of the sample the database could place, per tier.
type GeoCoverage struct {
	Placed      int `json:"placed"`
	Unplaced    int `json:"unplaced"`
	RegionKnown int `json:"region_known"`
	CityKnown   int `json:"city_known"`
}

// GeoSpread is the count judged at each tier, and which country the finer
// tiers were measured in.
type GeoSpread struct {
	Countries     int    `json:"countries"`
	Regions       int    `json:"regions"`
	RegionCountry string `json:"region_country"`
	Cities        int    `json:"cities"`
	CityCountry   string `json:"city_country"`
}

// GeoEvidence is the stored, admin-visible account of one verdict.
//
// It carries NO IP ADDRESSES, ever. It is persisted and served to the admin
// UI, and an address kept there would outlive the connection it described,
// turning a location summary into a log of where a subscriber connected
// from. Everything an operator needs to weigh a verdict — where, how many,
// how much was excluded or stale, how well the database covered it — is
// expressible without one.
type GeoEvidence struct {
	V        int         `json:"v"`
	Spots    []GeoSpot   `json:"spots"`
	Excluded GeoExcluded `json:"excluded"`
	Stale    int         `json:"stale"`
	Coverage GeoCoverage `json:"coverage"`
	Networks int         `json:"networks"`
	Spread   GeoSpread   `json:"spread"`
}

// GeoEvidenceFrom records an observation as evidence. Spots are copied (the
// stored value must not alias a slice the caller may reuse) and never nil, so
// the JSON always reads "spots": [] rather than null.
func GeoEvidenceFrom(obs GeoObservation) GeoEvidence {
	return GeoEvidence{
		V:        GeoEvidenceVersion,
		Spots:    append([]GeoSpot{}, obs.Spots...),
		Excluded: obs.Excluded,
		Stale:    obs.Stale,
		Coverage: GeoCoverage{
			Placed:      obs.Placed,
			Unplaced:    obs.Unplaced,
			RegionKnown: obs.RegionKnown,
			CityKnown:   obs.CityKnown,
		},
		Networks: obs.Networks,
		Spread: GeoSpread{
			Countries:     len(obs.Places),
			Regions:       obs.RegionSpread,
			RegionCountry: obs.RegionCountry,
			Cities:        obs.CitySpread,
			CityCountry:   obs.CityCountry,
		},
	}
}
