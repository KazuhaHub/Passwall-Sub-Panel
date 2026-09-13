package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-node/deployment"
	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/nodebootstrap"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const bootstrapTTL = 15 * time.Minute

// Tickets are process-local, bounded and short-lived. Restarting PSP invalidates
// delivery commands, not the persistent server identity. This is deliberately
// NOT a distributed migration coordinator.
type bootstrapTicket struct {
	mu                               sync.Mutex
	serverID                         int64
	expires                          time.Time
	version, endpoint, completionURL string
	agentID, digest                  string
	fingerprint, core                string
	allowRestricted                  bool
	migration                        bool
	downloaded                       bool
	callbackToken                    string
	// Remember the attempted identity before Apply: a lost commit acknowledgement
	// must never cause a retry to create a second identity.
	attempt        *domain.NodeAgent
	applyAttempted bool
}

type NodeBootstrapHandler struct {
	servers   *AdminServersHandler
	migration ports.ServerMigrationRepo
	gate      *operationgate.Gate
	mu        sync.Mutex
	tickets   map[[32]byte]*bootstrapTicket
	now       func() time.Time
}

func NewNodeBootstrapHandler(servers *AdminServersHandler, migration ports.ServerMigrationRepo, gate *operationgate.Gate) *NodeBootstrapHandler {
	return &NodeBootstrapHandler{servers: servers, migration: migration, gate: gate,
		tickets: make(map[[32]byte]*bootstrapTicket), now: time.Now}
}

var bootstrapToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var migrationFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (h *NodeBootstrapHandler) lookup(token string) *bootstrapTicket {
	if !bootstrapToken.MatchString(token) {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.tickets[sha256.Sum256([]byte(token))]
	if t == nil || !h.now().Before(t.expires) {
		return nil
	}
	return t
}

func (h *NodeBootstrapHandler) issue(t *bootstrapTicket) (string, error) {
	token, err := idgen.NewSubToken()
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, old := range h.tickets {
		if !h.now().Before(old.expires) {
			delete(h.tickets, key)
		}
	}
	// Migration uses two lookup keys. A bounded map prevents abandoned mint
	// requests retaining unbounded metadata or credentials in memory.
	needed := 1
	if t.migration {
		needed = 2
	}
	if len(h.tickets)+needed > 512 {
		return "", domain.ErrConflict
	}
	t.expires = h.now().Add(bootstrapTTL)
	if t.migration {
		callback, err := idgen.NewSubToken()
		if err != nil {
			return "", err
		}
		t.callbackToken = callback
		h.tickets[sha256.Sum256([]byte(callback))] = t
	}
	h.tickets[sha256.Sum256([]byte(token))] = t
	return token, nil
}

func (h *NodeBootstrapHandler) reviewedLinux(c *gin.Context, version string) bool {
	if h.servers.nodeReleases == nil {
		bootstrapError(c, http.StatusServiceUnavailable, "reviewed Node releases are unavailable")
		return false
	}
	list, err := h.servers.nodeReleases.List(c.Request.Context())
	if err != nil {
		bootstrapError(c, http.StatusServiceUnavailable, "cannot verify the selected Node release")
		return false
	}
	for _, release := range list.Releases {
		if release.Version != version {
			continue
		}
		linux, amd64, arm64 := false, false, false
		for _, method := range release.Methods {
			linux = linux || method == "linux"
		}
		for _, platform := range release.Platforms {
			amd64 = amd64 || platform.OS == "linux" && platform.Arch == "amd64"
			arm64 = arm64 || platform.OS == "linux" && platform.Arch == "arm64"
		}
		if linux && amd64 && arm64 {
			return true
		}
	}
	bootstrapError(c, http.StatusBadRequest, "select an exact reviewed Linux Node release")
	return false
}

func bootstrapError(c *gin.Context, status int, message string) {
	privateNodeResponse(c)
	c.JSON(status, gin.H{"error": message})
}

func (h *NodeBootstrapHandler) audit(c *gin.Context, t *bootstrapTicket, action string) bool {
	if h.servers.audit == nil {
		bootstrapError(c, http.StatusServiceUnavailable, "bootstrap auditing is unavailable")
		return false
	}
	if err := h.servers.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor: actorFromGin(c), Action: action, Target: "panel=" + strconv.FormatInt(t.serverID, 10),
		IP: c.ClientIP(), At: h.now().UTC(),
	}); err != nil {
		bootstrapError(c, http.StatusServiceUnavailable, "cannot audit bootstrap delivery")
		return false
	}
	return true
}

func (h *NodeBootstrapHandler) MintInstall(c *gin.Context) {
	panel, ok := h.servers.installationPanel(c)
	if !ok {
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if c.ShouldBindJSON(&req) != nil || !h.reviewedLinux(c, req.Version) {
		if !c.Writer.Written() {
			bootstrapError(c, http.StatusBadRequest, "an exact Node version is required")
		}
		return
	}
	provisioning, ok := h.servers.installation(c, panel)
	if !ok {
		return
	}
	// Validate the endpoint/version before issuing anything executable.
	if _, err := deployment.RenderLinux(deployment.Options{Endpoint: provisioning.Endpoint,
		AgentID: provisioning.AgentID, Credential: provisioning.Credential, Version: req.Version}); err != nil {
		bootstrapError(c, http.StatusBadRequest, "a canonical HTTPS PSP endpoint is required")
		return
	}
	digest := sha256.Sum256([]byte(provisioning.Credential))
	t := &bootstrapTicket{serverID: panel.ID, version: req.Version, endpoint: provisioning.Endpoint,
		agentID: provisioning.AgentID, digest: hex.EncodeToString(digest[:])}
	h.mintResponse(c, t)
}

func (h *NodeBootstrapHandler) MintMigration(c *gin.Context) {
	privateNodeResponse(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Version         string `json:"version"`
		Fingerprint     string `json:"fingerprint"`
		Core            string `json:"core_version"`
		AllowRestricted bool   `json:"allow_restricted_reality"`
		ManagedOnly     bool   `json:"managed_only"`
		SingleInstance  bool   `json:"confirm_single_instance"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&req) != nil || !req.ManagedOnly || !req.SingleInstance || !migrationFingerprint.MatchString(req.Fingerprint) {
		bootstrapError(c, http.StatusBadRequest, "confirm single PSP instance and managed-only scope with a current preview")
		return
	}
	if h.gate == nil || h.migration == nil || h.servers.serverMigration == nil || h.servers.native == nil || h.servers.agents == nil {
		bootstrapError(c, http.StatusServiceUnavailable, "single-instance migration is unavailable")
		return
	}
	if _, ok := h.servers.pool.(interface{ Replace(*domain.Panel) error }); !ok {
		bootstrapError(c, http.StatusServiceUnavailable, "atomic backend replacement is unavailable")
		return
	}
	if !h.reviewedLinux(c, req.Version) {
		return
	}
	preview, err := h.servers.serverMigration.Preview(c.Request.Context(), id, req.Core, req.AllowRestricted)
	if err != nil || preview == nil || preview.ServerID != id || !preview.CanMigrate || len(preview.Blockers) != 0 || preview.CoreVersion != req.Core || preview.AllowRestrictedReality != req.AllowRestricted ||
		subtle.ConstantTimeCompare([]byte(preview.Fingerprint), []byte(req.Fingerprint)) != 1 {
		bootstrapError(c, http.StatusConflict, "migration preview changed or contains unsupported configuration; refresh it")
		return
	}
	base := enrollBaseURL(c)
	t := &bootstrapTicket{serverID: id, version: req.Version, migration: true,
		fingerprint: req.Fingerprint, core: req.Core, allowRestricted: req.AllowRestricted,
		endpoint:      panelpath.PanelURL(base, panelpath.FromRequest(c.Request), "/v1/node/sync"),
		completionURL: panelpath.PanelURL(base, panelpath.FromRequest(c.Request), "/api/node-bootstrap/complete")}
	if _, err := nodebootstrap.RenderLinuxMigration(nodebootstrap.Options{CompletionURL: t.completionURL, Token: strings.Repeat("a", 43)}); err != nil {
		bootstrapError(c, http.StatusBadRequest, "a canonical HTTPS PSP endpoint is required")
		return
	}
	h.mintResponse(c, t)
}

func (h *NodeBootstrapHandler) mintResponse(c *gin.Context, t *bootstrapTicket) {
	if !h.audit(c, t, "node_bootstrap_mint") {
		return
	}
	token, err := h.issue(t)
	if err != nil {
		bootstrapError(c, http.StatusServiceUnavailable, "cannot issue an installation command; try again later")
		return
	}
	download := panelpath.PanelURL(enrollBaseURL(c), panelpath.FromRequest(c.Request), "/node-bootstrap/"+token)
	command, err := nodebootstrap.InstallCommand(download)
	if err != nil {
		bootstrapError(c, http.StatusBadRequest, "a canonical HTTPS installation URL is required")
		return
	}
	privateNodeResponse(c)
	c.JSON(http.StatusOK, gin.H{"server_id": t.serverID, "command": command, "expires_at": t.expires.UTC()})
}

func (h *NodeBootstrapHandler) Download(c *gin.Context) {
	privateNodeResponse(c)
	t := h.lookup(c.Param("token"))
	if t == nil {
		bootstrapError(c, http.StatusGone, "installation command expired or unavailable; regenerate it on the same server")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Callback tokens cannot also download a migration wrapper.
	if t.downloaded || !h.now().Before(t.expires) || t.callbackToken == c.Param("token") {
		bootstrapError(c, http.StatusGone, "installation command already used or expired")
		return
	}
	var script string
	var err error
	if t.migration {
		script, err = nodebootstrap.RenderLinuxMigration(nodebootstrap.Options{CompletionURL: t.completionURL, Token: t.callbackToken})
	} else {
		script, err = h.installScript(c.Request.Context(), t, t.agentID, t.digest)
	}
	if err != nil {
		bootstrapError(c, http.StatusConflict, "server identity or credential changed; regenerate installation instructions")
		return
	}
	if !h.audit(c, t, "node_bootstrap_download") {
		return
	}
	// Older curl releases can enforce --max-filesize only when the response
	// announces its size. Keep this bounded script response explicitly framed.
	if len(script) == 0 || len(script) > 1048576 {
		bootstrapError(c, http.StatusServiceUnavailable, "installation script is unavailable")
		return
	}
	t.downloaded = true
	c.Header("Content-Length", strconv.Itoa(len(script)))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
}

func (h *NodeBootstrapHandler) installScript(ctx context.Context, t *bootstrapTicket, agentID, digest string) (string, error) {
	panel, err := h.servers.repo.GetByID(ctx, t.serverID)
	if err != nil {
		return "", err
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP || panel.URL != "psp://"+agentID {
		return "", domain.ErrConflict
	}
	agent, err := h.servers.agents.GetByPanelID(ctx, t.serverID)
	if err != nil {
		return "", err
	}
	if agent.AgentID != agentID || subtle.ConstantTimeCompare([]byte(agent.CredentialSHA256), []byte(digest)) != 1 {
		return "", domain.ErrConflict
	}
	credential, err := h.servers.native.GetCredential(ctx, t.serverID)
	if err != nil {
		return "", err
	}
	rawDigest := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(rawDigest[:])), []byte(digest)) != 1 {
		return "", domain.ErrConflict
	}
	return deployment.RenderLinux(deployment.Options{Endpoint: t.endpoint, AgentID: agentID, Credential: credential, Version: t.version})
}

func (h *NodeBootstrapHandler) Complete(c *gin.Context) {
	privateNodeResponse(c)
	auth := c.GetHeader("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	t := h.lookup(token)
	var req struct {
		OldBackendStopped bool `json:"old_backend_stopped"`
	}
	if t == nil || !t.migration || token != t.callbackToken || auth != "Bearer "+token || c.ShouldBindJSON(&req) != nil || !req.OldBackendStopped {
		bootstrapError(c, http.StatusUnauthorized, "valid migration authorization and old-backend shutdown confirmation required")
		return
	}
	if h.gate == nil {
		bootstrapError(c, http.StatusServiceUnavailable, "single-instance migration is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	var script string
	// Lock order is admission -> ticket. Normal download holds read admission;
	// locking a ticket before exclusive admission would deadlock with it.
	err := h.gate.Exclusive(ctx, func(ctx context.Context) error {
		var err error
		c.Request = c.Request.WithContext(ctx)
		t.mu.Lock()
		defer t.mu.Unlock()
		if !t.downloaded || !h.now().Before(t.expires) {
			return domain.ErrConflict
		}
		if !h.audit(c, t, "node_backend_conversion") {
			return domain.ErrConflict
		}
		if t.attempt == nil {
			identity, err := idgen.NewSubToken()
			if err != nil {
				return err
			}
			t.attempt = &domain.NodeAgent{PanelID: t.serverID, AgentID: "agt_" + identity,
				DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: t.core, AllowRestrictedReality: t.allowRestricted}
		}
		if !t.applyAttempted {
			raw, digest, err := newNativeCredential()
			if err != nil {
				return err
			}
			t.attempt.CredentialSHA256 = digest
			t.applyAttempted = true
			err = h.migration.Apply(ctx, t.serverID, t.fingerprint, t.attempt, raw)
			if err != nil {
				// Known validation/serialization failures are safely retryable.
				// Unknown failures might be a lost commit acknowledgement.
				if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) {
					t.applyAttempted = false
					return err
				}
				_ = h.servers.pool.Remove(t.serverID)
				return err
			}
		}
		// Also serves idempotent retries after a lost response/commit ack. An
		// unrelated/rotated identity can never be released by this ticket.
		script, err = h.installScript(ctx, t, t.attempt.AgentID, t.attempt.CredentialSHA256)
		if err != nil {
			_ = h.servers.pool.Remove(t.serverID)
			return err
		}
		panel, err := h.servers.repo.GetByID(ctx, t.serverID)
		if err != nil {
			_ = h.servers.pool.Remove(t.serverID)
			return err
		}
		replacer, ok := h.servers.pool.(interface{ Replace(*domain.Panel) error })
		if !ok {
			_ = h.servers.pool.Remove(t.serverID)
			return domain.ErrConflict
		}
		if err = replacer.Replace(panel); err != nil {
			_ = h.servers.pool.Remove(t.serverID)
			return err
		}
		return nil
	})
	if err != nil {
		if c.Writer.Written() {
			return
		}
		bootstrapError(c, http.StatusConflict, "migration could not be confirmed; inspect this same server in PSP and regenerate recovery instructions; do not restart old x-ui automatically")
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
}
