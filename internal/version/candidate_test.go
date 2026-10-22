package version

import "testing"

func TestCandidateAllocation(t *testing.T) {
	for _, tc := range []struct {
		stage, target string
		tags          []string
		want          string
	}{
		{"beta", "", nil, "4.1.0-beta.1"},
		{"beta", "", []string{"v4.1.0-beta.2", "v4.1.0-beta.10"}, "4.1.0-beta.11"},
		{"rc", "", []string{"v4.1.0-beta.10"}, "4.1.0-rc.1"},
		{"rc", "", []string{"v4.1.0-rc.9", "v4.1.0-rc.10"}, "4.1.0-rc.11"},
		{"beta", "", []string{"v4.1.0"}, "4.1.1-beta.1"},
		{"beta", "4.1.3", []string{"v4.1.0"}, "4.1.3-beta.1"},
	} {
		line, _ := ParseReleaseLine("4.1")
		got, err := AllocateCandidate(line, tc.stage, tc.target, tc.tags)
		if err != nil || got != tc.want {
			t.Errorf("%+v: got %q, %v", tc, got, err)
		}
	}
	line, _ := ParseReleaseLine("4.0")
	if got, err := AllocateCandidate(line, "beta", "", []string{"release/4.0.1.2", "v4.0.1.22"}); err != nil || got != "4.0.2-beta.1" {
		t.Fatalf("legacy transition: %s %v", got, err)
	}
}

func TestCandidateAllocationRefusesRegressionAndInvalidTargets(t *testing.T) {
	line, _ := ParseReleaseLine("4.1")
	for _, tc := range []struct {
		stage, target string
		tags          []string
	}{
		{"stable", "", nil}, {"beta", "4.1.0.1", nil}, {"rc", "4.2.0", nil},
		{"beta", "", []string{"v4.1.0-rc.1"}}, {"beta", "4.1.0", []string{"v4.1.0"}},
		{"beta", "4.1.0", []string{"v4.1.1"}}, {"beta", "", []string{"v4.1.0-beta.2147483647"}},
	} {
		if got, err := AllocateCandidate(line, tc.stage, tc.target, tc.tags); err == nil || got != "" {
			t.Errorf("accepted %+v: %s %v", tc, got, err)
		}
	}
}
