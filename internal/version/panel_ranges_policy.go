package version

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PanelRangesPolicy is the per-major compat manifest's payload, published as ONE
// document that states the builds it applies to.
//
// WHY IT EXISTS. The per-major files are addressed by the product version's FIRST
// SEGMENT — v3.x reads v3.json — and under the product scheme that segment is a
// RELEASE LINE, not a compatibility major: 102.1.0 would ask for v102.json, which
// either does not exist or describes a different panel. A build reached its own
// ranges by that route and no longer can, and the route it took instead was
// nothing at all.
//
// THIS DOCUMENT IS REACHED BY NAME and states the range it applies to, instead of
// having one inferred from a number. It does NOT replace the per-major files:
// builds that do not know it keep reading theirs, and the per-entry
// psp_min/psp_max matching is unchanged — only how the document is found is new.
//
// RANGE IS A CLAIM ABOUT A BUILD, which is why the applicability window and the
// revision are on the document rather than inferred. A range that had to be
// guessed is a range nobody reviewed.
type PanelRangesPolicy struct {
	SchemaVersion int       `json:"schema_version"`
	Revision      int64     `json:"revision"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	// AppliesToPSP is the window of panel builds this document was reviewed for.
	// Per-entry psp_min/psp_max narrow it further; nothing may widen it.
	AppliesToPSP PolicyPSPRange         `json:"applies_to_psp"`
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
	if policy.Revision <= 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: revision must be positive, got %d", ErrPanelRangesMalformed, policy.Revision)
	}
	if policy.IssuedAt.IsZero() || policy.ExpiresAt.IsZero() || !policy.ExpiresAt.After(policy.IssuedAt) {
		return PanelRangesPolicy{}, fmt.Errorf("%w: the validity window must be present and end after it starts", ErrPanelRangesMalformed)
	}
	if !now.Before(policy.ExpiresAt) {
		return PanelRangesPolicy{}, fmt.Errorf("%w: it expired at %s", ErrPanelRangesExpired, policy.ExpiresAt.UTC().Format(time.RFC3339))
	}

	window, err := parsePolicyPSPRange(policy.AppliesToPSP)
	if err != nil {
		return PanelRangesPolicy{}, err
	}
	if len(policy.Entries) == 0 && len(policy.SUIEntries) == 0 {
		return PanelRangesPolicy{}, fmt.Errorf("%w: a document with no entries supports nothing", ErrPanelRangesMalformed)
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

// parsePolicyPSPRange validates the document's applicability window and returns
// it, so the entries can be checked against the same numbers.
func parsePolicyPSPRange(r PolicyPSPRange) ([2]string, error) {
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
