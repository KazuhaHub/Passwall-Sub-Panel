package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// Node enrollment: the panel hands out a one-line command, the node runs it and
// registers ITSELF.
//
// Every field an operator used to type by hand here was a way to get it wrong,
// and this session produced a live example of each: the panel URL (loopback is
// refused by the dial guard, so the obvious same-host answer fails), the API
// token's scope (a non-admin scope 403s, and PSP treats 403 as permanent), and
// XUI_ENABLE_FAIL2BAN (setting it to "1" reads as "on" and silently disables
// the concurrent-IP cap). None of those are judgement calls; they are facts the
// two machines can establish between themselves.
//
// The direction is deliberate. PSP cannot reach into a fresh node, but a node
// can always reach PSP — and by calling in, it hands PSP the one thing no form
// can supply: an address PSP has just demonstrably received a packet from.

// nodeEnrollTTL bounds the window in which a leaked one-liner is useful. Long
// enough to paste into a terminal and let a package manager run, short enough
// that a command left in shell history or a chat log is inert by the time
// anyone finds it.
const nodeEnrollTTL = 30 * time.Minute

// NodeEnrollHandler serves the three sides of enrollment: minting the command
// (admin), serving the script (public), and accepting the callback (public,
// gated by the one-time token).
type NodeEnrollHandler struct {
	tokens ports.AuthTokenRepo
	panels ports.XUIPanelRepo
	pool   ports.XUIPool
	// probe builds a client for a candidate URL and asks the panel who it is.
	// Injected so the callback path can be tested without a live panel.
	probe func(ctx context.Context, p *domain.XUIPanel) (*ports.ServerStatus, error)
}

func NewNodeEnrollHandler(tokens ports.AuthTokenRepo, panels ports.XUIPanelRepo, pool ports.XUIPool,
	probe func(ctx context.Context, p *domain.XUIPanel) (*ports.ServerStatus, error)) *NodeEnrollHandler {
	return &NodeEnrollHandler{tokens: tokens, panels: panels, pool: pool, probe: probe}
}

func hashEnrollToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ---- 1. mint (admin) ----

type enrollTokenResponse struct {
	Command   string    `json:"command"`
	Cautious  string    `json:"cautious_command"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Mint issues a single-use enrollment token and returns the command to paste.
//
// The token is stored hashed and attributed to the admin who asked for it, so
// the audit trail names a person rather than "the enrollment endpoint".
func (h *NodeEnrollHandler) Mint(c *gin.Context) {
	if h.probe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node enrollment is not wired in this build"})
		return
	}
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	uid := claims.UserID
	raw, err := idgen.NewSubToken()
	if err != nil {
		respondError(c, err)
		return
	}
	now := time.Now()
	tok := &domain.AuthToken{
		UserID:    uid,
		Purpose:   domain.NodeEnrollPurpose,
		TokenHash: hashEnrollToken(raw),
		ExpiresAt: now.Add(nodeEnrollTTL),
		CreatedAt: now,
	}
	if err := h.tokens.Create(c.Request.Context(), tok); err != nil {
		respondError(c, err)
		return
	}
	base := enrollBaseURL(c)
	c.JSON(http.StatusOK, enrollTokenResponse{
		Command:   fmt.Sprintf("bash <(curl -fsSL %s/enroll/%s)", base, raw),
		Cautious:  fmt.Sprintf("curl -fsSL %s/enroll/%s -o psp-enroll.sh && sha256sum psp-enroll.sh && less psp-enroll.sh && bash psp-enroll.sh", base, raw),
		ExpiresAt: tok.ExpiresAt,
	})
}

// ---- 2. serve the script (public) ----

// Script returns the installer. Deliberately NOT gated on the token being
// valid: the script is inert without a live token (the callback is what
// enforces anything), and checking here would turn this route into an oracle
// that answers "is this token still good" to anyone who asks.
func (h *NodeEnrollHandler) Script(c *gin.Context) {
	token := c.Param("token")
	if !validEnrollToken(token) {
		c.String(http.StatusBadRequest, "# invalid enrollment token\n")
		return
	}
	base := enrollBaseURL(c)
	if !EnrollBaseAllowed(base) {
		// The Host header is caller-controlled and this response is executed as
		// root, so an origin that cannot be represented safely is refused
		// outright rather than escaped.
		c.String(http.StatusBadRequest, "# refusing to build a script for this host\n")
		return
	}
	c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, renderEnrollScript(base, token))
}

// validEnrollToken keeps obviously-malformed input out of the script body,
// which interpolates it into a shell variable.
func validEnrollToken(t string) bool {
	if len(t) < 20 || len(t) > 128 {
		return false
	}
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ---- 3. callback (public, one-time token) ----

type enrollCallbackRequest struct {
	Scheme    string   `json:"scheme"`
	Port      int      `json:"port"`
	BasePath  string   `json:"base_path"`
	APIToken  string   `json:"api_token"`
	Addresses []string `json:"addresses"`
	Hostname  string   `json:"hostname"`
	Fail2ban  string   `json:"fail2ban"`
}

type enrollCallbackResponse struct {
	PanelID    int64    `json:"panel_id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Version    string   `json:"panel_version"`
	Tried      []string `json:"tried"`
	Fail2banAt string   `json:"fail2ban"`
}

// Callback consumes the one-time token and registers the node.
//
// Nothing is written until a candidate URL has answered a real probe. A panel
// row that cannot be reached is worse than no row: it looks configured, so the
// operator debugs the panel instead of the topology.
func (h *NodeEnrollHandler) Callback(c *gin.Context) {
	token := c.Param("token")
	if !validEnrollToken(token) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid enrollment token"})
		return
	}
	if h.probe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node enrollment is not wired in this build"})
		return
	}
	var req enrollCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.APIToken) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the node did not supply an API token"})
		return
	}

	ctx := c.Request.Context()
	// Consumed BEFORE any probing. A token that has been presented is spent
	// whether or not the rest succeeds — otherwise a node that fails to probe
	// leaves a live token that can be retried indefinitely by anyone holding
	// the one-liner.
	if _, err := h.tokens.ConsumeByTokenHash(ctx, domain.NodeEnrollPurpose, hashEnrollToken(token), time.Now()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "enrollment token is expired, already used, or unknown"})
			return
		}
		respondError(c, err)
		return
	}

	candidates, err := domain.NodeEnrollCandidates(domain.NodeEnrollReport{
		Scheme:    req.Scheme,
		Port:      req.Port,
		BasePath:  req.BasePath,
		APIToken:  req.APIToken,
		Addresses: req.Addresses,
		Hostname:  req.Hostname,
	}, enrollObservedAddr(c), safehttp.AllowsIP)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	name := domain.NodeEnrollName(req.Hostname, enrollObservedAddr(c))
	tried := make([]string, 0, len(candidates))
	var lastErr error
	for _, u := range candidates {
		tried = append(tried, domain.ParseEnrollHost(u))
		p := &domain.XUIPanel{
			Kind:       domain.PanelKind3XUI,
			Name:       name,
			URL:        u,
			APIToken:   req.APIToken,
			AuthMethod: domain.XUIAuthToken,
			Remark:     "enrolled automatically",
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		status, perr := h.probe(probeCtx, p)
		cancel()
		if perr != nil {
			lastErr = perr
			log.Debug("enroll: candidate did not answer", "url", domain.ParseEnrollHost(u), "err", perr)
			continue
		}
		p.PanelVersion = status.PanelVersion
		p.XrayVersion = status.XrayVersion
		if err := h.panels.Save(ctx, p); err != nil {
			respondError(c, err)
			return
		}
		if err := h.pool.Add(p); err != nil {
			_ = h.panels.Delete(ctx, p.ID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "register in pool: " + err.Error()})
			return
		}
		log.Info("node enrolled", "panel_id", p.ID, "name", p.Name,
			"url_host", domain.ParseEnrollHost(u), "panel_version", status.PanelVersion,
			"candidates_tried", len(tried), "fail2ban", req.Fail2ban)
		c.JSON(http.StatusCreated, enrollCallbackResponse{
			PanelID: p.ID, Name: p.Name, URL: u,
			Version: status.PanelVersion, Tried: tried, Fail2banAt: req.Fail2ban,
		})
		return
	}

	// Every candidate failed. The reply names what was tried, because the fix
	// is a topology question — a firewall, a listen address, a NAT — and an
	// operator cannot answer it without knowing which addresses were attempted.
	msg := "none of this node's addresses answered from PSP"
	if lastErr != nil {
		msg = msg + ": " + lastErr.Error()
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": msg, "tried": tried})
}

// enrollObservedAddr is the address PSP saw the callback arrive from.
//
// RemoteIP, not ClientIP: this is used to build an address PSP will dial back,
// so what matters is the peer PSP actually has a connection with, not a value
// derived from forwarding headers. Under a permissive trusted_proxies those
// headers are attacker-controlled — and even honest ones name the client of a
// proxy rather than a host PSP can route to.
func enrollObservedAddr(c *gin.Context) string {
	ip := c.RemoteIP()
	if ip == "" {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			return ""
		}
		return host
	}
	return ip
}

// enrollBaseURL is the PSP origin the node will call back to. Taken from the
// request the admin is already talking to PSP on, so a deployment behind a
// proxy or on a non-default port produces a command that works without any
// extra configuration.
func enrollBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if f := c.GetHeader("X-Forwarded-Proto"); f != "" && middleware.ProxyHeadersTrusted(c.Request.Context()) {
		scheme = f
	}
	return scheme + "://" + c.Request.Host
}
