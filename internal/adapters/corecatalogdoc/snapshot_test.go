package corecatalogdoc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// snapshotAt builds a document with a given review date, so a case can say which of
// two reviews is the newer one.
func snapshotAt(t *testing.T, review string, versions ...string) string {
	t.Helper()
	when, err := time.Parse(time.RFC3339, review)
	if err != nil {
		t.Fatal(err)
	}
	document := ports.CoreCatalogDocument{SchemaVersion: 1, UpdatedAt: when}
	for _, version := range versions {
		document.Releases = append(document.Releases, ports.CoreRelease{
			Engine: "xray", Version: version, Tier: "recommended", Selectable: true,
			PublishedAt: when,
		})
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A PANEL THAT HAS NEVER REACHED THE ORIGIN STILL HAS A REVIEW.
//
// This is the case the shipped snapshot exists for, and without it a fresh install
// with no network has nothing: an empty selector, a refused conversion, and a
// node-sync path that cannot check a selection — a panel that looks broken for want
// of data that was known when it was built.
func TestAColdStartWithNoOriginServesTheReviewTheBuildShippedWith(t *testing.T) {
	f := newFixture(t, documentFixture)
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	catalog := f.catalog(t, currentReleases())

	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatalf("a cold start with no origin had nothing to serve: %v", err)
	}
	if len(document.Releases) == 0 {
		t.Fatal("the served review carries no releases")
	}
	status := catalog.Status()
	if status.Source != shippedOrigin {
		t.Fatalf("the panel does not say it is serving the review it shipped with: %q", status.Source)
	}
	if !status.FallingBack {
		t.Fatal("a panel serving a fallback does not say so")
	}
	if status.ReviewTime == nil {
		t.Fatal("the age of the review in force is not reported")
	}
}

// THE NEWER REVIEW WINS, WHICHEVER DIRECTION IT CAME FROM.
//
// A panel that restarted after reading a newer review must not fall back to the one
// it was built with: the two can disagree in the direction that matters, because a
// release the newer review has withdrawn is still on the older one. Serving the
// older one would re-open a decision somebody made deliberately.
func TestTheNewerReviewWinsAcrossAWriteAndARestart(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "core-catalog.json")

	// The origin publishes a review NEWER than the shipped one; the panel reads it.
	f := newFixture(t, snapshotAt(t, "2026-09-19T00:00:00Z", "26.9.9"))
	withSnapshot := func() *Catalog {
		catalog, err := New(Options{
			Releases: currentReleases(), HTTPClient: f.server.Client(),
			BaseURL: f.server.URL + "/download/", PublicKey: f.key,
			Now: func() time.Time { return f.now }, SnapshotPath: path,
		})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	read, err := withSnapshot().Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Releases) != 1 || read.Releases[0].Version != "26.9.9" {
		t.Fatalf("the origin's review was not served: %+v", read.Releases)
	}
	// WRITTEN ATOMICALLY, with nothing left behind for the next reader to trip over.
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 || entries[0].Name() != "core-catalog.json" {
		t.Fatalf("the snapshot directory holds %v (err %v), want only the snapshot", entries, err)
	}

	// A RESTART WITH NO ORIGIN still serves that review, not the one shipped with
	// the build.
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	f.now = f.now.Add(24 * time.Hour)
	restart := withSnapshot()
	restarted, err := restart.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Releases) != 1 || restarted.Releases[0].Version != "26.9.9" {
		t.Fatalf("a restart fell back to an older review: %+v", restarted.Releases)
	}
	// ONE INSTANCE ANSWERS BOTH, which is the point: the source is a property of
	// what THIS process served, not of what is on disk.
	if status := restart.Status(); status.Source != "the last review this panel read" {
		t.Fatalf("the source does not name where the review came from: %q", status.Source)
	}
}

// AND THE SHIPPED REVIEW DOES NOT DISPLACE A NEWER ONE THAT IS ALREADY ON DISK. The
// shipped review is the last resort, not a reset: a build whose embedded review is
// newer than the panel's own would otherwise re-open a withdrawal the panel had
// already learned about.
func TestAShippedReviewOlderThanTheSnapshotIsNotServed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "core-catalog.json")
	if err := os.WriteFile(path, []byte(snapshotAt(t, "2026-09-19T00:00:00Z", "26.9.9")), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, documentFixture)
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	catalog := f.catalog(t, currentReleases())
	catalog.snapshotPath = path

	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Releases) != 1 || document.Releases[0].Version != "26.9.9" {
		t.Fatalf("the panel served the review it was built with over a newer one: %+v", document.Releases)
	}
}

// A SNAPSHOT THAT CANNOT BE WRITTEN IS A DEGRADATION, NOT A FAILED READ.
//
// The review is already in force and already being served; all that is lost is that
// the next start has to read it again, which is where a panel without a snapshot
// starts anyway. A read-only data directory must not turn a successful refresh into
// an error — and a directory that cannot be created is how that failure looks.
func TestAnUnwritableSnapshotDirectoryDoesNotFailTheRead(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())
	// A FILE where the directory should be: MkdirAll cannot succeed, so the write
	// fails the way a read-only data directory does, and it fails for root too.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog.snapshotPath = filepath.Join(blocker, "data", "core-catalog.json")

	document, err := catalog.Document(context.Background())
	if err != nil || len(document.Releases) == 0 {
		t.Fatalf("an unwritable snapshot directory failed the read: (%d releases, %v)", len(document.Releases), err)
	}
	if status := catalog.Status(); status.FallingBack || status.LastSuccess == nil {
		t.Fatalf("a successful read is reported as a fallback: %+v", status)
	}
}

// A SNAPSHOT THIS BUILD CANNOT USE IS DISCARDED RATHER THAN SERVED. It is a cache of
// something the panel can read again, so the honest answer is to ignore it — but it
// must not be trusted just because it is on disk.
func TestAnUnusableSnapshotIsIgnored(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not json", "{ not a document"},
		{"a schema this build does not read", `{"schema_version": 9, "updated_at": "2026-09-19T00:00:00Z", "releases": [{"engine":"xray","version":"26.9.9"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "core-catalog.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			f := newFixture(t, documentFixture)
			f.mu.Lock()
			f.fail = true
			f.mu.Unlock()
			catalog := f.catalog(t, currentReleases())
			catalog.snapshotPath = path

			document, err := catalog.Document(context.Background())
			if err != nil {
				t.Fatalf("an unusable snapshot was treated as a reason to refuse: %v", err)
			}
			if status := catalog.Status(); status.Source != shippedOrigin {
				t.Fatalf("the unusable snapshot was served: %+v", status)
			}
			_ = document
		})
	}
}

// THE STATUS SAYS WHAT IS BEING SERVED AND WHY, because a panel quietly serving an
// old review looks exactly like one serving a current one. It reports staleness and
// nothing more: this panel cannot know whether a review is current, only when it
// read it and what came back.
func TestTheStatusReportsTheReviewInForce(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())

	// Before anything is read, there is no success to report.
	if status := catalog.Status(); status.LastSuccess != nil {
		t.Fatalf("a reader that has read nothing reports a success: %+v", status)
	}
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := catalog.Status()
	if status.FallingBack || status.LastError != "" {
		t.Fatalf("a successful read reports a fallback: %+v", status)
	}
	if status.LastSuccess == nil || !status.LastSuccess.Equal(f.now) {
		t.Fatalf("the last successful read is not reported: %+v", status.LastSuccess)
	}
	if status.ReviewTime == nil || !status.ReviewTime.Equal(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("the review's own date is not reported: %+v", status.ReviewTime)
	}

	// And an unusable document is reported with its reason, not as a fallback.
	f.mu.Lock()
	f.publish(strings.Replace(documentFixture, `"schema_version": 1`, `"schema_version": 2`, 1))
	f.mu.Unlock()
	f.now = f.now.Add(2 * cacheTTL)
	if _, err := catalog.Document(context.Background()); !errors.Is(err, ErrDocumentUnusable) {
		t.Fatalf("an unusable document was served: %v", err)
	}
	if status := catalog.Status(); status.LastError == "" || status.FallingBack {
		t.Fatalf("a refusal is not reported with its reason: %+v", status)
	}
}

// THE REVIEW THIS BUILD SHIPS IS THE ONE THE PROJECT PUBLISHES.
//
// The snapshot is data copied from the publisher's own output, so a copy that is
// allowed to drift is two reviews — and the direction that matters is the silent
// one: a panel offering a release the project has pulled, or refusing one it now
// recommends, with nothing in this repository to say so.
//
// IT READS THE NODE CHECKOUT THE PINNED-SOURCE CONTRACT JOB PROVIDES, rather than
// resolving a module, because this repository no longer depends on that module — and
// because a guard that disappears along with a dependency is not a guard.
//
// IT COMPARES THE DOCUMENTS, NOT THEIR BYTES. The publisher's file is its own input
// and this one is its own output; what has to agree is what the two say, and a
// byte comparison would be a test of formatting.
func TestTheShippedReviewIsTheOneTheProjectPublishes(t *testing.T) {
	checkout := os.Getenv("PSP_LIVE_NODE_REPO")
	if checkout == "" {
		t.Skip("PSP_LIVE_NODE_REPO is unset, so there is no published catalog to compare against")
	}
	body, err := os.ReadFile(filepath.Join(checkout, "corecatalog", "catalog.json"))
	if err != nil {
		t.Skipf("the checked-out Node revision has no corecatalog/catalog.json: %v", err)
	}
	var published ports.CoreCatalogDocument
	if err := json.Unmarshal(body, &published); err != nil {
		t.Fatalf("the published catalog does not have the shape this reader declares: %v", err)
	}
	shipped, err := snapshotDocument()
	if err != nil {
		t.Fatal(err)
	}

	canonical := func(document ports.CoreCatalogDocument) string {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	if canonical(shipped) != canonical(published) {
		t.Fatalf("the review shipped with this build is not the one the project publishes.\n"+
			"Regenerate internal/adapters/corecatalogdoc/snapshot.json with the publisher's own\n"+
			"`go run ./deployment/cmd/publish-core-catalog -output …` and commit the result.\n"+
			"shipped:   %s\npublished: %s", canonical(shipped), canonical(published))
	}
}
