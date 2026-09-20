package version

import "testing"

// The upgrade gate has two ways to stop an admin: "PSP has not measured this
// release" and "PSP cannot talk to this release at all". The first is a risk the
// admin may accept; the second is not, because the panel is below the floor this
// binary's code was written against. These tests pin the line between them,
// because the handler used to treat every non-supported status as overridable.

func TestCanForceXUIOnlyOverridesMissingEvidence(t *testing.T) {
	for _, tc := range []struct {
		status CompatStatus
		want   bool
	}{
		{CompatSupported, false}, // nothing to force; the gate is already open
		{CompatUntested, true},   // the only thing missing is evidence
		{CompatTooOld, false},    // below the compiled floor: a hard rejection
		{CompatUnknown, false},   // PSP cannot classify it, so it cannot approve it
	} {
		if got := CanForceXUI(tc.status); got != tc.want {
			t.Errorf("CanForceXUI(%s) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestSameXUIReleaseIgnoresTheVPrefix(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		// 3X-UI's own two endpoints disagree on the prefix:
		// /server/status answers "3.2.8", getPanelUpdateInfo answers "v3.2.8".
		// A post-upgrade check comparing raw strings would call success a mismatch.
		{"3.2.8", "v3.2.8", true},
		{"v3.2.8", "3.2.8", true},
		{"3.2.8", "3.2.8", true},
		// A prerelease/build suffix does not change which release it is: the
		// panel is running that version's code either way.
		{"3.2.8-beta.1", "v3.2.8", true},
		// Different releases stay different.
		{"3.2.7", "v3.2.8", false},
		{"3.10.0", "3.9.0", false},
		// An unreadable version is never "the same release": a target PSP could
		// not parse back is not a target it can confirm was reached.
		{"", "v3.2.8", false},
		{"v3.2.8", "", false},
		{"dev", "v3.2.8", false},
		{"v3.2.8", "latest", false},
	} {
		if got := SameXUIRelease(tc.a, tc.b); got != tc.want {
			t.Errorf("SameXUIRelease(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
