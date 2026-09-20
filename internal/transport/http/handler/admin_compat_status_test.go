package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// The endpoint exists because every refusal in this area looks the same from
// outside. A stale range and an expired policy both read as "this upgrade is not
// offered", and the diagnosis an operator needs is which one it is.
func TestCompatStatusReportsWhatTheDecisionsRestOn(t *testing.T) {
	previousMin, previousMax := version.ActiveMinXUI(), version.ActiveMaxTestedXUI()
	previousPolicy, previousEnforcement := version.ActiveReleasesPolicy(), version.PolicyEnforcing()
	t.Cleanup(func() {
		version.SetActiveMinXUI(previousMin)
		version.SetActiveMaxTestedXUI(previousMax)
		version.SetActiveReleasesPolicy(previousPolicy)
		version.SetPolicyEnforcement(previousEnforcement)
	})

	version.SetActiveMinXUI("3.4.2")
	version.SetActiveMaxTestedXUI("3.8.5")
	version.SetActiveReleasesPolicy(nil)
	version.SetPolicyEnforcement(false)

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	t.Run("no policy and no successful fetch", func(t *testing.T) {
		status := buildCompatStatus(time.Time{}, nil, now)
		if status.XUI.MinVersion != "3.4.2" || status.XUI.MaxTested != "3.8.5" {
			t.Fatalf("xui = %+v", status.XUI)
		}
		// Nothing has been fetched, so there is no refresh time to report — and
		// reporting one would say the range came from somewhere it did not.
		if status.XUI.RefreshedAt != nil {
			t.Fatalf("refreshed_at = %v on a range that was never fetched", status.XUI.RefreshedAt)
		}
		if status.Policy.Installed || status.Policy.Applicable || status.Policy.Enforcing {
			t.Fatalf("policy = %+v, want nothing installed", status.Policy)
		}
	})

	t.Run("a stale range says so, and keeps the range", func(t *testing.T) {
		// The state an operator cannot otherwise distinguish from a fresh fetch:
		// the range works AND the last attempt to update it failed.
		status := buildCompatStatus(now.Add(-6*time.Hour), errors.New("github unreachable"), now)
		if status.XUI.MaxTested != "3.8.5" {
			t.Fatalf("a failed refresh must not blank the range: %+v", status.XUI)
		}
		if status.XUI.LastError != "github unreachable" {
			t.Fatalf("last_error = %q", status.XUI.LastError)
		}
		if status.XUI.RefreshedAt == nil || !status.XUI.RefreshedAt.Equal(now.Add(-6*time.Hour)) {
			t.Fatalf("refreshed_at = %v", status.XUI.RefreshedAt)
		}
	})

	t.Run("loaded is not enforcing", func(t *testing.T) {
		// The state that looks identical to "not working" unless it is named.
		expires := now.Add(24 * time.Hour)
		version.SetActiveReleasesPolicy(&version.ReleasesPolicy{
			SchemaVersion: 1, Revision: 7, IssuedAt: now.Add(-time.Hour), ExpiresAt: expires,
		})
		version.SetPolicyEnforcement(false)
		status := buildCompatStatus(time.Time{}, nil, now)
		// Installed, but a test build calls itself "dev", so it is not applicable
		// — and that is exactly the state worth distinguishing.
		if !status.Policy.Installed || status.Policy.Enforcing {
			t.Fatalf("policy = %+v, want installed and not enforcing", status.Policy)
		}
		if status.Policy.Applicable {
			t.Fatal(`a "dev" build is not the build the policy was reviewed for`)
		}
		if status.Policy.Expired {
			t.Fatal("a policy inside its window is not expired")
		}
	})

	t.Run("an expired policy is reported against the request time", func(t *testing.T) {
		// Expiry is computed rather than stored, so a policy that lapses while
		// the panel is running is reported as expired the moment it is asked.
		version.SetActiveReleasesPolicy(&version.ReleasesPolicy{
			SchemaVersion: 1, Revision: 7, IssuedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour),
		})
		status := buildCompatStatus(time.Time{}, nil, now)
		if !status.Policy.Expired {
			t.Fatal("a policy past its window must report as expired")
		}
		if !status.Policy.Installed {
			t.Fatal("expired is not the same as absent; it is still installed")
		}
	})
}
