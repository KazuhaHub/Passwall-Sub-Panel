package version

import (
	"encoding/json"
	"fmt"
	"time"
)

// The tested ceiling (max_tested_xui / max_tested_sui in the ranges documents)
// is the one compat fact that goes stale WITHOUT anybody editing anything: it
// is a statement about an upstream that keeps shipping. Everything else in that
// file drifts only when a human changes a line, and TestMinXUIConstMatchesCompatJSON
// already guards that direction.
//
// Staleness here is not silent-but-harmless. PSP refuses a panel upgrade whose
// target exceeds the ceiling (see MaxTestedXUI in compat.go), which is the right
// fail-safe direction but means a stale ceiling surfaces as an ADMIN being
// blocked from upgrading, with no signal reaching whoever maintains the file.
// This is the pure half of closing that loop; cmd/compatwatch does the fetching.

// CeilingVerdict is the outcome of comparing a shipped tested-ceiling against
// the upstream's newest release.
type CeilingVerdict string

const (
	// CeilingCurrent — upstream has published nothing newer than the ceiling.
	CeilingCurrent CeilingVerdict = "current"
	// CeilingBehind — upstream is ahead; the compat file needs a review pass.
	CeilingBehind CeilingVerdict = "behind"
	// CeilingUnknown — the comparison could not be made. Deliberately its own
	// verdict rather than being folded into "current": an upstream whose
	// releases we can no longer read (repo moved, API shape changed, rate
	// limited) looks EXACTLY like an upstream that has shipped nothing, and
	// collapsing the two turns a broken watcher into a clean bill of health.
	CeilingUnknown CeilingVerdict = "unknown"
)

// CeilingReport is one upstream's row. Every field is filled on every verdict so
// a caller can print the whole table unconditionally — the denominator has to
// stay visible, or a single "behind" row reads as the complete picture.
type CeilingReport struct {
	// Upstream is the human name ("3X-UI" / "S-UI").
	Upstream string
	// Ceiling is the shipped max_tested value, "" when none is published.
	Ceiling string
	// Latest is the upstream's newest release tag, "" when it could not be read.
	Latest string
	// Verdict is the outcome; Reason always says why, including for Current.
	Verdict CeilingVerdict
	Reason  string
}

// CompareCeiling judges one upstream. Both arguments are raw tags — leading "v"
// and any pre-release suffix are handled by parseSemver, so "v3.7.0" and "3.7.0"
// compare equal.
//
// Order matters: every "cannot tell" branch is checked BEFORE the comparison, so
// no unparseable or missing input can reach the equality test and come back
// looking current.
func CompareCeiling(upstream, ceiling, latest string) CeilingReport {
	out := CeilingReport{Upstream: upstream, Ceiling: ceiling, Latest: latest}
	if ceiling == "" {
		out.Verdict = CeilingUnknown
		out.Reason = "no tested ceiling is published for the newest PSP entry"
		return out
	}
	if latest == "" {
		out.Verdict = CeilingUnknown
		out.Reason = "upstream's newest release could not be read"
		return out
	}
	c, ok := parseSemver(ceiling)
	if !ok {
		out.Verdict = CeilingUnknown
		out.Reason = fmt.Sprintf("shipped ceiling %q is not a version", ceiling)
		return out
	}
	l, ok := parseSemver(latest)
	if !ok {
		out.Verdict = CeilingUnknown
		out.Reason = fmt.Sprintf("upstream tag %q is not a version", latest)
		return out
	}
	if cmpSemver(l, c) > 0 {
		out.Verdict = CeilingBehind
		out.Reason = fmt.Sprintf("upstream released %s, past the tested ceiling %s", latest, ceiling)
		return out
	}
	out.Verdict = CeilingCurrent
	out.Reason = fmt.Sprintf("upstream's newest is %s, at or below the tested ceiling %s", latest, ceiling)
	return out
}

// CeilingsFromCompatJSON extracts the two tested ceilings a CURRENT build
// resolves to out of a raw docs/compat/v<major>.json.
//
// "Current build" is the entry with the largest psp_max — the same selection
// TestMinXUIConstMatchesCompatJSON uses, and for the same reason: older entries
// keep their own historical ceilings on purpose, and holding those to the
// upstream's newest release would report permanent, unfixable staleness.
//
// A missing sui_entries block yields an empty S-UI ceiling rather than an error.
// That is a real state (the S-UI gate went unpublished for the whole v3.6-v3.9.1
// range) and CompareCeiling renders it as unknown, which is what it is.
func CeilingsFromCompatJSON(raw []byte) (xui string, sui string, err error) {
	var payload remoteCompatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", fmt.Errorf("parse compat json: %w", err)
	}
	if len(payload.Entries) == 0 {
		return "", "", fmt.Errorf("compat json has no entries")
	}
	xui, sui = ceilingsFromPayload(payload)
	if xui == "" {
		return "", "", fmt.Errorf("no entry has a parseable psp_max")
	}
	return xui, sui, nil
}

// CeilingFromDocument reads ONE product's ranges document and returns which
// product it is and the tested ceiling that product publishes.
//
// IT EXISTS BECAUSE CeilingsFromCompatJSON CANNOT ANSWER THIS. That function reads
// a manifest carrying both panels and requires the 3X-UI half to be present, which
// is right for the per-major files and wrong for a per-product document: an S-UI
// document has no entries at all, and reading one through the manifest's rules
// would call it empty rather than calling it S-UI's.
func CeilingFromDocument(raw []byte, now time.Time) (product string, ceiling string, err error) {
	policy, err := ParsePanelRangesPolicy(raw, now)
	if err != nil {
		return "", "", err
	}
	window := policy.AppliesToPSP
	payload := remoteCompatPayload{
		SchemaVersion: schemaVersion, Product: policy.Product,
		Entries: policy.Entries, SUIEntries: policy.SUIEntries,
		Advisories: policy.Advisories, SUIAdvisories: policy.SUIAdvisories,
		AppliesToPSP: &window,
	}
	xui, sui := ceilingsFromPayload(payload)
	if policy.Product == productXUI {
		return policy.Product, xui, nil
	}
	return policy.Product, sui, nil
}

// ceilingsFromPayload returns the tested ceiling each product's rows publish. AN
// ABSENT HALF YIELDS AN EMPTY CEILING RATHER THAN AN ERROR: a document that is
// about one product has nothing to say about the other, and that is a state the
// callers render as unknown rather than a defect to report.
func ceilingsFromPayload(payload remoteCompatPayload) (xui string, sui string) {
	var bestXUI [3]int
	var haveXUI bool
	for _, e := range payload.Entries {
		v, ok := parseSemver(e.PSPMax)
		if !ok {
			continue
		}
		if !haveXUI || cmpSemver(v, bestXUI) > 0 {
			bestXUI, haveXUI, xui = v, true, e.MaxTestedXUI
		}
	}
	var bestSUI [3]int
	var haveSUI bool
	for _, e := range payload.SUIEntries {
		v, ok := parseSemver(e.PSPMax)
		if !ok {
			continue
		}
		if !haveSUI || cmpSemver(v, bestSUI) > 0 {
			bestSUI, haveSUI, sui = v, true, e.MaxTestedSUI
		}
	}
	return xui, sui
}
