package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// validationVersionPlaceholder is a version that names no release, used only to
// carry a Docker floating image tag through the installer package's shape check.
// It is never rendered, never published and never compared to anything.
const validationVersionPlaceholder = "1.0.0"

// installTemplateUnverified is the ONE message for a publication that could not be
// obtained or trusted, wherever it happens. It reads the same every time on
// purpose: the operator cannot act on the difference between a signature that did
// not verify and a template that did not download, and a message that named the
// wrong one of the two would send them looking in the wrong place.
// installTemplateNotPublished says the selected release carries no script. It is a
// statement about that release, so the useful next step is a different release
// rather than another attempt at this one.
const installTemplateNotPublished = "the selected Node release does not publish an installation script; choose another release"

const installTemplateUnverified = "the selected Node release's installation script could not be obtained or verified; choose another release or try again"

// installTemplateRefused writes the refusal a failed render deserves and reports
// whether it wrote one.
//
// THE TWO KINDS GET DIFFERENT STATUS CODES because they send an operator to
// different places. A request that cannot be rendered is theirs to fix, so the
// caller's own message — naming what it asked for — is the useful one. A
// publication that could not be obtained or trusted is not theirs to fix, and
// answering "invalid request" to a signature failure would send someone to re-check
// an endpoint that is fine.
func installTemplateRefused(c *gin.Context, err error, requestMessage string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ports.ErrInstallTemplateRequest) {
		c.JSON(http.StatusBadRequest, gin.H{"error": requestMessage})
		return true
	}
	// CHECKED BEFORE THE GENERAL SOURCE FAILURE, which it also satisfies. A release
	// that does not publish the script is not something to try again: the operator
	// needs to pick another release, and telling them to retry sends them nowhere.
	if errors.Is(err, ports.ErrInstallTemplateMissing) {
		c.JSON(http.StatusBadGateway, gin.H{"error": installTemplateNotPublished})
		return true
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": installTemplateUnverified})
	return true
}

// renderInstallScript and validateInstallScript are the ONE place the panel reaches
// the template source. A handler built without one refuses here rather than
// panicking inside a request — the same nil-tolerant shape as every other optional
// dependency on this handler, except that this one has no useful degraded
// behaviour: there is no script to hand over.
func (h *AdminServersHandler) renderInstallScript(ctx context.Context, req ports.InstallTemplateRequest) (string, error) {
	if h.nodeInstall == nil {
		return "", fmt.Errorf("%w: no template source is configured", ports.ErrInstallTemplateSource)
	}
	return h.nodeInstall.Render(ctx, req)
}

func (h *AdminServersHandler) validateInstallScript(req ports.InstallTemplateRequest) error {
	if h.nodeInstall == nil {
		return fmt.Errorf("%w: no template source is configured", ports.ErrInstallTemplateSource)
	}
	return h.nodeInstall.Validate(req)
}

// The installation endpoints belong to the existing administrator-only server
// group. They never mint an identity, rotate a verifier, or publish a secret URL.
func privateNodeResponse(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
}

func (h *AdminServersHandler) installationPanel(c *gin.Context) (*domain.Panel, bool) {
	privateNodeResponse(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid server ID"})
		return nil, false
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return nil, false
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "installation is available only for PSP native servers"})
		return nil, false
	}
	if h.native == nil || h.agents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "native node provisioning is unavailable"})
		return nil, false
	}
	return panel, true
}

func (h *AdminServersHandler) installation(c *gin.Context, panel *domain.Panel) (nativeServerCreateResponse, bool) {
	base := enrollBaseURL(c)
	if !EnrollBaseAllowed(base) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot derive a safe public PSP endpoint"})
		return nativeServerCreateResponse{}, false
	}
	agent, err := h.agents.GetByPanelID(c.Request.Context(), panel.ID)
	if err != nil {
		mapServerError(c, err)
		return nativeServerCreateResponse{}, false
	}
	credential, err := h.native.GetCredential(c.Request.Context(), panel.ID)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusConflict, gin.H{
			"code":  "node_credential_unavailable",
			"error": "this legacy server stores only a credential digest; save the original credential or explicitly rotate it",
		})
		return nativeServerCreateResponse{}, false
	}
	if err != nil {
		// Do not echo a database/decryption error into a secret-bearing response.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot read the encrypted node credential"})
		return nativeServerCreateResponse{}, false
	}
	return nativeServerCreateResponse{
		Server: h.toServerDTOWithAgent(panel, agent, h.compatPolicy(c.Request.Context())), AgentID: agent.AgentID,
		Credential: credential,
		Endpoint:   panelpath.PanelURL(base, panelpath.FromRequest(c.Request), "/v1/node/sync"),
	}, true
}

func (h *AdminServersHandler) NodeInstallation(c *gin.Context) {
	panel, ok := h.installationPanel(c)
	if !ok {
		return
	}
	if result, ok := h.installation(c, panel); ok {
		if !h.auditNodeCredentialRead(c, panel.ID, result.AgentID) {
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// Generic write auditing runs after the response, so it cannot protect secret
// delivery. Installation/recovery reads require metadata-only auditing before
// releasing the private value, whether the route uses GET or POST.
func (h *AdminServersHandler) auditNodeCredentialRead(c *gin.Context, panelID int64, agentID string) bool {
	if h.audit == nil {
		return true // optional only in test/degraded handler compositions
	}
	if err := h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor: actorFromGin(c), Action: "node_credential_read",
		Target: "panel=" + strconv.FormatInt(panelID, 10) + " agent=" + agentID,
		IP:     c.ClientIP(), At: time.Now().UTC(),
	}); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot audit the node credential read"})
		return false
	}
	return true
}

// StoreNodeCredential backfills an old digest-only row. The repository checks
// the current verifier atomically: this operation cannot rebind the server.
func (h *AdminServersHandler) StoreNodeCredential(c *gin.Context) {
	panel, ok := h.installationPanel(c)
	if !ok {
		return
	}
	var req struct {
		Credential string `json:"credential"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validNodeCredential(req.Credential) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node credential"})
		return
	}
	if err := h.native.StoreCredential(c.Request.Context(), panel.ID, req.Credential); err != nil {
		if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "the supplied credential does not match this server"})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot save the encrypted node credential"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminServersHandler) NodeInstallScript(c *gin.Context) {
	panel, ok := h.installationPanel(c)
	if !ok {
		return
	}
	var req struct {
		Version string `json:"version"`
		// Mode is how an operator asks to replace the RELEASE of the installation
		// that is already on the host, keeping its identity. Absent means install.
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an exact published Node version is required"})
		return
	}
	provisioning, ok := h.installation(c, panel)
	if !ok {
		return
	}
	// THE VERSION IS CHECKED BY PSP'S RULE, and then the template renders it.
	//
	// THIS COULD NOT BE DONE FROM HERE UNTIL THE NODE SIDE MOVED. The template
	// used the version as the download path, so a release this panel accepts was
	// refused by the installer that had to fetch it, and the operator was told
	// something about the endpoint instead. The template takes the tag separately
	// now — deployment/release.go derives it — so the two rules agree and the
	// requested version is what it is rendered with.
	if !version.IsReleaseVersion(req.Version) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an exact published Node version is required"})
		return
	}
	script, err := h.renderInstallScript(c.Request.Context(), ports.InstallTemplateRequest{
		Endpoint: provisioning.Endpoint, AgentID: provisioning.AgentID,
		Credential: provisioning.Credential, Version: req.Version, Mode: req.Mode,
		// THE ADDRESS COMES FROM THE PANEL'S OWN CATALOG, not from the version:
		// the releases published before the namespace changed are not addressed
		// the way a derivation would name them.
		Tag: h.nodeReleaseTag(c.Request.Context(), req.Version),
	})
	if installTemplateRefused(c, err, "a canonical HTTPS PSP endpoint, an exact published Node version and a supported installation mode are required") {
		return
	}
	if !h.auditNodeCredentialRead(c, panel.ID, provisioning.AgentID) {
		return
	}
	c.Header("Content-Disposition", "attachment; filename=passwall-node-install.sh")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
}

// NodeInstallationFiles generates private administrator-delivered files, not a
// public bootstrap URL. Retrieving materials never creates or rotates an agent.
func (h *AdminServersHandler) NodeInstallationFiles(c *gin.Context) {
	panel, ok := h.installationPanel(c)
	if !ok {
		return
	}
	var req nodeInstallationFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.normalize() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an exact Node release, supported installation method and platform are required"})
		return
	}
	provisioning, ok := h.installation(c, panel)
	if !ok {
		return
	}
	// Reuse the released Node deployment package's canonical HTTPS, identity and
	// credential validation, with the version the request asked for.
	//
	// THE FLOATING IMAGE TAGS ARE THE ONE EXCEPTION, and they need a version the
	// rule accepts because they are not versions: normalize() has already reduced
	// them to the closed latest/beta set, which only the Docker method may use, so
	// a value is supplied solely to get past the shape check and is never rendered
	// to anyone. It is a version this project could publish — the placeholder used
	// to be v-prefixed, which was the only shape the rule accepted when the
	// installer version doubled as a download path.
	validationVersion := req.Version
	if validationVersion == "latest" || validationVersion == "beta" {
		validationVersion = validationVersionPlaceholder
	}
	if installTemplateRefused(c, h.validateInstallScript(ports.InstallTemplateRequest{
		Endpoint: provisioning.Endpoint, AgentID: provisioning.AgentID,
		Credential: provisioning.Credential, Version: validationVersion,
	}), "a canonical HTTPS PSP endpoint and supported Node image selection are required") {
		return
	}
	if !h.auditNodeCredentialRead(c, panel.ID, provisioning.AgentID) {
		return
	}
	req.Tag = h.nodeReleaseTag(c.Request.Context(), req.Version)
	result := renderNodeInstallationFiles(panel.ID, provisioning, req)
	c.JSON(http.StatusOK, result)
}

type nodeAgentStatusResponse struct {
	State           string     `json:"state"`
	LastSeen        *time.Time `json:"last_seen,omitempty"`
	CoreState       string     `json:"core_state,omitempty"`
	ConfiguredNodes int        `json:"configured_nodes"`
}

func (h *AdminServersHandler) NodeAgentStatus(c *gin.Context) {
	panel, ok := h.installationPanel(c)
	if !ok {
		return
	}
	result, err := h.nodeAgentStatus(c.Request.Context(), panel.ID, time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot read native node status"})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminServersHandler) nodeAgentStatus(ctx context.Context, panelID int64, now time.Time) (nodeAgentStatusResponse, error) {
	agent, err := h.agents.GetByPanelID(ctx, panelID)
	if err != nil {
		return nodeAgentStatusResponse{}, err
	}
	result := nodeAgentStatusResponse{State: "waiting", LastSeen: agent.LastSeen}
	// Configuration exists independently of liveness. A replaced/offline host
	// must not make its retained PSP nodes appear to have disappeared.
	nodes, err := h.nodes.List(ctx)
	if err != nil {
		return result, err
	}
	enabled := 0
	for _, node := range nodes {
		if node != nil && node.PanelID == panelID {
			result.ConfiguredNodes++
			if node.Enabled {
				enabled++
			}
		}
	}
	if agent.LastSeen == nil {
		return result, nil
	}
	poll := nodeprotocol.DefaultNextPollSeconds
	if h.nodeSettings != nil {
		settings, err := h.nodeSettings.Load(ctx, ports.UISettings{NodePollSeconds: poll})
		if err != nil {
			return result, err
		}
		if settings.NodePollSeconds > 0 {
			poll = settings.NodePollSeconds
		}
	}
	silence := 3 * time.Duration(poll) * time.Second
	if agent.OfflineAt(now, silence) {
		// A restart of the PANEL is not evidence about the NODE. last_seen is
		// durable, but it stops advancing while PSP is down, so any outage longer
		// than the silence window minus one poll leaves every healthy agent
		// looking dead the moment PSP returns — which is what an operator saw.
		//
		// Until this process has been listening for a full window, PSP genuinely
		// cannot tell "gone" from "not heard from yet", and reporting "offline"
		// states a failure it has not established. Report the uncertainty under
		// its own name instead; it resolves itself on the node's next check-in,
		// or hardens into offline once the window has actually elapsed.
		if now.Sub(h.startedAt) < silence {
			result.State = "awaiting_checkin"
			return result, nil
		}
		result.State = "offline"
		return result, nil
	}
	result.State = "applying"
	client, err := h.pool.Get(panelID)
	if err != nil {
		return result, err
	}
	status, err := client.GetServerStatus(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return result, nil // heartbeat alone or a stale full enumeration is not ready.
	}
	if err != nil {
		return result, err
	}
	result.CoreState = status.XrayState
	if status.XrayState == "degraded" {
		result.State = "error"
		return result, nil
	}
	streams, err := h.agents.ListStreams(ctx, agent.AgentID)
	if err != nil {
		return result, err
	}
	converged := 0
	for _, stream := range streams {
		if stream != nil && stream.Stream.Valid() && stream.Converged() && stream.AppliedEpoch == agent.Epoch {
			converged++
		}
	}
	if converged != 3 {
		return result, nil
	}
	if result.ConfiguredNodes == 0 {
		result.State = "unconfigured"
		return result, nil
	}
	if status.XrayState != "running" {
		return result, nil
	}
	inbounds, err := client.ListInbounds(ctx)
	if err != nil {
		return result, err
	}
	if len(inbounds) == enabled {
		result.State = "running"
	}
	return result, nil
}
