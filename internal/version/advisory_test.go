package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCompatJSONAdvisoriesDecode is the guard against a footgun: xui_advisories
// decodes into map[string]XUIAdvisory, so any NON-advisory (string) value under
// that key — e.g. a stray "_doc" — makes the WHOLE compat payload fail to
// unmarshal, silently breaking every panel's compat refresh. This decodes the
// real shipped docs/compat/v3.json and asserts the advisory map is well-formed.
func TestCompatJSONAdvisoriesDecode(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "compat", "v3.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode %s into remoteCompatPayload failed — a non-advisory value "+
			"under xui_advisories (e.g. a stray _doc string) breaks compat refresh for "+
			"every panel: %v", path, err)
	}
	adv, ok := payload.Advisories["3.5.0"]
	if !ok {
		t.Fatalf("expected a 3.5.0 advisory in %s, got keys %v", path, keysOf(payload.Advisories))
	}
	if adv.Text == "" {
		t.Fatalf("3.5.0 advisory has empty text")
	}
	if !adv.AffectsXray {
		t.Errorf("3.5.0 advisory should set affects_xray=true (upgrade bumps xray-core)")
	}
	if adv.Severity != "warning" && adv.Severity != "info" {
		t.Errorf("3.5.0 advisory severity = %q, want info|warning", adv.Severity)
	}
}

func keysOf(m map[string]XUIAdvisory) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLookupXUIAdvisory(t *testing.T) {
	// Raw map as authored: a "v"-prefixed key and a minor-only key both must
	// resolve after canonicalization; a non-semver key is dropped.
	SetActiveAdvisories(canonAdvisories(map[string]XUIAdvisory{
		"v3.5.0":  {Severity: "warning", AffectsXray: true, Text: "xray heads-up"},
		"3.6":     {Severity: "info", Text: "minor-only key"},
		"garbage": {Severity: "info", Text: "dropped"},
	}))
	t.Cleanup(func() { SetActiveAdvisories(nil) })

	if a, ok := LookupXUIAdvisory("3.5.0"); !ok || !a.AffectsXray {
		t.Fatalf(`LookupXUIAdvisory("3.5.0") = %+v, %v; want the xray advisory`, a, ok)
	}
	if a, ok := LookupXUIAdvisory("v3.5.0"); !ok || a.Text != "xray heads-up" {
		t.Fatalf(`LookupXUIAdvisory("v3.5.0") must match the same entry, got %+v, %v`, a, ok)
	}
	if _, ok := LookupXUIAdvisory("3.6.0"); !ok {
		t.Errorf(`minor-only key "3.6" should resolve for "3.6.0"`)
	}
	if _, ok := LookupXUIAdvisory("3.5.1"); ok {
		t.Errorf("advisory must be pinned to 3.5.0 exactly, must NOT leak onto 3.5.1")
	}
	if _, ok := LookupXUIAdvisory("not-a-version"); ok {
		t.Errorf("unparseable version must return no advisory")
	}
}
