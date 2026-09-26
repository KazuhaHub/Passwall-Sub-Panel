package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// geoReasonLocaleKeys is every key under admin:geo_anomalies.* the SPA's
// reasonText (web-react/src/utils/geoAnomaly.ts) reads to localize a code.
// The over tiers and the scope labels are listed whole rather than per tier:
// the code does not say which tier or scope a row will carry, so a code is
// only localized when every one it can meet is.
//
// A missing key does not fail anywhere at runtime. reasonText passes the
// stored English as the default, so the row quietly renders in English, and
// i18next's zh-CN fallback puts Chinese on an English page for a key only
// en-US lacks. This table is what turns either into a failing build.
var geoReasonLocaleKeys = map[GeoReasonCode][]string{
	GeoWhyDisabled:        {"reason_disabled"},
	GeoWhyExempt:          {"reason_exempt"},
	GeoWhyIdleStale:       {"reason_idle_stale"},
	GeoWhyIdleNone:        {"reason_idle_none"},
	GeoWhyUnknownExcluded: {"reason_unknown_excluded"},
	GeoWhyUnknownGeoOff:   {"reason_unknown_geo_off"},
	GeoWhyUnknownLowRatio: {"reason_unknown_low_ratio"},
	GeoWhySuspect: {
		"reason_over_country", "reason_over_region", "reason_over_city", "reason_suspect_suffix",
	},
	GeoWhyFlaggedSustained: {
		"reason_over_country", "reason_over_region", "reason_over_city", "reason_flagged_suffix",
	},
	// The tier clause names the tier with the chip labels the Geo tab
	// already ships, so those are part of this code's sentence too.
	GeoWhyFlaggedClearing: {
		"reason_flagged_clearing", "reason_flagged_clearing_tier",
		"tier_country", "tier_region", "tier_city",
	},
	GeoWhyCleanUnplaced: {"reason_clean_unplaced"},
	GeoWhyCleanWithin: {
		"reason_clean_within", "reason_scope_country", "reason_scope_region", "reason_scope_city",
	},
}

// TestGeoReasonCodesHaveLocaleKeys holds the SPA's two shipped languages to
// the codes EvaluateGeo writes. A code added here without its strings would
// render in English forever on every page; a table entry for a code that no
// longer exists is a translation nobody reads.
func TestGeoReasonCodesHaveLocaleKeys(t *testing.T) {
	codes := map[GeoReasonCode]bool{}
	for _, c := range AllGeoReasonCodes() {
		codes[c] = true
		if len(geoReasonLocaleKeys[c]) == 0 {
			t.Errorf("code %q has no locale keys in this table; add the keys reasonText reads for it", c)
		}
	}
	for c := range geoReasonLocaleKeys {
		if !codes[c] {
			t.Errorf("table lists %q, which AllGeoReasonCodes does not produce", c)
		}
	}

	for _, lang := range []string{"zh-CN", "en-US"} {
		geo := readGeoAnomalyLocale(t, lang)
		for _, c := range AllGeoReasonCodes() {
			for _, k := range geoReasonLocaleKeys[c] {
				s, ok := geo[k].(string)
				if !ok || s == "" {
					t.Errorf("%s admin.json: geo_anomalies.%s (for code %q) is missing or not a non-empty string", lang, k, c)
				}
			}
		}
	}
}

// readGeoAnomalyLocale returns the nested geo_anomalies object of one
// shipped language's admin bundle. The bundles are nested JSON that the SPA
// flattens at load time, so the lookup here walks one level, as the SPA's
// "geo_anomalies.<key>" does.
func readGeoAnomalyLocale(t *testing.T, lang string) map[string]any {
	t.Helper()
	geo := readAdminLocaleObject(t, lang, "geo_anomalies")
	if geo == nil {
		t.Fatalf("%s admin.json has no geo_anomalies object", lang)
	}
	return geo
}

// readAdminLocaleObject returns one top-level object of a shipped language's
// admin bundle, or nil when the bundle has no such object — the caller says
// what that means. A bundle that cannot be read or parsed fails the test
// outright: every guard built on it would otherwise pass vacuously.
func readAdminLocaleObject(t *testing.T, lang, name string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "web-react", "src", "locales", lang, "admin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (this guard must be able to read the SPA's locale bundle)", path, err)
	}
	var bundle map[string]any
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	obj, _ := bundle[name].(map[string]any)
	return obj
}
