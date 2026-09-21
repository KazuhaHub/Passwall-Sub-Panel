// Package corecatalogdoc reads the reviewed core catalog out of the Node release
// that published it.
//
// WHY THE PANEL DOES NOT COMPILE THE CATALOG IN. It used to, through the Node Go
// module, which kept that module in this repository's dependency graph. It also
// meant a core could be added to the review — or a tier corrected after a
// failure — only when this repository shipped a release, so the panel's
// enforcement boundary and the node runtime's review could disagree for as long
// as that took.
//
// THE DOCUMENT IS NOT RE-REVIEWED HERE. The deep checks that make the catalog
// trustworthy — one recommended release per engine, restricted implies
// confirmation, assets covering every target, handshake evidence agreeing with
// each REALITY conclusion, URLs inside the matching official release — are the
// publisher's, and they run before the signature is made. What this reader
// contributes is that the bytes are the ones that project signed, that the format
// is one it understands, and that the shape its own decisions read is well formed.
// Re-deriving the publisher's review here would be a second copy of it, and the
// copy that drifts is always the one nobody is looking at.
package corecatalogdoc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/releaseasset"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	// catalogAsset is the name the release publishes the document under.
	catalogAsset = "core-catalog.json"

	// supportedSchema is the one document format this build reads. An unknown
	// number is REFUSED rather than attempted: a schema bump is a change to what
	// the fields mean, and decoding a newer document into these fields would read
	// the same words with a different meaning.
	supportedSchema = 1

	// cacheTTL is how long one read is reused. The document changes when a review
	// changes, which is at most a few times a month; the TTL exists to keep the
	// GitHub API out of the request path rather than to track the origin closely.
	cacheTTL = 10 * time.Minute
)

// ErrUnavailable is returned when there is no catalog to serve — no release has
// published one, or none could be read and nothing good was read before.
//
// IT WRAPS domain.ErrUnavailable, so every handler that already maps that sentinel
// answers 503 instead of 500. The distinction is the one an operator acts on: the
// panel is running and the source it reads is not.
var ErrUnavailable = fmt.Errorf("%w: the reviewed core catalog is unavailable", domain.ErrUnavailable)

// THE TWO KINDS OF FAILURE ARE NOT THE SAME PROBLEM, and they must not share an
// answer.
//
// ErrRefreshable is a moment: the origin was unreachable, slow, or answered with
// a server error. What was read before is still the review this panel was
// enforcing a minute ago, so serving it is the honest answer and the alternative
// is turning a network blip into an empty core selector.
//
// ErrDocumentUnusable is a statement: the document this build received has a
// schema it does not know, is not valid JSON, breaks a constraint its own
// decisions read, or a release does not publish one at all. It will say the same
// thing on every attempt, and answering IT from the cache would leave the panel
// enforcing a review the publisher has moved past — visibly working, and wrong.
// That is why these never fall back.
var (
	ErrRefreshable      = errors.New("core catalog: a refreshable read failure")
	ErrDocumentUnusable = errors.New("core catalog: the published document cannot be used")
)

// readFailure carries which of the two a failed read was.
//
// IT IS A WRAPPER RATHER THAN A PREFIXED MESSAGE so the underlying error's text
// reaches the operator unchanged — the reason is the point — while errors.Is
// still answers the classification question.
type readFailure struct {
	err         error
	refreshable bool
}

func (f readFailure) Error() string { return f.err.Error() }
func (f readFailure) Unwrap() error { return f.err }

func (f readFailure) Is(target error) bool {
	switch target {
	case ErrRefreshable:
		return f.refreshable
	case ErrDocumentUnusable:
		return !f.refreshable
	default:
		return false
	}
}

func refreshable(err error) error { return readFailure{err: err, refreshable: true} }
func unusable(err error) error    { return readFailure{err: err} }

// Options are the seams and the two inputs the reader needs.
type Options struct {
	// Releases locates the release to read the document from. It is the reviewed
	// Node release catalog, so the document comes from a release this build has
	// already established compatibility with rather than from whichever release
	// happens to be newest.
	Releases   ports.NodeReleaseCatalog
	HTTPClient *http.Client
	BaseURL    string
	PublicKey  ed25519.PublicKey
	Now        func() time.Time
	// SnapshotPath is where the panel keeps the last review it read, so the next
	// start has it. Empty disables the file entirely — the review shipped with the
	// build is still served, and the panel simply re-reads from the origin on every
	// start.
	SnapshotPath string
}

type Catalog struct {
	assets       *releaseasset.Source
	releases     ports.NodeReleaseCatalog
	now          func() time.Time
	snapshotPath string
	shipped      ports.CoreCatalogDocument

	mu sync.Mutex
	// good is the document in force, and have says whether there is one. It is set
	// from the origin when a read succeeds and from the newest review available
	// when one does not.
	good    ports.CoreCatalogDocument
	have    bool
	expires time.Time
	flight  *load

	// The three fields below exist to answer "what am I being served, and how old
	// is it" — the question an operator has to be able to ask, because a panel that
	// is quietly serving an old review looks exactly like one serving a current one.
	servedReviewTime time.Time
	lastSuccess      time.Time
	lastError        error
	fallingBack      bool
	source           string
}

type load struct {
	done   chan struct{}
	result ports.CoreCatalogDocument
	err    error
}

var _ ports.CoreCatalog = (*Catalog)(nil)

func New(options Options) (*Catalog, error) {
	if options.Releases == nil {
		return nil, errors.New("core catalog: a reviewed Node release source is required")
	}
	source, err := releaseasset.NewSource(releaseasset.Options{
		HTTPClient: options.HTTPClient,
		BaseURL:    options.BaseURL,
		PublicKey:  options.PublicKey,
	})
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	shipped, err := snapshotDocument()
	if err != nil {
		// A BUILD WHOSE OWN CONSTANT IS UNUSABLE CANNOT RECOVER AT RUNTIME, so it
		// refuses to start rather than serving a fleet with no review at all. This
		// is a build defect and it is reported as one.
		return nil, err
	}
	return &Catalog{
		assets: source, releases: options.Releases, now: now,
		snapshotPath: strings.TrimSpace(options.SnapshotPath),
		shipped:      shipped,
		source:       shippedOrigin,
	}, nil
}

// Document returns the reviewed catalog, reusing the last read within its TTL.
//
// A FAILED READ DOES NOT ERASE WHAT WAS READ BEFORE. The origin being unreachable
// for a moment is not evidence that a core release stopped being reviewed, and
// answering "no catalog" to an operator who was reading one a minute ago would
// turn a network blip into an empty core selector and a refused upgrade. So the
// last good document is kept, and the failure is logged rather than surfaced —
// WITH ONE HONEST LIMIT: a document that stays unreadable keeps the old review in
// place for as long as the process lives, and nothing on the panel's status page
// says so yet. Only a first read that has no previous document to fall back on
// returns an error.
func (c *Catalog) Document(ctx context.Context) (ports.CoreCatalogDocument, error) {
	if err := ctx.Err(); err != nil {
		return ports.CoreCatalogDocument{}, err
	}
	c.mu.Lock()
	if c.have && c.now().Before(c.expires) {
		result := c.good
		c.mu.Unlock()
		return result, nil
	}
	if c.flight != nil {
		// ONE READ AT A TIME. Several callers reach this within the same
		// millisecond — a page that lists engines asks once per engine — and each
		// would otherwise fetch the same document.
		current := c.flight
		c.mu.Unlock()
		select {
		case <-current.done:
			return current.result, current.err
		case <-ctx.Done():
			return ports.CoreCatalogDocument{}, ctx.Err()
		}
	}
	current := &load{done: make(chan struct{})}
	c.flight = current
	c.mu.Unlock()

	document, err := c.load(ctx)

	c.mu.Lock()
	document, err = c.settle(document, err)
	c.flight = nil
	c.mu.Unlock()

	current.result, current.err = document, err
	close(current.done)
	return document, err
}

// settle records what one read produced and decides what the caller is told.
// Called with the lock held.
//
// THE TTL IS EXTENDED ON EVERY OUTCOME THAT SERVES SOMETHING, rather than left to
// expire: leaving it would make every subsequent call attempt the same failing
// read, turning one origin problem into a request-per-call.
func (c *Catalog) settle(fresh ports.CoreCatalogDocument, err error) (ports.CoreCatalogDocument, error) {
	switch {
	case err == nil:
		c.good, c.have = fresh, true
		c.expires = c.now().Add(cacheTTL)
		c.servedReviewTime, c.lastSuccess = fresh.UpdatedAt, c.now()
		c.lastError, c.fallingBack = nil, false
		c.source = "read from the published release"
		c.writeDiskSnapshot(fresh)
		return fresh, nil

	case errors.Is(err, ErrRefreshable):
		// OFFLINE IS A DECISION, NOT A FAILURE. The newest review this panel has is
		// still a review somebody made, and it is what the panel was enforcing a
		// moment ago; the alternative is emptying the selector and refusing every
		// conversion over a network blip.
		fallback, source, ok := c.newestReview()
		if !ok {
			return ports.CoreCatalogDocument{}, c.fail(err)
		}
		c.good, c.have, c.expires = fallback, true, c.now().Add(cacheTTL)
		c.servedReviewTime, c.fallingBack, c.source, c.lastError = fallback.UpdatedAt, true, source, err
		log.Warn("core catalog not refreshed; serving the newest review this panel has",
			"err", err, "source", source, "review_time", fallback.UpdatedAt)
		return fallback, nil

	default:
		// A DOCUMENT THIS BUILD CANNOT USE RETIRES EVERY REVIEW THIS PROCESS CAN
		// SERVE WITHOUT THE ORIGIN — the one in force AND the one the build shipped
		// with.
		//
		// The second half is the part worth stating. A schema bump means the
		// publisher's documents have changed in a way this build cannot interpret;
		// falling back to the review it was built with would answer with a review
		// that is BOTH older and known to be superseded in form, and would do it
		// silently, which is the failure the classification exists to prevent. So
		// the panel refuses with the reason until it can read a document again.
		//
		// ONE HONEST LIMIT: a RESTART re-arms the shipped review, because a restart
		// cannot be told from a first start and the offline case — an origin that
		// cannot be reached at all — is one this panel supports on purpose. Nothing
		// is persisted to say "the origin changed shape", so the next process
		// discovers it again on its first read.
		c.good, c.have, c.expires = ports.CoreCatalogDocument{}, false, time.Time{}
		c.shipped = ports.CoreCatalogDocument{}
		return ports.CoreCatalogDocument{}, c.fail(err)
	}
}

// fail records why a call is being refused. Called with the lock held.
func (c *Catalog) fail(err error) error {
	c.fallingBack, c.lastError = false, err
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// load reads, verifies and decodes the document the newest reviewed release
// published.
func (c *Catalog) load(ctx context.Context) (ports.CoreCatalogDocument, error) {
	list, err := c.releases.List(ctx)
	if err != nil {
		// The release list is a read from the same origin, so it fails the same
		// way one and is refreshable for the same reason.
		return ports.CoreCatalogDocument{}, refreshable(fmt.Errorf("locate the published catalog: %w", err))
	}
	tag := ""
	for _, release := range list.Releases {
		// A LEGACY RELEASE PUBLISHES NO DOCUMENT, and skipping it is the honest
		// answer rather than a fallback: it predates the review being published at
		// all. The list is newest first, so the first product-scheme release is
		// the newest review.
		if release.ReleaseTag == "" {
			continue
		}
		tag = release.ReleaseTag
		break
	}
	if tag == "" {
		// NO REVIEWED RELEASE PUBLISHES ONE AT ALL. Also availability: the panel
		// cannot name a source, which is what being offline looks like from here, and
		// the reason names the difference.
		return ports.CoreCatalogDocument{}, refreshable(errors.New("no reviewed Node release publishes a core catalog"))
	}
	body, err := c.assets.Asset(ctx, tag, catalogAsset)
	if err != nil {
		// THE LINE IS BETWEEN AVAILABILITY AND CONTENT.
		//
		// A manifest this project did not sign, and an asset that does not match the
		// digest the manifest names, are statements about WHAT ARRIVED: they will say
		// the same thing on the next attempt, and serving the previous review instead
		// would answer "somebody is publishing something I do not trust" with a panel
		// that looks like it is working.
		if errors.Is(err, releaseasset.ErrUntrusted) || errors.Is(err, releaseasset.ErrAssetMismatch) {
			return ports.CoreCatalogDocument{}, unusable(fmt.Errorf("%s: %w", tag, err))
		}
		// A RELEASE THAT DOES NOT CARRY THE ASSET IS A FAILURE OF AVAILABILITY, and
		// it falls back — WITH ITS OWN REASON, which is the part that matters. It is
		// not a moment of unreachability and must never be reported as one: a
		// packaging mistake that reads as a network blip is a packaging mistake
		// nobody fixes. But the panel's newest review is still the last thing both
		// sides agreed on, and refusing to offer anything over a missing file would
		// take the whole fleet's core selector down for it.
		if errors.Is(err, releaseasset.ErrAssetMissing) || errors.Is(err, releaseasset.ErrAssetNotPublished) {
			return ports.CoreCatalogDocument{}, refreshable(fmt.Errorf("%s does not publish %s: %w", tag, catalogAsset, err))
		}
		return ports.CoreCatalogDocument{}, refreshable(fmt.Errorf("read %s from %s: %w", catalogAsset, tag, err))
	}
	return parse(body)
}

// parse reads the document and checks the fields this build's own decisions read.
//
// ADDITIVE FIELDS ARE ALLOWED, AND THAT IS A CHANGE OF POSITION. This used to
// refuse any field it did not know, on the reasoning that a field added by the
// publisher is a condition the panel was meant to honor. The reasoning was right
// and the remedy was backwards: refusing meant one publisher-side addition turned
// every deployed panel dark until each was upgraded, and it made the schema number
// meaningless — a version exists precisely to say when the meaning changed rather
// than when the document grew.
//
// SO THE LINE IS DRAWN BY SCHEMA AND BY SEMANTICS:
//
//   - A field this build does not know, within a schema it does, is ignored. It
//     cannot change what the fields below mean, because that is what a schema bump
//     is for.
//   - A schema this build does not read is refused, whatever the fields say. The
//     same words could mean something else.
//   - The fields below are checked for the values and constraints THE PANEL'S OWN
//     DECISIONS depend on. The publisher's deeper review — assets covering every
//     target, handshake evidence agreeing with each REALITY conclusion, URLs inside
//     the matching official release — stays where it runs, before the signature.
func parse(body []byte) (ports.CoreCatalogDocument, error) {
	var document ports.CoreCatalogDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil {
		return ports.CoreCatalogDocument{}, unusable(fmt.Errorf("decode %s: %w", catalogAsset, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ports.CoreCatalogDocument{}, unusable(fmt.Errorf("decode %s: trailing JSON value", catalogAsset))
	}
	if err := validateDocument(document); err != nil {
		return ports.CoreCatalogDocument{}, unusable(err)
	}
	return document, nil
}

// validateDocument applies the checks this panel's decisions rest on.
func validateDocument(document ports.CoreCatalogDocument) error {
	if document.SchemaVersion != supportedSchema {
		return fmt.Errorf("core catalog is schema %d and this build reads %d; a schema number changes when the fields change meaning, so this document cannot be read as if it had not", document.SchemaVersion, supportedSchema)
	}
	if document.UpdatedAt.IsZero() {
		return errors.New("core catalog carries no review date")
	}
	if len(document.Releases) == 0 {
		return errors.New("core catalog carries no releases")
	}
	recommended := map[string]int{}
	seen := make(map[string]bool, len(document.Releases))
	for index, release := range document.Releases {
		// ENGINE AND VERSION ARE IDENTITIES, and both are compared against values
		// this panel produces: the engine against a closed set it offers, the
		// version against what a node reports about itself. A release whose either
		// is unreadable is one the panel cannot match to anything, and matching is
		// the only thing it does with them.
		if release.Engine != string(domain.NodeCoreXray) && release.Engine != string(domain.NodeCoreSingBox) {
			return fmt.Errorf("core catalog release %d names engine %q, which this panel cannot offer", index, release.Engine)
		}
		normalized, err := domain.NormalizeCoreVersion(release.Version)
		if err != nil || normalized != release.Version {
			return fmt.Errorf("core catalog release %d has version %q, which is not canonical: %v", index, release.Version, err)
		}
		// THE TIER IS GATED ON, so an unknown one would fall through every
		// comparison that decides what may be installed — and the panel's gates
		// test membership in a set, so an unknown tier is refused rather than
		// allowed. Naming it here is what turns that into a diagnosis.
		switch release.Tier {
		case domain.CoreTierRecommended, domain.CoreTierVerified, domain.CoreTierConfigVerified, domain.CoreTierRestricted:
		default:
			return fmt.Errorf("core catalog release %s names tier %q, which this panel does not know", release.Version, release.Tier)
		}
		// CONFIRMATION AND TIER HAVE TO AGREE. The panel stores the operator's
		// acknowledgement beside the version and compares the two on every write, so
		// a document where the flag and the tier disagree makes that comparison
		// wrong in one direction or the other.
		if (release.Tier == domain.CoreTierRestricted) != release.RequiresConfirmation {
			return fmt.Errorf("core catalog release %s is tier %q with requires_confirmation=%t; only a restricted release requires confirmation, and every restricted release does",
				release.Version, release.Tier, release.RequiresConfirmation)
		}
		key := release.Engine + "/" + release.Version
		if seen[key] {
			// Two entries for one identity are two reviews of one release, and a
			// lookup would return whichever came first.
			return fmt.Errorf("core catalog lists %s more than once", key)
		}
		seen[key] = true
		if release.Selectable && release.Tier == domain.CoreTierRecommended {
			recommended[release.Engine]++
		}
	}
	for engine, count := range recommended {
		if count > 1 {
			// WHICH ONE IS RECOMMENDED IS THE DOCUMENT'S ANSWER TO GIVE, and it can
			// only give one: the panel's default core comes from here, and picking
			// the first of two would make it depend on ordering.
			return fmt.Errorf("core catalog marks %d releases as recommended for %s; the panel takes its default from this and cannot choose between two", count, engine)
		}
	}
	return nil
}

// Unavailable is a catalog that can never answer, for a build that has no way to
// read the published document.
//
// IT EXISTS SO THAT NO CONSUMER NEEDS A NIL CHECK. The alternative was to hand a
// nil port to five call sites, each of which would then need its own decision
// about what a missing catalog means — and a forgotten check is a panic inside a
// request. One value that answers with a named reason makes "there is no catalog"
// an ordinary failure that every caller already handles.
func Unavailable(reason error) ports.CoreCatalog { return unavailable{reason: reason} }

type unavailable struct{ reason error }

func (u unavailable) Document(context.Context) (ports.CoreCatalogDocument, error) {
	return ports.CoreCatalogDocument{}, fmt.Errorf("%w: %v", ErrUnavailable, u.reason)
}

// Status reports the reason this catalog can never answer, so a status page says
// WHY rather than showing an empty source.
func (u unavailable) Status() ports.CoreCatalogStatus {
	return ports.CoreCatalogStatus{Source: "unavailable", LastError: u.reason.Error()}
}
