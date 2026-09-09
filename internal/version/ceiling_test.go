package version

import (
	"os"
	"path/filepath"
	"testing"
)

// The whole point of this file is one rule: "cannot tell" must never come back
// as "current". Every branch that can fail to produce a comparison is asserted
// individually, because a watcher that answers "current" when it is actually
// broken is worse than no watcher — it converts a missing signal into a
// positive assurance.
func TestCompareCeilingNeverCallsAnUnanswerableQuestionCurrent(t *testing.T) {
	cases := []struct {
		name            string
		ceiling, latest string
	}{
		{"no ceiling published", "", "3.8.0"},
		{"upstream unreadable", "3.7.0", ""},
		{"both missing", "", ""},
		{"ceiling is not a version", "latest", "3.8.0"},
		{"upstream tag is not a version", "3.7.0", "nightly"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareCeiling("3X-UI", tc.ceiling, tc.latest)
			if got.Verdict != CeilingUnknown {
				t.Fatalf("ceiling=%q latest=%q: verdict %q, want %q — an unanswerable comparison must not read as a clean bill of health",
					tc.ceiling, tc.latest, got.Verdict, CeilingUnknown)
			}
			if got.Reason == "" {
				t.Error("unknown without a reason is not actionable")
			}
		})
	}
}

// A date-shaped tag ("2026-09-09") is NOT unknown: parseSemver drops the "-"
// suffix and reads "2026" as a major, so it compares as an enormous version and
// the verdict is "behind". That is deliberately left alone. The invariant this
// watcher owes is "never falsely current", and an upstream that switched to
// date tags would raise an alarm rather than go quiet — the safe direction. A
// special case for it would be guessing at a scheme neither upstream uses.
func TestCompareCeilingReadsADateTagAsBehindNotCurrent(t *testing.T) {
	got := CompareCeiling("3X-UI", "3.7.0", "2026-09-09")
	if got.Verdict == CeilingCurrent {
		t.Fatalf("a date-shaped upstream tag must never read as current, got %+v", got)
	}
	if got.Verdict != CeilingBehind {
		t.Logf("date tag verdict is %q (%s) — acceptable as long as it is not current", got.Verdict, got.Reason)
	}
}

func TestCompareCeilingVerdicts(t *testing.T) {
	cases := []struct {
		name            string
		ceiling, latest string
		want            CeilingVerdict
	}{
		{"upstream ahead by a patch", "3.7.0", "3.7.1", CeilingBehind},
		{"upstream ahead by a minor", "3.7.0", "3.8.0", CeilingBehind},
		{"upstream ahead by a major", "3.7.0", "4.0.0", CeilingBehind},
		{"exactly at the ceiling", "3.7.0", "3.7.0", CeilingCurrent},
		{"tolerates a v prefix on either side", "v3.7.0", "3.7.0", CeilingCurrent},
		{"v prefix on the upstream tag", "3.7.0", "v3.7.0", CeilingCurrent},
		{"ceiling ahead of upstream (a ceiling verified pre-release)", "3.8.0", "3.7.0", CeilingCurrent},
		{"a prerelease of the ceiling is not ahead of it", "3.7.0", "3.7.0-rc1", CeilingCurrent},
		{"two-component upstream tag", "1.5.5", "1.6", CeilingBehind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompareCeiling("x", tc.ceiling, tc.latest); got.Verdict != tc.want {
				t.Fatalf("ceiling=%q latest=%q: verdict %q, want %q (%s)", tc.ceiling, tc.latest, got.Verdict, tc.want, got.Reason)
			}
		})
	}
}

// The report is printed as a whole table, so every row must carry its inputs
// back out regardless of verdict — a "behind" row that dropped the numbers
// would force the reader back to the JSON to learn what is behind what.
func TestCompareCeilingEchoesItsInputs(t *testing.T) {
	got := CompareCeiling("S-UI", "1.5.5", "1.6.0")
	if got.Upstream != "S-UI" || got.Ceiling != "1.5.5" || got.Latest != "1.6.0" {
		t.Fatalf("report dropped an input: %+v", got)
	}
	if got.Reason == "" {
		t.Error("every verdict needs a reason, including the actionable ones")
	}
}

// The selection rule has to match the one TestMinXUIConstMatchesCompatJSON
// uses (largest psp_max), or the watcher would police a historical entry's
// ceiling and report staleness nobody can ever fix.
func TestCeilingsFromCompatJSONPicksTheNewestEntry(t *testing.T) {
	raw := []byte(`{
	  "entries": [
	    {"psp_min":"v3.9.1","psp_max":"v3.99.99","min_xui":"3.4.2","max_tested_xui":"3.7.0"},
	    {"psp_min":"v3.6.2","psp_max":"v3.8.99","min_xui":"3.2.0","max_tested_xui":"3.2.7"}
	  ],
	  "sui_entries": [
	    {"psp_min":"v3.9.2","psp_max":"v3.99.99","max_tested_sui":"1.5.5"},
	    {"psp_min":"v3.9.0","psp_max":"v3.9.1","max_tested_sui":"1.5.0"}
	  ]
	}`)
	xui, sui, err := CeilingsFromCompatJSON(raw)
	if err != nil {
		t.Fatalf("CeilingsFromCompatJSON: %v", err)
	}
	if xui != "3.7.0" {
		t.Errorf("xui ceiling %q, want 3.7.0 (the newest entry's, not a historical one)", xui)
	}
	if sui != "1.5.5" {
		t.Errorf("sui ceiling %q, want 1.5.5", sui)
	}
}

// A file with no sui_entries is valid and was the shipped state for the whole
// v3.6-v3.9.1 range. It must produce an empty ceiling (-> unknown), not an error
// and not a silently-current row.
func TestCeilingsFromCompatJSONTreatsAbsentSUIEntriesAsUnpublished(t *testing.T) {
	raw := []byte(`{"entries":[{"psp_min":"v3.9.1","psp_max":"v3.99.99","max_tested_xui":"3.7.0"}]}`)
	xui, sui, err := CeilingsFromCompatJSON(raw)
	if err != nil {
		t.Fatalf("a file without sui_entries is valid: %v", err)
	}
	if xui != "3.7.0" {
		t.Errorf("xui ceiling %q, want 3.7.0", xui)
	}
	if sui != "" {
		t.Errorf("sui ceiling %q, want empty", sui)
	}
	if got := CompareCeiling("S-UI", sui, "1.5.5"); got.Verdict != CeilingUnknown {
		t.Errorf("an unpublished S-UI ceiling must read as unknown, got %q", got.Verdict)
	}
}

func TestCeilingsFromCompatJSONRejectsUnusableFiles(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"not json", "{"},
		{"no entries", `{"entries":[]}`},
		{"no parseable psp_max", `{"entries":[{"psp_max":"tip","max_tested_xui":"3.7.0"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := CeilingsFromCompatJSON([]byte(tc.raw)); err == nil {
				t.Fatal("want an error — a compat file the watcher cannot read must stop it, not yield empty ceilings that read as unknown-but-fine")
			}
		})
	}
}

// The shipped file must be readable by the watcher that runs against it. This
// is the same class of guard as TestMinXUIConstMatchesCompatJSON: it fails the
// build if an edit to the JSON puts it out of reach of its own automation.
func TestShippedCompatJSONYieldsBothCeilings(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "compat", "v3.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	xui, sui, err := CeilingsFromCompatJSON(raw)
	if err != nil {
		t.Fatalf("the shipped compat file must be readable by cmd/compatwatch: %v", err)
	}
	if xui == "" {
		t.Error("shipped file publishes no 3X-UI ceiling for the newest PSP entry")
	}
	if sui == "" {
		t.Error("shipped file publishes no S-UI ceiling for the newest PSP entry")
	}
	// Both must be versions, or the watcher can only ever answer "unknown".
	if got := CompareCeiling("3X-UI", xui, xui); got.Verdict != CeilingCurrent {
		t.Errorf("shipped 3X-UI ceiling %q is not comparable: %s", xui, got.Reason)
	}
	if got := CompareCeiling("S-UI", sui, sui); got.Verdict != CeilingCurrent {
		t.Errorf("shipped S-UI ceiling %q is not comparable: %s", sui, got.Reason)
	}
}
