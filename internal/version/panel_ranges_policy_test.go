package version_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE PANEL RANGES POLICY: the per-major manifest's payload, as ONE document
// that states the builds it applies to.
//
// The per-major files are addressed by the product version's FIRST SEGMENT — v3.x
// reads v3.json — and under the product scheme that segment is a RELEASE LINE,
// not a compatibility major: 102.1.0 would ask for v102.json, which either does
// not exist or describes a different panel. A build therefore cannot reach its
// own ranges by that route at all, and it currently reaches none.
//
// This document is reached BY NAME and states the range it applies to, instead of
// having one inferred from a number. It does not replace the per-major files:
// builds that do not know it keep reading theirs, and the per-entry
// psp_min/psp_max matching is unchanged — what changes is only how the document is
// found.
func panelRangesDocument(t *testing.T, overrides func(map[string]any)) []byte {
	t.Helper()
	document := map[string]any{
		"schema_version": 1,
		"revision":       7,
		"issued_at":      "2026-09-16T00:00:00Z",
		"expires_at":     "2027-09-16T00:00:00Z",
		"applies_to_psp": map[string]any{"min": "4.0.0", "max": "4.99.99"},
		"entries": []any{map[string]any{
			"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0", "notes": "reviewed",
		}},
		"sui_entries": []any{map[string]any{
			"psp_min": "4.0.0", "psp_max": "4.99.99", "max_tested_sui": "1.6.3", "notes": "reviewed",
		}},
	}
	if overrides != nil {
		overrides(document)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var panelRangesNow = time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)

func TestPanelRangesPolicyAcceptsAReviewedDocument(t *testing.T) {
	policy, err := version.ParsePanelRangesPolicy(panelRangesDocument(t, nil), panelRangesNow)
	if err != nil {
		t.Fatalf("a reviewed document must parse: %v", err)
	}
	if policy.Revision != 7 || policy.AppliesToPSP.Min != "4.0.0" || policy.AppliesToPSP.Max != "4.99.99" {
		t.Fatalf("policy = %+v", policy)
	}
	if len(policy.Entries) != 1 || policy.Entries[0].MaxTestedXUI != "3.7.0" {
		t.Fatalf("entries = %+v", policy.Entries)
	}
	if len(policy.SUIEntries) != 1 || policy.SUIEntries[0].MaxTestedSUI != "1.6.3" {
		t.Fatalf("sui entries = %+v", policy.SUIEntries)
	}
	// The payload is the SAME shape the per-major manifest carries, so the
	// existing per-entry matching applies to it unchanged.
	if policy.Entries[0].PSPMin != "4.0.0" || policy.Entries[0].MinXUI != "3.4.2" {
		t.Fatalf("the payload did not survive as the manifest's shape: %+v", policy.Entries[0])
	}
}

// Each refusal names a way a document could be published that would widen what
// this panel claims to support. A document is refused rather than repaired: a
// range that had to be guessed is a range nobody reviewed.
func TestPanelRangesPolicyRefusesWhatItCannotStandBehind(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides func(map[string]any)
		now       time.Time
		want      error
		why       string
	}{
		{
			name: "an unknown schema", want: version.ErrPanelRangesSchema,
			overrides: func(d map[string]any) { d["schema_version"] = 2 },
			why:       "a newer document may mean something different by the same names",
		},
		{
			name: "an expired window", want: version.ErrPanelRangesExpired,
			overrides: func(d map[string]any) { d["expires_at"] = "2026-09-16T12:00:00Z" },
			why:       "a range is a claim about a build, and it stops applying when it is stale",
		},
		{
			name: "a revision that is not one", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) { d["revision"] = 0 },
			why:       "revision is what makes a newer document newer",
		},
		{
			name: "an inverted document range", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) { d["applies_to_psp"] = map[string]any{"min": "4.99.99", "max": "4.0.0"} },
			why:       "a range nothing can be inside is not an applicability statement",
		},
		{
			name: "an unprefixed-range typo", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) { d["applies_to_psp"] = map[string]any{"min": "4.0", "max": "4.99.99"} },
			why:       "a range endpoint that is not a version cannot be compared",
		},
		{
			name: "no entries at all", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) { d["entries"] = []any{}; d["sui_entries"] = []any{} },
			why:       "a document that states no range supports nothing",
		},
		{
			name: "an entry whose psp range is inverted", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) {
				d["entries"] = []any{map[string]any{"psp_min": "4.9.0", "psp_max": "4.1.0", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}}
			},
			why: "the manifest SKIPS such an entry, which is right for a fetched file and wrong for a reviewed document — " +
				"a row that silently does nothing is a row somebody believes is published",
		},
		{
			name: "an entry whose XUI ceiling is below its floor", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) {
				d["entries"] = []any{map[string]any{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.8.0", "max_tested_xui": "3.4.2"}}
			},
			why: "a ceiling below the floor certifies nothing",
		},
		{
			name: "an entry reaching past the document range", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) {
				d["entries"] = []any{map[string]any{"psp_min": "4.0.0", "psp_max": "5.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}}
			},
			why: "the document says which builds it applies to; an entry wider than that is a claim about builds it does not cover",
		},
		{
			name: "a sui entry with no ceiling", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) {
				d["sui_entries"] = []any{map[string]any{"psp_min": "4.0.0", "psp_max": "4.99.99"}}
			},
			why: "the ceiling is the whole claim",
		},
		{
			name: "not JSON", want: version.ErrPanelRangesMalformed,
			overrides: nil, now: panelRangesNow,
			why: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("not json")
			if tc.overrides != nil {
				raw = panelRangesDocument(t, tc.overrides)
			}
			now := tc.now
			if now.IsZero() {
				now = panelRangesNow
			}
			_, err := version.ParsePanelRangesPolicy(raw, now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v — %s", err, tc.want, tc.why)
			}
			// A refusal an operator cannot act on is indistinguishable from one
			// this validator got wrong by accident.
			if tc.why != "" && strings.TrimSpace(err.Error()) == "" {
				t.Fatal("the refusal says nothing")
			}
		})
	}
}

// The document and the per-major manifest carry the SAME payload, so the
// instruction not to diverge is checked rather than asserted in prose: a document
// that drops a field the manifest needs would parse here and fail there.
func TestTheDocumentCarriesTheManifestsPayload(t *testing.T) {
	policy, err := version.ParsePanelRangesPolicy(panelRangesDocument(t, nil), panelRangesNow)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Entries[0].Notes == "" || policy.SUIEntries[0].Notes == "" {
		t.Fatal("the notes are the review record; a document without them is not reviewed")
	}
}

// THE REPOSITORY'S OWN DOCUMENT, validated by the same rule that will read it.
//
// A schema nothing exercises is a schema that drifts from its data: the validator
// and the file are edited by different people on different days, and the failure
// lands in a deployment rather than in CI. This reads the file that ships, so an
// edit that makes it unparseable is a red build.
func TestTheShippedPanelRangesDocumentParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "panel-ranges-v1.json"))
	if err != nil {
		t.Fatalf("read the shipped document: %v", err)
	}
	policy, err := version.ParsePanelRangesPolicy(raw, panelRangesNow)
	if err != nil {
		t.Fatalf("the shipped document does not parse: %v", err)
	}
	// It must carry the ranges a 4.x build needs, not an empty shell: a document
	// that parses and says nothing would pass the check above and be useless.
	if len(policy.Entries) == 0 || policy.Entries[0].MaxTestedXUI == "" {
		t.Fatalf("the shipped document carries no XUI ceiling: %+v", policy.Entries)
	}
	if len(policy.SUIEntries) == 0 || policy.SUIEntries[0].MaxTestedSUI == "" {
		t.Fatalf("the shipped document carries no S-UI ceiling: %+v", policy.SUIEntries)
	}
	// And it must agree with the per-major file it was derived from, or the two
	// would certify different panels depending on which one a build could reach.
	manifest, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fromManifest struct {
		Entries    []map[string]any `json:"entries"`
		SUIEntries []map[string]any `json:"sui_entries"`
	}
	if err := json.Unmarshal(manifest, &fromManifest); err != nil {
		t.Fatal(err)
	}
	if len(fromManifest.Entries) != len(policy.Entries) {
		t.Fatalf("the document has %d XUI entries, v4.json has %d", len(policy.Entries), len(fromManifest.Entries))
	}
	for i, entry := range fromManifest.Entries {
		if entry["max_tested_xui"] != policy.Entries[i].MaxTestedXUI || entry["min_xui"] != policy.Entries[i].MinXUI {
			t.Fatalf("entry %d disagrees with v4.json: %v vs %+v", i, entry, policy.Entries[i])
		}
	}
}
