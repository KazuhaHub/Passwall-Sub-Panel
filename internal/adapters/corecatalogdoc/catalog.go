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
}

type Catalog struct {
	assets   *releaseasset.Source
	releases ports.NodeReleaseCatalog
	now      func() time.Time

	mu      sync.Mutex
	good    ports.CoreCatalogDocument
	have    bool
	expires time.Time
	flight  *load
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
	return &Catalog{assets: source, releases: options.Releases, now: now}, nil
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
	switch {
	case err == nil:
		c.good, c.have = document, true
		c.expires = c.now().Add(cacheTTL)
	case errors.Is(err, ErrRefreshable):
		if c.have {
			// The previous document is served, and the TTL is EXTENDED rather than
			// left to expire: leaving it would make every subsequent call attempt
			// the same failing read, turning one origin problem into a
			// request-per-call.
			c.expires = c.now().Add(cacheTTL)
		}
	default:
		// A DOCUMENT THIS BUILD CANNOT USE RETIRES THE PREVIOUS ONE. Keeping it
		// would mean a later network failure served a review the publisher has
		// moved past, so the cache is dropped and every call fails with the reason
		// until a usable document arrives.
		c.good, c.have, c.expires = ports.CoreCatalogDocument{}, false, time.Time{}
	}
	c.flight = nil
	document, err = c.resolve(document, err)
	c.mu.Unlock()

	current.result, current.err = document, err
	close(current.done)
	return document, err
}

// resolve decides what a call reports once the lock is held again: the fresh
// document, the previous one, or nothing at all.
//
// THE CLASSIFICATION IS THE WHOLE DECISION. Only a refreshable failure may be
// answered from the previous document; everything else is reported with its
// reason, because the alternative is a panel that enforces a review nobody
// publishes any more while looking exactly like a working one.
func (c *Catalog) resolve(fresh ports.CoreCatalogDocument, err error) (ports.CoreCatalogDocument, error) {
	if err == nil {
		return fresh, nil
	}
	if errors.Is(err, ErrRefreshable) && c.have {
		log.Warn("core catalog not refreshed; serving the last reviewed document", "err", err)
		return c.good, nil
	}
	// BOTH ARE WRAPPED. `%v` for the reason would print it and drop the chain,
	// which is exactly the classification the caller asked for.
	return ports.CoreCatalogDocument{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
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
		// A FLEET WHERE NO RELEASE PUBLISHES ONE is a statement about the fleet,
		// not a moment, and waiting will not change it.
		return ports.CoreCatalogDocument{}, unusable(errors.New("no reviewed Node release publishes a core catalog"))
	}
	body, err := c.assets.Asset(ctx, tag, catalogAsset)
	if err != nil {
		// A RELEASE THAT DOES NOT CARRY THE ASSET IS NOT A NETWORK PROBLEM. It will
		// answer the same way on every attempt, and reporting it as an unreachable
		// origin is how a packaging mistake stays hidden behind a retry.
		if errors.Is(err, releaseasset.ErrAssetMissing) {
			return ports.CoreCatalogDocument{}, unusable(fmt.Errorf("%s: %w", tag, err))
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
