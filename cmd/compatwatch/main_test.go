package main

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func report(v version.CeilingVerdict) version.CeilingReport {
	return version.CeilingReport{Upstream: "x", Verdict: v, Reason: "r"}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		in   []version.CeilingVerdict
		want int
	}{
		{"all current", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingCurrent}, 0},
		{"one behind", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingBehind}, 1},
		{"one unknown", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingUnknown}, 2},
		{"behind outranks unknown", []version.CeilingVerdict{version.CeilingUnknown, version.CeilingBehind}, 1},
		{"unknown first, still 2", []version.CeilingVerdict{version.CeilingUnknown, version.CeilingCurrent}, 2},
		{"no rows at all", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reports := make([]version.CeilingReport, 0, len(tc.in))
			for _, v := range tc.in {
				reports = append(reports, report(v))
			}
			if got := exitCode(reports); got != tc.want {
				t.Fatalf("exitCode = %d, want %d", got, tc.want)
			}
		})
	}
}

// The single most important property of this job: an all-clear exit is
// reachable ONLY when every row was actually compared. If "unknown" ever
// mapped to 0 the watcher would go green while blind, which is the exact
// failure it was built to prevent.
func TestUnknownIsNeverAnAllClear(t *testing.T) {
	for _, mixed := range [][]version.CeilingVerdict{
		{version.CeilingUnknown},
		{version.CeilingUnknown, version.CeilingCurrent},
		{version.CeilingCurrent, version.CeilingUnknown},
	} {
		reports := make([]version.CeilingReport, 0, len(mixed))
		for _, v := range mixed {
			reports = append(reports, report(v))
		}
		if exitCode(reports) == 0 {
			t.Fatalf("%v exited 0 — a run that could not compare an upstream must not read as all-clear", mixed)
		}
	}
}

// Every row is rendered whatever the verdicts, so one alarm cannot stand in for
// the whole picture: the reader must be able to see that the other upstream was
// checked, and what it said.
func TestRenderKeepsTheDenominatorVisible(t *testing.T) {
	var sb strings.Builder
	writeTable(&sb, []version.CeilingReport{
		{Upstream: "3X-UI", Ceiling: "3.7.0", Latest: "3.8.0", Verdict: version.CeilingBehind, Reason: "ahead"},
		{Upstream: "S-UI", Ceiling: "1.5.5", Latest: "", Verdict: version.CeilingUnknown, Reason: "unreadable"},
	})
	out := sb.String()
	for _, want := range []string{"3X-UI", "S-UI", "3.7.0", "3.8.0", "1.5.5", "behind", "unknown", "ahead", "unreadable"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered table is missing %q:\n%s", want, out)
		}
	}
	// An empty field must render as something, not vanish into an ambiguous
	// blank cell that reads as "same as above".
	if !strings.Contains(out, "—") {
		t.Errorf("a missing value must render visibly:\n%s", out)
	}
}
