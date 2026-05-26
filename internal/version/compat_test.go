package version

import (
	"testing"
	"time"
)

// resetCompatForTest puts the active state back to "no override loaded" so
// each test case starts from the same baseline. Without this a test that
// installs an override leaks into the next test's view of CheckXUI.
func resetCompatForTest(t *testing.T) {
	t.Helper()
	SetActiveMaxTestedXUI("")
	refreshMu.Lock()
	refreshLastAt = time.Time{}
	refreshLastError = nil
	refreshMu.Unlock()
}

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

func TestCheckXUI_UnknownWhenNoRemoteLoaded(t *testing.T) {
	// Fresh process / before any RefreshRemoteCompat: every panel is
	// Unknown, including ones that would be Supported under a loaded
	// range. Forces admin into "force upgrade" or "wait for refresh".
	resetCompatForTest(t)
	for _, v := range []string{"3.1.0", "v3.1.0", "3.2.0"} {
		if got := CheckXUI(v); got != CompatUnknown {
			t.Fatalf("%s with no remote loaded should be Unknown, got %s", v, got)
		}
	}
}

func TestCheckXUI_SupportedExactlyAtBoundary(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	if got := CheckXUI("3.1.0"); got != CompatSupported {
		t.Fatalf("3.1.0 with max=3.1.0 should be Supported, got %s", got)
	}
	if got := CheckXUI("v3.1.0"); got != CompatSupported {
		t.Fatalf("v3.1.0 with max=3.1.0 should be Supported, got %s", got)
	}
}

func TestCheckXUI_TooOldBelowMin(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	for _, v := range []string{"3.0.0", "3.0.2", "2.5.5", "1.0.0"} {
		if got := CheckXUI(v); got != CompatTooOld {
			t.Fatalf("%s should be TooOld, got %s", v, got)
		}
	}
}

func TestCheckXUI_UntestedAboveMax(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	for _, v := range []string{"3.1.1", "3.2.0", "4.0.0"} {
		if got := CheckXUI(v); got != CompatUntested {
			t.Fatalf("%s should be Untested, got %s", v, got)
		}
	}
}

func TestCheckXUI_OverrideWidensRange(t *testing.T) {
	// Whole point of the dynamic override: a panel that would've been
	// Untested at "max=3.1.0" becomes Supported once max widens. This
	// is the "ship a new tested version via JSON, no PSP release" path.
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	if got := CheckXUI("3.2.0"); got != CompatUntested {
		t.Fatalf("3.2.0 with max=3.1.0 should be Untested, got %s", got)
	}
	SetActiveMaxTestedXUI("3.5.0")
	if got := CheckXUI("3.2.0"); got != CompatSupported {
		t.Fatalf("3.2.0 with max=3.5.0 should be Supported, got %s", got)
	}
}

func TestCheckXUI_UnknownOnUnparseable(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	for _, v := range []string{"", "garbage", "  "} {
		if got := CheckXUI(v); got != CompatUnknown {
			t.Fatalf("%q should be Unknown, got %s", v, got)
		}
	}
}

func TestActiveMinXUI_AlwaysReturnsConst(t *testing.T) {
	// MinXUI is a code-level requirement; no remote-override path should
	// be able to budge it. Sanity-check the accessor.
	if ActiveMinXUI() != MinXUI {
		t.Fatalf("ActiveMinXUI = %q, want const MinXUI = %q", ActiveMinXUI(), MinXUI)
	}
}

func TestCompatMessage_ContainsKeyFacts(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.1.0")
	for _, c := range []struct {
		v    string
		st   CompatStatus
		want []string
	}{
		{"3.1.0", CompatSupported, []string{"3.1.0", MinXUI, "3.1.0"}},
		{"3.0.2", CompatTooOld, []string{"3.0.2", MinXUI}},
		{"3.2.0", CompatUntested, []string{"3.2.0", "3.1.0"}},
	} {
		msg := CompatMessage(c.v, c.st)
		for _, w := range c.want {
			if !contains(msg, w) {
				t.Fatalf("CompatMessage(%q, %s) missing %q: %q", c.v, c.st, w, msg)
			}
		}
	}
}

func TestCompatMessage_UnknownDistinguishesNoRemoteFromBadInput(t *testing.T) {
	// When max isn't loaded, the Unknown message should hint at "open
	// Servers / click Test to refresh" so admin knows what to do. When
	// max IS loaded but the input is garbage, the message should just
	// say "couldn't parse panel reply".
	resetCompatForTest(t)
	msg := CompatMessage("3.1.0", CompatUnknown)
	if !contains(msg, "not loaded yet") {
		t.Fatalf("Unknown message when no remote loaded should mention refresh hint: %q", msg)
	}

	SetActiveMaxTestedXUI("3.1.0")
	msg = CompatMessage("garbage", CompatUnknown)
	if !contains(msg, "couldn't") || !contains(msg, "garbage") {
		t.Fatalf("Unknown message with loaded max should describe parse failure: %q", msg)
	}
}

func TestLookupForPSPVersion_ExtractsMajorMinor(t *testing.T) {
	payload := remoteCompatPayload{
		PSPCompat: map[string]remoteCompatPSPEntry{
			"v3.6": {MinXUI: "3.1.0", MaxTestedXUI: "3.5.0"},
			"v3.7": {MinXUI: "3.1.0", MaxTestedXUI: "4.0.0"},
		},
	}
	cases := []struct {
		pspVer  string
		wantKey string
		wantMax string
		wantOK  bool
	}{
		{"v3.6.0", "v3.6", "3.5.0", true},
		{"v3.6.0-beta.5", "v3.6", "3.5.0", true},
		{"3.6.99", "v3.6", "3.5.0", true},
		{"v3.7.0", "v3.7", "4.0.0", true},
		{"v3.8.0", "v3.8", "", false}, // not in table
		{"dev", "", "", false},
		{"garbage", "", "", false},
	}
	for _, c := range cases {
		entry, key, ok := lookupForPSPVersion(payload, c.pspVer)
		if ok != c.wantOK || key != c.wantKey {
			t.Fatalf("lookupForPSPVersion(%q): key=%q ok=%v, want key=%q ok=%v",
				c.pspVer, key, ok, c.wantKey, c.wantOK)
		}
		if ok && entry.MaxTestedXUI != c.wantMax {
			t.Fatalf("lookupForPSPVersion(%q): max=%q, want %q",
				c.pspVer, entry.MaxTestedXUI, c.wantMax)
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
