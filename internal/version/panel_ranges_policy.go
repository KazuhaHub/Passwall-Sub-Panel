package version

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PanelRangesPolicy is ONE PRODUCT's compat ranges, published as a document that
// states the builds it applies to and which product it is.
//
// WHY IT IS SHAPED THIS WAY, in three parts.
//
// THE PRODUCT IS ON THE DOCUMENT. The ranges used to arrive in one document
// carrying both panels, so a review of one panel rewrote the file that carried the
// other's ceiling. They are now separate documents, and each says which product it
// is rather than having that inferred from which of the two optional field lists
// happens to be populated.
//
// THE MAJOR IS IN THE NAME. A legacy build reads a per-major manifest, whose name
// is its compatibility major. A product version's first segment is a RELEASE LINE
// rather than a compatibility major — 102.1.0 would ask for v102.json, which
// either does not exist or describes a different panel — so the per-product names
// are derived from the PANEL major instead. The name decides WHERE TO LOOK, and
// the window below decides whether what was found COUNTS: a name is not evidence,
// and a document that arrived under a name this build asked for can still be about
// a different build.
//
// RANGE IS A CLAIM ABOUT A BUILD, which is why the applicability window and the
// revision are on the document rather than inferred. A range that had to be
// guessed is a range nobody reviewed.
// CompatPSPRange is the window of panel builds a ranges document was reviewed
// for — and the FIRST GATE every such document passes, before any entry in it is
// looked at.
//
// IT LIVES HERE RATHER THAN WITH A DOCUMENT TYPE because two shapes carry it: a
// product's ranges document states its own window, and a per-major manifest's
// converted payload carries the one it was read under. Both are "which builds is
// this a claim about", and a claim that had to be inferred from a file NAME is a
// claim nobody wrote down.
type CompatPSPRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

type PanelRangesPolicy struct {
	SchemaVersion int `json:"schema_version"`
	// Product says which document this is, and it must be one of the products
	// this build knows. It is REQUIRED rather than inferred from which fields are
	// populated, because the two products' fields are both optional in the shape
	// and a document that forgot to say would otherwise install nothing under one
	// product's name — a ceiling that stops moving with nothing recording why.
	//
	// It is also the cross-check against the ADDRESS: the fetch knows which
	// document it asked for, and a document that turns out to be the other
	// product's is a wrong file at that URL, not a review that removed a range.
	Product   string    `json:"product"`
	Revision  int64     `json:"revision"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// AppliesToPSP is the window of panel builds this document was reviewed for.
	// Per-entry psp_min/psp_max narrow it further; nothing may widen it.
	AppliesToPSP CompatPSPRange         `json:"applies_to_psp"`
	Entries      []remoteCompatPSPEntry `json:"entries"`
	// SUIEntries is optional for the same reason it is optional in the manifest:
	// a document published before S-UI ranges were reviewed carries none, and
	// that is a gap rather than an error.
	SUIEntries []remoteCompatSUIEntry `json:"sui_entries,omitempty"`
	// Advisories is the manifest's version→advisory map, carried unchanged.
	Advisories map[string]XUIAdvisory `json:"xui_advisories,omitempty"`
	// SUIAdvisories mirrors Advisories for S-UI releases, as in the manifest.
	SUIAdvisories map[string]XUIAdvisory `json:"sui_advisories,omitempty"`
}

// panelRangesSchema is the only schema this build reads. An unknown one is
// refused rather than parsed optimistically: a newer document may mean something
// different by the same field names, and the fields here are ranges.
const panelRangesSchema = 1

var (
	// ErrPanelRangesSchema means the document is a format this build does not read.
	ErrPanelRangesSchema = errors.New("panel ranges policy: unsupported schema_version")
	// ErrPanelRangesExpired means the document's validity window has passed.
	ErrPanelRangesExpired = errors.New("panel ranges policy: expired")
	// ErrPanelRangesMalformed means the document is internally inconsistent.
	ErrPanelRangesMalformed = errors.New("panel ranges policy: malformed")
	// ErrPanelRangesProduct means the document does not say which product it is,
	// or names one this build does not know.
	ErrPanelRangesProduct = errors.New("panel ranges policy: unknown product")
)

// ParsePanelRangesPolicy validates a panel ranges document.
//
// It REFUSES RATHER THAN REPAIRS, and that is a deliberate difference from how
// the per-major manifest is treated. The manifest SKIPS an entry whose range is
// unusable — the right answer for a fetched file that is mostly good, because the
// alternative is reading no ranges at all. A reviewed document is different: a row
// that silently does nothing is a row somebody believes is published, and the
// failure surfaces as a ceiling nobody can explain. So one bad entry refuses the
// document, and the message says which.
func ParsePanelRangesPolicy(raw []byte, now time.Time) (PanelRangesPolicy, error) {
	var policy PanelRangesPolicy
	// Unknown FIELDS are tolerated, as in the manifest and the releases policy: a
	// document published for a newer reader may carry more than this build
	// understands, and refusing it would mean reading no ranges at all. Unknown
	// SCHEMAS are not.
	if err := json.Unmarshal(raw, &policy); err != nil {
		return PanelRangesPolicy{}, fmt.Errorf("%w: %v", ErrPanelRangesMalformed, err)
	}
	if policy.SchemaVersion != panelRangesSchema {
		return PanelRangesPolicy{}, fmt.Errorf("%w: %d, this build reads %d", ErrPanelRangesSchema, policy.SchemaVersion, panelRangesSchema)
	}
	if !knownProduct(policy.Product) {
		return PanelRangesPolicy{}, fmt.Errorf("%w: product %q is not one of %s or %s", ErrPanelRangesProduct, policy.Product, productXUI, productSUI)
	}
	if policy.Revision <= 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: revision must be positive, got %d", ErrPanelRangesMalformed, policy.Revision)
	}
	if policy.IssuedAt.IsZero() || policy.ExpiresAt.IsZero() || !policy.ExpiresAt.After(policy.IssuedAt) {
		return PanelRangesPolicy{}, fmt.Errorf("%w: the validity window must be present and end after it starts", ErrPanelRangesMalformed)
	}
	if !now.Before(policy.ExpiresAt) {
		return PanelRangesPolicy{}, fmt.Errorf("%w: it expired at %s", ErrPanelRangesExpired, policy.ExpiresAt.UTC().Format(time.RFC3339))
	}

	window, err := parseCompatPSPRange(policy.AppliesToPSP)
	if err != nil {
		return PanelRangesPolicy{}, err
	}
	// A DOCUMENT WITH NO ROWS OF ITS OWN SUPPORTS NOTHING, and which rows those
	// are follows from the product it named. This replaces one combined check:
	// with the products separated, a `3x-ui` document that carries only
	// `sui_entries` is not "a document with some entries" — it is the wrong
	// document, and it would install an empty 3X-UI range set.
	if len(policy.Entries) == 0 && len(policy.SUIEntries) == 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: a document with no entries supports nothing", ErrPanelRangesMalformed)
	}
	if policy.Product == productXUI && len(policy.Entries) == 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: a %s document carries no entries", ErrPanelRangesMalformed, productXUI)
	}
	if policy.Product == productSUI && len(policy.SUIEntries) == 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: a %s document carries no sui_entries", ErrPanelRangesMalformed, productSUI)
	}
	for i, entry := range policy.Entries {
		if err := checkPanelRangeEntry(i, entry.PSPMin, entry.PSPMax, window); err != nil {
			return PanelRangesPolicy{}, err
		}
		if entry.MaxTestedXUI == "" {
			return PanelRangesPolicy{}, fmt.Errorf("%w: entry %d has no max_tested_xui; the ceiling is the whole claim", ErrPanelRangesMalformed, i)
		}
		if err := checkCeiling("min_xui", entry.MinXUI, entry.MaxTestedXUI, i); err != nil {
			return PanelRangesPolicy{}, err
		}
	}
	for i, entry := range policy.SUIEntries {
		if err := checkPanelRangeEntry(i, entry.PSPMin, entry.PSPMax, window); err != nil {
			return PanelRangesPolicy{}, err
		}
		if entry.MaxTestedSUI == "" {
			return PanelRangesPolicy{}, fmt.Errorf("%w: sui entry %d has no max_tested_sui; the ceiling is the whole claim", ErrPanelRangesMalformed, i)
		}
		// MinSUI may be empty: the manifest publishes no S-UI floor, deliberately.
		if entry.MinSUI != "" {
			if err := checkCeiling("min_sui", entry.MinSUI, entry.MaxTestedSUI, i); err != nil {
				return PanelRangesPolicy{}, err
			}
		}
	}
	return policy, nil
}

// parseCompatPSPRange validates the document's applicability window and returns
// it, so the entries can be checked against the same numbers.
func parseCompatPSPRange(r CompatPSPRange) ([2]string, error) {
	if !IsReleaseVersion(r.Min) || !IsReleaseVersion(r.Max) {
		return [2]string{}, fmt.Errorf("%w: applies_to_psp must name release versions, got %q..%q", ErrPanelRangesMalformed, r.Min, r.Max)
	}
	if CompareRelease(r.Min, r.Max) > 0 {
		return [2]string{}, fmt.Errorf("%w: applies_to_psp is inverted, %q..%q", ErrPanelRangesMalformed, r.Min, r.Max)
	}
	return [2]string{r.Min, r.Max}, nil
}

// checkPanelRangeEntry validates one entry's PSP range and requires it to stay
// inside the document's window.
func checkPanelRangeEntry(index int, min, max string, window [2]string) error {
	if !IsReleaseVersion(min) || !IsReleaseVersion(max) || CompareRelease(min, max) > 0 {
		return fmt.Errorf("%w: entry %d has an unusable psp range %q..%q", ErrPanelRangesMalformed, index, min, max)
	}
	if CompareRelease(min, window[0]) < 0 || CompareRelease(max, window[1]) > 0 {
		return fmt.Errorf("%w: entry %d reaches %q..%q, outside the document's %q..%q", ErrPanelRangesMalformed, index, min, max, window[0], window[1])
	}
	return nil
}

// checkCeiling validates a floor/ceiling pair. Panel versions are compared the
// way the manifest compares them — the three numeric segments, ignoring a
// prerelease suffix — because that is the comparison the lookup applies, and a
// document validated by a different rule would be accepted here and skipped
// there.
func checkCeiling(floorName, floor, ceiling string, index int) error {
	low, lok := parseSemver(floor)
	high, hok := parseSemver(ceiling)
	if !lok || !hok {
		return fmt.Errorf("%w: entry %d has an unusable %s/ceiling pair %q..%q", ErrPanelRangesMalformed, index, floorName, floor, ceiling)
	}
	if cmpSemver(low, high) > 0 {
		return fmt.Errorf("%w: entry %d has %s %q above its ceiling %q", ErrPanelRangesMalformed, index, floorName, floor, ceiling)
	}
	return nil
}
