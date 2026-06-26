package traffic

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestShouldRollPeriod_DBReadbackInUTCDoesNotSpuriouslyReRoll pins the fix for a
// CRITICAL dialect-dependent bug: shouldRollPeriod compares calendar fields, but
// `now` is in the panel tz while `periodStart` comes straight from the DB. On
// MySQL/Postgres a panel-tz instant is handed back in UTC, so a periodStart of
// 2026-03-01 00:00 +0800 (= 2026-02-28 16:00 UTC) reads back as February. Without
// normalizing both operands to the same Location, shouldRollPeriod sees
// now.Month()=March != periodStart.Month()=February and re-rolls EVERY poll all
// month — pinning the period baseline so the quota never trips (free traffic) and
// repeatedly un-suspending legitimately-over-quota users. (SQLite escaped it only
// because glebarez returns a fixed-offset Location.)
func TestShouldRollPeriod_DBReadbackInUTCDoesNotSpuriouslyReRoll(t *testing.T) {
	east := time.FixedZone("UTC+8", 8*3600) // panel tz, no DST for determinism

	// Panel-tz "now": mid-March.
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, east)
	// The period start written at the last roll, as MySQL/Postgres hand it back:
	// the SAME instant as 2026-03-01 00:00 +0800, but carrying a UTC Location.
	periodStartUTC := time.Date(2026, 3, 1, 0, 0, 0, 0, east).UTC()
	if periodStartUTC.Month() != time.February {
		t.Fatalf("precondition: UTC readback of 2026-03-01+0800 should look like February, got %v", periodStartUTC.Month())
	}

	if shouldRollPeriod(now, periodStartUTC, domain.ResetMonthly) {
		t.Fatal("must NOT roll: now and periodStart are the same panel-tz month; the UTC readback only LOOKS like the previous month")
	}
	// Quarterly + yearly straddle the same boundary instant — must also not re-roll.
	if shouldRollPeriod(now, periodStartUTC, domain.ResetQuarterly) {
		t.Fatal("quarterly must NOT spuriously re-roll across the UTC-readback boundary")
	}
	yearNow := time.Date(2026, 1, 15, 10, 0, 0, 0, east)
	yearStartUTC := time.Date(2026, 1, 1, 0, 0, 0, 0, east).UTC() // = 2025-12-31 16:00 UTC
	if shouldRollPeriod(yearNow, yearStartUTC, domain.ResetYearly) {
		t.Fatal("yearly must NOT spuriously re-roll: 2026-01-01+0800 reads back as 2025 in UTC")
	}
}

// The fix must not break a GENUINE rollover: a real new month/quarter/year still
// rolls, even with the periodStart carrying a UTC Location.
func TestShouldRollPeriod_GenuineRolloverStillFires(t *testing.T) {
	east := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 4, 2, 10, 0, 0, 0, east)           // April, panel tz
	marStartUTC := time.Date(2026, 3, 1, 0, 0, 0, 0, east).UTC() // March period, UTC readback
	if !shouldRollPeriod(now, marStartUTC, domain.ResetMonthly) {
		t.Fatal("a genuine month change (Mar -> Apr) must still roll")
	}
	if !shouldRollPeriod(now, marStartUTC, domain.ResetQuarterly) {
		t.Fatal("Q1 -> Q2 (Mar -> Apr) must still roll")
	}
	nextYear := time.Date(2027, 1, 2, 10, 0, 0, 0, east)
	if !shouldRollPeriod(nextYear, marStartUTC, domain.ResetYearly) {
		t.Fatal("a genuine year change must still roll")
	}
}
