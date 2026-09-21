package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE DOCUMENT PER PRODUCT, AND EACH ONE JUDGED ON ITS OWN.
//
// The ranges used to arrive in a single document carrying both panels, so a
// build had one revision, one applicability window and one install. Splitting it
// is what makes a review of one panel stop rewriting the other's ceiling — and
// it is also what makes the three guards below necessary, because every piece of
// state that used to be singular now has to say WHICH document it belongs to.
//
// THE SHARED-STATE ONES ARE THE DANGEROUS HALF. Each of them fails silently
// rather than loudly: the panel keeps working, reports a ceiling, and the ceiling
// is stale or missing for a reason nobody can trace back to a fetch.

func xuiRangesJSON(issued string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "product": "3x-ui", "revision": 3,
	  "issued_at": %q, "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.8.5", "notes": "reviewed"}],
	  "xui_advisories": {"3.8.0": {"severity": "warning", "affects_xray": true, "text": "reviewed"}}
	}`, issued))
}

func suiRangesJSON(issued string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "product": "sui", "revision": 3,
	  "issued_at": %q, "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "sui_entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "max_tested_sui": "1.6.3", "notes": "reviewed"}],
	  "sui_advisories": {"1.6.0": {"severity": "warning", "affects_xray": true, "text": "reviewed"}}
	}`, issued))
}

// THE REVISION GUARD IS PER DOCUMENT, AND THIS IS THE BUG IT EXISTS FOR.
//
// The two panels are reviewed at different times, so their documents carry
// different dates and arrive in different orders. With one revision for the
// process, the later S-UI document advances the value a 3X-UI document is then
// compared against — so an in-order, legitimate 3X-UI document is refused as a
// regression, and the 3X-UI ceiling silently stops moving. Nothing reports it:
// the panel keeps serving the previous range and looks healthy.
func TestTheRevisionOfOneProductDoesNotJudgeAnother(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	resetAppliedRevisions()

	if err := applyXUICompatDocument(xuiRangesJSON("2026-09-20T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatalf("the first 3X-UI document was refused: %v", err)
	}
	if err := applySUICompatDocument(suiRangesJSON("2026-09-25T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatalf("the S-UI document was refused: %v", err)
	}
	// NEWER THAN WHAT 3X-UI HAS APPLIED, OLDER THAN WHAT S-UI HAS. Both are true
	// at once, and only a per-document revision can say so.
	if err := applyXUICompatDocument(xuiRangesJSON("2026-09-22T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatalf("a 3X-UI document newer than the applied 3X-UI one was refused because another product's date was compared against it: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.8.5" {
		t.Fatalf("3X-UI ceiling = %q, want 3.8.5", got)
	}
}

// And the guard still refuses a genuine regression WITHIN one product.
//
// A per-document revision is a narrower guard, not a weaker one: the case it was
// written for — a stale CDN edge, a reverted commit — is still refused, and the
// refusal has to name the date so an operator can see which document lost.
func TestAnOlderDocumentStillLosesToANewerOneOfTheSameProduct(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	resetAppliedRevisions()

	if err := applyXUICompatDocument(xuiRangesJSON("2026-09-22T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatal(err)
	}
	err := applyXUICompatDocument(xuiRangesJSON("2026-09-20T00:00:00Z"), panelRangesApplyNow)
	if err == nil {
		t.Fatal("an older 3X-UI document replaced a newer one")
	}
	if !strings.Contains(err.Error(), "2026-09-20") {
		t.Errorf("the refusal does not name the date that lost: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.8.5" {
		t.Fatalf("the refused document changed the ceiling: %q", got)
	}
}

// A DOCUMENT HAS TO SAY WHICH PRODUCT IT IS, and the answer has to match the
// address it came from.
//
// Without this, a `sui-v4.json` served at the 3X-UI address is a valid document
// that installs no 3X-UI entries — which reads as "the range was reviewed and
// removed" rather than "the wrong file is published". The ceiling would be left
// at whatever it was, with nothing recording why it stopped moving.
func TestAProductDocumentMustBeTheProductItsAddressNames(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	resetAppliedRevisions()

	if err := applyXUICompatDocument(xuiRangesJSON("2026-09-20T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatal(err)
	}
	before := ActiveMaxTestedXUI()

	err := applyXUICompatDocument(suiRangesJSON("2026-09-20T00:00:00Z"), panelRangesApplyNow)
	if err == nil {
		t.Fatal("the 3X-UI entry point installed an S-UI document")
	}
	if !strings.Contains(err.Error(), "sui") {
		t.Errorf("the refusal does not say which product the document claims: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != before {
		t.Fatalf("the 3X-UI ceiling moved to %q on a document that was refused", got)
	}
}

// A product builds its own document names from its own major, so `4.0.0` asks
// for the v4 documents and no name has to be looked up.
func TestAProductBuildDerivesOneDocumentNamePerProduct(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })

	Version = "4.0.0"
	sources, err := compatDocumentSources()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, source := range sources {
		got[source.Product] = source.Name
	}
	if len(sources) != 2 {
		t.Fatalf("a product build reads %d documents, want one per product: %v", len(sources), sources)
	}
	if got[productXUI] != "3x-ui-v4.json" || got[productSUI] != "sui-v4.json" {
		t.Fatalf("document names = %v, want 3x-ui-v4.json and sui-v4.json", got)
	}
	for _, source := range sources {
		if !strings.HasSuffix(source.URL, "/docs/compat/"+source.Name) {
			t.Errorf("URL %q does not address %q", source.URL, source.Name)
		}
	}

	// A LEGACY BUILD STILL READS ITS OWN PER-MAJOR FILE. That path is frozen, not
	// removed: the v3 line's builds fetch v3.json by a name their own version
	// derives, and nothing about this split is allowed to reach them.
	Version = "v4.0.0-beta.25"
	sources, err = compatDocumentSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].URL != defaultRemoteCompatURLBase+"v4.json" {
		t.Fatalf("a legacy build reads %v, want the single per-major manifest", sources)
	}
}

// THE SNAPSHOT HAS TO CARRY EVERY PRODUCT, AND REPLAY THEM THROUGH THE SAME
// PER-PRODUCT APPLY THE FETCH USED.
//
// A boot that installs a cached document by a looser rule than the fetch that
// stored it is how an instance comes back believing a range its own version is
// not covered by. Two products in one container is also why it stays ONE file:
// a single atomic replace is what keeps the two replays from disagreeing, and
// two files have no cross-file atomicity.
func TestTheSnapshotReplaysEveryProductItHolds(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	resetAppliedRevisions()

	if err := applyXUICompatDocument(xuiRangesJSON("2026-09-20T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatal(err)
	}
	if err := applySUICompatDocument(suiRangesJSON("2026-09-25T00:00:00Z"), panelRangesApplyNow); err != nil {
		t.Fatal(err)
	}
	SetActiveMaxTestedXUI("")
	SetActiveMaxTestedSUI("")
	resetAppliedRevisions()

	if err := LoadPolicySnapshot(); err != nil {
		t.Fatalf("replaying the snapshot failed: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.8.5" {
		t.Errorf("3X-UI ceiling after replay = %q, want 3.8.5", got)
	}
	if got := ActiveMaxTestedSUI(); got != "1.6.3" {
		t.Errorf("S-UI ceiling after replay = %q, want 1.6.3", got)
	}
	// The container is one file, and it has to hold both.
	raw, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot policySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Documents) != 2 {
		t.Fatalf("the snapshot holds %d documents, want one per product: %s", len(snapshot.Documents), raw)
	}
}

// A SNAPSHOT WRITTEN BEFORE THE SPLIT IS REFUSED, NOT PARTIALLY READ.
//
// Its one payload carries both products and no product name, so reading it as a
// product document would either install the wrong subset or be dropped half-way.
// Refusing it costs the next boot one fetch, which is the state a fresh install
// starts in; tolerating it silently installs a document this build cannot
// attribute to a product, and the ceiling that comes back is one nobody can
// explain.
func TestASnapshotFromBeforeTheSplitIsRefused(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	resetAppliedRevisions()

	// The OLD shape, written by hand: one payload, no documents map.
	old := []byte(`{"snapshot_schema": 1, "policy_schema": 2, "revision": "2026-09-19T00:00:00Z",
	  "source": "per-major manifest v4", "fetched_at": "2026-09-19T00:00:00Z", "digest": "",
	  "payload": {"schema_version": 2, "major": 4, "updated_at": "2026-09-19T00:00:00Z",
	    "entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.8.5"}]}}`)
	if err := os.WriteFile(filepath.Join(dir, policySnapshotFile), old, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadPolicySnapshot(); err == nil {
		t.Fatal("a snapshot in the previous format was accepted; reading it would install a document this build cannot attribute to a product")
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("the previous format's payload installed a ceiling: %q", got)
	}
}
