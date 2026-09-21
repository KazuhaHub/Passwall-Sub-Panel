package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// The endpoint exists because every refusal in this area looks the same from
// outside: a range that could not be refreshed reads exactly like one that was
// never established, and the diagnosis an operator needs is which one it is.
func TestCompatStatusReportsWhatTheDecisionsRestOn(t *testing.T) {
	previousMin, previousMax := version.ActiveMinXUI(), version.ActiveMaxTestedXUI()
	t.Cleanup(func() {
		version.SetActiveMinXUI(previousMin)
		version.SetActiveMaxTestedXUI(previousMax)
	})

	version.SetActiveMinXUI("3.4.2")
	version.SetActiveMaxTestedXUI("3.8.5")

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	t.Run("a range that was never fetched", func(t *testing.T) {
		status := buildCompatStatus(time.Time{}, nil)
		if status.XUI.MinVersion != "3.4.2" || status.XUI.MaxTested != "3.8.5" {
			t.Fatalf("xui = %+v", status.XUI)
		}
		// Nothing has been fetched, so there is no refresh time to report — and
		// reporting one would say the range came from somewhere it did not.
		if status.XUI.RefreshedAt != nil {
			t.Fatalf("refreshed_at = %v on a range that was never fetched", status.XUI.RefreshedAt)
		}
	})

	t.Run("a stale range says so, and keeps the range", func(t *testing.T) {
		// The state an operator cannot otherwise distinguish from a fresh fetch:
		// the range works AND the last attempt to update it failed.
		status := buildCompatStatus(now.Add(-6*time.Hour), errors.New("github unreachable"))
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
}
