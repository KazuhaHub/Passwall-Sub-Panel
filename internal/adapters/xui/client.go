package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
	// earlier change. inboundWriteMu guards the map itself. (Client writes no
	// longer use this — 3.2.0's /clients/* endpoints are email-keyed and need
	// no inbound read-modify-write.)
	inboundWriteMu    sync.Mutex
	inboundWriteLocks map[int]*sync.Mutex
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
		panelName:         p.Name,
		baseURL:           strings.TrimRight(p.URL, "/"),
		apiToken:          apiToken,
		username:          p.Username,
		password:          p.Password,
		http:              httpClient,
		jar:               jar,
		inboundWriteLocks: make(map[int]*sync.Mutex),
	}, nil
}

// lockInbound serializes read-modify-write updates to one inbound within this
// process and returns the unlock func. Use as: defer c.lockInbound(id)().
func (c *Client) lockInbound(id int) func() {
	c.inboundWriteMu.Lock()
	mu, ok := c.inboundWriteLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		c.inboundWriteLocks[id] = mu
	}
	c.inboundWriteMu.Unlock()
	mu.Lock()
	return mu.Unlock
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

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	return c.doJSONRetry(ctx, method, path, body, out, false)
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
func (c *Client) doJSONRetry(ctx context.Context, method, path string, body any, out any, retried bool) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
	resp, err := c.http.Do(req)
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
		return c.doJSONRetry(ctx, method, path, body, out, true)
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
		hint := "verify URL and api_token / username+password — 3X-UI returns an empty body when auth is wrong"
		if c.apiToken == "" {
			hint = "verify username/password — 3X-UI returns an empty body when cookie auth is wrong"
		}
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
	mergedSpec.Settings = settings
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
// call (POST /clients/add, body {client, inboundIds}). Per-protocol secrets
// present in spec are sent verbatim; the panel generates any omitted ones.
func (c *Client) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return err
	}
	body := map[string]any{
		"client":     json.RawMessage(clientJSON),
		"inboundIds": []int{inboundID},
	}
	return c.doJSON(ctx, http.MethodPost, "/panel/api/clients/add", body, nil)
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
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return err
	}
	path := "/panel/api/clients/update/" + url.PathEscape(spec.Email)
	return c.doJSON(ctx, http.MethodPost, path, json.RawMessage(clientJSON), nil)
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
// attached to — for PSP that is exactly one, since the email encodes the
// node. keepTraffic=0 drops the xray traffic row too; PSP keeps its own
// accounting. The ownership guard in sync.DelOwnedClient runs before this, so
// only PSP-managed clients ever reach here.
func (c *Client) DelClientByEmail(ctx context.Context, inboundID int, email string) error {
	path := "/panel/api/clients/del/" + url.PathEscape(email) + "?keepTraffic=0"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// BulkAddToInbound creates many clients on one inbound via POST
// /panel/api/clients/bulkCreate. The body is a JSON array of {client,
// inboundIds} items (the same per-item shape /clients/add accepts); the panel
// processes them sequentially, restarts xray at most once, and returns
// {created, skipped:[{email,reason}]}. Per-protocol secrets follow the same
// rules as AddClient (buildClientJSON). An empty specs slice is a no-op.
func (c *Client) BulkAddToInbound(ctx context.Context, inboundID int, specs []ports.ClientSpec) (ports.BulkAddResult, error) {
	if len(specs) == 0 {
		return ports.BulkAddResult{}, nil
	}
	items := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		clientJSON, err := buildClientJSON(s)
		if err != nil {
			return ports.BulkAddResult{}, err
		}
		items = append(items, map[string]any{
			"client":     json.RawMessage(clientJSON),
			"inboundIds": []int{inboundID},
		})
	}
	var out struct {
		Created int `json:"created"`
		Skipped []struct {
			Email  string `json:"email"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/clients/bulkCreate", items, &out); err != nil {
		return ports.BulkAddResult{}, err
	}
	res := ports.BulkAddResult{Created: out.Created}
	for _, s := range out.Skipped {
		res.Skipped = append(res.Skipped, ports.BulkSkip{Email: s.Email, Reason: s.Reason})
	}
	return res, nil
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
	}, nil
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
