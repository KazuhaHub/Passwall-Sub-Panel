package version

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// APPLYING A PANEL RANGES DOCUMENT, and replaying it from the cache.
//
// The interesting half is the SECOND one. A document that installs from a fetch
// and then fails to install from the cache leaves a range in force that the next
// boot silently drops — the panel would report a supported ceiling until it
// restarted and then stop. Both paths go through payloadApplies for that reason,
// and these tests drive both.
func panelRangesJSON(min, max string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "revision": 3,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": %q, "max": %q},
	  "entries": [{"psp_min": %q, "psp_max": %q, "min_xui": "3.4.2", "max_tested_xui": "3.7.0", "notes": "reviewed"}],
	  "sui_entries": [{"psp_min": %q, "psp_max": %q, "max_tested_sui": "1.6.3", "notes": "reviewed"}]
	}`, min, max, min, max, min, max))
}

var panelRangesApplyNow = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

func TestAPanelRangesDocumentInstallsForTheBuildItNames(t *testing.T) {
	// A PRODUCT version: three integers, no prefix. This is the build that could
	// reach no ranges at all before the document existed.
	isolatedCompatCache(t, "4.0.0")
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("ceiling before install = %q", got)
	}
	if err := applyPanelRangesDocument(panelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow); err != nil {
		t.Fatalf("the document names this build: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.7.0" {
		t.Fatalf("ceiling = %q, want 3.7.0", got)
	}
	if got := ActiveMaxTestedSUI(); got != "1.6.3" {
		t.Fatalf("sui ceiling = %q, want 1.6.3", got)
	}
}

func TestAPanelRangesDocumentIsRefusedForABuildItDoesNotName(t *testing.T) {
	isolatedCompatCache(t, "5.0.0")
	err := applyPanelRangesDocument(panelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow)
	if err == nil {
		t.Fatal("a document that names another window installed here")
	}
	// The message has to say which window, or an operator cannot tell a
	// mispublished document from a broken one.
	for _, want := range []string{"4.0.0", "4.99.99", "5.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("a refused document established a ceiling: %q", got)
	}
}

// The cache must reach the SAME conclusion the fetch did. A document cached by
// one build and read by another is the case that separates the two paths.
func TestTheCachedRangesDocumentReplaysTheSameDecision(t *testing.T) {
	raw := panelRangesJSON("4.0.0", "4.99.99")

	for _, tc := range []struct {
		name    string
		build   string
		wantMax string
		wantErr bool
	}{
		{name: "the build the document names", build: "4.0.0", wantMax: "3.7.0"},
		{name: "a later release line", build: "5.0.0", wantErr: true},
		{name: "outside the window", build: "3.9.2", wantErr: true},
		{name: "no release identity at all", build: "dev", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, tc.build)
			// Written as the fetch would have written it: the converted payload,
			// carrying its window.
			policy, err := ParsePanelRangesPolicy(raw, panelRangesApplyNow)
			if err != nil {
				t.Fatal(err)
			}
			window := policy.AppliesToPSP
			writeSnapshot(t, dir, remoteCompatPayload{
				SchemaVersion: schemaVersion, UpdatedAt: "2026-09-19T00:00:00Z",
				Entries: policy.Entries, SUIEntries: policy.SUIEntries, AppliesToPSP: &window,
			})
			if got := ActiveMaxTestedXUI(); got != "" {
				t.Fatalf("ceiling before load = %q", got)
			}

			err = LoadPolicySnapshot()
			if (err != nil) != tc.wantErr {
				t.Fatalf("load error = %v, wantError = %v", err, tc.wantErr)
			}
			if got := ActiveMaxTestedXUI(); got != tc.wantMax {
				t.Fatalf("ceiling = %q, want %q", got, tc.wantMax)
			}
		})
	}
}

// An expired document is refused by the strict validator before anything is
// installed, which is the reason the document is parsed by its own rules rather
// than as a payload: the payload shape has no expiry to check.
func TestAnExpiredRangesDocumentInstallsNothing(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	expired := []byte(`{
	  "schema_version": 1, "revision": 3,
	  "issued_at": "2026-01-01T00:00:00Z", "expires_at": "2026-02-01T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}]
	}`)
	err := applyPanelRangesDocument(expired, panelRangesApplyNow)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want an expiry refusal", err)
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("an expired document established a ceiling: %q", got)
	}
}
