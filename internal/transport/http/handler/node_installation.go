package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-node/deployment"
	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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
		Server: h.toServerDTOWithAgent(panel, agent), AgentID: agent.AgentID,
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

// Generic audit middleware records writes, not GETs. Recovery reads get a
// dedicated metadata-only audit record before releasing the private value.
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
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an exact published Node version is required"})
		return
	}
	provisioning, ok := h.installation(c, panel)
	if !ok {
		return
	}
	script, err := deployment.RenderLinux(deployment.Options{
		Endpoint: provisioning.Endpoint, AgentID: provisioning.AgentID,
		Credential: provisioning.Credential, Version: req.Version,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a canonical HTTPS PSP endpoint and exact published Node version are required"})
		return
	}
	c.Header("Content-Disposition", "attachment; filename=passwall-node-install.sh")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
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
	poll := 30
	if h.nodeSettings != nil {
		settings, err := h.nodeSettings.Load(ctx, ports.UISettings{NodePollSeconds: poll})
		if err != nil {
			return result, err
		}
		if settings.NodePollSeconds > 0 {
			poll = settings.NodePollSeconds
		}
	}
	if agent.OfflineAt(now, 3*time.Duration(poll)*time.Second) {
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
