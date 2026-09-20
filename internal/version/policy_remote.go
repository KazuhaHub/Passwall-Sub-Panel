package version

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
)

// Fetching a release policy.
//
// IT RUNS ONLY WHEN A SOURCE IS CONFIGURED. Neither the source nor the keys exist
// in a default deployment, so nothing is fetched and no admission decision
// changes; a deployment that sets them has opted in. The plan sequences enabling
// the policy path after S05's evidence, and `policy_enforce` is what actually
// switches admission — loading a policy reports it and gates nothing.

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
	// ONE REFRESH AT A TIME. Two callers fetching the same publication would
	// install it twice, and the second install would compare an equal revision
	// and report a state that looks like a refusal for no stated reason.
	policyRefreshMu.Lock()
	defer policyRefreshMu.Unlock()
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

// Background refresh.
//
// A policy loaded at boot is only current until the next publication. The plan's
// parameters are 30 minutes with jitter, and a refresh that cannot overlap itself
// — a manual refresh and the loop must not both be fetching, because the second
// would install a document the first already installed and the revision check
// would report a confusing "equal revision" for no reason.

// policyRefreshMu serialises refreshes. A fetch is cheap and rare, so the cost of
// holding the lock across it is a blocked second caller, not a stalled panel —
// and the alternative is two installs racing on the same global state.
var policyRefreshMu sync.Mutex

// policyMaxBackoff bounds the failure backoff. A source that is down should be
// retried less often, not never, and not at a rate that turns an outage into
// load.
const policyMaxBackoff = 4 * time.Hour

// RunPolicyRefresh refreshes the policy until ctx is done.
//
// jitterFn is a parameter rather than a call to math/rand inside, so a test can
// pin the schedule. Production passes a jittered interval.
//
// A failure does NOT stop the loop: the panel keeps whatever policy it has, and
// the backoff grows so a source that stays down is not hammered.
func RunPolicyRefresh(ctx context.Context, source string, interval time.Duration, jitterFn func(time.Duration) time.Duration, now func() time.Time) {
	if source == "" || interval <= 0 {
		return
	}
	if jitterFn == nil {
		jitterFn = func(d time.Duration) time.Duration { return d }
	}
	if now == nil {
		now = time.Now
	}
	backoff := interval
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitterFn(backoff)):
		}
		// The clock is read here rather than captured, so a policy whose window
		// closed while the panel was idle is refused on the next attempt.
		if err := RefreshReleasesPolicy(ctx, source, now().UTC()); err != nil {
			backoff *= 2
			if backoff > policyMaxBackoff {
				backoff = policyMaxBackoff
			}
			continue
		}
		backoff = interval
	}
}
