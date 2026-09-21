package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
		status := buildCompatStatus(time.Time{}, nil, ports.CoreCatalogStatus{})
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
		status := buildCompatStatus(now.Add(-6*time.Hour), errors.New("github unreachable"), ports.CoreCatalogStatus{})
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

// THE CORE CATALOG IS REPORTED BESIDE THE RANGES, because it is the same kind of
// fact: something read from outside the panel, that can go stale, and that decides
// what the panel will offer.
//
// WHAT AN OPERATOR HAS TO BE ABLE TO TELL APART: a review read from the origin a
// minute ago, and one the panel is serving because it cannot reach anything. Those
// look identical from the selector, and the second one is the state in which a
// release that has since been withdrawn is still on offer.
func TestCompatStatusReportsTheCoreCatalogAndWhetherItIsFallingBack(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	read := now.Add(-4 * time.Minute)
	review := now.Add(-36 * time.Hour)

	t.Run("read from the origin", func(t *testing.T) {
		status := buildCompatStatus(time.Time{}, nil, ports.CoreCatalogStatus{
			Source: "read from the published release", ReviewTime: &review, LastSuccess: &read,
		})
		if status.CoreCatalog.Source == "" || status.CoreCatalog.FallingBack {
			t.Fatalf("core_catalog = %+v", status.CoreCatalog)
		}
		if status.CoreCatalog.ReviewTime == nil || !status.CoreCatalog.ReviewTime.Equal(review) {
			t.Fatalf("the age of the review is not reported: %+v", status.CoreCatalog.ReviewTime)
		}
		if status.CoreCatalog.LastSuccess == nil || !status.CoreCatalog.LastSuccess.Equal(read) {
			t.Fatalf("the last successful read is not reported: %+v", status.CoreCatalog.LastSuccess)
		}
	})

	t.Run("served from the newest review the panel has", func(t *testing.T) {
		status := buildCompatStatus(time.Time{}, nil, ports.CoreCatalogStatus{
			Source:     "shipped with this panel build; regenerated when this build is cut",
			ReviewTime: &review, LastError: "github unreachable", FallingBack: true,
		})
		if !status.CoreCatalog.FallingBack || status.CoreCatalog.LastError == "" {
			t.Fatalf("a fallback is not reported as one: %+v", status.CoreCatalog)
		}
		// AND IT STILL SAYS HOW OLD THE REVIEW IS. The fallback is not the failure
		// an operator is looking for; the AGE is.
		if status.CoreCatalog.ReviewTime == nil {
			t.Fatal("a fallback hides the age of the review it is serving")
		}
		// A process that has never read a document must not claim it has.
		if status.CoreCatalog.LastSuccess != nil {
			t.Fatalf("last_success = %v on a process that has read nothing", status.CoreCatalog.LastSuccess)
		}
	})

	// A reader that cannot answer at all is reported as such rather than as healthy.
	t.Run("no reader to ask", func(t *testing.T) {
		h := &AdminServersHandler{}
		if got := h.coreCatalogStatus(); got.Source != "unavailable" {
			t.Fatalf("a panel with no catalog reader reports %+v", got)
		}
	})
}
