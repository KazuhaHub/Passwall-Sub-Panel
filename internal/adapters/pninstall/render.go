// Package pninstall renders Passwall Node's installation script from the template
// a release PUBLISHED.
//
// WHY IT IS FETCHED RATHER THAN COMPILED IN. The template used to arrive as a Go
// package in the Node module, which made PSP's install path a consumer of that
// module — and a module dependency is what keeps a repository in go.mod. The
// template belongs to the Node project (it knows the install root, the unit, the
// download shape), so the panel consumes it instead of owning it, and the Node
// module leaves this repository's dependency graph.
//
// THE READ IS NOT IMPLEMENTED HERE. internal/pkg/releaseasset owns fetching a
// signed asset out of a published Node release, because the core catalog needs
// the same four steps and a signature check written twice is one that will
// eventually disagree with itself.
//
// WHAT IS HERE IS THE PART THAT IS ABOUT INSTALLING: which release version maps
// to which tag, that the endpoint is the sync path the daemon connects to, that
// the credential fits the shared envelope, and that every marker in the template
// gets filled. Values are shell-quoted through internal/pkg/shellquote, because
// the failure that prevents is a command injection rather than a wrong answer.
package pninstall

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/releaseasset"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/shellquote"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

const (
	// templateAsset is the name the release publishes the template under. A
	// rename there is a rename here, and the read fails loudly rather than
	// rendering something else.
	templateAsset = "passwall-node-install-template.sh"

	// syncPath is the endpoint suffix the daemon's connection requires; the
	// template writes it into the environment file the daemon parses.
	syncPath = "/v1/node/sync"
)

// THE SENTINELS ARE THE PORT'S. They are two kinds — the request, and the
// publication — and a caller that had to import this package to tell them apart
// would be reaching past the boundary the port exists to draw.
var (
	// ErrNotRenderable means the request cannot be rendered at all, and is decided
	// before anything is fetched.
	ErrNotRenderable = ports.ErrInstallTemplateRequest
	// ErrUnavailable means the published release could not be read.
	ErrUnavailable = fmt.Errorf("%w: the release could not be read", ports.ErrInstallTemplateSource)
	// ErrUntrusted means the manifest is not signed by this project.
	ErrUntrusted = fmt.Errorf("%w: the release manifest is not signed by this project", ports.ErrInstallTemplateSource)
	// ErrTemplateMismatch means the template is not the file the manifest names.
	ErrTemplateMismatch = fmt.Errorf("%w: the published template does not match its manifest", ports.ErrInstallTemplateSource)
	// ErrUnknownPlaceholder means the template carries a marker this build cannot
	// fill, which would otherwise ship a script that fails on the host.
	ErrUnknownPlaceholder = fmt.Errorf("%w: the template carries a placeholder this build cannot fill", ports.ErrInstallTemplateSource)
)

// The request shape is the PORT's, not this package's. An adapter that declared
// its own would need a conversion at every call site, and the two would then be
// free to drift into exactly the mismatch the port exists to prevent.
type Options = ports.InstallTemplateRequest

// RendererOptions are the seams: the client and base URL for tests, and the
// verification key, because the production one is a release secret's counterpart.
type RendererOptions struct {
	HTTPClient *http.Client
	BaseURL    string
	PublicKey  ed25519.PublicKey
}

type Renderer struct {
	assets *releaseasset.Source
}

func New(options RendererOptions) (*Renderer, error) {
	source, err := releaseasset.NewSource(releaseasset.Options{
		HTTPClient: options.HTTPClient,
		BaseURL:    options.BaseURL,
		PublicKey:  options.PublicKey,
	})
	if err != nil {
		return nil, err
	}
	return &Renderer{assets: source}, nil
}

// Validate reports whether the request could be rendered, WITHOUT fetching
// anything.
//
// IT IS SEPARATE FROM RENDER BECAUSE SOME CALLERS ONLY VALIDATE. Two of the four
// callers discard the script: one renders the file bundle (the Docker agent path,
// where no install script is handed over at all), and one is about to MINT a
// one-shot ticket rather than hand anything over yet. Making those fetch a
// release's template would add a network round trip — and a failure mode — to
// paths that never wanted the bytes, and would refuse a Docker selection because
// a template could not be downloaded.
func (r *Renderer) Validate(options Options) error {
	_, err := prepare(options)
	return err
}

// Render returns the installation script for this identity and release.
//
// EVERY REFUSAL IS DECIDED IN ONE OF TWO PLACES: what cannot be rendered at all is
// refused BEFORE the network, and what arrived but cannot be trusted is refused
// after it. A script that reaches an operator has passed both.
func (r *Renderer) Render(ctx context.Context, options Options) (string, error) {
	tag, err := prepare(options)
	if err != nil {
		return "", err
	}
	template, err := r.assets.Asset(ctx, tag, templateAsset)
	if err != nil {
		return "", assetError(err)
	}
	return substitute(string(template), options, tag)
}

// assetError restates a shared read failure in this feature's words. The KIND is
// preserved — a caller classifying the failure still sees which of the two it is
// — and the detail is replaced so a message names the installation script rather
// than the generic asset a shared reader was asked for.
func assetError(err error) error {
	switch {
	case errors.Is(err, releaseasset.ErrUntrusted):
		return ErrUntrusted
	case errors.Is(err, releaseasset.ErrAssetMismatch):
		return ErrTemplateMismatch
	case errors.Is(err, releaseasset.ErrUnavailable):
		return fmt.Errorf("%w: %s", ErrUnavailable, err)
	default:
		return err
	}
}

// prepare validates everything the render needs and derives the tag. It contacts
// nothing, so a request that cannot be rendered never reaches the network.
func prepare(options Options) (string, error) {
	if !version.IsReleaseVersion(options.Version) {
		return "", fmt.Errorf("%w: %q is not a release version", ErrNotRenderable, options.Version)
	}
	tag, ok := version.ReleaseTagFor(options.Version)
	if !ok {
		return "", fmt.Errorf("%w: %q has no tag", ErrNotRenderable, options.Version)
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		!strings.HasSuffix(endpoint.Path, syncPath) {
		// THE SYNC PATH IS PART OF THE CONTRACT, not a convenience: the daemon
		// connects to exactly this endpoint, and a script that wrote another one
		// would install a node that never reports.
		return "", fmt.Errorf("%w: the endpoint must be an https URL ending %s with no credentials, query or fragment", ErrNotRenderable, syncPath)
	}
	if strings.TrimSpace(options.AgentID) == "" {
		return "", fmt.Errorf("%w: an agent identity is required", ErrNotRenderable)
	}
	// THE CREDENTIAL ENVELOPE IS THE SHARED CONTRACT'S, not a number written here:
	// the daemon accepts the same range, and a second copy would eventually admit a
	// credential the other end refuses.
	credential := options.Credential
	if len(credential) < nodeprotocol.MinNodeCredentialBytes || len(credential) > nodeprotocol.MaxNodeCredentialBytes {
		return "", fmt.Errorf("%w: the credential must be %d..%d bytes", ErrNotRenderable, nodeprotocol.MinNodeCredentialBytes, nodeprotocol.MaxNodeCredentialBytes)
	}
	for i := 0; i < len(credential); i++ {
		if credential[i] <= ' ' || credential[i] > '~' {
			return "", fmt.Errorf("%w: the credential must be visible ASCII with no whitespace", ErrNotRenderable)
		}
	}
	return tag, nil
}

// placeholders are the markers the template carries, in the order the Node
// project's renderer fills them. A template that gains one this build does not
// know is REFUSED rather than rendered: the script would carry `@@NEW@@` and fail
// on the host, after the operator had already run it.
var placeholders = []string{"@@VERSION@@", "@@TAG@@", "@@AGENT_ID@@", "@@ENDPOINT@@", "@@CREDENTIAL@@", "@@ENVIRONMENT@@"}

var markerPattern = regexp.MustCompile(`@@[A-Z0-9_]+@@`)

func substitute(template string, options Options, tag string) (string, error) {
	known := make(map[string]bool, len(placeholders))
	for _, placeholder := range placeholders {
		known[placeholder] = true
	}
	for _, marker := range markerPattern.FindAllString(template, -1) {
		if !known[marker] {
			return "", fmt.Errorf("%w: %s", ErrUnknownPlaceholder, marker)
		}
	}
	environment := "PSP_NODE_AGENT_ID=" + quote(options.AgentID) + "\n" +
		"PSP_NODE_ENDPOINT=" + quote(options.Endpoint) + "\n"
	replacer := strings.NewReplacer(
		"@@VERSION@@", shellquote.Quote(options.Version),
		"@@TAG@@", shellquote.Quote(tag),
		"@@AGENT_ID@@", shellquote.Quote(options.AgentID),
		"@@ENDPOINT@@", shellquote.Quote(options.Endpoint),
		"@@CREDENTIAL@@", shellquote.Quote(options.Credential),
		"@@ENVIRONMENT@@", shellquote.Quote(environment),
	)
	return replacer.Replace(template), nil
}

// quote renders a value the way the daemon's environment-file parser reads it: a
// Go-quoted string, which is the format that parser accepts. It is NOT
// shellquoting — the file is not a script, and this value is written as data.
func quote(value string) string { return strconv.Quote(value) }
