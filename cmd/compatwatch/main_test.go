package main

import (
	"errors"
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

// unaccountedReleases is the rule behind "every published release is either
// reviewed or explicitly excluded". It is what makes forgetting impossible
// WITHOUT making the decision automatic: the registry still says which releases
// are offered, and this only refuses to let one go unmentioned.
func TestUnaccountedReleases(t *testing.T) {
	published := []string{"v0.0.1-beta3", "v0.0.1-beta4", "v0.0.1-beta9", "v0.0.1-beta10", "v0.0.1-beta11"}

	// Everything decided: nothing to report.
	if got := unaccountedReleases(published, []string{"v0.0.1-beta3", "v0.0.1-beta4", "v0.0.1-beta9", "v0.0.1-beta10", "v0.0.1-beta11"}); len(got) != 0 {
		t.Fatalf("fully accounted registry reported %v", got)
	}

	// A release nobody has ruled on — the live case this exists for.
	got := unaccountedReleases(published, []string{"v0.0.1-beta3", "v0.0.1-beta4", "v0.0.1-beta9"})
	if len(got) != 2 || got[0] != "v0.0.1-beta10" || got[1] != "v0.0.1-beta11" {
		t.Fatalf("unaccounted = %v, want the two newest", got)
	}

	// A MIDDLE release that was skipped rather than the newest — the case a
	// "is the newest reviewed?" check would miss entirely.
	got = unaccountedReleases(published, []string{"v0.0.1-beta3", "v0.0.1-beta4", "v0.0.1-beta10", "v0.0.1-beta11"})
	if len(got) != 1 || got[0] != "v0.0.1-beta9" {
		t.Fatalf("unaccounted = %v, want the skipped middle release", got)
	}

	// Order is the published order, so the report reads the way releases
	// happened rather than the way a map happened to iterate.
	got = unaccountedReleases([]string{"v0.0.1-beta11", "v0.0.1-beta10"}, nil)
	if len(got) != 2 || got[0] != "v0.0.1-beta11" || got[1] != "v0.0.1-beta10" {
		t.Fatalf("unaccounted = %v, want the published order preserved", got)
	}
}

// The registry row asks the opposite question from the panel rows above it, so
// the thing worth pinning is that both directions still reach the SAME two
// states: a real gap exits 1, and anything unreadable exits 2 rather than
// passing for an all-clear.
func TestNodeRegistryReport(t *testing.T) {
	published := []string{"v0.0.1-beta11", "v0.0.1-beta10", "v0.0.1-beta9", "v0.0.1-beta3"}

	t.Run("an unruled-on release is a gap, naming the release", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "v0.0.1-beta3"}, {Version: "v0.0.1-beta9"}},
		}, published, nil)
		if got.Verdict != version.CeilingBehind {
			t.Fatalf("verdict = %s, want behind", got.Verdict)
		}
		// The ceiling is the newest release that WAS ruled on, so the row reads
		// as the gap: releases exist past the point anyone reviewed.
		if got.Ceiling != "v0.0.1-beta9" || got.Latest != "v0.0.1-beta11" {
			t.Fatalf("ceiling/latest = %s/%s, want the newest reviewed and the newest published", got.Ceiling, got.Latest)
		}
		for _, want := range []string{"v0.0.1-beta10", "v0.0.1-beta11"} {
			if !strings.Contains(got.Reason, want) {
				t.Errorf("reason does not name %s, so the reader cannot act: %q", want, got.Reason)
			}
		}
		if exitCode([]version.CeilingReport{got}) != 1 {
			t.Error("a real gap must exit 1")
		}
	})

	t.Run("a full accounting is current", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{
				{Version: "v0.0.1-beta3"}, {Version: "v0.0.1-beta9"},
				{Version: "v0.0.1-beta10"}, {Version: "v0.0.1-beta11"},
			},
		}, published, nil)
		if got.Verdict != version.CeilingCurrent {
			t.Fatalf("verdict = %s, want current", got.Verdict)
		}
		if exitCode([]version.CeilingReport{got}) != 0 {
			t.Error("a fully accounted registry must exit 0")
		}
	})

	t.Run("an exclusion counts only when it says why", func(t *testing.T) {
		// Decided, and the next reader can see why.
		excluded := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "v0.0.1-beta3"}},
			Excluded: []nodeRegistryExclusion{
				{Version: "v0.0.1-beta9", Reason: "broken installer"},
				{Version: "v0.0.1-beta10", Reason: "superseded by beta11"},
				{Version: "v0.0.1-beta11", Reason: "withdrawn before rollout"},
			},
		}, published, nil)
		if excluded.Verdict != version.CeilingCurrent {
			t.Fatalf("verdict = %s, want current — an exclusion with a reason is a decision", excluded.Verdict)
		}
		// A bare version moves the silence one line down instead of resolving
		// it, so it must not count as having ruled on the release.
		silent := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "v0.0.1-beta3"}},
			Excluded: []nodeRegistryExclusion{
				{Version: "v0.0.1-beta9", Reason: "broken installer"},
				{Version: "v0.0.1-beta10", Reason: "superseded by beta11"},
				{Version: "v0.0.1-beta11"},
			},
		}, published, nil)
		if silent.Verdict != version.CeilingBehind || !strings.Contains(silent.Reason, "v0.0.1-beta11") {
			t.Fatalf("an exclusion with no reason must stay unaccounted: %+v", silent)
		}
	})

	t.Run("unreadable is never an all-clear", func(t *testing.T) {
		failed := nodeRegistryReport(nodeRegistry{}, published, errors.New("HTTP 403"))
		if failed.Verdict != version.CeilingUnknown {
			t.Fatalf("verdict = %s, want unknown", failed.Verdict)
		}
		if exitCode([]version.CeilingReport{failed}) == 0 {
			t.Fatal("a registry that could not be read must not read as all-clear")
		}
		// An empty listing is the shape a silently-wrong endpoint produces, not
		// evidence that there is nothing to review.
		empty := nodeRegistryReport(nodeRegistry{}, nil, nil)
		if empty.Verdict != version.CeilingUnknown || exitCode([]version.CeilingReport{empty}) == 0 {
			t.Fatalf("an empty release listing must not read as all-clear: %+v", empty)
		}
	})
}

// The remedy is what a maintainer acts on, so a gap in our own registry must not
// be answered with the instruction for raising a panel ceiling.
func TestRemedyPointsAtTheRightFile(t *testing.T) {
	var sb strings.Builder
	writeTable(&sb, []version.CeilingReport{
		{Upstream: "3X-UI", Ceiling: "3.7.0", Latest: "3.8.0", Verdict: version.CeilingBehind, Reason: "ahead"},
		{Upstream: nodeRegistryUpstream, Ceiling: "v0.0.1-beta9", Latest: "v0.0.1-beta11", Verdict: version.CeilingBehind, Reason: "unaccounted"},
	})
	out := sb.String()
	if !strings.Contains(out, compatPath) {
		t.Errorf("the panel row must name %s:\n%s", compatPath, out)
	}
	if !strings.Contains(out, nodeRegistryPath) {
		t.Errorf("the registry row must name %s:\n%s", nodeRegistryPath, out)
	}
	// Line-level, not just presence: each remedy must be attributed to its own
	// row, or a reader cannot tell which gap the instruction answers.
	var panelLine, registryLine bool
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "3X-UI ") && strings.Contains(line, compatPath) {
			panelLine = true
		}
		if strings.HasPrefix(line, nodeRegistryUpstream+" ") && strings.Contains(line, nodeRegistryPath) {
			registryLine = true
		}
	}
	if !panelLine || !registryLine {
		t.Errorf("each remedy must sit on its own row (panel=%v registry=%v):\n%s", panelLine, registryLine, out)
	}
}
