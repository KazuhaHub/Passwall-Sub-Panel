package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// resetSUI clears both S-UI bounds so each case starts from the
// "nothing published" state regardless of test ordering.
func resetSUI(t *testing.T) {
	t.Helper()
	SetActiveMaxTestedSUI("")
	SetActiveMinSUI("")
	SetActiveSUIAdvisories(nil)
	t.Cleanup(func() {
		SetActiveMaxTestedSUI("")
		SetActiveMinSUI("")
		SetActiveSUIAdvisories(nil)
	})
}

// The default state matters most: PSP ships with no verified S-UI range, and in
// that state the gate must stay silent rather than guess. A regression here
// would put an un-actionable badge on every S-UI panel.
func TestCheckSUIUnknownUntilRangePublished(t *testing.T) {
	resetSUI(t)
	if got := CheckSUI("1.0.0"); got != CompatUnknown {
		t.Fatalf("no published range: got %v, want unknown", got)
	}
	// Unparseable versions stay unknown even once a range exists.
	SetActiveMaxTestedSUI("1.2.0")
	if got := CheckSUI("not-a-version"); got != CompatUnknown {
		t.Fatalf("unparseable version: got %v, want unknown", got)
	}
}

func TestCheckSUISupportedAndUntestedAgainstCeiling(t *testing.T) {
	resetSUI(t)
	SetActiveMaxTestedSUI("1.2.0")
	for _, tc := range []struct {
		version string
		want    CompatStatus
	}{
		{"1.2.0", CompatSupported}, // at the ceiling
		{"1.1.9", CompatSupported}, // below it
		{"v1.2.0", CompatSupported},
		{"1.2.1", CompatUntested}, // above it
		{"2.0.0", CompatUntested},
	} {
		if got := CheckSUI(tc.version); got != tc.want {
			t.Fatalf("CheckSUI(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

// A floor nobody published must never be used to tell an admin their panel is
// too old — with a ceiling but no floor, even a very old version is Supported.
func TestCheckSUITooOldOnlyWhenFloorPublished(t *testing.T) {
	resetSUI(t)
	SetActiveMaxTestedSUI("1.2.0")
	if got := CheckSUI("0.0.1"); got != CompatSupported {
		t.Fatalf("no floor published: CheckSUI(0.0.1) = %v, want supported", got)
	}
	SetActiveMinSUI("1.1.0")
	if got := CheckSUI("1.0.9"); got != CompatTooOld {
		t.Fatalf("floor published: CheckSUI(1.0.9) = %v, want too_old", got)
	}
	if got := CheckSUI("1.1.0"); got != CompatSupported {
		t.Fatalf("at floor: got %v, want supported", got)
	}
}

// Absence of sui_entries must clear a previously-installed range instead of
// leaving it stale — otherwise rolling a row back in the JSON would silently
// keep the old ceiling in effect on already-running panels.
func TestApplySUICompatClearsWhenPayloadPublishesNone(t *testing.T) {
	resetSUI(t)
	SetActiveMaxTestedSUI("1.2.0")
	SetActiveMinSUI("1.0.0")
	applySUICompat(remoteCompatPayload{}) // no sui_entries at all
	if ActiveMaxTestedSUI() != "" || ActiveMinSUI() != "" {
		t.Fatalf("stale range survived: max=%q min=%q", ActiveMaxTestedSUI(), ActiveMinSUI())
	}
}

func TestApplySUICompatInstallsMatchingEntryAndIgnoresUnusableOnes(t *testing.T) {
	resetSUI(t)
	cur := Version
	Version = "v3.9.1"
	t.Cleanup(func() { Version = cur })

	applySUICompat(remoteCompatPayload{SUIEntries: []remoteCompatSUIEntry{
		{PSPMin: "v3.0.0", PSPMax: "v3.5.0", MaxTestedSUI: "0.9.0"}, // out of range
		{PSPMin: "v3.9.0", PSPMax: "v3.99.99", MinSUI: "1.0.0", MaxTestedSUI: "1.2.0"},
	}})
	if ActiveMaxTestedSUI() != "1.2.0" || ActiveMinSUI() != "1.0.0" {
		t.Fatalf("matching entry not installed: max=%q min=%q", ActiveMaxTestedSUI(), ActiveMinSUI())
	}

	// An unparseable ceiling is treated as unpublished, never guessed at.
	applySUICompat(remoteCompatPayload{SUIEntries: []remoteCompatSUIEntry{
		{PSPMin: "v3.9.0", PSPMax: "v3.99.99", MaxTestedSUI: "not-a-version"},
	}})
	if ActiveMaxTestedSUI() != "" {
		t.Fatalf("unparseable ceiling installed: %q", ActiveMaxTestedSUI())
	}

	// An unparseable floor drops only the floor; the good ceiling still applies.
	applySUICompat(remoteCompatPayload{SUIEntries: []remoteCompatSUIEntry{
		{PSPMin: "v3.9.0", PSPMax: "v3.99.99", MinSUI: "??", MaxTestedSUI: "1.3.0"},
	}})
	if ActiveMaxTestedSUI() != "1.3.0" || ActiveMinSUI() != "" {
		t.Fatalf("bad floor not isolated: max=%q min=%q", ActiveMaxTestedSUI(), ActiveMinSUI())
	}
}

func TestLookupSUIAdvisoryMatchesExactVersion(t *testing.T) {
	resetSUI(t)
	applySUICompat(remoteCompatPayload{SUIAdvisories: map[string]XUIAdvisory{
		"v1.2.0": {Severity: "warning", Text: "heads up"},
	}})
	if a, ok := LookupSUIAdvisory("1.2.0"); !ok || a.Text != "heads up" {
		t.Fatalf("advisory not found for 1.2.0: %#v ok=%v", a, ok)
	}
	if _, ok := LookupSUIAdvisory("1.2.1"); ok {
		t.Fatal("advisory leaked onto a neighbouring version")
	}
}

// The shipped JSON must stay parseable by THIS build's structs, and its S-UI
// rows (whenever someone adds them) must carry a usable ceiling — the same
// standard TestMinXUIConstMatchesCompatJSON holds the 3X-UI side to.
func TestCompatJSONSUIEntriesAreWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "compat", "v3.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for i, e := range payload.SUIEntries {
		if _, ok := parseSemver(e.PSPMin); !ok {
			t.Errorf("sui_entries[%d]: unparseable psp_min %q", i, e.PSPMin)
		}
		if _, ok := parseSemver(e.PSPMax); !ok {
			t.Errorf("sui_entries[%d]: unparseable psp_max %q", i, e.PSPMax)
		}
		if _, ok := parseSemver(e.MaxTestedSUI); !ok {
			t.Errorf("sui_entries[%d]: unparseable max_tested_sui %q (a published row must carry a usable ceiling)", i, e.MaxTestedSUI)
		}
		if e.MinSUI != "" {
			if _, ok := parseSemver(e.MinSUI); !ok {
				t.Errorf("sui_entries[%d]: unparseable min_sui %q", i, e.MinSUI)
			}
		}
	}
	for k := range payload.SUIAdvisories {
		if _, ok := canonSemverKey(k); !ok {
			t.Errorf("sui_advisories key %q is not a parseable version", k)
		}
	}
}
