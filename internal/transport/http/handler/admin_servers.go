package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-node/corecatalog"
	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// AdminServersHandler exposes CRUD for upstream panels and native PSP servers
// under /api/admin/servers. Nodes reference a stable server ID independently
// of installing a native agent or changing its machine address.
//
// Mutations keep the DB and the in-memory XUIPool in lockstep so changes
// take effect immediately without restarting the panel binary.
//
// audit + async are used by the upgrade-panel / upgrade-xray handlers
// (v3.6.0-beta.3) to write audit-trail rows and to schedule the post-upgrade
// smoke probe; they're optional for the CRUD/Test flows.
type AdminServersHandler struct {
	repo             ports.XUIPanelRepo
	pool             ports.XUIPool
	nodes            ports.NodeRepo
	audit            ports.AuditRepo
	async            AsyncDispatcher
	invalidateRender func()
	native           ports.NativeAgentProvisioningRepo
	agents           ports.NodeAgentRepo
	nodeSettings     ports.SettingsRepo
	nativeUpgrade    NativeAgentUpgradeService
	nodeReleases     ports.NodeReleaseCatalog
}

func (h *AdminServersHandler) WithNativeAgentProvisioning(repo ports.NativeAgentProvisioningRepo) *AdminServersHandler {
	h.native = repo
	return h
}

func (h *AdminServersHandler) WithNodeAgents(repo ports.NodeAgentRepo) *AdminServersHandler {
	h.agents = repo
	return h
}

func (h *AdminServersHandler) WithNodeSettings(repo ports.SettingsRepo) *AdminServersHandler {
	h.nodeSettings = repo
	return h
}

func NewAdminServersHandler(repo ports.XUIPanelRepo, pool ports.XUIPool, nodes ports.NodeRepo, audit ports.AuditRepo, async AsyncDispatcher, invalidateRender func()) *AdminServersHandler {
	return &AdminServersHandler{
		repo: repo, pool: pool, nodes: nodes, audit: audit, async: async, invalidateRender: invalidateRender,
	}
}

// serverDTO is the API representation. Sensitive fields (api_token /
// password) are NEVER returned in plaintext — the response carries only
// "has_api_token" / "has_password" booleans. The edit dialog re-enters
// secrets when changing them.
//
// Version-identity fields reflect the last successful probe via the boot probe
// + traffic-poll-piggyback path (v3.6.0-beta.1) or the manual "test
// connection" trigger. Native nodes additionally expose desired core identity
// from NodeAgent separately from the last observed runtime identity.
type serverDTO struct {
	ID           int64                   `json:"id"`
	Kind         string                  `json:"panel_type"`
	Capabilities []ports.PanelCapability `json:"capabilities"`
	Name         string                  `json:"name"`
	URL          string                  `json:"url"`
	Username     string                  `json:"username,omitempty"`
	Remark       string                  `json:"remark,omitempty"`
	HasAPIToken  bool                    `json:"has_api_token"`
	HasPassword  bool                    `json:"has_password"`
	// AuthMethod is the EFFECTIVE auth mode ("token" | "password") so the edit
	// form pre-selects correctly — resolved from the stored method, falling back
	// to inference for legacy rows. InsecureHTTPS skips TLS cert verification.
	AuthMethod         string     `json:"auth_method"`
	InsecureHTTPS      bool       `json:"insecure_https"`
	PanelVersion       string     `json:"panel_version,omitempty"`
	XrayVersion        string     `json:"xray_version,omitempty"`
	CoreEngine         string     `json:"core_engine,omitempty"`
	CoreVersion        string     `json:"core_version,omitempty"`
	DesiredCoreEngine  string     `json:"desired_core_engine,omitempty"`
	DesiredCoreVersion string     `json:"desired_core_version,omitempty"`
	VersionCheckedAt   *time.Time `json:"version_checked_at,omitempty"`
	CompatStatus       string     `json:"compat_status,omitempty"`  // "supported" | "too_old" | "untested" | "unknown"
	CompatMessage      string     `json:"compat_message,omitempty"` // human-readable, for tooltip / banner
	// LatestXUIVersion / UpdateAvailable are derived per-request from the
	// PSP-wide version.LatestXUI() snapshot (one GitHub query feeds every
	// row) compared against this panel's PanelVersion. NOT persisted per
	// panel — the latest tag is panel-independent and storing it per row
	// would just be N copies of the same string going stale together.
	LatestXUIVersion string `json:"latest_xui_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available,omitempty"`
	// IPLimitEnforcement is what the node's fail2ban probe concluded about the
	// concurrent-IP cap: whether a limit pushed here is acted on at all. It is
	// NOT a capability — 3X-UI stores limitIp on every supported version and
	// says nothing about acting on it — so it is reported separately and, on
	// purpose, INCLUDING "unknown". A panel we cannot read must not be
	// indistinguishable from one that is enforcing.
	//
	// 3X-UI only. S-UI has no concept of the cap at all, which the capability
	// list already reports; adding a second permanently-unknown badge there
	// would be noise an admin has to learn to ignore.
	IPLimitEnforcement string     `json:"ip_limit_enforcement,omitempty"`
	IPLimitProbedAt    *time.Time `json:"ip_limit_probed_at,omitempty"`
}

type serverCreateRequest struct {
	Kind          string `json:"panel_type"`
	Name          string `json:"name" binding:"required"`
	URL           string `json:"url"`
	APIToken      string `json:"api_token"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Remark        string `json:"remark"`
	AuthMethod    string `json:"auth_method"` // "" (auto) | "token" | "password"
	InsecureHTTPS bool   `json:"insecure_https"`
}

type nativeServerCreateResponse struct {
	Server     serverDTO `json:"server"`
	AgentID    string    `json:"agent_id"`
	Credential string    `json:"credential"`
	Endpoint   string    `json:"endpoint"`
}

func newNativeCredential() (raw, digest string, err error) {
	secret, err := idgen.NewSubToken()
	if err != nil {
		return "", "", err
	}
	raw = "pspn_" + secret
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// serverUpdateRequest uses pointers so omitted fields preserve existing
// values; admin only re-enters secrets when actually changing them.
type serverUpdateRequest struct {
	Kind          *string `json:"panel_type,omitempty"`
	Name          *string `json:"name,omitempty"`
	URL           *string `json:"url,omitempty"`
	APIToken      *string `json:"api_token,omitempty"`
	Username      *string `json:"username,omitempty"`
	Password      *string `json:"password,omitempty"`
	Remark        *string `json:"remark,omitempty"`
	AuthMethod    *string `json:"auth_method,omitempty"`
	InsecureHTTPS *bool   `json:"insecure_https,omitempty"`
}

// validAuthMethod gates the auth_method input to the known values.
func validAuthMethod(m string) bool {
	switch domain.XUIAuthMethod(m) {
	case domain.XUIAuthAuto, domain.XUIAuthToken, domain.XUIAuthPassword:
		return true
	default:
		return false
	}
}

// effectiveAuthMethod resolves the concrete mode for the edit form: the stored
// method when set, else the legacy inference (token present → token, else
// password) so a pre-existing panel pre-selects the right radio.
func effectiveAuthMethod(p *domain.XUIPanel) domain.XUIAuthMethod {
	if p.AuthMethod != domain.XUIAuthAuto {
		return p.AuthMethod
	}
	if p.APIToken != "" {
		return domain.XUIAuthToken
	}
	return domain.XUIAuthPassword
}

func (h *AdminServersHandler) List(c *gin.Context) {
	p := parsePagination(c)
	ctx := c.Request.Context()
	panels, total, err := h.repo.ListPaged(ctx, p)
	if err != nil {
		respondError(c, err)
		return
	}
	agentsByPanel := make(map[int64]*domain.NodeAgent)
	if h.agents != nil {
		panelIDs := make([]int64, 0, len(panels))
		for _, panel := range panels {
			if panel != nil && domain.NormalizePanelKind(panel.Kind) == domain.PanelKindPSP {
				panelIDs = append(panelIDs, panel.ID)
			}
		}
		agents, listErr := h.agents.ListByPanelIDs(ctx, panelIDs)
		if listErr != nil {
			respondError(c, listErr)
			return
		}
		for _, agent := range agents {
			if agent != nil {
				agentsByPanel[agent.PanelID] = agent
			}
		}
	}
	out := make([]serverDTO, len(panels))
	for i, panel := range panels {
		out[i] = h.toServerDTOWithAgent(panel, agentsByPanel[panel.ID])
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminServersHandler) Create(c *gin.Context) {
	var req serverCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validAuthMethod(req.AuthMethod) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid auth_method"})
		return
	}
	kind := domain.NormalizePanelKind(domain.PanelKind(req.Kind))
	if validator, ok := h.pool.(ports.PanelKindValidator); ok && !validator.SupportsKind(kind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown panel_type: " + string(kind)})
		return
	}
	if kind == domain.PanelKindSUI && (domain.XUIAuthMethod(req.AuthMethod) == domain.XUIAuthPassword || req.APIToken == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "S-UI requires API token authentication"})
		return
	}
	if kind != domain.PanelKindPSP && req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server URL is required"})
		return
	}
	if _, err := h.repo.GetByName(c.Request.Context(), req.Name); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Server name already exists"})
		return
	} else if !errors.Is(err, domain.ErrNotFound) {
		respondError(c, err)
		return
	}
	if kind == domain.PanelKindPSP {
		h.createNative(c, req)
		return
	}
	p := &domain.XUIPanel{
		Kind:               kind,
		Name:               req.Name,
		URL:                req.URL,
		APIToken:           req.APIToken,
		Username:           req.Username,
		Password:           req.Password,
		Remark:             req.Remark,
		AuthMethod:         domain.XUIAuthMethod(req.AuthMethod),
		InsecureSkipVerify: req.InsecureHTTPS,
	}
	if err := h.repo.Save(c.Request.Context(), p); err != nil {
		mapServerError(c, err)
		return
	}
	if err := h.pool.Add(p); err != nil {
		// DB succeeded but pool wiring failed; rollback so they stay in sync.
		_ = h.repo.Delete(c.Request.Context(), p.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Register in pool: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, h.toServerDTO(c.Request.Context(), p))
}

func (h *AdminServersHandler) createNative(c *gin.Context, req serverCreateRequest) {
	if h.native == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "native node provisioning is not wired in this build"})
		return
	}
	if req.URL != "" || req.APIToken != "" || req.Username != "" || req.Password != "" || req.InsecureHTTPS || req.AuthMethod != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PSP native nodes do not accept an inbound URL or upstream credentials"})
		return
	}
	base := enrollBaseURL(c)
	if !EnrollBaseAllowed(base) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot derive a safe public PSP endpoint from this request"})
		return
	}
	identity, err := idgen.NewSubToken()
	if err != nil {
		respondError(c, err)
		return
	}
	agentID := "agt_" + identity
	credential, digest, err := newNativeCredential()
	if err != nil {
		respondError(c, err)
		return
	}
	release, err := corecatalog.Recommended("xray")
	if err != nil {
		respondError(c, err)
		return
	}
	panel := &domain.XUIPanel{
		Kind: domain.PanelKindPSP, Name: req.Name, URL: "psp://" + agentID,
		Remark: req.Remark,
	}
	agent := &domain.NodeAgent{
		AgentID: agentID, CredentialSHA256: digest,
		DesiredCoreEngine:  domain.NodeCoreXray,
		DesiredCoreVersion: release.Version,
	}
	if err := h.native.CreateWithCredential(c.Request.Context(), panel, agent, credential); err != nil {
		mapServerError(c, err)
		return
	}
	if err := h.pool.Add(panel); err != nil {
		if rollbackErr := h.native.DeleteConverged(c.Request.Context(), panel.ID); rollbackErr != nil {
			log.Error("native panel create rollback failed", "panel_id", panel.ID, "agent_id", agentID, "err", rollbackErr)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Register native node in pool: " + err.Error()})
		return
	}
	privateNodeResponse(c)
	c.JSON(http.StatusCreated, nativeServerCreateResponse{
		Server: h.toServerDTO(c.Request.Context(), panel), AgentID: agentID, Credential: credential,
		Endpoint: panelpath.PanelURL(base, panelpath.FromRequest(c.Request), "/v1/node/sync"),
	})
}

// RotateNativeCredential invalidates the old native-agent credential and
// returns and encrypts the replacement for later installation retrieval.
// No agent identity or convergence
// coordinate changes, so a restarted daemon resumes its existing state.
func (h *AdminServersHandler) RotateNativeCredential(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if h.native == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "native node provisioning is not wired in this build"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Credential rotation is available only for PSP native nodes"})
		return
	}
	base := enrollBaseURL(c)
	if !EnrollBaseAllowed(base) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot derive a safe public PSP endpoint from this request"})
		return
	}
	credential, _, err := newNativeCredential()
	if err != nil {
		respondError(c, err)
		return
	}
	agent, err := h.native.RotateCredentialWithSecret(c.Request.Context(), id, credential)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if h.audit != nil {
		_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
			Actor: actorFromGin(c), Action: "native_node_credential_rotated",
			Target: "panel=" + strconv.FormatInt(id, 10) + " agent=" + agent.AgentID,
			At:     time.Now(),
		})
	}
	privateNodeResponse(c)
	c.JSON(http.StatusOK, nativeServerCreateResponse{
		Server: h.toServerDTO(c.Request.Context(), panel), AgentID: agent.AgentID, Credential: credential,
		Endpoint: panelpath.PanelURL(base, panelpath.FromRequest(c.Request), "/v1/node/sync"),
	})
}

func (h *AdminServersHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	before := *existing
	var req serverUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Kind != nil {
		kind := domain.NormalizePanelKind(domain.PanelKind(*req.Kind))
		if validator, ok := h.pool.(ports.PanelKindValidator); ok && !validator.SupportsKind(kind) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown panel_type: " + string(kind)})
			return
		}
		if (existing.Kind == domain.PanelKindPSP) != (kind == domain.PanelKindPSP) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot convert between a native node and an upstream panel"})
			return
		}
	}
	if existing.Kind == domain.PanelKindPSP && (req.URL != nil || req.APIToken != nil || req.Username != nil ||
		req.Password != nil || req.AuthMethod != nil || req.InsecureHTTPS != nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only name and remark can be edited for a PSP native node"})
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Kind != nil {
		existing.Kind = domain.NormalizePanelKind(domain.PanelKind(*req.Kind))
	}
	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.APIToken != nil {
		existing.APIToken = *req.APIToken
	}
	if req.Username != nil {
		existing.Username = *req.Username
	}
	if req.Password != nil {
		existing.Password = *req.Password
	}
	if req.Remark != nil {
		existing.Remark = *req.Remark
	}
	if req.AuthMethod != nil {
		if !validAuthMethod(*req.AuthMethod) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid auth_method"})
			return
		}
		existing.AuthMethod = domain.XUIAuthMethod(*req.AuthMethod)
	}
	if req.InsecureHTTPS != nil {
		existing.InsecureSkipVerify = *req.InsecureHTTPS
	}
	if domain.NormalizePanelKind(existing.Kind) == domain.PanelKindSUI &&
		(existing.AuthMethod == domain.XUIAuthPassword || existing.APIToken == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "S-UI requires API token authentication"})
		return
	}
	if err := h.repo.Save(c.Request.Context(), existing); err != nil {
		mapServerError(c, err)
		return
	}
	// Panel rename no longer needs to cascade to nodes / user_xui_clients —
	// the panel_name columns were dropped in v3. The pool refresh below
	// makes every subsequent name lookup return the new value automatically.
	//
	// Re-register in the pool. Prefer an atomic Replace (production *xui.Pool) so
	// a concurrent Get from traffic/reconcile/render/sync never hits the brief
	// "not registered" gap a Remove()+Add() pair exposes; fall back to the pair
	// for pools that don't implement it (test fakes).
	if rp, ok := h.pool.(interface {
		Replace(*domain.XUIPanel) error
	}); ok {
		if err := rp.Replace(existing); err != nil {
			if rollbackErr := h.repo.Save(c.Request.Context(), &before); rollbackErr != nil {
				log.Error("admin server update rollback failed", "panel_id", id, "err", rollbackErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Re-register in pool: " + err.Error()})
			return
		}
	} else {
		_ = h.pool.Remove(id)
		if err := h.pool.Add(existing); err != nil {
			_ = h.pool.Add(&before)
			if rollbackErr := h.repo.Save(c.Request.Context(), &before); rollbackErr != nil {
				log.Error("admin server update rollback failed", "panel_id", id, "err", rollbackErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Re-register in pool: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, h.toServerDTO(c.Request.Context(), existing))
}

// Test issues a lightweight ListInbounds against the named server. Returns
// {ok: bool, error: string, inbound_count: int} so the frontend can show a
// pass/fail badge next to the server row.
//
// Name is read from the JSON body (not the URL path) to dodge a Gin routing
// quirk where /servers/:name/test conflicts with the bare /servers/:name
// CRUD routes and falls through to the SPA NoRoute handler.
type testServerRequest struct {
	ID int64 `json:"id" binding:"required"`
}

func (h *AdminServersHandler) Test(c *gin.Context) {
	var req testServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), req.ID)
	if err != nil {
		mapServerError(c, err)
		return
	}
	isXUI := domain.NormalizePanelKind(panel.Kind) == domain.PanelKind3XUI
	isNative := domain.NormalizePanelKind(panel.Kind) == domain.PanelKindPSP
	// The compat (v3.json) tested range is fetched REACTIVELY — not here, but
	// only if the panel probed below turns out to sit outside the cached range
	// (see the CheckXUI block). A supported fleet makes zero GitHub compat calls.
	//
	// The centralized latest-3X-UI tag fetch IS proactive: one PSP-wide query
	// drives every panel's "update available" badge, and there's no local
	// signal that a new upstream release exists to react to. The 30-minute
	// throttle inside RefreshLatestXUI keeps page-refresh churn under twice/hour.
	// Background ctx (not c.Request.Context()) so an admin navigating away
	// mid-fetch doesn't cancel the call; RefreshLatestXUI enforces its own 8s
	// timeout so the background ctx can't leak.
	if isXUI {
		if rerr := version.RefreshLatestXUI(context.Background()); rerr != nil {
			log.Debug("admin test: refresh latest 3X-UI failed", "err", rerr)
		}
	}
	client, err := h.pool.Get(req.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Server not registered in pool: " + err.Error()})
		return
	}
	var observedStatus *ports.ServerStatus
	if isNative {
		observedStatus, err = client.GetServerStatus(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": "native agent has not delivered a full report"})
			return
		}
	}
	inbounds, err := client.ListInbounds(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Connection works — piggyback a version probe so the admin's "test
	// connection" click doubles as a manual version refresh. This is the
	// out-of-band manual trigger that complements the boot probe +
	// traffic-poll-piggyback re-probe (see app.go) — admin gets immediate
	// feedback after a 3X-UI upgrade instead of waiting for the next poll
	// tick. Best-effort: if the version probe fails for any reason we
	// still report the connection test result; the "ok: true" semantics
	// is about the panel being reachable + credentials valid.
	resp := gin.H{
		"ok":            true,
		"inbound_count": len(inbounds),
	}
	status, perr := observedStatus, error(nil)
	if status == nil {
		status, perr = client.GetServerStatus(c.Request.Context())
	}
	if perr == nil && status != nil {
		now := time.Now()
		if uerr := h.repo.UpdateVersion(c.Request.Context(), req.ID, status.PanelVersion, status.XrayVersion, &now); uerr != nil {
			log.Warn("admin test: write version", "panel_id", req.ID, "err", uerr)
		} else if h.invalidateRender != nil {
			h.invalidateRender()
		}
		if isXUI {
			compatStatus := version.CheckXUI(status.PanelVersion)
			if compatStatus != version.CompatSupported {
				// Reactive compat fetch: this panel's version isn't in the cached
				// tested range — the published range may have been bumped to cover
				// it. Pull the manifest (throttled) and re-check before reporting.
				// Background ctx so navigating away can't 60s-lock the throttle.
				if rerr := version.RefreshRemoteCompat(context.Background(), "", false); rerr != nil {
					log.Debug("admin test: reactive compat refresh", "panel_id", req.ID, "err", rerr)
				}
				compatStatus = version.CheckXUI(status.PanelVersion)
			}
			resp["compat_status"] = compatStatus.String()
			resp["compat_message"] = version.CompatMessage(status.PanelVersion, compatStatus)
		}
		resp["panel_version"] = status.PanelVersion
		resp["xray_version"] = status.XrayVersion
		resp["xray_state"] = status.XrayState
		if status.CoreEngine != "" {
			resp["core_engine"] = status.CoreEngine
		}
		resp["core_version"] = status.XrayVersion
		resp["core_state"] = status.XrayState
		resp["version_checked_at"] = now
		// Carry the centralized "latest 3X-UI tag" snapshot back so the
		// UI refreshes its ⋮ kebab badge in lockstep with the probed
		// panel version. The latest tag itself is panel-independent
		// (one PSP-wide value), but UpdateAvailable becomes meaningful
		// only when paired with this specific panel's version.
		if latest := version.LatestXUI(); isXUI && latest != "" {
			resp["latest_xui_version"] = latest
			resp["update_available"] = version.IsXUIUpdateAvailable(status.PanelVersion)
		}
		// Same click, same reason: an admin who just installed fail2ban on the
		// node should not have to wait out the next traffic-poll probe to see the
		// IP cap go from "stored and ignored" to "enforced".
		if isXUI {
			if state, ok := h.refreshIPLimitEnforcement(c.Request.Context(), req.ID, client, now); ok {
				resp["ip_limit_enforcement"] = string(state)
			}
		}
	} else {
		log.Warn("admin test: version probe", "panel_id", req.ID, "err", perr)
		// Record the attempt time without touching the stored
		// versions (preserve last-known-good). Mirrors the boot/
		// piggyback probe's failure-path semantics so the UI's
		// "checked X minutes ago" indicator stays accurate.
		if uerr := h.repo.UpdateVersionCheckedAt(c.Request.Context(), req.ID, time.Now()); uerr != nil {
			log.Warn("admin test: write checked-at", "panel_id", req.ID, "err", uerr)
		}
	}
	c.JSON(http.StatusOK, resp)
}

// UpgradePanel triggers a remote 3X-UI panel self-upgrade after a
// pre-flight compat check: PSP refuses to fire the upgrade if the
// remote panel's reported "latest available" version falls outside the
// currently-loaded tested range (driven by docs/compat/xui-compat.json
// via RefreshRemoteCompat). The gate prevents admin from accidentally
// pulling a future schema-breaking release (e.g. the 2026-05-23 v3.1.0
// inbound serialization change that v3.5.1 had to special-case).
//
// Admin can bypass the gate with body {"force": true} — that path is
// the explicit "I know it's untested, do it anyway" escape hatch and is
// audited separately as panel_upgrade_forced so the trail is obvious.
// Force is also the only way to recover when remote-compat JSON has
// never been fetched (CheckXUI returns Unknown for everything, normal
// path always refuses).
//
// 3X-UI's /updatePanel has no version-selection knob — it always pulls
// latest from GitHub. So PSP can't "downgrade" or "pin" the target; it
// can only refuse the call when latest is out of range, or be forced.
//
// On success: a panel_upgrade_initiated (or _forced) audit row is
// written, the post-upgrade smoke probe is scheduled, and the handler
// returns 202 Accepted immediately (the panel restart drops the
// connection mid-call; the adapter swallows the resulting EOF). The
// smoke probe writes a follow-up panel_upgrade_succeeded / _failed
// audit row when it eventually concludes.
type upgradePanelRequest struct {
	Force bool `json:"force"`
}

// UpgradePreview is the READ-ONLY pre-flight for the remote 3X-UI upgrade. It
// reports what version the panel would upgrade TO (3X-UI's /updatePanel only
// ever pulls the latest GitHub release — there is no version-pin knob), whether
// that target is inside PSP's tested range, and any admin-facing advisory for
// that version (breaking changes, especially ones that also restart/upgrade the
// bundled xray-core, which /updatePanel does in one shot). It fires NOTHING —
// the UI shows this in the upgrade confirm dialog, then POSTs UpgradePanel on
// confirm. GET so it's clearly side-effect-free and cacheable.
func (h *AdminServersHandler) UpgradePreview(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	updater, ok := client.(ports.PanelUpdater)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	info, err := updater.GetPanelUpdateInfo(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to query 3X-UI update info: " + err.Error()})
		return
	}
	resp := gin.H{
		"update_available": info.UpdateAvailable,
		"current_version":  info.CurrentVersion,
		"target_version":   info.LatestVersion,
	}
	if !info.UpdateAvailable {
		// Nothing to upgrade — the UI skips the confirm and shows "already latest".
		resp["already_latest"] = true
		c.JSON(http.StatusOK, resp)
		return
	}
	// Force a fresh compat-JSON fetch so BOTH the support gate and the advisory
	// reflect the newest published data, not a stale boot / last-page-open cache
	// (mirrors UpgradePanel's pre-gate refresh). Best-effort: on failure we fall
	// back to the currently-active range + advisories.
	if rerr := version.RefreshRemoteCompat(context.Background(), "", true); rerr != nil {
		log.Debug("upgrade-preview: force-refresh remote compat", "panel_id", id, "err", rerr)
	}
	compat := version.CheckXUI(info.LatestVersion)
	resp["compat_status"] = compat.String()
	resp["compat_message"] = version.CompatMessage(info.LatestVersion, compat)
	resp["psp_min_xui"] = version.ActiveMinXUI()
	resp["psp_max_xui"] = version.ActiveMaxTestedXUI()
	// can_force mirrors UpgradePanel's gate: an untested target is overridable,
	// so the UI can pre-color the confirm as "forced" before the admin proceeds.
	resp["can_force"] = compat != version.CompatSupported
	if adv, ok := version.LookupXUIAdvisory(info.LatestVersion); ok {
		resp["advisory"] = gin.H{
			"severity":     adv.Severity,
			"affects_xray": adv.AffectsXray,
			"text":         adv.Text,
		}
	}
	c.JSON(http.StatusOK, resp)
}

func (h *AdminServersHandler) UpgradePanel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	var req upgradePanelRequest
	_ = c.ShouldBindJSON(&req) // body optional; missing → force=false
	// Pre-flight: ask the panel what version it would upgrade TO.
	updater, ok := client.(ports.PanelUpdater)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"ok": false, "error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	info, err := updater.GetPanelUpdateInfo(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to query 3X-UI update info: " + err.Error()})
		return
	}
	if !info.UpdateAvailable {
		// Already on the latest release — fire nothing. /server/updatePanel on
		// an up-to-date panel is a no-op that 3X-UI reports as an error, which
		// otherwise surfaces as a spurious "upgrade failed" (the common
		// select-all-and-upgrade case for a panel already at the target). No
		// audit row: nothing happened.
		c.JSON(http.StatusOK, gin.H{
			"ok":              true,
			"already_latest":  true,
			"current_version": info.CurrentVersion,
			"latest_version":  info.LatestVersion,
			"message":         "Panel is already on the latest version (" + info.CurrentVersion + "); nothing to upgrade.",
		})
		return
	}
	// Force a FRESH compat-JSON fetch right before the support gate. The active
	// tested range is otherwise only as new as boot or the last Servers-page
	// open (≤ once/60s), so a just-published bump (e.g. max_tested → 3.2.8)
	// wouldn't be seen and the upgrade could be wrongly blocked on a stale
	// cache. force=true bypasses the 60s throttle; best-effort — if GitHub is
	// unreachable we fall back to the current active range (same as before).
	if rerr := version.RefreshRemoteCompat(context.Background(), "", true); rerr != nil {
		log.Warn("upgrade-panel: force-refresh remote compat", "panel_id", id, "err", rerr)
	}
	compat := version.CheckXUI(info.LatestVersion)
	if compat != version.CompatSupported && !req.Force {
		// Refuse: latest is outside the currently-loaded tested range
		// (or compat data isn't loaded yet → everything is Unknown).
		// Audit the rejection so the trail shows admin tried + got
		// blocked + can retry with force.
		h.writeUpgradeAudit(c, "panel_upgrade_blocked", panel, info.LatestVersion,
			"target latest version "+info.LatestVersion+" outside PSP active tested range ["+version.ActiveMinXUI()+", "+version.ActiveMaxTestedXUI()+"] (compat="+compat.String()+")")
		c.JSON(http.StatusConflict, gin.H{
			"ok":             false,
			"reason":         "untested_target",
			"latest_version": info.LatestVersion,
			"compat_status":  compat.String(),
			"psp_min_xui":    version.ActiveMinXUI(),
			"psp_max_xui":    version.ActiveMaxTestedXUI(),
			"message":        version.CompatMessage(info.LatestVersion, compat) + " — upgrade PSP first, or resend with {force: true} to override at your own risk",
			"can_force":      true,
		})
		return
	}
	// Fire the upgrade. The adapter swallows the EOF/reset that the
	// panel restart produces, so a nil err here means "upgrade signal
	// accepted; panel is restarting".
	if err := updater.UpdatePanel(c.Request.Context()); err != nil {
		h.writeUpgradeAudit(c, "panel_upgrade_failed", panel, info.LatestVersion, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "updatePanel call failed: " + err.Error()})
		return
	}
	// Distinguish "normal initiate" vs "forced through gate" in the
	// audit trail — both are admin-initiated but forced carries an
	// implicit "operator accepted the untested-target risk" context.
	action := "panel_upgrade_initiated"
	detail := ""
	if req.Force && compat != version.CompatSupported {
		action = "panel_upgrade_forced"
		detail = "compat=" + compat.String() + " (out of active tested range), admin overrode the gate"
	}
	h.writeUpgradeAudit(c, action, panel, info.LatestVersion, detail)
	// Schedule the smoke probe — runs in the panel-wide background
	// context so it survives this request returning. async + audit
	// must be present for the smoke to run; if either is nil this
	// gracefully degrades to "fire and forget, no follow-up audit".
	if h.async != nil {
		panelID := id
		panelName := panel.Name
		targetVersion := info.LatestVersion
		h.async.Go("upgrade-panel.smoke", func(bg context.Context) {
			h.runPostUpgradeSmoke(bg, panelID, panelName, targetVersion)
		})
	}
	c.JSON(http.StatusAccepted, gin.H{
		"ok":             true,
		"started":        true,
		"target_version": info.LatestVersion,
		"message":        "3X-UI upgrade initiated; the panel is restarting. PSP will run a smoke probe in ~60s and log success or failure to the audit trail.",
	})
}

// ListXrayVersions returns the versions this backend can install. A native PSP
// node returns the exact shared catalog plus its audit/handshake evidence;
// legacy 3X-UI returns its own tag list. GET keeps the lookup read-only and
// cacheable.
func (h *AdminServersHandler) ListXrayVersions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	updater, ok := client.(ports.CoreUpdater)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	versions, err := updater.GetCoreVersionList(c.Request.Context())
	if err != nil {
		// Legacy 3X-UI can still fall back to "latest" in the frontend. A
		// native node fails closed because an unavailable catalog must not
		// turn into an unaudited install.
		c.JSON(http.StatusBadGateway, gin.H{"error": "GetXrayVersion failed: " + err.Error()})
		return
	}
	available := make(map[string]struct{}, len(versions))
	for _, coreVersion := range versions {
		available[coreVersion] = struct{}{}
	}
	catalog, catalogErr := corecatalog.List("xray")
	if catalogErr != nil {
		respondError(c, catalogErr)
		return
	}
	metadata := make([]corecatalog.Release, 0, len(catalog))
	for _, release := range catalog {
		if _, ok := available[release.Version]; ok {
			metadata = append(metadata, release)
		}
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions, "releases": metadata})
}

// ListCoreReleases returns the complete audited engine/version matrix for a
// PSP-native node. Keeping this separate from the legacy xray-versions route
// avoids pretending a 3X-UI panel can switch engine families.
func (h *AdminServersHandler) ListCoreReleases(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Core engine selection is available only for PSP native nodes"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	selector, ok := client.(ports.CoreEngineSelector)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	engines := []domain.NodeCoreEngine{domain.NodeCoreXray, domain.NodeCoreSingBox}
	releases := make([]corecatalog.Release, 0)
	for _, engine := range engines {
		versions, listErr := selector.GetCoreVersionListForEngine(c.Request.Context(), engine)
		if listErr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Get core catalog failed: " + listErr.Error()})
			return
		}
		available := make(map[string]struct{}, len(versions))
		for _, version := range versions {
			available[version] = struct{}{}
		}
		catalog, catalogErr := corecatalog.List(string(engine))
		if catalogErr != nil {
			respondError(c, catalogErr)
			return
		}
		for _, release := range catalog {
			if _, exists := available[release.Version]; exists {
				releases = append(releases, release)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"releases": releases})
}

type selectCoreRequest struct {
	Engine            string `json:"engine"`
	Version           string `json:"version"`
	ConfirmRestricted bool   `json:"confirm_restricted"`
}

// SelectCore records one exact, catalog-audited deployment identity. The
// dialing node downloads and validates it on its next synchronization round;
// the HTTP success therefore means accepted intent, never observed runtime.
func (h *AdminServersHandler) SelectCore(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Core engine selection is available only for PSP native nodes"})
		return
	}
	var req selectCoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	engine := domain.NodeCoreEngine(req.Engine)
	if !engine.Valid() || req.Version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "reason": "invalid_core_selection", "error": "supported engine and exact version are required"})
		return
	}
	release, err := corecatalog.Resolve(string(engine), req.Version)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "reason": "core_not_audited", "error": err.Error()})
		return
	}
	if release.RequiresConfirmation && !req.ConfirmRestricted {
		c.JSON(http.StatusConflict, gin.H{
			"ok": false, "reason": "restricted_core_confirmation_required",
			"engine": release.Engine, "version": release.Version, "release": release,
		})
		return
	}
	if !release.RequiresConfirmation && req.ConfirmRestricted {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "reason": "unexpected_restricted_confirmation"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	selector, ok := client.(ports.CoreEngineSelector)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"ok": false, "error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	target := release.Engine + "/" + release.Version
	if err := selector.InstallCoreEngine(c.Request.Context(), engine, release.Version, req.ConfirmRestricted); err != nil {
		h.writeUpgradeAudit(c, "core_selection_failed", panel, target, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "select core failed: " + err.Error()})
		return
	}
	h.writeUpgradeAudit(c, "core_selection_requested", panel, target, "")
	c.JSON(http.StatusAccepted, gin.H{
		"ok": true, "engine": release.Engine, "version": release.Version,
		"message": "Core intent recorded; the native node will download, validate, switch atomically and report the observed deployment.",
	})
}

// WebCert proxies GET /panel/api/server/getWebCertFiles on the panel and
// returns that panel's own web TLS cert/key file PATHS (not the PEM bytes).
// Panel-scoped: the cert is identical for every inbound on the panel, and this
// serves both the create- and edit-node forms (each carries a panel_id).
// Backs cert_source=from_panel. A pre-3.2.7 panel has no such route, which the
// xui adapter surfaces as ports.ErrXUIEndpointUnsupported → reported here as
// 200 {"supported":false} so the node form greys out the "fetch from panel"
// button WITHOUT firing the global error toast a 4xx/5xx would trigger. A
// genuine upstream failure still 502s so the normal error path applies.
func (h *AdminServersHandler) WebCert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	provider, ok := client.(ports.WebCertProvider)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"supported": false})
		return
	}
	wc, err := provider.GetWebCertFiles(c.Request.Context())
	if err != nil {
		if errors.Is(err, ports.ErrXUIEndpointUnsupported) {
			c.JSON(http.StatusOK, gin.H{"supported": false})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "getWebCertFiles failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"supported": true, "cert_file": wc.CertFile, "key_file": wc.KeyFile})
}

// UpgradeXray triggers or declares a core install. Legacy 3X-UI keeps its
// synchronous upstream behavior. A PSP-native node accepts only the shared
// audited catalog, persists the chosen version as desired state, and returns
// 202 until the dialing agent applies and reports it.
type upgradeXrayRequest struct {
	Version           string `json:"version"`
	ConfirmRestricted bool   `json:"confirm_restricted"`
}

func (h *AdminServersHandler) UpgradeXray(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	var req upgradeXrayRequest
	// Legacy 3X-UI keeps its historical empty=latest behavior. A native PSP
	// node instead defaults to the audited recommended release and never
	// accepts latest or an unlisted version.
	_ = c.ShouldBindJSON(&req)
	if req.Version == "" && panel.Kind == domain.PanelKindPSP {
		recommended, resolveErr := corecatalog.Recommended("xray")
		if resolveErr != nil {
			respondError(c, resolveErr)
			return
		}
		req.Version = recommended.Version
	} else if req.Version == "" {
		req.Version = "latest"
	}
	if panel.Kind == domain.PanelKindPSP {
		release, resolveErr := corecatalog.Resolve("xray", req.Version)
		if resolveErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "reason": "core_not_audited", "error": resolveErr.Error()})
			return
		}
		if release.RequiresConfirmation && !req.ConfirmRestricted {
			c.JSON(http.StatusConflict, gin.H{
				"ok": false, "reason": "restricted_core_confirmation_required",
				"version": release.Version, "release": release,
			})
			return
		}
		if !release.RequiresConfirmation && req.ConfirmRestricted {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "reason": "unexpected_restricted_confirmation"})
			return
		}
	}
	updater, ok := client.(ports.CoreUpdater)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"ok": false, "error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	if err := updater.InstallCore(c.Request.Context(), req.Version); err != nil {
		h.writeUpgradeAudit(c, "xray_upgrade_failed", panel, req.Version, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "installXray failed: " + err.Error()})
		return
	}
	if applier, ok := client.(ports.AsynchronousApplier); ok && applier.ApplyIsAsynchronous() {
		h.writeUpgradeAudit(c, "xray_upgrade_requested", panel, req.Version, "")
		c.JSON(http.StatusAccepted, gin.H{
			"ok": true, "version": req.Version,
			"message": "Core version intent recorded; the native node will download, validate, switch and report the observed version.",
		})
		return
	}
	h.writeUpgradeAudit(c, "xray_upgrade_completed", panel, req.Version, "")
	// Refresh version snapshot — installXray triggers an xray restart
	// so the panel's reported xray.version field updates immediately.
	if status, perr := client.GetServerStatus(c.Request.Context()); perr == nil {
		now := time.Now()
		if err := h.repo.UpdateVersion(c.Request.Context(), id, status.PanelVersion, status.XrayVersion, &now); err != nil {
			log.Warn("xray upgrade: write version", "panel_id", id, "err", err)
		} else if h.invalidateRender != nil {
			h.invalidateRender()
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"version": req.Version,
		"message": "Xray upgrade completed.",
	})
}

// runPostUpgradeSmoke waits for the panel to come back after a
// /updatePanel restart, then verifies both that /server/status returns
// 200 (panel is alive) and that /inbounds/list decodes (schema didn't
// silently break, à la the v3.1.0 incident). Records the outcome via
// audit. Runs in the panel-wide background context so it survives the
// admin's HTTP request returning.
//
// Timing: 60s initial grace (3X-UI usually takes ~10-15s to come back
// up; 60s gives broad headroom), then poll every 10s for up to 2 minutes
// of additional retries (12 attempts). If status comes back but
// /inbounds/list errors with a JSON decode failure, that's the v3.1.0
// pattern — flagged explicitly so admin grep on "schema_break" finds it.
func (h *AdminServersHandler) runPostUpgradeSmoke(ctx context.Context, panelID int64, panelName, targetVersion string) {
	const initialGrace = 60 * time.Second
	const probeInterval = 10 * time.Second
	const maxAttempts = 12

	select {
	case <-time.After(initialGrace):
	case <-ctx.Done():
		// PSP shutting down before the grace period elapsed. Record
		// the abort with a background ctx (the caller's ctx is already
		// cancelled, so audit.Insert on it would fail) so the audit
		// trail doesn't dead-end at panel_upgrade_initiated and admin
		// can see the upgrade never got its smoke probe.
		h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
			"smoke probe cancelled (PSP shutdown?) during initial grace window — admin should manually verify the panel via test/Servers page")
		return
	}
	client, err := h.pool.Get(panelID)
	if err != nil {
		h.writeSmokeAudit(ctx, "panel_upgrade_failed", panelID, panelName, targetVersion,
			"pool lost panel client: "+err.Error())
		return
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
				"smoke probe cancelled (PSP shutdown?) during retry loop on attempt "+strconv.Itoa(attempt)+" — admin should manually verify")
			return
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, serr := client.GetServerStatus(probeCtx)
		cancel()
		if serr != nil {
			lastErr = serr
			log.Debug("upgrade smoke: panel still down", "panel_id", panelID, "attempt", attempt, "err", serr)
			select {
			case <-time.After(probeInterval):
			case <-ctx.Done():
				h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
					"smoke probe cancelled during retry sleep on attempt "+strconv.Itoa(attempt)+" — admin should manually verify")
				return
			}
			continue
		}
		// /status came back — second probe: schema-decode of
		// /inbounds/list. If decoding errors, that's the schema-break
		// signature (the v3.1.0 incident shape).
		listCtx, lcancel := context.WithTimeout(ctx, 15*time.Second)
		_, lerr := client.ListInbounds(listCtx)
		lcancel()
		if lerr != nil {
			h.writeSmokeAudit(ctx, "panel_upgrade_schema_break", panelID, panelName, targetVersion,
				"panel reachable but /inbounds/list decode failed: "+lerr.Error())
			return
		}
		// All good — refresh the cached version snapshot too so the
		// Servers UI immediately reflects the post-upgrade version.
		now := time.Now()
		if err := h.repo.UpdateVersion(ctx, panelID, status.PanelVersion, status.XrayVersion, &now); err != nil {
			log.Warn("post-upgrade smoke: write version", "panel_id", panelID, "err", err)
		} else if h.invalidateRender != nil {
			h.invalidateRender()
		}
		h.writeSmokeAudit(ctx, "panel_upgrade_succeeded", panelID, panelName, targetVersion,
			"panel back online at "+status.PanelVersion+" (xray "+status.XrayVersion+"), inbounds decode ok")
		return
	}
	if lastErr == nil {
		lastErr = errors.New("unknown")
	}
	h.writeSmokeAudit(ctx, "panel_upgrade_failed", panelID, panelName, targetVersion,
		"panel still unreachable after "+initialGrace.String()+" grace + "+(probeInterval*maxAttempts).String()+" of retries: "+lastErr.Error())
}

// writeUpgradeAudit is the in-request audit writer. detail is opaque text
// stored in the after_json column (audit table reuses it as a free-form
// field for non-CRUD events).
func (h *AdminServersHandler) writeUpgradeAudit(c *gin.Context, action string, panel *domain.XUIPanel, targetVersion, detail string) {
	if h.audit == nil {
		return
	}
	target := "panel=" + strconv.FormatInt(panel.ID, 10) + " name=" + panel.Name + " target=" + targetVersion
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor:     actorFromGin(c),
		Action:    action,
		Target:    target,
		AfterJSON: detail,
		IP:        c.ClientIP(),
		At:        time.Now(),
	})
}

// writeSmokeAudit is the smoke-probe audit writer. Runs in the background
// context (no gin.Context), so actor is hardcoded to "upgrade-smoke".
func (h *AdminServersHandler) writeSmokeAudit(ctx context.Context, action string, panelID int64, panelName, targetVersion, detail string) {
	if h.audit == nil {
		return
	}
	target := "panel=" + strconv.FormatInt(panelID, 10) + " name=" + panelName + " target=" + targetVersion
	_ = h.audit.Insert(ctx, &domain.AuditEntry{
		Actor:     "upgrade-smoke",
		Action:    action,
		Target:    target,
		AfterJSON: detail,
		At:        time.Now(),
	})
}

// actorFromGin extracts the acting admin's UPN from the request's JWT claims
// for the audit trail. The auth middleware sets the parsed Claims (not a bare
// "upn" context key — reading that always missed and logged everyone as
// "admin"), so go through ClaimsFrom like the audit middleware does.
func actorFromGin(c *gin.Context) string {
	if claims := middleware.ClaimsFrom(c); claims != nil && claims.UPN != "" {
		return claims.UPN
	}
	return "admin"
}

func (h *AdminServersHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	// Refuse if any node still references this server.
	all, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	for _, n := range all {
		if n.PanelID == id {
			c.JSON(http.StatusConflict, gin.H{
				"error": "Server still has nodes attached; delete or reassign them first",
			})
			return
		}
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if panel.Kind == domain.PanelKindPSP && h.native == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "native node provisioning is not wired in this build"})
		return
	}
	if panel.Kind == domain.PanelKindPSP {
		err = h.native.DeleteConverged(c.Request.Context(), id)
	} else {
		err = h.repo.Delete(c.Request.Context(), id)
	}
	if err != nil {
		mapServerError(c, err)
		return
	}
	_ = h.pool.Remove(id)
	c.Status(http.StatusNoContent)
}

// ---- helpers ----

func toServerDTO(p *domain.XUIPanel) serverDTO {
	dto := serverDTO{
		ID:               p.ID,
		Kind:             string(domain.NormalizePanelKind(p.Kind)),
		Name:             p.Name,
		URL:              p.URL,
		Username:         p.Username,
		Remark:           p.Remark,
		HasAPIToken:      p.APIToken != "",
		HasPassword:      p.Password != "",
		AuthMethod:       string(effectiveAuthMethod(p)),
		InsecureHTTPS:    p.InsecureSkipVerify,
		PanelVersion:     p.PanelVersion,
		XrayVersion:      p.XrayVersion,
		VersionCheckedAt: p.VersionCheckedAt,
	}
	if domain.NormalizePanelKind(p.Kind) == domain.PanelKindPSP {
		dto.URL = ""
		dto.AuthMethod = ""
	}
	// Only compute compat fields when there's actually a probed version —
	// "never probed" panels stay blank rather than displaying a meaningless
	// "unknown" badge that admins would have to dismiss.
	switch {
	case domain.NormalizePanelKind(p.Kind) == domain.PanelKind3XUI && p.PanelVersion != "":
		status := version.CheckXUI(p.PanelVersion)
		dto.CompatStatus = status.String()
		dto.CompatMessage = version.CompatMessage(p.PanelVersion, status)
	// S-UI reports compat only once a verified range has actually been published
	// (sui_entries in the compat JSON). Without that, ActiveMaxTestedSUI is ""
	// and every panel would render a permanent, un-actionable "unknown" badge —
	// so stay blank, exactly as before S-UI gating existed. Publishing a row
	// lights this up with no PSP release.
	case domain.NormalizePanelKind(p.Kind) == domain.PanelKindSUI &&
		p.PanelVersion != "" && version.ActiveMaxTestedSUI() != "":
		status := version.CheckSUI(p.PanelVersion)
		dto.CompatStatus = status.String()
		dto.CompatMessage = version.CompatMessageSUI(p.PanelVersion, status)
	}
	if domain.NormalizePanelKind(p.Kind) == domain.PanelKind3XUI {
		state := p.IPLimitEnforcement
		if !state.Valid() {
			state = domain.IPLimitEnforcementUnknown
		}
		dto.IPLimitEnforcement = string(state)
		dto.IPLimitProbedAt = p.IPLimitProbedAt
	}
	// Derive the "update available" indicator from the PSP-wide latest
	// tag rather than a per-panel column. Same snapshot drives every
	// panel's badge, so the kebab dots flip in lockstep with one fetch.
	if latest := version.LatestXUI(); domain.NormalizePanelKind(p.Kind) == domain.PanelKind3XUI && latest != "" && p.PanelVersion != "" {
		dto.LatestXUIVersion = latest
		dto.UpdateAvailable = version.IsXUIUpdateAvailable(p.PanelVersion)
	}
	return dto
}

// refreshIPLimitEnforcement re-probes one node's fail2ban preconditions and
// stores the result, returning what it concluded.
//
// ok=false means the probe produced nothing usable and the STORED state was
// left alone. That is deliberate and matches the background probe: a blip must
// not turn "enforced" into "unknown", because unknown is what an operator sees
// when the probe itself is broken and it would send them after the wrong fault.
//
// A panel older than 3.7.0 has no such route, which is an answer — recorded as
// unsupported rather than as a fault, so it does not become a permanent warning
// on a node that may well be enforcing fine.
func (h *AdminServersHandler) refreshIPLimitEnforcement(
	ctx context.Context, panelID int64, client ports.XUIClient, now time.Time,
) (domain.IPLimitEnforcement, bool) {
	reader, ok := client.(ports.Fail2banReader)
	if !ok {
		return "", false
	}
	st, err := reader.GetFail2banStatus(ctx)
	state := domain.IPLimitEnforcementUnsupported
	switch {
	case errors.Is(err, ports.ErrXUIEndpointUnsupported):
	case err != nil:
		log.Debug("admin test: ip-cap probe failed; keeping the last known state",
			"panel_id", panelID, "err", err)
		return "", false
	default:
		state = domain.ClassifyIPLimit(*st)
	}
	if uerr := h.repo.UpdateIPLimitEnforcement(ctx, panelID, state, now); uerr != nil {
		log.Warn("admin test: write ip-cap state", "panel_id", panelID, "err", uerr)
	}
	return state, true
}

func (h *AdminServersHandler) toServerDTO(ctx context.Context, p *domain.Panel) serverDTO {
	var agent *domain.NodeAgent
	if h.agents != nil && domain.NormalizePanelKind(p.Kind) == domain.PanelKindPSP {
		loaded, err := h.agents.GetByPanelID(ctx, p.ID)
		if err == nil {
			agent = loaded
		} else if !errors.Is(err, domain.ErrNotFound) {
			log.Warn("admin servers: load native core selection", "panel_id", p.ID, "err", err)
		}
	}
	return h.toServerDTOWithAgent(p, agent)
}

func (h *AdminServersHandler) toServerDTOWithAgent(p *domain.Panel, agent *domain.NodeAgent) serverDTO {
	dto := toServerDTO(p)
	if domain.NormalizePanelKind(p.Kind) == domain.PanelKindPSP {
		dto.CoreVersion = p.XrayVersion
		if agent != nil {
			dto.CoreEngine = string(agent.ObservedCoreEngine)
			dto.DesiredCoreEngine = string(domain.NormalizeNodeCoreEngine(agent.DesiredCoreEngine))
			dto.DesiredCoreVersion = agent.DesiredCoreVersion
		}
	}
	client, err := h.pool.Get(p.ID)
	if err != nil {
		return dto
	}
	if provider, ok := client.(ports.CapabilityProvider); ok {
		dto.Capabilities = provider.Capabilities()
	}
	return dto
}

func mapServerError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		respondError(c, err)
	}
}
