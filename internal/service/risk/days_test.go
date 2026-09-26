package risk

import (
	"testing"
	"time"
)

// dayStart is the first instant of a local date, the bound the store cuts
// at. It is the date's 00:00 — except on a date whose 00:00 the clocks
// skipped (the day starts at the jump, not at the 23:00 the evening before
// time.Date resolves it to) and on a date whose 00:00 the clocks pass twice
// (the day starts at the first pass, not at the second time.Date resolves it
// to). Either way round, the resolved midnight loses an hour at one end of a
// read: the evening before's last hour as an upper bound, the day's first as
// a lower one.
func TestDayStart_IsTheDatesFirstInstant(t *testing.T) {
	for _, c := range []struct {
		name string
		zone string
		y    int
		m    time.Month
		d    int
		want string // RFC 3339, in the zone
	}{
		{"ordinary day", "Asia/Shanghai", 2026, time.September, 25, "2026-09-25T00:00:00+08:00"},
		// A 02:00 jump leaves midnight alone.
		{"DST day, jump at 02:00", "America/New_York", 2026, time.March, 8, "2026-03-08T00:00:00-05:00"},
		{"midnight skipped", "America/Santiago", 2026, time.September, 6, "2026-09-06T01:00:00-03:00"},
		{"midnight skipped", "America/Havana", 2026, time.March, 8, "2026-03-08T01:00:00-04:00"},
		// Jordan fell back from 01:00 to 00:00 on 2021-10-29: 00:00–01:00
		// happened twice, first at +03, then at +02.
		{"midnight twice", "Asia/Amman", 2021, time.October, 29, "2021-10-29T00:00:00+03:00"},
	} {
		t.Run(c.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(c.zone)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, c.want)
			if err != nil {
				t.Fatal(err)
			}
			// Any instant of the date is an anchor, in any zone.
			for _, anchor := range []time.Time{
				time.Date(c.y, c.m, c.d, 12, 0, 0, 0, loc),
				time.Date(c.y, c.m, c.d, 23, 30, 0, 0, loc).UTC(),
			} {
				if got := dayStart(anchor, loc); !got.Equal(want) {
					t.Fatalf("%s: dayStart(%s) = %s, want %s", c.name, anchor, got.In(loc), want)
				}
			}
			// The test's own answer, checked against the definition.
			if ref := dayStartOf(t, time.Date(c.y, c.m, c.d, 12, 0, 0, 0, loc), loc); !ref.Equal(want) {
				t.Fatalf("%s: the date's first instant is %s, not the %s this case expects", c.name, ref, want)
			}
		})
	}
}
