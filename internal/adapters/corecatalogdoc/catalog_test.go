package corecatalogdoc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
  "updated_at": "2026-09-20T00:00:00Z",
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
	// absent serves a 404 for the document, which is what a release that does not
	// publish the asset answers.
	absent bool
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
		if f.absent {
			http.NotFound(w, r)
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
	_, err = catalog.Document(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a manifest signed by another key was accepted: %v", err)
	}
	// AND IT IS NOT A MOMENT. A signature that does not verify is a statement about
	// the publication, not a blip: falling back to the review this panel already has
	// would answer a "somebody is publishing something I do not trust" event with a
	// working-looking panel.
	if errors.Is(err, ErrRefreshable) {
		t.Fatalf("a signature failure was classified as a refreshable read: %v", err)
	}
}

// ADDITIVE FIELDS ARE ALLOWED. A field this build does not know, inside a schema it
// does, cannot change what the fields it reads mean — that is what a schema number
// is for. Refusing it instead meant one publisher-side addition turned every
// deployed panel dark until each was upgraded, and made the schema number
// meaningless.
//
// The case is the same document with fields added at both levels, and it must read
// exactly as before.
func TestItIgnoresFieldsAddedWithinAKnownSchema(t *testing.T) {
	extended := strings.Replace(documentFixture, `"releases": [`, `"core_policy": {"pinned": true}, "releases": [`, 1)
	extended = strings.Replace(extended, `"tier": "recommended"`, `"tier": "recommended", "quarantined": false, "review": {"by": "someone"}`, 1)
	if extended == documentFixture {
		t.Fatal("the fixture carries no field to add; this case would pass vacuously")
	}
	document, err := newFixture(t, extended).catalog(t, currentReleases()).Document(context.Background())
	if err != nil {
		t.Fatalf("a document with additive fields was refused: %v", err)
	}
	if len(document.Releases) != 3 {
		t.Fatalf("document = %+v", document)
	}
	recommended, err := document.Recommended("xray")
	if err != nil || recommended.Version != "26.6.27" {
		t.Fatalf("Recommended = %q, %v", recommended.Version, err)
	}
}

// A DOCUMENT THIS BUILD CANNOT READ IS REFUSED, and these are the two forms that
// are about the document itself rather than about its fields: a schema whose
// meaning this build does not know, and bytes that are not a document.
func TestItRefusesADocumentItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
	}{
		{"a newer schema", strings.Replace(documentFixture, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"truncated", documentFixture[:len(documentFixture)/2]},
		{"not JSON", "not a document at all"},
		{"no releases", `{"schema_version": 1, "updated_at": "2026-09-20T00:00:00Z", "releases": []}`},
		{"no date", `{"schema_version": 1, "updated_at": "0001-01-01T00:00:00Z", "releases": [{"engine": "xray", "version": "26.6.27"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.document)
			// The manifest is re-signed over the tampered body, so what is being
			// tested is the parse and not the digest.
			_, err := f.catalog(t, currentReleases()).Document(context.Background())
			if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrDocumentUnusable) {
				t.Fatalf("a document this build cannot read was accepted: %v", err)
			}
			// AND IT IS NOT CLASSIFIED AS A MOMENT. This is the difference that
			// decides whether the panel may keep serving what it read before.
			if errors.Is(err, ErrRefreshable) {
				t.Fatalf("an unusable document was classified as a refreshable read: %v", err)
			}
		})
	}
}

// reviewedDocument builds a publishable document from Go values, so a case mutates
// a FIELD rather than text: editing the JSON would collide with the fields already
// in the object, and a duplicate key is resolved by the decoder taking the last
// one — which turns a case that looks like it changes something into one that
// changes nothing.
func reviewedDocument(t *testing.T, mutate func(*ports.CoreCatalogDocument)) string {
	t.Helper()
	base := func(engine, version, tier string, restricted bool) ports.CoreRelease {
		return ports.CoreRelease{
			Engine: engine, Version: version, Tier: tier, Selectable: true,
			RequiresConfirmation: restricted,
			PublishedAt:          time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
		}
	}
	document := ports.CoreCatalogDocument{
		SchemaVersion: 1,
		UpdatedAt:     time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC),
		Releases: []ports.CoreRelease{
			base("xray", "26.6.27", domain.CoreTierRecommended, false),
			base("xray", "26.9.9", domain.CoreTierRestricted, true),
			base("sing-box", "1.14.0", domain.CoreTierRecommended, false),
		},
	}
	if mutate != nil {
		mutate(&document)
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// THE FIELDS THE PANEL'S OWN DECISIONS READ ARE CHECKED, because its gates test
// membership in closed sets and compare flags against tiers. A value outside those
// sets does not make the panel offer something wrong — it makes every gate fall
// through, and a fall-through is silent unless the reason is named.
func TestItRefusesValuesItsOwnDecisionsCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ports.CoreCatalogDocument)
	}{
		{"an engine the panel cannot offer", func(d *ports.CoreCatalogDocument) {
			d.Releases[0].Engine = "v2ray"
		}},
		{"a version that is not canonical", func(d *ports.CoreCatalogDocument) {
			d.Releases[0].Version = "v26.6.27"
		}},
		{"a tier the panel does not know", func(d *ports.CoreCatalogDocument) {
			d.Releases[0].Tier = "probably_fine"
		}},
		{"confirmation on a release that is not restricted", func(d *ports.CoreCatalogDocument) {
			d.Releases[0].RequiresConfirmation = true
		}},
		{"a restriction that does not ask for confirmation", func(d *ports.CoreCatalogDocument) {
			d.Releases[1].RequiresConfirmation = false
		}},
		{"one release listed twice", func(d *ports.CoreCatalogDocument) {
			d.Releases = append(d.Releases, d.Releases[0])
		}},
		{"no review date", func(d *ports.CoreCatalogDocument) {
			d.UpdatedAt = time.Time{}
		}},
		{"no releases", func(d *ports.CoreCatalogDocument) {
			d.Releases = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := reviewedDocument(t, tc.mutate)
			err := errorsOf(t, body)
			if !errors.Is(err, ErrDocumentUnusable) {
				t.Fatalf("a value the panel's decisions cannot use was accepted: %v", err)
			}
			if errors.Is(err, ErrRefreshable) {
				t.Fatalf("it was classified as a moment rather than a statement: %v", err)
			}
		})
	}
}

// TWO RECOMMENDED RELEASES ON ONE ENGINE is the document's answer to give, and it
// can only give one: the panel's default core comes from here, and taking the first
// of two would make it depend on ordering.
func TestItRefusesTwoRecommendedReleasesForOneEngine(t *testing.T) {
	body := reviewedDocument(t, func(d *ports.CoreCatalogDocument) {
		d.Releases[1].Tier = domain.CoreTierRecommended
		d.Releases[1].RequiresConfirmation = false
	})
	if err := errorsOf(t, body); !errors.Is(err, ErrDocumentUnusable) {
		t.Fatalf("two recommended releases were accepted: %v", err)
	}
}

// A SECOND RECOMMENDED RELEASE THAT IS NOT SELECTABLE IS NOT ONE. The panel only
// ever lists and resolves selectable releases, so a withheld entry cannot make the
// default ambiguous — and refusing the document for it would reject something the
// publisher's own review allows.
func TestASecondRecommendedThatIsNotSelectableIsAccepted(t *testing.T) {
	body := reviewedDocument(t, func(d *ports.CoreCatalogDocument) {
		d.Releases[1].Tier = domain.CoreTierRecommended
		d.Releases[1].RequiresConfirmation = false
		d.Releases[1].Selectable = false
	})
	if err := errorsOf(t, body); err != nil {
		t.Fatalf("a document the publisher's review allows was refused: %v", err)
	}
}

// errorsOf reads one document through the adapter and returns whatever it said.
func errorsOf(t *testing.T, body string) error {
	t.Helper()
	_, err := newFixture(t, body).catalog(t, currentReleases()).Document(context.Background())
	return err
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

// A STATEMENT IS NEVER ANSWERED FROM THE CACHE.
//
// The case above is a moment: an origin that stops answering, where the document
// read a minute ago is still the review this panel was enforcing. This is the
// other kind — the document arrived and cannot be used — and serving the previous
// one would leave the panel enforcing a review its publisher has moved past, while
// looking exactly like a working panel. It is refused, with the reason, and the
// previous document is retired rather than kept for a later network failure to
// resurrect.
func TestAPermanentFailureIsNeverAnsweredFromTheCache(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The publisher moves to a schema this build does not read.
	f.mu.Lock()
	f.publish(strings.Replace(documentFixture, `"schema_version": 1`, `"schema_version": 2`, 1))
	f.mu.Unlock()
	f.now = f.now.Add(2 * cacheTTL)

	_, err := catalog.Document(context.Background())
	if !errors.Is(err, ErrDocumentUnusable) {
		t.Fatalf("an unusable document was answered with: %v", err)
	}
	if errors.Is(err, ErrRefreshable) {
		t.Fatalf("it was classified as a moment: %v", err)
	}

	// AND THE PREVIOUS DOCUMENT IS GONE. A later moment — the origin going down —
	// must not resurrect a review the publisher has abandoned.
	f.mu.Lock()
	f.fail, f.served = true, map[string][]byte{}
	f.mu.Unlock()
	f.now = f.now.Add(2 * cacheTTL)
	if document, err := catalog.Document(context.Background()); err == nil {
		t.Fatalf("a retired document came back after an unrelated failure: %d releases", len(document.Releases))
	}
}

// A RELEASE THAT DOES NOT PUBLISH THE ASSET FALLS BACK, AND SAYS WHY.
//
// It is a failure of AVAILABILITY rather than of content: the panel cannot name a
// document, which is what being offline looks like from here, so its newest review
// — the last thing both sides agreed on — stays in force rather than the whole
// selector going empty over a missing file.
//
// WHAT MUST NOT HAPPEN IS THE REASON BEING LOST. A packaging mistake that reads as
// a network blip is a packaging mistake nobody fixes, so the reason names the
// release and the asset, and it reaches the status an operator reads.
func TestAMissingAssetKeepsTheNewestReviewAndNamesTheReason(t *testing.T) {
	f := newFixture(t, documentFixture)
	catalog := f.catalog(t, currentReleases())
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	f.absent = true
	f.mu.Unlock()
	f.now = f.now.Add(2 * cacheTTL)

	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatalf("a release without the asset emptied the catalog: %v", err)
	}
	if len(document.Releases) == 0 {
		t.Fatal("the fallback carries no releases")
	}
	status := catalog.Status()
	if !status.FallingBack {
		t.Fatalf("a fallback is not reported as one: %+v", status)
	}
	if !strings.Contains(status.LastError, "does not publish") {
		t.Fatalf("the reason does not name the missing asset: %q", status.LastError)
	}
	if !strings.Contains(status.LastError, "release/4.0.1") {
		t.Fatalf("the reason does not name the release: %q", status.LastError)
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

// NO RELEASE PUBLISHES A DOCUMENT is a named state rather than an empty catalog: a
// fleet of legacy releases predates the review being published at all.
//
// IT FALLS BACK, AND NAMES THAT REASON. The panel cannot name a source, which is
// what being offline looks like from here; the newest review it has is still the
// last thing both sides agreed on, and emptying the selector over it would take the
// fleet's core choices away for a fact about the release list.
func TestNoPublishingReleaseFallsBackAndNamesThatReason(t *testing.T) {
	f := newFixture(t, documentFixture)
	releases := &releaseList{releases: []ports.NodeReleaseCatalogEntry{{Version: "v0.0.1-beta9", Scheme: "legacy"}}}
	catalog := f.catalog(t, releases)
	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatalf("no publishing release emptied the catalog: %v", err)
	}
	if len(document.Releases) == 0 {
		t.Fatal("the fallback carries no releases")
	}
	// NOT ONE CONTACT WITH THE ORIGIN: the release list already said there is nothing
	// to read, and asking anyway would be a request per call for a known answer.
	if f.calls != 0 {
		t.Fatalf("the reader contacted the origin %d times for a release it had already rejected", f.calls)
	}
	if status := catalog.Status(); !status.FallingBack || !strings.Contains(status.LastError, "publishes a core catalog") {
		t.Fatalf("the reason is not reported: %+v", status)
	}
}

// AN UNREACHABLE RELEASE LIST IS A MOMENT, NOT AN EMPTY CATALOG.
//
// The panel cannot name a release to read from, which is what being offline looks
// like from here — so it serves the newest review it has rather than showing a
// working panel with no cores offered. The status says it is falling back, because
// the alternative is a selector that looks current and is not.
func TestAnUnavailableReleaseListFallsBackAndRecovers(t *testing.T) {
	f := newFixture(t, documentFixture)
	releases := currentReleases()
	releases.err = errors.New("github is unreachable")
	catalog := f.catalog(t, releases)

	document, err := catalog.Document(context.Background())
	if err != nil {
		t.Fatalf("an unreachable release list emptied the catalog: %v", err)
	}
	if len(document.Releases) == 0 {
		t.Fatal("the fallback document carries no releases")
	}
	status := catalog.Status()
	if !status.FallingBack || status.Source == "" || status.ReviewTime == nil {
		t.Fatalf("a panel serving a fallback does not say so: %+v", status)
	}
	if status.LastSuccess != nil {
		t.Fatalf("a process that has never read a document claims it has: %+v", status.LastSuccess)
	}
	if status.LastError == "" {
		t.Fatal("the reason the origin was not read is not reported")
	}

	// And it recovers without a restart, and stops saying it is falling back.
	releases.err = nil
	f.now = f.now.Add(2 * cacheTTL)
	if _, err := catalog.Document(context.Background()); err != nil {
		t.Fatalf("did not recover once the list was reachable again: %v", err)
	}
	if status := catalog.Status(); status.FallingBack || status.LastError != "" || status.LastSuccess == nil {
		t.Fatalf("a recovered reader still reports a fallback: %+v", status)
	}
}
