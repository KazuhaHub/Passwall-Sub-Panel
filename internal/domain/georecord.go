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
	Places []string
	// LiveIPs is how many distinct source addresses backed this verdict.
	LiveIPs int
	// Complete is false when a panel holding this user's clients could not be
	// read, which makes LiveIPs a FLOOR rather than a total. It travels with
	// the verdict because a reader who sees only the number would take a
	// partial count for a clean bill of health.
	Complete bool
	// UpdatedAtMS is when the poll last judged this user. A latched flag from
	// three weeks ago and one from two minutes ago mean different things.
	UpdatedAtMS int64
}
