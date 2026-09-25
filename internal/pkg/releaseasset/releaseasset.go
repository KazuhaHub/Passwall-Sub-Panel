// Package releaseasset reads a signed asset out of a published Passwall Node
// release.
//
// WHY THIS IS ONE PACKAGE AND NOT TWO COPIES. Two panel features now consume
// something the Node project publishes — the installation script, and the
// reviewed core catalog — and both need the same four steps: find the release,
// read its checksum manifest, check the manifest's detached signature, and
// confirm the asset matches the digest the manifest names. A signature check
// written twice is a signature check that will disagree with itself, and the
// failure of the weaker copy is silent: it accepts bytes the other would refuse.
//
// THE SIGNATURE IS THE TRUST, AND THE DIGEST ALONE IS NOT. The manifest and the
// asset arrive from the same host over the same channel, so anything that could
// replace one could replace the other; a digest checked against an unsigned
// manifest establishes nothing about who produced it. The verification key is
// compiled in rather than fetched, because a key fetched from the origin the
// signature came from authenticates nothing at all — and it is a COPY of the Node
// project's, made deliberately, because that package is internal to its module.
//
// A ROTATION THERE IS A REFUSAL HERE. Pinning a publisher means a key change
// requires a panel change, and that is the point rather than an inconvenience.
package releaseasset

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
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

const (
	// ChecksumAsset and SignatureAsset are the two files every release carries.
	// The asset being read is named by the caller; the names are the Node
	// project's, and a rename there is a rename here — the read fails loudly
	// rather than returning something else.
	ChecksumAsset  = "SHA256SUMS.txt"
	SignatureAsset = "SHA256SUMS.txt.sig"

	DefaultBaseURL = "https://github.com/KazuhaHub/Passwall-Node/releases/download/"
	requestTimeout = 10 * time.Second
	maxAssetBytes  = 1 << 20

	// releaseSigningKeyBase64 is the Passwall Node release-signing public key,
	// copied from that project's internal/releaseauth.
	releaseSigningKeyBase64 = "ugFw5h6D5JY9toC182RZZW//soFUvNjpGl+Ka7FIpMk="
)

var (
	// ErrUnavailable means the release or one of its assets could not be read.
	ErrUnavailable = errors.New("release asset: the release could not be read")
	// ErrAssetMissing means the release does not carry the asset at all — a 404.
	//
	// IT IS SEPARATE FROM ErrUnavailable BECAUSE THE TWO ARE NOT THE SAME PROBLEM.
	// An unreachable origin is a moment, and a consumer may reasonably carry on
	// with what it read before. A release that does not publish the asset is a
	// statement about that release, it will say the same thing on every attempt,
	// and reporting it as a transient read failure is how a packaging mistake
	// stays hidden behind a retry. Wraps ErrUnavailable because it is still a read
	// that produced nothing.
	ErrAssetMissing = fmt.Errorf("%w: the release does not publish this asset", ErrUnavailable)
	// ErrUntrusted means the manifest is not signed by this project.
	ErrUntrusted = errors.New("release asset: the release manifest is not signed by this project")
	// ErrAssetNotPublished means the release's manifest is authentic and names no
	// such asset — the release simply does not carry it.
	//
	// IT IS A DIFFERENT FACT FROM ErrAssetMismatch, which is about bytes that
	// arrived and did not match. One is a publication that does not include the
	// file; the other is content that cannot be trusted, and they call for different
	// answers: the first is a release to pick again, the second is an event.
	ErrAssetNotPublished = errors.New("release asset: the release's manifest names no such asset")
	// ErrAssetMismatch means the asset is not the file the manifest names.
	ErrAssetMismatch = errors.New("release asset: the asset does not match its manifest")
)

// Options are the seams: the client and base URL for tests, and the verification
// key, because the production one is a release secret's counterpart.
type Options struct {
	HTTPClient *http.Client
	BaseURL    string
	PublicKey  ed25519.PublicKey
}

type Source struct {
	client *http.Client
	base   string
	key    ed25519.PublicKey
}

func NewSource(options Options) (*Source, error) {
	client := options.HTTPClient
	if client == nil {
		client = safehttp.NewClient(requestTimeout)
	} else {
		clone := *client
		if clone.Timeout <= 0 || clone.Timeout > requestTimeout {
			clone.Timeout = requestTimeout
		}
		// A REDIRECT IS REFUSED, so neither the origin's policy nor an injected
		// client's can move the read somewhere the signature was never about.
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrUnavailable }
		client = &clone
	}
	key := options.PublicKey
	if len(key) == 0 {
		decoded, err := base64.StdEncoding.DecodeString(releaseSigningKeyBase64)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("release asset: the compiled release-signing key is unusable")
		}
		key = ed25519.PublicKey(decoded)
	}
	base := options.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return &Source{client: client, base: base, key: key}, nil
}

// Asset returns the named asset of one release, and only if this project signed
// that release's manifest and the asset matches the digest it names.
func (s *Source) Asset(ctx context.Context, tag, asset string) ([]byte, error) {
	// THE ASSET NAME IS NOT BUILT FROM ANYTHING A CALLER PASSED — it is the
	// caller's own constant — but the tag is, so it is checked before it is
	// joined into a URL: a tag carrying a separator would address another release.
	if !version.IsReleaseTag(tag) {
		return nil, fmt.Errorf("%w: %q is not a release tag", ErrUnavailable, tag)
	}
	manifest, err := s.fetch(ctx, tag, ChecksumAsset)
	if err != nil {
		return nil, err
	}
	signature, err := s.fetch(ctx, tag, SignatureAsset)
	if err != nil {
		return nil, err
	}
	if err := VerifyManifest(s.key, manifest, signature); err != nil {
		return nil, err
	}
	wanted, err := digestOf(manifest, asset)
	if err != nil {
		return nil, err
	}
	body, err := s.fetch(ctx, tag, asset)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != wanted {
		return nil, fmt.Errorf("%w: %s", ErrAssetMismatch, asset)
	}
	return body, nil
}

func (s *Source) fetch(ctx context.Context, tag, asset string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+tag+"/"+asset, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, asset)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrAssetMissing, asset)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered HTTP %d", ErrUnavailable, asset, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAssetBytes+1))
	if err != nil || len(body) > maxAssetBytes {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, asset)
	}
	return body, nil
}

// VerifyManifest authenticates the exact manifest bytes before any digest from it
// is trusted. The signature is canonical base64 with an optional trailing newline.
func VerifyManifest(key ed25519.PublicKey, manifest, encodedSignature []byte) error {
	trimmed := bytes.TrimSuffix(encodedSignature, []byte("\n"))
	if len(trimmed) == 0 || bytes.ContainsAny(trimmed, " \t\r\n") {
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
	return "", fmt.Errorf("%w: the manifest names no %s", ErrAssetNotPublished, asset)
}
