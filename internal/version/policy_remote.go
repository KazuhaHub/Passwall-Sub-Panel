package version

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
)

// Fetching a release policy.
//
// THIS IS BUILT AND NOT WIRED. Nothing calls RefreshReleasesPolicy, so installing
// the trust root and this fetch path together change no decision. That is
// deliberate and it is what the plan allows — code may be merged switched off —
// and the switch is the instance upgrade API's admission, which waits for the
// evidence in S05. Building it now means the fetch is REVIEWED on its own rather
// than as part of the change that turns it on.

// PolicyDocumentAsset and PolicySignatureAsset are the two files a policy
// publication consists of. Two files, not one: the signature is detached so the
// bytes that were signed are the bytes that are stored, and a document carrying
// its own signature invites validating the parsed form instead of the original.
const (
	PolicyDocumentAsset  = "releases-v1.json"
	PolicySignatureAsset = "releases-v1.json.sig"
)

// maxPolicyDocumentBytes bounds what the panel will read. A policy is a small
// JSON document; anything larger is a wrong URL, an error page, or an attempt to
// make the panel hold something it never asked for.
const maxPolicyDocumentBytes = 256 << 10

// policyHTTPClient is the SSRF-refusing client. A policy URL is configuration,
// which means it is a string an operator can point anywhere — the same reason
// every other operator-supplied URL in this panel goes through safehttp.
var policyHTTPClient = safehttp.NewClient(httpFetchTimeout)

// RefreshReleasesPolicy fetches the policy and its detached signature, verifies,
// validates and installs it.
//
// baseURL is passed in rather than read from a global so the caller decides where
// a policy comes from, and so a test can serve one without touching process
// state. Nothing in this build passes a base URL yet.
//
// FAILURE LEAVES THE INSTALLED POLICY ALONE. Every refusal path returns before
// installing anything, and LoadReleasesPolicy installs only after verification and
// validation both pass — so a bad publication cannot withdraw the policy a panel
// is running on. That is the opposite of what a naive "clear and reload" would
// do, and it is the property a policy fetch most needs.
func RefreshReleasesPolicy(ctx context.Context, baseURL string, now time.Time) error {
	if baseURL == "" {
		return errors.New("release policy: no source configured")
	}
	root := ActivePolicyTrustRoot()
	if root == nil {
		// Not a failure of the publication: the panel has not been given keys, so
		// no policy could be verified whatever arrived.
		return errors.New("release policy: no trust root is configured, so nothing can be verified")
	}
	document, err := fetchPolicyAsset(ctx, baseURL, PolicyDocumentAsset)
	if err != nil {
		return err
	}
	signature, err := fetchPolicyAsset(ctx, baseURL, PolicySignatureAsset)
	if err != nil {
		return err
	}
	if _, err := LoadReleasesPolicy(document, signature, root, now); err != nil {
		return fmt.Errorf("release policy from %s: %w", baseURL, err)
	}
	return nil
}

// fetchPolicyAsset reads one asset of a policy publication.
//
// A non-200 is an error rather than an empty body: a 404 for the signature must
// not read as "unsigned", and an error page must not be parsed as a document.
func fetchPolicyAsset(ctx context.Context, baseURL, asset string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, baseURL+"/"+asset, nil)
	if err != nil {
		return nil, fmt.Errorf("release policy: %s: %w", asset, err)
	}
	resp, err := policyHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("release policy: fetch %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release policy: %s returned %d", asset, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPolicyDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("release policy: read %s: %w", asset, err)
	}
	if len(body) > maxPolicyDocumentBytes {
		return nil, fmt.Errorf("release policy: %s is larger than %d bytes", asset, maxPolicyDocumentBytes)
	}
	return body, nil
}
