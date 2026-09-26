package domain

import "testing"

// A region code is a display attribute: it names a province in the admin UI's
// language without trusting the database's English spelling. A code that is
// not plausibly the region part of ISO 3166-2 must read as "no code", so a
// reader falls back to the region name instead of looking up something wrong.
//
// "gſ" and "ı" are the trap: strings.ToUpper maps U+017F to "S" and U+0131 to
// "I", so a check made after upper-casing would admit "gſ" as "GS" — a real
// Chinese province code for a value no database ever meant as one.
func TestNormalizeRegionCode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"GD", "GD"},
		{" gd ", "GD"},
		{"13", "13"},   // JP Tokyo: some countries' codes are numeric
		{"HCW", "HCW"}, // three alphanumerics is the ISO maximum
		{"CN-GD", ""},  // a prefixed code is not what MaxMind's iso_code holds
		{"GUAN", ""},
		{"G D", ""},
		{"广东", ""},
		{"gſ", ""},
		{"ı", ""},
		{"", ""},
	} {
		if got := NormalizeRegionCode(c.in); got != c.want {
			t.Errorf("NormalizeRegionCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// One region, one code per computation, whatever order its sources arrive
// in: the bytewise-smallest non-empty one. An empty code never wins over a
// real one, and a numeric pre-2017 code sorts before the letters.
func TestPreferRegionCode(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"", "GD", "GD"},
		{"GX", "GD", "GD"},
		{"GD", "", "GD"},
		{"22", "JL", "22"},
		{"", "", ""},
	} {
		if got := PreferRegionCode(c.a, c.b); got != c.want {
			t.Errorf("PreferRegionCode(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
