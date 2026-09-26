package risk

import "time"

// The risk windows are panel-local calendar days, and two different things
// are built from a day here: the DATE it is (which day a row falls on, the
// label the evidence carries) and the INSTANT it starts at (a store bound).
// Local midnight serves neither reliably, so each has its own helper.

// localNoon is 12:00 on the panel-local date `days` days after t's (negative:
// before). It is how a date is carried as an instant here: the anchor
// domain.CivilDaysBetween counts from and paneltz.DateString labels.
//
// Not midnight. A zone that springs forward AT midnight (America/Santiago on
// 2026-09-06, America/Havana on 2026-03-08) has no 00:00 on that date, and
// time.Date resolves the missing wall time with the offset before the jump —
// to 23:00 of the day BEFORE. An anchor built that way reads as the previous
// date: every day counted from it lands one late, and the newest day falls
// off the end of its window. Noon exists on every date a panel runs on.
func localNoon(t time.Time, days int, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d+days, 12, 0, 0, 0, loc)
}

// dayStart is the first instant of anchor's panel-local date: the bound a
// store read starts or stops at. Both stores cut exactly there — the fetch
// scan drops what came before its bound, the hourly reads are half-open — so
// the bound has to be the day's real start, not just near it. Every row
// read is still placed by its own date afterwards.
//
// On almost every date that is time.Date's 00:00. Two kinds of date differ,
// and on each the resolved midnight would lose an hour of a read:
//   - 00:00 skipped (the clocks jump 00:00 → 01:00): time.Date resolves it to
//     23:00 the evening before. The day really starts at the jump — where
//     the zone period that 23:00 lies in ends. As an upper bound the resolved
//     midnight would cut the evening before's last hour.
//   - 00:00 passed twice (the clocks fall back 01:00 → 00:00, as Jordan's did
//     on 2021-10-29): time.Date resolves it to the SECOND pass. The day
//     started at the first, still in the period before the fall-back. As a
//     lower bound the second pass would cut the day's first hour.
func dayStart(anchor time.Time, loc *time.Location) time.Time {
	y, m, d := anchor.In(loc).Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	start, end := t.ZoneBounds()
	if !sameDate(t, y, m, d) {
		if !end.IsZero() {
			return end
		}
		return t
	}
	// Did the date already begin in the zone period before t's? Only a
	// fall-back after its first midnight leaves the instant just before the
	// period's start on the same date.
	if !start.IsZero() {
		before := start.Add(-time.Nanosecond)
		if sameDate(before, y, m, d) {
			_, offset := before.Zone()
			return time.Date(y, m, d, 0, 0, 0, 0, time.FixedZone("", offset)).In(loc)
		}
	}
	return t
}

// sameDate reports whether t, read in its own location, falls on y-m-d.
func sameDate(t time.Time, y int, m time.Month, d int) bool {
	ty, tm, td := t.Date()
	return ty == y && tm == m && td == d
}
