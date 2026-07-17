package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Client implements ports.XUIClient for a single 3X-UI panel.
//
// Auth priority:
//  1. If APIToken is non-empty, requests use Authorization: Bearer.
//  2. Otherwise the client logs in with username/password once and reuses
//     the cookie session; on 401 it re-logs in transparently.
//
// All client-write operations (AddClient/UpdateClient) follow a
// read-modify-write pattern: fetch the current Inbound, mutate
// settings.clients[], then post the modified object back. This is the only
// way to avoid clobbering pre-existing clients that the panel does not
// manage (e.g. the operator's personal clients).
type Client struct {
	panelName string
	baseURL   string
	apiToken  string
	username  string
	password  string

	http   *http.Client
	mu     sync.Mutex
	jar    http.CookieJar
	authed bool
	// csrfToken is the per-session CSRF token fetched after a cookie-mode
	// login. 3X-UI 3.2.x+ requires it as X-CSRF-Token on unsafe methods in
	// cookie (username/password) mode; Bearer-token mode bypasses CSRF and
	// leaves this empty. Guarded by mu alongside authed.
	csrfToken string

	// inboundWriteLocks serializes read-modify-write inbound updates per
	// inbound ID within this process. UpdateInbound GETs the whole inbound,
	// re-injects settings.clients[], and POSTs it back; two concurrent writers
	// on the same inbound (e.g. reconcile axis-A and a node edit) would each
	// act on the same snapshot and the later POST would silently drop the
	// earlier change. Idle keyed locks are removed after the final waiter. (Client writes no
	// longer use this — 3.2.0's /clients/* endpoints are email-keyed and need
	// no inbound read-modify-write.)
	inboundWriteLocks keyedmutex.Map[int]
}

// Capabilities advertises the operations implemented by the 3X-UI adapter.
// Endpoint-level version gates (for example web cert files) are still checked
// by the method itself; this list describes the adapter surface.
func (c *Client) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{
		ports.CapabilityInboundRead,
		ports.CapabilityInboundWrite,
		ports.CapabilityInboundCreate,
		ports.CapabilityInboundUpdate,
		ports.CapabilityInboundDelete,
		ports.CapabilityInboundEnable,
		ports.CapabilityClientRead,
		ports.CapabilityClientWrite,
		ports.CapabilityTrafficRead,
		ports.CapabilityStatusRead,
		ports.CapabilityPanelUpgrade,
		ports.CapabilityCoreUpgrade,
		ports.CapabilityWebCertRead,
		ports.CapabilityRealityScan,
	}
}

// clientWriteLocks serializes mutating client operations per (backend, email)
// GLOBALLY across all *Client instances in this process. 3X-UI's first-class
// client endpoints (/clients/add, /update, /addClient (attach), /delClient
// (detach), /del) are NOT safe to run concurrently on the SAME client: two
// in-flight mutations race on 3X-UI's client_inbounds join table and one fails
// with "email already in use" or "UNIQUE constraint failed: client_inbounds.
// client_id, client_inbounds.inbound_id" (the whole transaction rolls back, so
// the enable/expiry change is lost). PSP has several concurrent sources on one
// client — the sync-task migrate/resync drain, the 2-min traffic-poll lifecycle
// push, the reconcile/boot heal, and admin enable/disable.
//
// The lock is keyed by baseURL+email and lives at PACKAGE scope — NOT per *Client
// — on purpose: if one physical 3X-UI server is registered as MORE THAN ONE PSP
// panel (e.g. an "OLD" and a current registration of the same host), the Pool
// holds a distinct *Client per panel, the shared client is provisioned through
// BOTH, and a per-*Client lock would NOT serialize those two writers hitting the
// same backend row. Keying on the backend URL makes every panel fronting the same
// 3X-UI share one lock. Different clients (and different backends) still mutate in
// parallel. Reads (GetClient/ListInbounds) are unlocked — only mutations conflict.
var clientWriteLocks keyedmutex.Map[string] // key: baseURL "\x00" email

// lockClientEmail acquires the global per-(backend,email) write lock and returns
// its unlock func (use `defer c.lockClientEmail(email)()`). See clientWriteLocks.
// An empty email is a no-op (returns a nil-safe unlock) so callers needn't pre-check.
func (c *Client) lockClientEmail(email string) func() {
	if email == "" {
		return func() {}
	}
	return clientWriteLocks.Lock(c.baseURL + "\x00" + email)
}

// isInboundConflict reports whether a client mutation failed on 3X-UI's transient
// "UNIQUE constraint failed: client_inbounds..." — raised when two writers touch
// one inbound's client_inbounds join rows at once. 3X-UI rolls the whole
// transaction back, so the change did NOT apply and a retry can clear it.
func isInboundConflict(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "client_inbounds")
}

// mutateWithRetry runs a client-mutation POST, retrying on isInboundConflict with a
// short jittered backoff. lockClientEmail already serializes SAME-process writes to
// one client; this additionally survives CROSS-process / multi-instance races
// (e.g. a second PSP instance, or a not-yet-stopped old container, running the same
// migrate/poll loops against the same panel) that a per-process lock cannot cover —
// the racer commits in ~ms, so an immediate jittered retry succeeds. Bounded so a
// genuinely stuck client surfaces the error instead of looping forever.
func (c *Client) mutateWithRetry(ctx context.Context, path string, body any) error {
	const maxAttempts = 5
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err = c.doJSON(ctx, http.MethodPost, path, body, nil); err == nil || !isInboundConflict(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt)*40*time.Millisecond + rand.N(60*time.Millisecond)):
		}
	}
	return err
}

// lockClientEmails locks several emails at once (BulkDelByEmail), acquiring in
// sorted+deduped order so two overlapping bulk calls can't deadlock. Returns a
// single unlock func that releases all of them.
func (c *Client) lockClientEmails(emails []string) func() {
	uniq := make([]string, 0, len(emails))
	seen := make(map[string]bool, len(emails))
	for _, e := range emails {
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		uniq = append(uniq, e)
	}
	sort.Strings(uniq)
	unlocks := make([]func(), 0, len(uniq))
	for _, e := range uniq {
		unlocks = append(unlocks, c.lockClientEmail(e))
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// New constructs a Client for the given 3X-UI panel.
//
// The transport is built via safehttp.NewClient so the dialer refuses
// to connect to loopback / link-local / unspecified addresses (notably
// the 169.254.169.254 cloud-metadata endpoint). The panel URL is
// admin-supplied DB content; without the guard a compromised admin
// account or a stored XSS could point the "panel" at internal services
// and trick PSP into proxying unauthenticated GETs/POSTs there. Private
// LAN ranges (10/8, 172.16/12, 192.168/16) remain reachable because
// legitimate self-hosted 3X-UI deployments live there.
func New(p *domain.XUIPanel) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	// InsecureSkipVerify relaxes ONLY cert validation (for a self-signed panel);
	// the SSRF dial guard is unchanged.
	httpClient := safehttp.NewClientTLS(30*time.Second, p.InsecureSkipVerify)
	httpClient.Jar = jar
	// Resolve the effective Bearer token from the explicit auth method. The rest
	// of the client keys auth off apiToken != "" (Bearer) vs "" (cookie login),
	// so password mode forces it empty even when a token is also stored, and
	// auto mode keeps the legacy infer-from-presence behavior.
	apiToken := p.APIToken
	if p.AuthMethod == domain.XUIAuthPassword {
		apiToken = ""
	}
	return &Client{
		panelName: p.Name,
		baseURL:   strings.TrimRight(p.URL, "/"),
		apiToken:  apiToken,
		username:  p.Username,
		password:  p.Password,
		http:      httpClient,
		jar:       jar,
	}, nil
}

// lockInbound serializes read-modify-write updates to one inbound within this
// process and returns the unlock func. Use as: defer c.lockInbound(id)().
func (c *Client) lockInbound(id int) func() {
	return c.inboundWriteLocks.Lock(id)
}

// --- Auth ---

func (c *Client) ensureAuth(ctx context.Context) error {
	if c.apiToken != "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authed {
		return nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", c.username)
	form.Set("password", c.password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("login: %w (raw: %s)", err, string(b))
	}
	if !r.Success {
		return fmt.Errorf("login failed: %s", r.Msg)
	}
	c.authed = true
	// Cookie mode needs a CSRF token for unsafe methods on 3X-UI 3.2.x+.
	// Best-effort and intentionally ignored: a pre-CSRF panel (no /csrf-token
	// route) or a transient failure leaves the token empty — reads still work,
	// and a write that then 401/403s re-auths and re-fetches once (doJSONRetry).
	_ = c.fetchCSRFLocked(ctx)
	return nil
}

// fetchCSRFLocked retrieves and caches a CSRF token for cookie-mode unsafe
// requests. Called from loginLocked with c.mu held, so it must NOT route
// through doJSON (which re-acquires c.mu via ensureAuth → deadlock); it issues
// a raw GET instead. The returned error is for testability — loginLocked
// ignores it (best-effort by design). On any failure the cached token is
// cleared so a stale token from a prior session never lingers.
func (c *Client) fetchCSRFLocked(ctx context.Context) error {
	c.csrfToken = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/csrf-token", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("csrf-token: HTTP %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("csrf-token: parse: %w", err)
	}
	if !r.Success {
		return fmt.Errorf("csrf-token: %s", r.Msg)
	}
	var token string
	if err := json.Unmarshal(r.Obj, &token); err != nil {
		return fmt.Errorf("csrf-token: decode obj: %w", err)
	}
	c.csrfToken = token
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) (err error) {
	return c.doJSONWithClient(ctx, method, path, body, out, c.http)
}

// doJSONWithTimeout is the long-running-request variant used by REALITY CIDR
// discovery. A 512-target scan runs in bounded waves on 3X-UI and can
// legitimately exceed the normal adapter-wide 30s timeout; copying http.Client
// only changes this call's deadline while reusing the same transport and jar.
func (c *Client) doJSONWithTimeout(ctx context.Context, method, path string, body any, out any, timeout time.Duration) error {
	hc := *c.http
	hc.Timeout = timeout
	return c.doJSONWithClient(ctx, method, path, body, out, &hc)
}

func (c *Client) doJSONWithClient(ctx context.Context, method, path string, body any, out any, hc *http.Client) (err error) {
	// Tag every request error with which PANEL it came from. The 3X-UI API path is
	// relative (e.g. /panel/api/clients/update/<email>), so in a multi-panel
	// deployment a bare error can't tell you which 3X-UI failed — which made a
	// client_inbounds failure impossible to localize. %w preserves the underlying
	// message so isInboundConflict / errors.Is still match.
	defer func() {
		if err != nil {
			err = fmt.Errorf("[%s] %w", c.reqTag(), err)
		}
	}()
	return c.doJSONRetry(ctx, method, path, body, out, false, hc)
}

// reqTag identifies the panel a request belongs to, for error messages. Format:
// `name @ host` (or just host when the name is unset, e.g. in tests).
func (c *Client) reqTag() string {
	host := c.baseURL
	if u, perr := url.Parse(c.baseURL); perr == nil && u.Host != "" {
		host = u.Host
	}
	if c.panelName != "" {
		return c.panelName + " @ " + host
	}
	return host
}

// doJSONRetry issues the request and, in cookie-auth mode, transparently
// re-authenticates and retries EXACTLY ONCE on a 401. The retried flag bounds
// the work to two requests per call. Without it, a panel whose /login returns
// success but whose protected API path keeps answering 401 (reverse-proxy
// path-scoped auth, webBasePath mismatch, rotated session secret) drives
// unbounded non-tail recursion → a fatal stack overflow that safego's
// recover() cannot shield, taking the whole process down. On a second
// consecutive 401 we surface a normal auth error the task/probe paths already
// handle instead of recursing.
func (c *Client) doJSONRetry(ctx context.Context, method, path string, body any, out any, retried bool, hc *http.Client) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	var reader io.Reader
	contentType := ""
	if body != nil {
		if form, ok := body.(url.Values); ok {
			reader = strings.NewReader(form.Encode())
			contentType = "application/x-www-form-urlencoded"
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(b)
			contentType = "application/json"
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	} else if isUnsafeMethod(method) {
		// Cookie mode: 3X-UI 3.2.x+ requires X-CSRF-Token on unsafe methods.
		// Empty token (pre-CSRF panel / fetch failed) sends no header — the
		// request either succeeds (old panel) or 403s into the re-auth retry.
		c.mu.Lock()
		tok := c.csrfToken
		c.mu.Unlock()
		if tok != "" {
			req.Header.Set("X-CSRF-Token", tok)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// In cookie mode a 401 (expired session) or 403 (stale/missing CSRF token
	// after a session rotation) means drop the cached session + CSRF token,
	// re-login (which re-fetches the token), and retry exactly once. Bearer
	// mode never reaches here (apiToken is set), so a Bearer 403 stays a
	// permanent ErrValidation below.
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && c.apiToken == "" {
		if retried {
			// Re-auth already happened and the path is still rejected — stop
			// here. Returning a plain (transient-classified) error lets the
			// bounded task-level retry/backoff handle it instead of recursing.
			return fmt.Errorf("%s %s: still HTTP %d after re-authentication — "+
				"login succeeds but the API path stays rejected "+
				"(check reverse-proxy path auth, webBasePath, CSRF, or a rotated session secret)",
				method, path, resp.StatusCode)
		}
		c.mu.Lock()
		c.authed = false
		c.csrfToken = ""
		c.mu.Unlock()
		if err := c.ensureAuth(ctx); err != nil {
			return err
		}
		return c.doJSONRetry(ctx, method, path, body, out, true, hc)
	}

	b, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(b))

	// Distinguish common 3X-UI failure shapes from a real JSON parse error
	// so the operator gets an actionable message instead of "unexpected end
	// of JSON input".
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 4xx (except 401/408/429) indicates the request itself is wrong —
		// invalid spec, missing field, wrong id. Wrap in ErrValidation so
		// task runners can mark the task permanently failed instead of
		// retrying forever. 401 means re-auth (handled above), 408/429 are
		// transient and stay as raw errors so retry logic kicks in.
		// 5xx and network errors also stay raw → retried.
		base := fmt.Errorf("%s %s: HTTP %d (body: %s)",
			method, path, resp.StatusCode, snippet(trimmed, 200))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
			resp.StatusCode != http.StatusUnauthorized &&
			resp.StatusCode != http.StatusRequestTimeout &&
			resp.StatusCode != http.StatusTooManyRequests {
			// A 404 means the route isn't registered on this panel — a
			// version-gated endpoint (e.g. getWebCertFiles, added in 3X-UI
			// 3.2.7) called against an older panel. Mark it
			// ErrXUIEndpointUnsupported IN ADDITION to the permanent-failure
			// ErrValidation so callers can errors.Is and degrade gracefully
			// instead of surfacing a generic validation error.
			if resp.StatusCode == http.StatusNotFound {
				return fmt.Errorf("%w: %w: %s", domain.ErrValidation, ports.ErrXUIEndpointUnsupported, base.Error())
			}
			return fmt.Errorf("%w: %s", domain.ErrValidation, base.Error())
		}
		return base
	}
	if trimmed == "" {
		// A blank 200 with no JSON has two common causes: (1) wrong auth — 3X-UI
		// answers an empty body when the api_token / username+password is invalid;
		// (2) a reverse proxy / WAF / CDN in front of the panel intercepting THIS
		// endpoint (a blank 200 with no Content-Type on one path while every other
		// endpoint returns JSON is the tell — verify by hitting the panel directly,
		// bypassing the proxy). Don't assume auth: if other endpoints on this panel
		// work, it's almost certainly the proxy/WAF on this path.
		hint := "any of: (1) auth is wrong (verify api_token / username+password); (2) a reverse proxy / WAF in front of the panel is intercepting this endpoint; (3) the panel's x-ui process is hung/half-broken (a restart often fixes it — LIVE-OBSERVED: a hung x-ui returned a blank 200 on clients/add while every other endpoint worked, and `x-ui restart` cleared it). If OTHER endpoints on this panel work, it is NOT auth — suspect the proxy or a stuck x-ui process and test the panel directly"
		return fmt.Errorf("%s %s: empty response body (HTTP %d) — %s",
			method, path, resp.StatusCode, hint)
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return fmt.Errorf("%s %s: non-JSON response (HTTP %d) — likely an auth redirect or wrong endpoint (preview: %s)",
			method, path, resp.StatusCode, snippet(trimmed, 120))
	}

	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("%s %s: parse: %w (raw: %s)", method, path, err, snippet(trimmed, 200))
	}
	if !r.Success {
		base := fmt.Errorf("%s %s: %s", method, path, r.Msg)
		// 3X-UI signals permanent client-level rejections as HTTP 200 +
		// {success:false} (duplicate email on /clients/add, "client not found"
		// on update/del) rather than a 4xx — so the generic 4xx→ErrValidation
		// wrapping above never fires for them. Without this, the sync-task
		// runners classify a duplicate-add as transient and burn the full
		// ~100-attempt retry budget hammering the panel for an unsatisfiable
		// op. Wrap the known-permanent shapes in ErrValidation so they fail
		// fast — mirroring node.go's "port already exists" handling for the
		// inbound case.
		if isPermanentPanelMsg(r.Msg) {
			return fmt.Errorf("%w: %s", domain.ErrValidation, base.Error())
		}
		return base
	}
	if out != nil && len(r.Obj) > 0 {
		if err := json.Unmarshal(r.Obj, out); err != nil {
			return fmt.Errorf("%s %s: decode obj: %w", method, path, err)
		}
	}
	return nil
}

// isPermanentPanelMsg reports whether a 3X-UI {success:false} message
// describes a permanent (non-retryable) condition — a duplicate/conflicting
// create or a missing target — rather than a transient panel/network blip.
// These are returned with HTTP 200, so they bypass the 4xx→ErrValidation
// path; callers wrap them in domain.ErrValidation so task runners fail fast
// instead of retrying to the attempt cap.
func isPermanentPanelMsg(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "duplicate") ||
		strings.Contains(m, "already exist") ||
		// AddInbound port conflict. "already exist" above catches the old
		// "port already exists" wording; >= 3.4.2 panels return the
		// transport-aware "port <p> (<proto>) already used by inbound ..."
		// form (web/service/port_conflict.go) — a permanent condition too.
		strings.Contains(m, "already used by inbound") ||
		strings.Contains(m, "client not found") ||
		strings.Contains(m, "not found in inbound")
}

// isUnsafeMethod reports whether m is a state-changing HTTP method that 3X-UI's
// cookie-mode CSRF middleware guards. PSP only issues GET and POST, but the
// full set is listed for correctness.
func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// snippet truncates s to n chars with an ellipsis, suitable for embedding
// in an error message without dumping a huge HTML body to logs.
func snippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- Inbound ---

func (c *Client) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	return c.listInbounds(ctx, "/panel/api/inbounds/list")
}

// ListInboundsSlim hits /panel/api/inbounds/list/slim — identical per-inbound
// shape (and full clientStats: up/down/total/email/lastOnline/...) but with
// settings.clients[] trimmed to {email,enable} and clientStats not enriched
// with uuid/subId. The traffic poll only reads clientStats, so the slim
// payload carries everything it needs while dropping the per-client settings
// blobs that dominate the response on panels with thousands of clients. The
// slim route is live-verified present on the min_xui=3.2.0 floor, so there's
// no version fallback (consistent with PSP's hard-cut compat model).
func (c *Client) ListInboundsSlim(ctx context.Context) ([]ports.Inbound, error) {
	return c.listInbounds(ctx, "/panel/api/inbounds/list/slim")
}

func (c *Client) listInbounds(ctx context.Context, path string) ([]ports.Inbound, error) {
	var raws []rawInbound
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raws); err != nil {
		return nil, err
	}
	out := make([]ports.Inbound, len(raws))
	for i := range raws {
		out[i] = rawToInbound(&raws[i])
	}
	return out, nil
}

func (c *Client) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	var raw rawInbound
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/inbounds/get/"+strconv.Itoa(id), nil, &raw); err != nil {
		return nil, err
	}
	in := rawToInbound(&raw)
	return &in, nil
}

func (c *Client) AddInbound(ctx context.Context, spec ports.InboundSpec) (int, error) {
	// PSP stores its inbound snapshot client-less (inboundcfg.StripClients), so
	// the settings reaching here usually carry no clients[] field. 3X-UI's
	// POST /clients/add appends to settings.clients IN PLACE; if that field is
	// absent the append blanks out — HTTP 200, empty body, no client created,
	// no log — so a freshly created/recreated inbound becomes permanently
	// un-addable. VLESS only escaped because 3X-UI re-adds clients:[] itself on
	// inbound creation; SHADOWSOCKS does not. Guarantee the array at creation
	// time. (Verified live on 3X-UI 3.4.0: SS inbound minus clients[] → blank
	// 200 on every /clients/add; with clients:[] → succeeds.)
	spec.Settings = ensureClientsArray(spec.Settings)
	body := specToRaw(&spec, 0)
	var out rawInbound
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/add", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func (c *Client) UpdateInbound(ctx context.Context, id int, spec ports.InboundSpec) error {
	defer c.lockInbound(id)()
	mergedSpec := spec
	settings, err := c.settingsWithCurrentClients(ctx, id, spec.Settings)
	if err != nil {
		return err
	}
	// Guarantee clients[] even when the RMW found zero live clients (it returns the
	// snapshot verbatim, which PSP stores client-less) — otherwise updating to a
	// clients-less SS inbound leaves /clients/add blank-200ing. See AddInbound.
	mergedSpec.Settings = ensureClientsArray(settings)
	body := specToRaw(&mergedSpec, id)
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/update/"+strconv.Itoa(id), body, nil)
}

func (c *Client) DelInbound(ctx context.Context, id int) error {
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/del/"+strconv.Itoa(id), nil, nil)
}

func (c *Client) SetInboundEnable(ctx context.Context, id int, enable bool) error {
	body := map[string]any{"enable": enable}
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/setEnable/"+strconv.Itoa(id), body, nil)
}

// --- Client (3X-UI 3.2.0 first-class /clients/* API) ---
//
// 3.2.0 removed the inbound-scoped per-client endpoints (addClient,
// delClient*, copyClients, getClientTraffics*, resetClientTraffic) and made
// clients first-class entities keyed by their panel-wide unique email. PSP's
// u{userID}-n{nodeID}@domain scheme keeps that email unique per (panel,
// inbound), so the inboundID / clientUUID arguments below are vestigial —
// retained only for source-compatibility with existing callers. See
// docs/3xui-3.2-clients-migration.md.

// AddClient creates a first-class client and attaches it to inboundID in one
// call. Thin single-inbound wrapper over AddClientToInbounds; retained for the
// existing per-node callers. Per-protocol secrets present in spec are sent
// verbatim; the panel generates any omitted ones.
func (c *Client) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return c.AddClientToInbounds(ctx, []int{inboundID}, spec)
}

// AddClientToInbounds creates one first-class client attached to every id in
// inboundIDs in a single POST /clients/add (body {client, inboundIds}) — one
// Xray restart regardless of fan-out. Backs the v3.9.0 shared-client model.
func (c *Client) AddClientToInbounds(ctx context.Context, inboundIDs []int, spec ports.ClientSpec) error {
	if len(inboundIDs) == 0 {
		return fmt.Errorf("AddClientToInbounds: at least one inbound id is required")
	}
	defer c.lockClientEmail(spec.Email)()
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return err
	}
	body := map[string]any{
		"client":     json.RawMessage(clientJSON),
		"inboundIds": inboundIDs,
	}
	return c.mutateWithRetry(ctx, "/panel/api/clients/add", body)
}

// AttachClient attaches the existing client identified by email to the given
// inbounds (POST /clients/{email}/attach, body {inboundIds}). Ids it is already
// on are no-ops upstream. Empty inboundIDs sends no request.
func (c *Client) AttachClient(ctx context.Context, email string, inboundIDs []int) error {
	if email == "" {
		return fmt.Errorf("AttachClient: email is required")
	}
	if len(inboundIDs) == 0 {
		return nil
	}
	defer c.lockClientEmail(email)()
	path := "/panel/api/clients/" + url.PathEscape(email) + "/attach"
	return c.mutateWithRetry(ctx, path, map[string]any{"inboundIds": inboundIDs})
}

// DetachClient removes the client identified by email from the given inbounds
// (POST /clients/{email}/detach) without deleting the client record. Pairs
// where it is not attached are silent no-ops. Empty inboundIDs sends no request.
func (c *Client) DetachClient(ctx context.Context, email string, inboundIDs []int) error {
	if email == "" {
		return fmt.Errorf("DetachClient: email is required")
	}
	if len(inboundIDs) == 0 {
		return nil
	}
	defer c.lockClientEmail(email)()
	path := "/panel/api/clients/" + url.PathEscape(email) + "/detach"
	return c.mutateWithRetry(ctx, path, map[string]any{"inboundIds": inboundIDs})
}

// BulkAttach attaches many existing clients to many inbounds in one
// POST /clients/bulkAttach (single Xray restart). The panel returns
// {attached, skipped, errors}; absent fields decode as empty slices. Empty
// emails or inboundIDs is a no-op.
func (c *Client) BulkAttach(ctx context.Context, emails []string, inboundIDs []int) (ports.BulkAttachResult, error) {
	if len(emails) == 0 || len(inboundIDs) == 0 {
		return ports.BulkAttachResult{}, nil
	}
	defer c.lockClientEmails(emails)()
	body := map[string]any{"emails": emails, "inboundIds": inboundIDs}
	var out struct {
		Attached []string          `json:"attached"`
		Skipped  []string          `json:"skipped"`
		Errors   []json.RawMessage `json:"errors"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/clients/bulkAttach", body, &out); err != nil {
		return ports.BulkAttachResult{}, err
	}
	return ports.BulkAttachResult{Done: out.Attached, Skipped: out.Skipped, Errors: bulkErrStrings(out.Errors)}, nil
}

// BulkDetach detaches many clients from many inbounds in one
// POST /clients/bulkDetach (single Xray restart). Mirror of BulkAttach; the
// panel keeps client records even if they end up orphaned. Empty inputs no-op.
func (c *Client) BulkDetach(ctx context.Context, emails []string, inboundIDs []int) (ports.BulkAttachResult, error) {
	if len(emails) == 0 || len(inboundIDs) == 0 {
		return ports.BulkAttachResult{}, nil
	}
	defer c.lockClientEmails(emails)()
	body := map[string]any{"emails": emails, "inboundIds": inboundIDs}
	var out struct {
		Detached []string          `json:"detached"`
		Skipped  []string          `json:"skipped"`
		Errors   []json.RawMessage `json:"errors"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/clients/bulkDetach", body, &out); err != nil {
		return ports.BulkAttachResult{}, err
	}
	return ports.BulkAttachResult{Done: out.Detached, Skipped: out.Skipped, Errors: bulkErrStrings(out.Errors)}, nil
}

// BulkCreateClients creates many NEW clients in one POST /clients/bulkCreate —
// the body is a JSON ARRAY of {client, inboundIds} (the same per-item shape
// AddClientToInbounds sends), so a node-provision / heal fan-out of N creates
// collapses to ONE Xray restart. ensureClients: callers pass only emails not yet
// present on the panel (already-present ones go through BulkAttach), but a race
// is harmless — the panel skips an existing email and the per-user resync heals
// it. Items with no target inbound are dropped (a create needs a destination).
func (c *Client) BulkCreateClients(ctx context.Context, items []ports.BulkCreateClientItem) (ports.BulkCreateResult, error) {
	if len(items) == 0 {
		return ports.BulkCreateResult{}, nil
	}
	emails := make([]string, 0, len(items))
	arr := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if len(it.InboundIDs) == 0 {
			continue
		}
		clientJSON, err := buildClientJSON(it.Spec)
		if err != nil {
			return ports.BulkCreateResult{}, err
		}
		emails = append(emails, it.Spec.Email)
		arr = append(arr, map[string]any{"client": json.RawMessage(clientJSON), "inboundIds": it.InboundIDs})
	}
	if len(arr) == 0 {
		return ports.BulkCreateResult{}, nil
	}
	defer c.lockClientEmails(emails)()
	var out struct {
		Created int `json:"created"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/clients/bulkCreate", arr, &out); err != nil {
		return ports.BulkCreateResult{}, err
	}
	return ports.BulkCreateResult{Created: out.Created}, nil
}

// bulkErrStrings normalises the panel's bulkAttach/bulkDetach "errors" entries
// to plain strings. The field is empty in the common case; its element shape is
// not pinned in the API spec, so an entry that is a JSON string is unquoted and
// anything else (e.g. an object) is kept as its raw JSON text — never dropped.
func bulkErrStrings(raws []json.RawMessage) []string {
	if len(raws) == 0 {
		return nil
	}
	out := make([]string, 0, len(raws))
	for _, r := range raws {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, s)
		} else {
			out = append(out, string(r))
		}
	}
	return out
}

// UpdateClient replaces the client keyed by spec.Email (POST
// /clients/update/{email}); the change propagates to every inbound the client
// is attached to. Full-replace semantics: the body carries the complete
// intended state (buildClientJSON emits every field PSP manages), so fields
// PSP does not set — e.g. a manually-added comment — are not preserved. A
// UUID rotation is simply a new "id" under the unchanged email, so the old
// clientUUID argument is unused.
func (c *Client) UpdateClient(ctx context.Context, inboundID int, clientUUID string, spec ports.ClientSpec) error {
	if spec.Email == "" {
		return fmt.Errorf("UpdateClient: spec.Email is required (3.2.0 keys clients by email)")
	}
	defer c.lockClientEmail(spec.Email)()
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return err
	}
	path := "/panel/api/clients/update/" + url.PathEscape(spec.Email)
	return c.mutateWithRetry(ctx, path, json.RawMessage(clientJSON))
}

// UpdateClientWithInbound delegated to a read-modify-write of the inbound in
// the ≤3.1.x era to save a GetInbound round-trip. 3.2.0 updates clients by
// email with no inbound read, so the pre-fetched inbound is unused; this
// delegates to UpdateClient. Kept for caller source-compatibility.
func (c *Client) UpdateClientWithInbound(ctx context.Context, inb *ports.Inbound, clientUUID string, spec ports.ClientSpec) error {
	if inb == nil {
		return fmt.Errorf("UpdateClientWithInbound: inb is nil")
	}
	return c.UpdateClient(ctx, inb.ID, clientUUID, spec)
}

// DelClientByEmail deletes the client by its panel-wide email key (POST
// /clients/del/{email}). This removes the client from every inbound it is
// attached to. keepTraffic=0 drops the xray traffic row too; PSP keeps its own
// accounting. Callers are responsible for scoping deletes to PSP-managed
// legacy or shared-client emails before reaching here.
func (c *Client) DelClientByEmail(ctx context.Context, inboundID int, email string) error {
	defer c.lockClientEmail(email)()
	path := "/panel/api/clients/del/" + url.PathEscape(email) + "?keepTraffic=0"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// BulkDelByEmail deletes many clients by email via POST
// /panel/api/clients/bulkDel. The body is {emails, keepTraffic:false} (a bare
// array is rejected — the panel decodes into a struct). Emails already absent
// upstream are silently skipped (not counted). Returns the panel-reported
// deleted count. An empty emails slice is a no-op.
func (c *Client) BulkDelByEmail(ctx context.Context, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	defer c.lockClientEmails(emails)()
	body := map[string]any{"emails": emails, "keepTraffic": false}
	var out struct {
		Deleted int `json:"deleted"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/clients/bulkDel", body, &out); err != nil {
		return 0, err
	}
	return out.Deleted, nil
}

// GetServerStatus hits /panel/api/server/status and returns the
// version-identity subset (panel + xray). 3X-UI 3.1.0 reports panelVersion
// as "3.1.0" while /panel/api/server/getPanelUpdateInfo reports the same
// release as "v3.1.0"; version.parseSemver tolerates both forms.
func (c *Client) GetServerStatus(ctx context.Context) (*ports.ServerStatus, error) {
	var raw struct {
		PanelVersion string `json:"panelVersion"`
		Xray         struct {
			Version string `json:"version"`
			State   string `json:"state"`
		} `json:"xray"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/status", nil, &raw); err != nil {
		return nil, err
	}
	return &ports.ServerStatus{
		PanelVersion: raw.PanelVersion,
		XrayVersion:  raw.Xray.Version,
		XrayState:    raw.Xray.State,
	}, nil
}

// GetPanelUpdateInfo hits /panel/api/server/getPanelUpdateInfo. Returns the
// panel's current version, the latest 3X-UI tag reachable on GitHub, and
// whether an update is available. CurrentVersion is reported without a "v"
// prefix ("3.1.0") while LatestVersion typically carries one ("v3.1.0").
// PSP normalizes both via version.parseSemver.
func (c *Client) GetPanelUpdateInfo(ctx context.Context) (*ports.PanelUpdateInfo, error) {
	var raw struct {
		CurrentVersion  string `json:"currentVersion"`
		LatestVersion   string `json:"latestVersion"`
		UpdateAvailable bool   `json:"updateAvailable"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/getPanelUpdateInfo", nil, &raw); err != nil {
		return nil, err
	}
	return &ports.PanelUpdateInfo{
		CurrentVersion:  raw.CurrentVersion,
		LatestVersion:   raw.LatestVersion,
		UpdateAvailable: raw.UpdateAvailable,
	}, nil
}

// ScanRealityTargets asks THIS 3X-UI host to probe the supplied domains, IPs,
// or CIDRs. Keeping the network operation upstream is intentional: the result
// must represent the Xray node's reachability and latency, not PSP's host.
// 3X-UI reads this endpoint with PostForm, so the payload must be URL-encoded
// rather than JSON. An empty targets value selects the upstream seed list.
func (c *Client) ScanRealityTargets(ctx context.Context, targets string) ([]ports.RealityScanResult, error) {
	form := url.Values{}
	if strings.TrimSpace(targets) != "" {
		form.Set("targets", targets)
	}
	var out []ports.RealityScanResult
	// Upstream caps scans at 512 tasks with 32 workers. IP/CIDR discovery uses
	// a 4s timeout, while domain probes use 10s, so 512 worst-case domains can
	// take about 160s in 16 waves. Allow 180s without relaxing the normal 30s
	// timeout used by every other XUI call.
	if err := c.doJSONWithTimeout(ctx, http.MethodPost, "/panel/api/server/scanRealityTargets", form, &out, 180*time.Second); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdatePanel triggers /panel/api/server/updatePanel. The 3X-UI panel
// self-updates to the latest GitHub release and restarts. The HTTP
// connection drops mid-call as the panel binary exits — that is the
// expected success path, NOT an error. We swallow EOF / connection-reset
// errors here so the caller (admin handler) sees a clean nil and can
// proceed straight to scheduling the post-upgrade smoke probe.
func (c *Client) UpdatePanel(ctx context.Context) error {
	err := c.doJSON(ctx, http.MethodPost, "/panel/api/server/updatePanel", nil, nil)
	if err == nil {
		return nil
	}
	// A panel that already started its restart returns EOF / "connection
	// reset" / "unexpected end of JSON input" — that's the success
	// signature. Treat anything that looks like a transport-side close
	// after a successful POST as ok.
	msg := err.Error()
	if strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "empty response body") ||
		strings.Contains(msg, "unexpected end of JSON") {
		return nil
	}
	return err
}

// InstallXray triggers /panel/api/server/installXray/:version. Pass "latest"
// for the newest xray-core release. Unlike UpdatePanel, the 3X-UI panel
// itself keeps running across this call; only the xray-core child process
// is restarted, so the HTTP response comes back normally.
func (c *Client) InstallXray(ctx context.Context, version string) error {
	if version == "" {
		version = "latest"
	}
	return c.doJSON(ctx, http.MethodPost, "/panel/api/server/installXray/"+url.PathEscape(version), nil, nil)
}

// InstallCore implements ports.CoreUpdater. On 3X-UI the managed core is
// xray-core, so the generic operation delegates to the existing endpoint.
func (c *Client) InstallCore(ctx context.Context, version string) error {
	return c.InstallXray(ctx, version)
}

// GetXrayVersionList hits /panel/api/server/getXrayVersion. 3X-UI returns
// the obj field as a JSON array of tag strings, populated from the panel's
// known-good xray-core releases. Order is upstream's (typically newest
// first). Empty / missing → empty slice + nil error (panel rebooted into
// a state without the list yet — admin can still type "latest" by hand).
func (c *Client) GetXrayVersionList(ctx context.Context) ([]string, error) {
	var versions []string
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/getXrayVersion", nil, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// GetCoreVersionList implements ports.CoreUpdater for xray-core.
func (c *Client) GetCoreVersionList(ctx context.Context) ([]string, error) {
	return c.GetXrayVersionList(ctx)
}

// GetWebCertFiles hits GET /panel/api/server/getWebCertFiles and returns the
// panel's own web TLS certificate + key file PATHS (not the PEM bytes). The
// endpoint was added in 3X-UI 3.2.7; older panels have no such route and answer
// HTTP 404, which doJSON marks as ports.ErrXUIEndpointUnsupported so the caller
// can degrade (grey out "fetch cert from panel"). Backs cert_source=from_panel:
// fill a node-assigned inbound with file-mode paths that exist on the node.
func (c *Client) GetWebCertFiles(ctx context.Context) (*ports.WebCertFiles, error) {
	var raw struct {
		WebCertFile string `json:"webCertFile"`
		WebKeyFile  string `json:"webKeyFile"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/getWebCertFiles", nil, &raw); err != nil {
		return nil, err
	}
	return &ports.WebCertFiles{
		CertFile: raw.WebCertFile,
		KeyFile:  raw.WebKeyFile,
	}, nil
}

// GetClient fetches one client by its panel-wide email via GET
// /panel/api/clients/get/{email}. The obj is {client:{...}, inboundIds:[...]}.
// Returns (nil, nil) when no such client exists — 3.2.x answers HTTP 200 +
// {success:false, msg:" (record not found)", obj:null}, which doJSON surfaces
// as a plain error; we recognise the not-found shape and report absence as a
// clean nil so callers (presence checks, claim-by-email) don't treat it as a
// transport failure. ClientDetail.ID is mapped from client.uuid (the xray
// client id), NOT client.id (the numeric DB row id).
func (c *Client) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	if email == "" {
		return nil, fmt.Errorf("GetClient: email is required")
	}
	var out struct {
		Client struct {
			UUID       string `json:"uuid"`
			Email      string `json:"email"`
			Enable     *bool  `json:"enable"`
			Flow       string `json:"flow"`
			Password   string `json:"password"`
			Auth       string `json:"auth"`
			ExpiryTime int64  `json:"expiryTime"`
			TotalGB    int64  `json:"totalGB"`
		} `json:"client"`
		InboundIDs []int `json:"inboundIds"`
	}
	path := "/panel/api/clients/get/" + url.PathEscape(email)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		if isClientNotFoundMsg(err.Error()) {
			return nil, nil
		}
		return nil, err
	}
	enable := true
	if out.Client.Enable != nil {
		enable = *out.Client.Enable
	}
	return &ports.ClientDetail{
		ID:         out.Client.UUID,
		Email:      out.Client.Email,
		Enable:     enable,
		Flow:       out.Client.Flow,
		Password:   out.Client.Password,
		Auth:       out.Client.Auth,
		ExpiryTime: out.Client.ExpiryTime,
		TotalGB:    out.Client.TotalGB,
		InboundIDs: out.InboundIDs,
	}, nil
}

// ListClientInbounds returns every client on the panel keyed by email, valued by
// the set of inbound IDs it is attached to. It parses the clients array out of each
// inbound's settings in a SINGLE /list call (no per-inbound round-trips). Used by
// the shared-client orphan reconcile to discover clients PSP no longer tracks in its
// psp_client table (e.g. pre-merge per-class clients whose rows were already pruned)
// — listing the live state is robust to email-suffix or domain drift, which a
// reconstruct-the-email approach is not.
func (c *Client) ListClientInbounds(ctx context.Context) (map[string][]int, error) {
	ibs, err := c.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]int{}
	for _, ib := range ibs {
		if ib.Settings == "" {
			continue
		}
		var s struct {
			Clients []struct {
				Email string `json:"email"`
			} `json:"clients"`
		}
		if json.Unmarshal([]byte(ib.Settings), &s) != nil {
			continue
		}
		for _, cl := range s.Clients {
			if cl.Email == "" {
				continue
			}
			out[cl.Email] = append(out[cl.Email], ib.ID)
		}
	}
	return out, nil
}

// isClientNotFoundMsg reports whether a 3X-UI error message describes a
// missing client. /clients/get answers a missing email with GORM's sentinel
// " (record not found)"; update/del report "client not found" / "not found in
// inbound". Substring match mirrors isPermanentPanelMsg — fragile across
// locales/versions, but it's the only signal the panel gives.
func isClientNotFoundMsg(s string) bool {
	m := strings.ToLower(s)
	return strings.Contains(m, "record not found") ||
		strings.Contains(m, "client not found") ||
		strings.Contains(m, "not found in inbound")
}

// --- helpers ---

func rawToInbound(r *rawInbound) ports.Inbound {
	return ports.Inbound{
		ID:             r.ID,
		Up:             r.Up,
		Down:           r.Down,
		Total:          r.Total,
		Remark:         r.Remark,
		Enable:         r.Enable,
		ExpiryTime:     r.ExpiryTime,
		Listen:         r.Listen,
		Port:           r.Port,
		Protocol:       r.Protocol,
		Settings:       string(r.Settings),
		StreamSettings: string(r.StreamSettings),
		Tag:            r.Tag,
		Sniffing:       string(r.Sniffing),
		Allocate:       string(r.Allocate),
		ClientStats:    rawTrafficsToPorts(r.ClientStats),
	}
}

func specToRaw(s *ports.InboundSpec, id int) map[string]any {
	return map[string]any{
		"id":             id,
		"remark":         s.Remark,
		"enable":         s.Enable,
		"listen":         s.Listen,
		"port":           s.Port,
		"protocol":       s.Protocol,
		"settings":       s.Settings,
		"streamSettings": s.StreamSettings,
		"sniffing":       s.Sniffing,
		"allocate":       s.Allocate,
		"expiryTime":     s.ExpiryTime,
	}
}

// ensureClientsArray guarantees the inbound settings JSON carries a clients[]
// array, injecting an empty one when absent. See AddInbound for why this is
// load-bearing (3X-UI panics appending to a missing clients field, yielding a
// blank-200 on every subsequent /clients/add). Non-object / malformed input is
// returned verbatim — better to push what we have than to lose the snapshot.
func ensureClientsArray(settings string) string {
	if strings.TrimSpace(settings) == "" {
		return `{"clients":[]}`
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(settings), &m); err != nil {
		return settings
	}
	if _, ok := m["clients"]; ok {
		return settings
	}
	m["clients"] = json.RawMessage("[]")
	out, err := json.Marshal(m)
	if err != nil {
		return settings
	}
	return string(out)
}

func rawTrafficsToPorts(raws []rawClientTraffic) []ports.ClientTraffic {
	out := make([]ports.ClientTraffic, len(raws))
	for i, r := range raws {
		out[i] = ports.ClientTraffic{
			ID:         r.ID,
			InboundID:  r.InboundID,
			Email:      r.Email,
			Up:         r.Up,
			Down:       r.Down,
			Total:      r.Total,
			Enable:     r.Enable,
			ExpiryTime: r.ExpiryTime,
			Reset:      r.Reset,
			LastOnline: r.LastOnline,
		}
	}
	return out
}

func (c *Client) settingsWithCurrentClients(ctx context.Context, inboundID int, nextSettings string) (string, error) {
	// Empty/blank input would previously short-circuit and reach 3X-UI as a
	// literal empty settings — which can wipe every live client. Treat it as
	// "{}" so replaceSettingsClients always runs and injects whatever clients
	// 3X-UI currently has (PSP-managed + manually-created, both preserved).
	if strings.TrimSpace(nextSettings) == "" {
		nextSettings = "{}"
	}
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return "", err
	}
	return replaceSettingsClients(nextSettings, inb.Settings)
}

func replaceSettingsClients(nextSettings, currentSettings string) (string, error) {
	currentClients, err := clientsFromSettings(currentSettings)
	if err != nil {
		return "", err
	}
	if len(currentClients) == 0 {
		return nextSettings, nil
	}
	var next map[string]any
	if err := json.Unmarshal([]byte(nextSettings), &next); err != nil {
		return "", fmt.Errorf("decode inbound settings: %w", err)
	}
	next["clients"] = currentClients
	b, err := json.Marshal(next)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func clientsFromSettings(settingsJSON string) ([]map[string]any, error) {
	if strings.TrimSpace(settingsJSON) == "" {
		return nil, nil
	}
	var settings struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("decode inbound settings: %w", err)
	}
	return settings.Clients, nil
}

// buildClientJSON serializes a ClientSpec into the JSON shape 3X-UI expects.
// Field names follow the 3X-UI XrayClient model:
// id / email / enable / flow / limitIp / totalGB / expiryTime / subId / tgId / reset / password / method
func buildClientJSON(s ports.ClientSpec) (json.RawMessage, error) {
	// 3X-UI 3.2.0 types tgId as int64 and rejects a JSON string ("cannot
	// unmarshal string into ... tgId of type int64"), so /clients/add and
	// /clients/update fail outright if tgId is sent as a string. PSP never
	// uses 3X-UI's Telegram integration, so emit the numeric form (0 when
	// unset; parse defensively for any non-empty value). Verified against a
	// live 3.2.0 panel — see docs/3xui-3.2-clients-migration.md §P2.
	tgID, _ := strconv.Atoi(s.TgID)
	obj := map[string]any{
		"email":      s.Email,
		"enable":     s.Enable,
		"limitIp":    s.LimitIP,
		"totalGB":    s.TotalGB,
		"expiryTime": s.ExpiryTime,
		"subId":      s.SubID,
		"tgId":       tgID,
		"reset":      s.Reset,
	}
	if s.ID != "" {
		obj["id"] = s.ID
	}
	if s.Flow != "" {
		obj["flow"] = s.Flow
	}
	if s.Password != "" {
		obj["password"] = s.Password
	}
	if s.Method != "" {
		obj["method"] = s.Method
	}
	if s.Auth != "" {
		obj["auth"] = s.Auth
	}
	return json.Marshal(obj)
}
