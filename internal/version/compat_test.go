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
	SetActiveMinXUI("")
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
	SetActiveMaxTestedXUI("3.3.0")
	if got := CheckXUI("3.3.0"); got != CompatSupported {
		t.Fatalf("3.3.0 with min=max=3.3.0 should be Supported, got %s", got)
	}
	if got := CheckXUI("v3.3.0"); got != CompatSupported {
		t.Fatalf("v3.3.0 with min=max=3.3.0 should be Supported, got %s", got)
	}
}

func TestCheckXUI_TooOldBelowMin(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.4.0")
	// v3.9.0 raised the floor 3.2.0 → 3.3.0: the shared-client model's
	// /clients/update path breaks on 3X-UI 3.2.x ("UNIQUE constraint failed:
	// client_inbounds"), so 3.2.x — and everything older — must read TooOld.
	for _, v := range []string{"3.2.0", "3.2.9", "3.1.0", "3.0.2", "2.5.5", "1.0.0"} {
		if got := CheckXUI(v); got != CompatTooOld {
			t.Fatalf("%s should be TooOld (min=%s), got %s", v, MinXUI, got)
		}
	}
}

func TestCheckXUI_UntestedAboveMax(t *testing.T) {
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.4.0")
	for _, v := range []string{"3.4.1", "3.5.0", "4.0.0"} {
		if got := CheckXUI(v); got != CompatUntested {
			t.Fatalf("%s should be Untested (max=3.4.0), got %s", v, got)
		}
	}
}

func TestCheckXUI_OverrideWidensRange(t *testing.T) {
	// Whole point of the dynamic override: a panel that would've been
	// Untested at one max becomes Supported once max widens. This is the
	// "ship a new tested version via JSON, no PSP release" path.
	resetCompatForTest(t)
	SetActiveMaxTestedXUI("3.3.0")
	if got := CheckXUI("3.4.0"); got != CompatUntested {
		t.Fatalf("3.4.0 with max=3.3.0 should be Untested, got %s", got)
	}
	SetActiveMaxTestedXUI("3.5.0")
	if got := CheckXUI("3.4.0"); got != CompatSupported {
		t.Fatalf("3.4.0 with max=3.5.0 should be Supported, got %s", got)
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

func TestActiveMinXUI_BackstopWhenNotLoaded(t *testing.T) {
	// With no JSON min loaded, ActiveMinXUI returns the compiled backstop.
	resetCompatForTest(t)
	if ActiveMinXUI() != MinXUI {
		t.Fatalf("ActiveMinXUI = %q, want compiled backstop %q", ActiveMinXUI(), MinXUI)
	}
}

func TestActiveMinXUI_JSONRaisesFloorButCannotLowerIt(t *testing.T) {
	resetCompatForTest(t)
	// A JSON min AT or BELOW the backstop is clamped — a stale/wrong JSON
	// must not be able to lower PSP's floor below what its code can speak.
	for _, below := range []string{"3.0.0", "3.1.0", MinXUI, "garbage", ""} {
		SetActiveMinXUI(below)
		if got := ActiveMinXUI(); got != MinXUI {
			t.Fatalf("min_xui %q (<= backstop) must clamp to %q, got %q", below, MinXUI, got)
		}
	}
	// A JSON min ABOVE the backstop raises the operational floor — an
	// operational hard-cut shipped via the JSON alone, no PSP rebuild.
	SetActiveMinXUI("3.5.0")
	if got := ActiveMinXUI(); got != "3.5.0" {
		t.Fatalf("min_xui above backstop should raise floor to 3.5.0, got %q", got)
	}
	// CheckXUI honours the raised floor.
	SetActiveMaxTestedXUI("3.6.0")
	if got := CheckXUI("3.4.0"); got != CompatTooOld {
		t.Fatalf("3.4.0 with raised min=3.5.0 should be TooOld, got %s", got)
	}
	if got := CheckXUI("3.5.0"); got != CompatSupported {
		t.Fatalf("3.5.0 at the raised floor should be Supported, got %s", got)
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

func TestLookupForPSPVersion_RangeMatchAndFirstWins(t *testing.T) {
	// Schema v2: array of entries, each carries [psp_min, psp_max] closed
	// interval. first-match-wins is the documented semantics so admin
	// puts narrower / newer ranges earlier; the test guards that.
	payload := remoteCompatPayload{
		SchemaVersion: 2,
		Major:         3,
		Entries: []remoteCompatPSPEntry{
			// narrower entry first — wins over the broader one below
			{PSPMin: "v3.6.5", PSPMax: "v3.6.8", MinXUI: "3.1.0", MaxTestedXUI: "3.2.0", Notes: "narrow hotfix"},
			// broader baseline
			{PSPMin: "v3.6.0", PSPMax: "v3.6.99", MinXUI: "3.1.0", MaxTestedXUI: "3.1.0", Notes: "v3.6 baseline"},
			{PSPMin: "v3.5.0", PSPMax: "v3.5.99", MinXUI: "3.1.0", MaxTestedXUI: "3.0.5"},
		},
	}
	cases := []struct {
		pspVer  string
		wantMax string
		wantOK  bool
	}{
		{"v3.6.0", "3.1.0", true},        // hits broader v3.6 baseline
		{"v3.6.0-beta.7", "3.1.0", true}, // pre-release counted as the stable it targets
		{"v3.6.5", "3.2.0", true},        // hits the narrow hotfix entry (first match wins)
		{"v3.6.8", "3.2.0", true},        // upper bound inclusive
		{"v3.6.9", "3.1.0", true},        // just past hotfix → falls back to baseline
		{"v3.6.99", "3.1.0", true},       // upper bound of baseline inclusive
		{"v3.5.0", "3.0.5", true},        // separate range
		{"v3.4.99", "", false},           // no entry covers it
		{"v3.7.0", "", false},            // no entry covers it
		{"dev", "", false},               // unparseable
		{"garbage", "", false},           // unparseable
	}
	for _, c := range cases {
		entry, ok := lookupForPSPVersion(payload, c.pspVer)
		if ok != c.wantOK {
			t.Fatalf("lookupForPSPVersion(%q): ok=%v, want ok=%v",
				c.pspVer, ok, c.wantOK)
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
