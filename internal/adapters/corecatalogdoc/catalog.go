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
	if err == nil {
		c.good, c.have = document, true
		c.expires = c.now().Add(cacheTTL)
	} else if c.have {
		// The previous document is served, and the TTL is EXTENDED rather than
		// left to expire: leaving it would make every subsequent call attempt the
		// same failing read, turning one origin problem into a request-per-call.
		c.expires = c.now().Add(cacheTTL)
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
func (c *Catalog) resolve(fresh ports.CoreCatalogDocument, err error) (ports.CoreCatalogDocument, error) {
	if err == nil {
		return fresh, nil
	}
	if c.have {
		log.Warn("core catalog not refreshed; serving the last reviewed document", "err", err)
		return c.good, nil
	}
	return ports.CoreCatalogDocument{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
}

// load reads, verifies and decodes the document the newest reviewed release
// published.
func (c *Catalog) load(ctx context.Context) (ports.CoreCatalogDocument, error) {
	list, err := c.releases.List(ctx)
	if err != nil {
		return ports.CoreCatalogDocument{}, fmt.Errorf("locate the published catalog: %w", err)
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
		return ports.CoreCatalogDocument{}, errors.New("no reviewed Node release publishes a core catalog")
	}
	body, err := c.assets.Asset(ctx, tag, catalogAsset)
	if err != nil {
		return ports.CoreCatalogDocument{}, fmt.Errorf("read %s from %s: %w", catalogAsset, tag, err)
	}
	return decode(body)
}

// decode reads the document, refusing anything this build cannot be sure it
// understands.
//
// UNKNOWN FIELDS ARE REFUSED, which is stricter than it needs to be to read the
// fields below and is the point: the publisher is another repository, and a field
// added there is a change to a document this panel enforces on. Refusing means
// the disagreement shows up as a failed read with a name in it, rather than as a
// panel that quietly ignores a new condition it was supposed to honor.
func decode(body []byte) (ports.CoreCatalogDocument, error) {
	var document ports.CoreCatalogDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return ports.CoreCatalogDocument{}, fmt.Errorf("decode %s: %w", catalogAsset, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ports.CoreCatalogDocument{}, fmt.Errorf("decode %s: trailing JSON value", catalogAsset)
	}
	if document.SchemaVersion != supportedSchema {
		return ports.CoreCatalogDocument{}, fmt.Errorf("core catalog schema %d, this build reads %d", document.SchemaVersion, supportedSchema)
	}
	if document.UpdatedAt.IsZero() || len(document.Releases) == 0 {
		return ports.CoreCatalogDocument{}, errors.New("core catalog carries no dated review")
	}
	return document, nil
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
