package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientdetect"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// blockViolationDedupWindow caps how often a single user's blocked-client
// violation count can advance. Proxy clients (Clash, etc.) poll the
// subscription on a timer; without this window, passive polling with a
// blocked UA alone would climb to the auto-disable threshold and lock out a
// user who never touched anything. One fetch burst ≈ one violation.
const blockViolationDedupWindow = 10 * time.Minute

// SubHandler serves the public subscription endpoint.
type SubHandler struct {
	user     *user.Service
	render   *render.Service
	subLogs  ports.SubLogRepo
	settings ports.SettingsRepo
	users    ports.UserRepo
	mailer   *mailer.Service
	async    AsyncDispatcher
}

func NewSubHandler(userSvc *user.Service, renderSvc *render.Service, subLogs ports.SubLogRepo, settings ports.SettingsRepo, users ports.UserRepo, mailerSvc *mailer.Service, async AsyncDispatcher) *SubHandler {
	return &SubHandler{user: userSvc, render: renderSvc, subLogs: subLogs, settings: settings, users: users, mailer: mailerSvc, async: async}
}

func (h *SubHandler) Get(c *gin.Context) {
	// Extract token from the path. The path structure is:
	//   /<sub_path_prefix>/<token>
	// We need to get the last segment as the token.
	token := extractToken(c.Request.URL.Path)
	if token == "" {
		c.String(http.StatusBadRequest, "invalid path")
		return
	}

	// Load settings to get client rules.
	settings, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		// Public endpoint: never surface the raw error (would leak DB /
		// internal details to anonymous callers). Log full detail server-side.
		log.Warn("sub: load settings failed", "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	// Detect client from User-Agent (primary, for access control).
	ua := c.GetHeader("User-Agent")
	detected := clientdetect.Detect(ua, settings.SubClients)

	// Blacklist (default) blocks only a matched-but-disabled family; whitelist
	// blocks anything that isn't matched-and-enabled (so unknown clients are
	// blocked too). See clientdetect.ClientBlocked.
	clientBlocked := clientdetect.ClientBlocked(settings.SubClientFilterMode, detected)

	// Look up user by token.
	u, err := h.user.GetBySubToken(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.String(http.StatusNotFound, "")
			return
		}
		log.Warn("sub: lookup by token failed", "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	now := time.Now()
	if u.EmergencyUntil != nil && now.After(*u.EmergencyUntil) && u.AutoDisabledReason == domain.DisabledTrafficExceeded {
		c.String(http.StatusForbidden, "emergency access expired")
		return
	}
	if !u.Enabled {
		c.String(http.StatusForbidden, "disabled")
		return
	}
	if u.IsExpired(now) {
		c.String(http.StatusForbidden, "expired")
		return
	}

	// If client is blocked, handle violation tracking and potential auto-disable.
	if clientBlocked {
		// Log the violation (best-effort).
		_ = h.subLogs.Insert(c.Request.Context(), &domain.SubLog{
			UserID:     u.ID,
			IP:         c.ClientIP(),
			UA:         ua,
			ClientType: detected.ClientName,
			AccessedAt: time.Now(),
		})

		// Dedup: only advance the count once per window so a polling client
		// can't auto-disable a passive user. When skipped we also skip the
		// DB write below — the whole point is to cut churn on the hot path.
		countViolation := u.LastBlockViolationAt == nil ||
			now.Sub(*u.LastBlockViolationAt) >= blockViolationDedupWindow

		if countViolation {
			u.BlockViolationCount++
			u.LastBlockViolationAt = &now
			u.DisableDetail = fmt.Sprintf("last blocked client: %s", detected.ClientName)
		}

		// Check if auto-disable is enabled and threshold reached. Only a counted
		// violation can newly cross the threshold.
		if countViolation && settings.SubBlockAutoDisable && u.BlockViolationCount >= settings.SubBlockAutoDisableCount {
			detail := fmt.Sprintf("auto-disabled after %d violations, last client: %s", u.BlockViolationCount, detected.ClientName)
			u.DisableDetail = detail

			// Persist the violation count before SetEnabledAndSync reloads
			// the user and propagates the disabled state to 3X-UI.
			if err := h.users.Update(c.Request.Context(), u); err != nil {
				log.Warn("failed to update blocked-client violation count", "user_id", u.ID, "err", err)
			}
			if err := h.user.SetEnabledAndSync(c.Request.Context(), u.ID, false, domain.DisabledBlockedClient, detail); err != nil {
				log.Warn("failed to auto-disable user", "user_id", u.ID, "err", err)
			}
			u.Enabled = false
			u.AutoDisabledReason = domain.DisabledBlockedClient

			// Send account disabled notification email (async).
			if h.mailer != nil && h.async != nil {
				userCopy := u
				h.async.Go("sub.disabled-email", func(ctx context.Context) {
					if err := h.mailer.SendAccountDisabledNotification(ctx, userCopy, "使用被禁止的客户端", userCopy.DisableDetail); err != nil {
						log.Warn("failed to send disable notification", "user_id", userCopy.ID, "err", err)
					}
				})
			}

			c.String(http.StatusForbidden, "account disabled due to repeated use of blocked client")
			return
		}

		// Save updated violation count (only when we actually advanced it).
		if countViolation {
			if err := h.users.Update(c.Request.Context(), u); err != nil {
				log.Warn("failed to update violation count", "user_id", u.ID, "err", err)
			}
		}

		// Soft notice (async): tell the user they used a blocked client, before
		// they hit the auto-disable threshold. Gate on the already-loaded
		// setting here so we don't spawn a goroutine (and its DB reads) when
		// the feature is off — the per-day cap inside the mailer handles the
		// rest. Pass the loaded UI settings through to avoid re-reading them.
		if settings.SubBlockNotifyUser && h.mailer != nil && h.async != nil {
			userCopy := u
			clientName := detected.ClientName
			uiCfg := settings
			h.async.Go("sub.blocked-client-warning", func(ctx context.Context) {
				if err := h.mailer.SendBlockedClientWarning(ctx, userCopy, clientName, uiCfg); err != nil {
					log.Warn("failed to send blocked-client warning", "user_id", userCopy.ID, "err", err)
				}
			})
		}

		c.String(http.StatusForbidden, "client not allowed")
		return
	}

	// Determine render format: query param can override if client is allowed.
	renderFormat := detected.RenderFormat
	if qf := c.Query("client"); qf != "" {
		renderFormat = clientdetect.NormalizeRenderFormat(qf)
	}
	ct := domain.ClientType(renderFormat)

	// Render subscription.
	out, err := h.render.RenderForUser(c.Request.Context(), u, ct)
	if err != nil {
		log.Warn("sub: render failed", "user_id", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	// ETag is computed off the rendered body so it tracks any change in the
	// generated config — node enable flag, ownership, template, rules, the
	// user's UUID, etc. all flow through render output. Weak form is used
	// because future transport-level transforms (gzip etc.) should not break
	// revalidation: HTTP-semantic equivalence is what we care about.
	etag := computeWeakETag(out.Body)

	// Always advertise the ETag + Cache-Control so the client knows it may
	// revalidate. "no-cache" makes the client always hit us with
	// If-None-Match — we still want the audit-log row, blocked-client check,
	// and traffic-userinfo header refresh on every fetch.
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-cache")

	// Log access (best-effort). Record original UA without modification.
	// 304 still counts as a fetch — admins reading sub_logs would otherwise
	// see an active polling client appear dormant.
	_ = h.subLogs.Insert(c.Request.Context(), &domain.SubLog{
		UserID:     u.ID,
		IP:         c.ClientIP(),
		UA:         ua,
		ClientType: detected.ClientName,
		AccessedAt: time.Now(),
	})

	// Subscription-Userinfo et al. carry live traffic / expiry data — they
	// must be written on both 200 and 304 so a revalidating client still
	// learns the current usage numbers.
	for k, v := range out.Headers {
		c.Header(k, v)
	}

	if ifNoneMatch := c.GetHeader("If-None-Match"); etagMatches(ifNoneMatch, etag) {
		c.Status(http.StatusNotModified)
		return
	}

	c.Data(http.StatusOK, out.ContentType, out.Body)
}

// computeWeakETag returns a weak ETag derived from the response body. 16 hex
// chars (8 bytes of SHA-256) is collision-safe enough for revalidation —
// HTTP caching is best-effort; the worst case of a collision is a stale
// subscription delivered to one client for one revalidation window.
func computeWeakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// etagMatches honors the RFC 9110 §13.1.2 rules just enough for our use:
// a comma-separated list of candidates, plus the "*" wildcard. Weak/strong
// markers compare equal (weak comparison) since we only ever emit weak ETags.
func etagMatches(header, current string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	cur := stripETagWeakPrefix(current)
	for _, raw := range strings.Split(header, ",") {
		if stripETagWeakPrefix(strings.TrimSpace(raw)) == cur {
			return true
		}
	}
	return false
}

func stripETagWeakPrefix(s string) string {
	return strings.TrimPrefix(s, "W/")
}

// extractToken extracts the subscription token from the URL path.
// It returns the last non-empty segment of the path.
func extractToken(path string) string {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
