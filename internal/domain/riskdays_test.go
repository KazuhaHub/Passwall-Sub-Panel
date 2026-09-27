package domain

import (
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

// The risk windows are panel-local calendar days, not UTC days and not
// 24-hour spans since the window start. A fetch at 23:59 Shanghai time and
// one a minute later fall on different days even though they are both on
// the same UTC date — counting in UTC would put an evening's fetches into
// the wrong day's column for every panel east of Greenwich.
func TestCivilDaysBetween_ShanghaiBoundary(t *testing.T) {
	sh := mustZone(t, "Asia/Shanghai")
	day0 := time.Date(2026, 9, 20, 0, 0, 0, 0, sh)
	for _, c := range []struct {
		at   time.Time
		want int
	}{
		{time.Date(2026, 9, 19, 16, 0, 0, 0, time.UTC), 0},  // 09-20 00:00 local
		{time.Date(2026, 9, 20, 15, 59, 0, 0, time.UTC), 0}, // 09-20 23:59 local
		{time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC), 1},  // 09-21 00:00 local
		{time.Date(2026, 9, 26, 15, 59, 0, 0, time.UTC), 6}, // 09-26 23:59 local
		{time.Date(2026, 9, 19, 15, 59, 0, 0, time.UTC), -1},
	} {
		if got := CivilDaysBetween(day0, c.at, sh); got != c.want {
			t.Errorf("CivilDaysBetween(09-20 local, %s) = %d, want %d", c.at.Format(time.RFC3339), got, c.want)
		}
	}
	// The time of day of the start does not matter, only its date.
	late := time.Date(2026, 9, 20, 23, 30, 0, 0, sh)
	if got := CivilDaysBetween(late, time.Date(2026, 9, 21, 0, 30, 0, 0, sh), sh); got != 1 {
		t.Errorf("23:30 to 00:30 the next day = %d, want 1 (an hour apart, a day apart)", got)
	}
}

// A DST day is 23 or 25 hours long and is still one day. Dividing elapsed
// time by 24 hours gets the short day wrong (23h/24h rounds down to 0), which
// would fold the day after a spring-forward into the day before it. Shanghai
// has no DST, so New York stands in.
func TestCivilDaysBetween_DSTDayIsOneDay(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	for _, c := range []struct {
		name string
		a, b time.Time
		want int
	}{
		{"fall back (25h)", time.Date(2026, 11, 1, 0, 0, 0, 0, ny), time.Date(2026, 11, 2, 0, 0, 0, 0, ny), 1},
		{"spring forward (23h)", time.Date(2026, 3, 8, 0, 0, 0, 0, ny), time.Date(2026, 3, 9, 0, 0, 0, 0, ny), 1},
		{"a week across fall back", time.Date(2026, 10, 29, 0, 0, 0, 0, ny), time.Date(2026, 11, 5, 23, 59, 0, 0, ny), 7},
	} {
		if got := CivilDaysBetween(c.a, c.b, ny); got != c.want {
			t.Errorf("%s: CivilDaysBetween = %d, want %d", c.name, got, c.want)
		}
	}
}

// A day mask holds one bit per window day; the count is how many distinct
// days a pattern was seen on — the number min_days is compared with.
func TestDayCount(t *testing.T) {
	for _, c := range []struct {
		mask uint8
		want int
	}{
		{0, 0},
		{0b0000_0001, 1},
		{0b0100_0001, 2},
		{0b0111_1111, 7},
		{0xff, 8},
	} {
		if got := DayCount(c.mask); got != c.want {
			t.Errorf("DayCount(%08b) = %d, want %d", c.mask, got, c.want)
		}
	}
}
