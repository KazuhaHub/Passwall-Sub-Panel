package version

import "testing"

func TestParseSemver_StripsLeadingV(t *testing.T) {
	cases := []string{"3.1.0", "v3.1.0", "V3.1.0"}
	want := [3]int{3, 1, 0}
	for _, in := range cases {
		got, ok := parseSemver(in)
		if !ok || got != want {
			t.Fatalf("parseSemver(%q) = %v %v, want %v true", in, got, ok, want)
		}
	}
}

func TestParseSemver_DropsPreReleaseSuffix(t *testing.T) {
	// 3X-UI sometimes ships pre-releases (v3.1.0-beta.1); we compare the
	// core triple only.
	got, ok := parseSemver("v3.1.0-beta.1")
	if !ok || got != [3]int{3, 1, 0} {
		t.Fatalf("parseSemver pre-release: %v %v", got, ok)
	}
}

func TestParseSemver_AcceptsTwoParts(t *testing.T) {
	got, ok := parseSemver("3.1")
	if !ok || got != [3]int{3, 1, 0} {
		t.Fatalf("parseSemver two-part: %v %v", got, ok)
	}
}

func TestParseSemver_RejectsGarbage(t *testing.T) {
	cases := []string{"", "abc", "3.x.0", "3.1.0.0", "  ", "3..0"}
	for _, in := range cases {
		if _, ok := parseSemver(in); ok {
			t.Fatalf("parseSemver(%q) should fail", in)
		}
	}
}

func TestCheckXUI_SupportedExactlyAtBoundary(t *testing.T) {
	// MinXUI == MaxTestedXUI == 3.1.0 at the time of writing.
	if got := CheckXUI("3.1.0"); got != CompatSupported {
		t.Fatalf("3.1.0 should be Supported, got %s", got)
	}
	if got := CheckXUI("v3.1.0"); got != CompatSupported {
		t.Fatalf("v3.1.0 should be Supported, got %s", got)
	}
}

func TestCheckXUI_TooOldBelowMin(t *testing.T) {
	for _, v := range []string{"3.0.0", "3.0.2", "2.5.5", "1.0.0"} {
		if got := CheckXUI(v); got != CompatTooOld {
			t.Fatalf("%s should be TooOld, got %s", v, got)
		}
	}
}

func TestCheckXUI_UntestedAboveMax(t *testing.T) {
	for _, v := range []string{"3.1.1", "3.2.0", "4.0.0"} {
		if got := CheckXUI(v); got != CompatUntested {
			t.Fatalf("%s should be Untested, got %s", v, got)
		}
	}
}

func TestCheckXUI_UnknownOnUnparseable(t *testing.T) {
	for _, v := range []string{"", "garbage", "  "} {
		if got := CheckXUI(v); got != CompatUnknown {
			t.Fatalf("%q should be Unknown, got %s", v, got)
		}
	}
}

func TestCompatMessage_ContainsKeyFacts(t *testing.T) {
	// Every status's message should mention either the reported version or
	// the constraint, so log scrapers don't need a separate context lookup.
	for _, c := range []struct {
		v    string
		st   CompatStatus
		want []string
	}{
		{"3.1.0", CompatSupported, []string{"3.1.0", MinXUI, MaxTestedXUI}},
		{"3.0.2", CompatTooOld, []string{"3.0.2", MinXUI}},
		{"3.2.0", CompatUntested, []string{"3.2.0", MaxTestedXUI}},
		{"", CompatUnknown, []string{"unknown"}},
	} {
		msg := CompatMessage(c.v, c.st)
		for _, w := range c.want {
			if !contains(msg, w) {
				t.Fatalf("CompatMessage(%q, %s) missing %q: %q", c.v, c.st, w, msg)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
