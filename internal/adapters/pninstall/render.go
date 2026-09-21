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
// THE SIGNATURE IS THE TRUST, and the digest alone is not. The manifest and the
// template arrive from the same host over the same channel, so anything that could
// replace one could replace the other; the detached signature over the manifest is
// what makes a digest from it worth checking. The key is compiled in, because a key
// fetched from the origin the signature came from authenticates nothing — and it is
// a COPY of the Node project's, because that package is internal to its module.
//
// IT IS A COPY OF A THREE-CHARACTER RULE TOO, AND THAT ONE IS SHARED: quoting is
// in internal/pkg/shellquote, because the failure it prevents is a command
// injection rather than a wrong answer.
package pninstall

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/shellquote"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

const (
	// The assets a release publishes: the template itself, and the checksum
	// manifest with its detached signature. The names are the Node project's; a
	// rename there is a rename here, and the fetch fails loudly rather than
	// rendering something else.
	templateAsset  = "passwall-node-install-template.sh"
	checksumAsset  = "SHA256SUMS.txt"
	signatureAsset = "SHA256SUMS.txt.sig"

	defaultBaseURL = "https://github.com/KazuhaHub/Passwall-Node/releases/download/"
	requestTimeout = 10 * time.Second
	maxAssetBytes  = 1 << 20

	// syncPath is the endpoint suffix the daemon's connection requires; the
	// template writes it into the environment file the daemon parses.
	syncPath = "/v1/node/sync"

	// releaseSigningKeyBase64 is the Passwall Node release-signing public key,
	// copied from that project's internal/releaseauth. A ROTATION THERE IS A
	// REFUSAL HERE, deliberately: pinning a publisher means a key change requires
	// a panel change, and the alternative — fetching the key — authenticates
	// nothing at all.
	releaseSigningKeyBase64 = "ugFw5h6D5JY9toC182RZZW//soFUvNjpGl+Ka7FIpMk="
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
// verification key, because the production one is a release secret.
type RendererOptions struct {
	HTTPClient *http.Client
	BaseURL    string
	PublicKey  ed25519.PublicKey
}

type Renderer struct {
	client *http.Client
	base   string
	key    ed25519.PublicKey
}

func New(options RendererOptions) (*Renderer, error) {
	client := options.HTTPClient
	if client == nil {
		client = safehttp.NewClient(requestTimeout)
	} else {
		clone := *client
		if clone.Timeout <= 0 || clone.Timeout > requestTimeout {
			clone.Timeout = requestTimeout
		}
		// A REDIRECT IS REFUSED, so neither the origin's policy nor an injected
		// client's can move the fetch somewhere the signature was never about.
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrUnavailable }
		client = &clone
	}
	key := options.PublicKey
	if len(key) == 0 {
		decoded, err := base64.StdEncoding.DecodeString(releaseSigningKeyBase64)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("install script: the compiled release-signing key is unusable")
		}
		key = ed25519.PublicKey(decoded)
	}
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Renderer{client: client, base: base, key: key}, nil
}

// Render returns the installation script for this identity and release.
//
// EVERY REFUSAL IS DECIDED IN ONE OF TWO PLACES: what cannot be rendered at all is
// refused BEFORE the network, and what arrived but cannot be trusted is refused
// after it. A script that reaches an operator has passed both.
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

func (r *Renderer) Render(ctx context.Context, options Options) (string, error) {
	tag, err := prepare(options)
	if err != nil {
		return "", err
	}
	template, err := r.fetchTemplate(ctx, tag)
	if err != nil {
		return "", err
	}
	return substitute(template, options, tag)
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

// fetchTemplate reads the release's template and returns it only if the project
// signed its manifest and the template matches the digest that manifest names.
func (r *Renderer) fetchTemplate(ctx context.Context, tag string) (string, error) {
	manifest, err := r.fetch(ctx, tag, checksumAsset)
	if err != nil {
		return "", err
	}
	signature, err := r.fetch(ctx, tag, signatureAsset)
	if err != nil {
		return "", err
	}
	if err := verifyManifest(r.key, manifest, signature); err != nil {
		return "", err
	}
	wanted, err := digestOf(manifest, templateAsset)
	if err != nil {
		return "", err
	}
	body, err := r.fetch(ctx, tag, templateAsset)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != wanted {
		return "", fmt.Errorf("%w: %s", ErrTemplateMismatch, templateAsset)
	}
	return string(body), nil
}

func (r *Renderer) fetch(ctx context.Context, tag, asset string) ([]byte, error) {
	// THE ASSET NAME IS NOT INTERPOLATED FROM ANYTHING A CALLER PASSED: it is one of
	// three constants, and the tag came from a validated version. The join is still
	// checked because a tag with a separator in it would address another release.
	if !version.IsReleaseTag(tag) {
		return nil, fmt.Errorf("%w: %q is not a release tag", ErrUnavailable, tag)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+tag+"/"+asset, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	response, err := r.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, asset)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered HTTP %d", ErrUnavailable, asset, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAssetBytes+1))
	if err != nil || len(body) > maxAssetBytes {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, asset)
	}
	return body, nil
}

// verifyManifest authenticates the exact manifest bytes before any digest from it
// is trusted. The signature is canonical base64 with an optional trailing newline.
func verifyManifest(key ed25519.PublicKey, manifest, encodedSignature []byte) error {
	trimmed := bytes.TrimSuffix(encodedSignature, []byte("\n"))
	if len(trimmed) == 0 || bytes.IndexAny(trimmed, " \t\r\n") >= 0 {
		return fmt.Errorf("%w: the signature has invalid framing", ErrUntrusted)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(string(trimmed))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: the signature is malformed", ErrUntrusted)
	}
	if !ed25519.Verify(key, manifest, signature) {
		return fmt.Errorf("%w: the signature does not verify", ErrUntrusted)
	}
	return nil
}

// digestOf finds an asset's digest in a sha256sum manifest.
func digestOf(manifest []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// `sha256sum` writes the name plainly, and `sha256sum --binary` writes it
		// with a leading `*`; both name the same file.
		if strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			break
		}
		return strings.ToLower(fields[0]), nil
	}
	return "", fmt.Errorf("%w: the manifest names no %s", ErrTemplateMismatch, asset)
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
