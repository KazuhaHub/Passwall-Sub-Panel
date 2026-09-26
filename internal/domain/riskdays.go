package domain

import (
	"math/bits"
	"time"
)

// CivilDaysBetween counts calendar days from a's date to b's date in loc
// (b − a): 0 on the same local date, 1 on the next, negative when b's date
// is earlier. The time of day of either instant does not matter.
//
// The risk windows are panel-local calendar days — the days an admin reads in
// the table — so an instant is placed by its local date, never by 24-hour
// spans from a start: a DST day is 23 or 25 hours long, and elapsed/24h would
// fold the short one into the day before it. Both dates are rebuilt as UTC
// midnights before subtracting, where every day is exactly 24 hours.
//
// A nil loc is time.Local, like paneltz's fallback.
func CivilDaysBetween(a, b time.Time, loc *time.Location) int {
	if loc == nil {
		loc = time.Local
	}
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	from := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	to := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(to.Sub(from) / (24 * time.Hour))
}

// DayCount is the number of days set in a day mask (bit i = window day i,
// 0 = oldest): how many distinct days a place or a device was seen on, the
// figure min_days is compared with.
func DayCount(mask uint8) int {
	return bits.OnesCount8(mask)
}
