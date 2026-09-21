package corecatalogdoc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/releaseasset"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE TRUST IS THE SIGNATURE, AND THE DIGEST ALONE IS NOT. The manifest and the
// document arrive from the same host over the same channel, so anything that
// could replace one could replace the other — which is why several cases below
// tamper with a document and expect a refusal rather than a different answer.
//
// Signed with a test key here, because the production private key is a release
// secret; the reader takes the verification key as a seam for exactly this reason.

const documentFixture = `{
  "schema_version": 1,
  "updated_at": "2026-09-11T00:00:00Z",
  "releases": [
    {
      "engine": "xray",
      "version": "26.6.27",
      "tier": "recommended",
      "prerelease": true,
      "selectable": true,
      "requires_confirmation": false,
      "published_at": "2026-06-27T13:20:31Z",
      "source_url": "https://github.com/XTLS/Xray-core/releases/tag/v26.6.27",
      "summary": {"en": "baseline", "zh_cn": "基线"},
      "reality": {"xray": "supported", "mihomo": "supported", "sing_box": "supported", "uri_list": "supported"},
      "evidence": {"source_audited": true, "config_tested": true, "handshake_tested": true},
      "assets": []
    },
    {
      "engine": "xray",
      "version": "26.8.1",
      "tier": "restricted",
      "prerelease": false,
      "selectable": true,
      "requires_confirmation": true,
      "published_at": "2026-08-01T00:00:00Z",
      "source_url": "https://github.com/XTLS/Xray-core/releases/tag/v26.8.1",
      "summary": {"en": "restricted", "zh_cn": "受限"},
      "reality": {"xray": "conditional", "mihomo": "unsupported", "sing_box": "unsupported", "uri_list": "supported"},
      "evidence": {"source_audited": true, "config_tested": true, "handshake_tested": true},
      "assets": []
    },
    {
      "engine": "xray",
      "version": "26.9.0",
      "tier": "verified",
      "prerelease": false,
      "selectable": false,
      "requires_confirmation": false,
      "published_at": "2026-09-01T00:00:00Z",
      "source_url": "https://github.com/XTLS/Xray-core/releases/tag/v26.9.0",
      "summary": {"en": "withheld", "zh_cn": "已撤回"},
      "reality": {"xray": "supported", "mihomo": "supported", "sing_box": "supported", "uri_list": "supported"},
      "evidence": {"source_audited": true, "config_tested": true, "handshake_tested": true},
      "assets": []
    }
  ]
}`

type releaseList struct {
	ports.NodeReleaseCatalog
	releases []ports.NodeReleaseCatalogEntry
	err      error
	calls    int
}

func (r *releaseList) List(context.Context) (ports.NodeReleaseList, error) {
	r.calls++
	if r.err != nil {
		return ports.NodeReleaseList{}, r.err
	}
	return ports.NodeReleaseList{Releases: r.releases}, nil
}

// productRelease is the newest reviewed release, and the one whose tag addresses
// the published document.
var productRelease = ports.NodeReleaseCatalogEntry{
	Version: "4.0.1", ProductVersion: "4.0.1", ReleaseTag: "release/4.0.1", Scheme: "product",
}

type fixture struct {
	server *httptest.Server
	key    ed25519.PublicKey
	secret ed25519.PrivateKey
	mu     sync.Mutex
	served map[string][]byte
	fail   bool
	calls  int
	now    time.Time
}

// newFixture serves a signed release for any tag. The document is re-signed
// whenever `served` changes, so a case can publish a tampered document and have
// the manifest remain valid over it — which is what makes those cases about the
// reader rather than about the digest.
func newFixture(t *testing.T, document string) *fixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{key: public, secret: private, served: map[string][]byte{}, now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
	f.publish(document)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if f.fail {
			http.Error(w, "origin down", http.StatusBadGateway)
			return
		}
		body, ok := f.served[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) publish(document string) {
	digest := sha256.Sum256([]byte(document))
	manifest := fmt.Sprintf("%s  core-catalog.json\n", hex.EncodeToString(digest[:]))
	f.served["core-catalog.json"] = []byte(document)
	f.served[releaseasset.ChecksumAsset] = []byte(manifest)
	f.served[releaseasset.SignatureAsset] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(f.secret, []byte(manifest))) + "\n")
}

func (f *fixture) catalog(t *testing.T, releases *releaseList) *Catalog {
	t.Helper()
	catalog, err := New(Options{
		Releases:   releases,
		HTTPClient: f.server.Client(),
		BaseURL:    f.server.URL + "/download/",
		PublicKey:  f.key,
		Now:        func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func currentReleases() *releaseList {
	return &releaseList{releases: []ports.NodeReleaseCatalogEntry{
		productRelease,
		// A LEGACY RELEASE SITS BELOW IT AND MUST BE SKIPPED. It publishes no
		// document, so a reader that took the newest entry by position alone would
		// ask for an asset that was never uploaded.
		{Version: "v0.0.1-beta9", Scheme: "legacy"},
	}}
}

func TestItReadsTheDocumentTheReviewedReleasePublished(t *testing.T) {
	f := newFixture(t, documentFixture)
	document, err := f.catalog(t, currentReleases()).Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != supportedSchema || len(document.Releases) != 3 {
		t.Fatalf("document = %+v", document)
	}
	recommended, err := document.Recommended("xray")
	if err != nil || recommended.Version != "26.6.27" {
		t.Fatalf("Recommended = %q, %v", recommended.Version, err)
	}
	// THE WITHHELD RELEASE IS IN THE DOCUMENT AND NOT IN THE SELECTION. A release
	// the publisher marked unselectable was reviewed and deliberately withheld; it
	// stays on the record and must not be offered or resolved.
	if len(document.List("xray")) != 2 {
		t.Fatalf("List returned %d selectable releases", len(document.List("xray")))
	}
	if _, err := document.Resolve("xray", "26.9.0"); err == nil {
		t.Fatal("an unselectable release was resolved")
	}
	// AND A v-PREFIXED REQUEST NAMES THE SAME RELEASE, because upstream tags
	// releases that way and an operator pastes what they see.
	if release, err := document.Resolve("xray", "v26.6.27"); err != nil || release.Version != "26.6.27" {
		t.Fatalf("Resolve(v26.6.27) = %q, %v", release.Version, err)
	}
}

// A MANIFEST THAT IS NOT SIGNED BY THIS PROJECT IS REFUSED, and there is nothing
// to fall back on for a first read: an empty catalog is the honest answer, not a
// silently empty core selector that looks like the review found nothing.
func TestItRefusesAReleaseThisProjectDidNotSign(t *testing.T) {
	f := newFixture(t, documentFixture)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := New(Options{Releases: currentReleases(), HTTPClient: f.server.Client(), BaseURL: f.server.URL + "/download/", PublicKey: other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Document(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a manifest signed by another key was accepted: %v", err)
	}
}

// A DOCUMENT THIS BUILD CANNOT BE SURE IT UNDERSTANDS IS REFUSED.
//
// A schema bump changes what the fields mean, so decoding a newer document into
// these fields reads the same words with a different meaning; and a field added
// for another repository is a condition this panel was supposed to honor. Both
// failures have to have a name in them rather than being absorbed.
func TestItRefusesADocumentItCannotUnderstand(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
	}{
		{"a newer schema", strings.Replace(documentFixture, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"a field this build does not know", strings.Replace(documentFixture, `"releases": [`, `"core_policy": {"pinned": true}, "releases": [`, 1)},
		{"a field inside a release", strings.Replace(documentFixture, `"tier": "recommended"`, `"tier": "recommended", "quarantined": true`, 1)},
		{"no releases", `{"schema_version": 1, "updated_at": "2026-09-11T00:00:00Z", "releases": []}`},
		{"no date", `{"schema_version": 1, "updated_at": "0001-01-01T00:00:00Z", "releases": [{"engine": "xray", "version": "26.6.27"}]}`},
		{"truncated", documentFixture[:len(documentFixture)/2]},
		{"not JSON", "not a document at all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.document)
			// The manifest is re-signed over the tampered body, so what is being
			// tested is the decode and not the digest.
			if _, err := f.catalog(t, currentReleases()).Document(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("a document this build cannot read was accepted: %v", err)
			}
		})
	}
}

// AN ORIGIN THAT STOPS ANSWERING DOES NOT EMPTY THE SELECTOR.
//
// A moment of unreachability is not evidence that a core release stopped being
// reviewed, and the alternative — answering "no catalog" — turns a network blip
// into an empty core list and a refused upgrade. The last document read is kept,
// including across the moment its TTL expires.
func TestAFailedRefreshKeepsServingTheLastReviewedDocument(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	// Past the TTL, so the read is attempted rather than served from the cache.
	f.now = f.now.Add(2 * cacheTTL)

	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatalf("a failed refresh emptied the catalog: %v", err)
	}
	if len(document.Releases) != 3 {
		t.Fatalf("the fallback document has %d releases", len(document.Releases))
	}
	// AND A FAILING ORIGIN IS NOT RE-ASKED ON EVERY CALL. The TTL is extended so
	// one origin problem does not become a request per call.
	before := f.calls
	for i := 0; i < 5; i++ {
		if _, err := catalog.Document(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls != before {
		t.Fatalf("a failing origin was contacted %d more times", f.calls-before)
	}
}

// ONE READ AT A TIME. A page that lists both engines asks twice within the same
// millisecond, and a naive cache miss on both would fetch the same document twice
// — and, worse, could install the two answers independently.
func TestConcurrentCallersShareOneRead(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := catalog.Document(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	// Three assets per read: the manifest, its signature, and the document.
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 3 {
		t.Fatalf("eight callers caused %d asset reads, want one read (3)", f.calls)
	}
}

// NO RELEASE PUBLISHES A DOCUMENT is a named state rather than an empty catalog:
// a fleet of legacy releases predates the review being published at all.
func TestNoPublishingReleaseIsReported(t *testing.T) {
	f := newFixture(t, documentFixture)
	releases := &releaseList{releases: []ports.NodeReleaseCatalogEntry{{Version: "v0.0.1-beta9", Scheme: "legacy"}}}
	if _, err := f.catalog(t, releases).Document(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no publishing release reported %v", err)
	}
	if f.calls != 0 {
		t.Fatal("the reader contacted the origin for a release it had already rejected")
	}
}

// AN UNREACHABLE RELEASE LIST IS NOT AN EMPTY ONE, and the failure must not be
// cached as a successful read of nothing.
func TestAnUnavailableReleaseListIsNotAnEmptyCatalog(t *testing.T) {
	f := newFixture(t, documentFixture)
	releases := currentReleases()
	releases.err = errors.New("github is unreachable")
	catalog := f.catalog(t, releases)
	if _, err := catalog.Document(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unavailable release list reported %v", err)
	}
	// And it recovers without a restart.
	releases.err = nil
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatalf("did not recover once the list was reachable again: %v", err)
	}
}
