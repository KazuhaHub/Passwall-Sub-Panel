package version_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// ONE DOCUMENT PER PRODUCT: each states the builds it applies to, and which
// product it is.
//
// The per-major files were addressed by the product version's FIRST SEGMENT —
// v3.x reads v3.json — which is a compatibility major under the legacy scheme and
// a RELEASE LINE under the product scheme. A product build therefore derives its
// own per-product names instead, and a document that arrived from somewhere else
// says its product rather than having one inferred from which fields are
// populated: both products' field lists are optional in the shape, so a document
// that forgot to say would install nothing under one product's name, and the
// ceiling would stop moving with nothing recording why.
func panelRangesDocument(t *testing.T, overrides func(map[string]any)) []byte {
	t.Helper()
	return marshalDocument(t, map[string]any{
		"schema_version": 1,
		"product":        "3x-ui",
		"revision":       7,
		"issued_at":      "2026-09-16T00:00:00Z",
		"expires_at":     "2027-09-16T00:00:00Z",
		"applies_to_psp": map[string]any{"min": "4.0.0", "max": "4.99.99"},
		"entries": []any{map[string]any{
			"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0", "notes": "reviewed",
		}},
	}, overrides)
}

func suiPanelRangesDocument(t *testing.T, overrides func(map[string]any)) []byte {
	t.Helper()
	return marshalDocument(t, map[string]any{
		"schema_version": 1,
		"product":        "sui",
		"revision":       7,
		"issued_at":      "2026-09-16T00:00:00Z",
		"expires_at":     "2027-09-16T00:00:00Z",
		"applies_to_psp": map[string]any{"min": "4.0.0", "max": "4.99.99"},
		"sui_entries": []any{map[string]any{
			"psp_min": "4.0.0", "psp_max": "4.99.99", "max_tested_sui": "1.6.3", "notes": "reviewed",
		}},
	}, overrides)
}

func marshalDocument(t *testing.T, document map[string]any, overrides func(map[string]any)) []byte {
	t.Helper()
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
	if policy.Product != "3x-ui" {
		t.Fatalf("the document's product was not read: %+v", policy)
	}
	if policy.Revision != 7 || policy.AppliesToPSP.Min != "4.0.0" || policy.AppliesToPSP.Max != "4.99.99" {
		t.Fatalf("policy = %+v", policy)
	}
	if len(policy.Entries) != 1 || policy.Entries[0].MaxTestedXUI != "3.7.0" {
		t.Fatalf("entries = %+v", policy.Entries)
	}
	// The payload is the SAME shape the per-major manifest carries, so the
	// existing per-entry matching applies to it unchanged.
	if policy.Entries[0].PSPMin != "4.0.0" || policy.Entries[0].MinXUI != "3.4.2" {
		t.Fatalf("the payload did not survive as the manifest's shape: %+v", policy.Entries[0])
	}

	sui, err := version.ParsePanelRangesPolicy(suiPanelRangesDocument(t, nil), panelRangesNow)
	if err != nil {
		t.Fatalf("a reviewed S-UI document must parse: %v", err)
	}
	if sui.Product != "sui" || len(sui.SUIEntries) != 1 || sui.SUIEntries[0].MaxTestedSUI != "1.6.3" {
		t.Fatalf("sui policy = %+v", sui)
	}
}

// Each refusal names a way a document could be published that would widen what
// this panel claims to support. A document is refused rather than repaired: a
// range that had to be guessed is a range nobody reviewed.
func TestPanelRangesPolicyRefusesWhatItCannotStandBehind(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sui       bool
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
			name: "no product named", want: version.ErrPanelRangesProduct,
			overrides: func(d map[string]any) { delete(d, "product") },
			why: "which product a document is about cannot be inferred from which fields are populated — " +
				"an unnamed document would install nothing under the name of the address it came from",
		},
		{
			name: "an unknown product", want: version.ErrPanelRangesProduct,
			overrides: func(d map[string]any) { d["product"] = "sing-box" },
			why:       "a product this build does not read is a document whose rows it cannot place",
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
			overrides: func(d map[string]any) { d["entries"] = []any{} },
			why:       "a document that states no range supports nothing",
		},
		{
			// THE WRONG PRODUCT'S ROWS ARE NOT THE RIGHT PRODUCT'S ROWS. A 3X-UI
			// document carrying only sui_entries would install an empty 3X-UI range
			// set — a ceiling that stops moving rather than an error.
			name: "a 3x-ui document with only S-UI rows", want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) {
				delete(d, "entries")
				d["sui_entries"] = []any{map[string]any{"psp_min": "4.0.0", "psp_max": "4.99.99", "max_tested_sui": "1.6.3"}}
			},
			why: "the product decides which row list is the document's own",
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
			name: "a sui document with no S-UI rows", sui: true, want: version.ErrPanelRangesMalformed,
			overrides: func(d map[string]any) { d["sui_entries"] = []any{} },
			why:       "the product decides which row list is the document's own",
		},
		{
			name: "a sui entry with no ceiling", sui: true, want: version.ErrPanelRangesMalformed,
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
				if tc.sui {
					raw = suiPanelRangesDocument(t, tc.overrides)
				} else {
					raw = panelRangesDocument(t, tc.overrides)
				}
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
	if policy.Entries[0].Notes == "" {
		t.Fatal("the notes are the review record; a document without them is not reviewed")
	}
	sui, err := version.ParsePanelRangesPolicy(suiPanelRangesDocument(t, nil), panelRangesNow)
	if err != nil {
		t.Fatal(err)
	}
	if sui.SUIEntries[0].Notes == "" {
		t.Fatal("the notes are the review record; a document without them is not reviewed")
	}
}

// THE REPOSITORY'S OWN DOCUMENTS, validated by the same rule that will read them.
//
// A schema nothing exercises is a schema that drifts from its data: the validator
// and the files are edited by different people on different days, and the failure
// lands in a deployment rather than in CI. This reads the files that ship, so an
// edit that makes one unparseable is a red build.
func TestTheShippedPanelRangesDocumentsParse(t *testing.T) {
	for _, tc := range []struct {
		product     string
		wantCeiling string
	}{
		{product: "3x-ui", wantCeiling: "3.8.5"},
		{product: "sui", wantCeiling: "1.6.3"},
	} {
		t.Run(tc.product, func(t *testing.T) {
			name := fmt.Sprintf(map[string]string{"3x-ui": "3x-ui-v%d.json", "sui": "sui-v%d.json"}[tc.product], 4)
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", name))
			if err != nil {
				t.Fatalf("read the shipped document: %v", err)
			}
			policy, err := version.ParsePanelRangesPolicy(raw, panelRangesNow)
			if err != nil {
				t.Fatalf("the shipped document does not parse: %v", err)
			}
			if policy.Product != tc.product {
				t.Fatalf("the shipped %s document says it is %q", name, policy.Product)
			}
			// It must carry the range a 4.x build needs, not an empty shell: a
			// document that parses and says nothing would pass the check above and
			// be useless.
			//
			// AND IT CARRIES ONLY ITS OWN PRODUCT'S ROWS, which is the property the
			// split exists for: a document that repeated the other product's rows
			// would be two places to edit again, and the ceiling a reviewer approved
			// for one panel could be moved by editing the file for the other.
			switch tc.product {
			case "3x-ui":
				if len(policy.Entries) == 0 || policy.Entries[0].MaxTestedXUI != tc.wantCeiling {
					t.Fatalf("the shipped 3X-UI document carries %+v, want the reviewed ceiling %s", policy.Entries, tc.wantCeiling)
				}
				if len(policy.SUIEntries) != 0 || len(policy.SUIAdvisories) != 0 {
					t.Fatal("the 3X-UI document carries S-UI rows; the two products are separate documents")
				}
				if len(policy.Advisories) == 0 {
					t.Fatal("the 3X-UI document carries no advisories")
				}
			case "sui":
				if len(policy.SUIEntries) == 0 || policy.SUIEntries[0].MaxTestedSUI != tc.wantCeiling {
					t.Fatalf("the shipped S-UI document carries %+v, want the reviewed ceiling %s", policy.SUIEntries, tc.wantCeiling)
				}
				if len(policy.Entries) != 0 || len(policy.Advisories) != 0 {
					t.Fatal("the S-UI document carries 3X-UI rows; the two products are separate documents")
				}
				if len(policy.SUIAdvisories) == 0 {
					t.Fatal("the S-UI document carries no advisories")
				}
			}
		})
	}
}
