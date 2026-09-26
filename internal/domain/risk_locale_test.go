package domain

import (
	"slices"
	"testing"
)

// TestRiskCodesHaveLocaleKeys holds the SPA's two shipped languages to the
// codes the risk evaluators write, the way TestGeoReasonCodesHaveLocaleKeys
// does for the concurrent-location reasons.
//
// The admin tab renders a signal's code through
// admin:risk_signals.code.<kind>.<code> with the code itself as the default,
// so a missing string fails nowhere at runtime: the cell's tooltip just reads
// "spread_building", and i18next's zh-CN fallback puts Chinese on an English
// page for a key only en-US lacks. This is what turns either into a failing
// build.
//
// It also rejects the reverse — a string for a kind or code no evaluator
// produces. The SPA's own copy of the code table is checked against these
// same bundle keys (utils/riskSignals.test.ts), so the two tests together
// hold that copy to AllRiskCodes() without either side importing the other.
func TestRiskCodesHaveLocaleKeys(t *testing.T) {
	all := AllRiskCodes()
	for _, lang := range []string{"zh-CN", "en-US"} {
		risk := readAdminLocaleObject(t, lang, "risk_signals")
		if risk == nil {
			t.Errorf("%s admin.json has no risk_signals object", lang)
			continue
		}

		// The column header of each kind: a kind without one is a column
		// titled with its raw key.
		kinds, _ := risk["kind"].(map[string]any)
		for _, k := range RiskKinds() {
			if s, ok := kinds[string(k)].(string); !ok || s == "" {
				t.Errorf("%s admin.json: risk_signals.kind.%s is missing or not a non-empty string", lang, k)
			}
		}

		codes, ok := risk["code"].(map[string]any)
		if !ok {
			t.Errorf("%s admin.json has no risk_signals.code object", lang)
			continue
		}
		for kind, list := range all {
			byKind, _ := codes[string(kind)].(map[string]any)
			for _, c := range list {
				if s, ok := byKind[string(c)].(string); !ok || s == "" {
					t.Errorf("%s admin.json: risk_signals.code.%s.%s is missing or not a non-empty string", lang, kind, c)
				}
			}
		}
		for kind, v := range codes {
			list, known := all[RiskKind(kind)]
			if !known {
				t.Errorf("%s admin.json: risk_signals.code.%s names a kind RiskKinds does not have", lang, kind)
				continue
			}
			byKind, _ := v.(map[string]any)
			for c := range byKind {
				if !slices.Contains(list, RiskCode(c)) {
					t.Errorf("%s admin.json: risk_signals.code.%s.%s is a code AllRiskCodes()[%s] does not produce", lang, kind, c, kind)
				}
			}
		}
	}
}
