package handler

import (
	"context"
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

	// Check if the detected client's family is allowed (same first-match order
	// as Detect, so we read the Enabled flag off the family that matched).
	clientBlocked := false
	if detected.Matched {
		for _, fam := range settings.SubClients {
			for _, kw := range fam.Keywords {
				if kw != "" && strings.Contains(strings.ToLower(ua), strings.ToLower(kw)) {
					if !fam.Enabled {
						clientBlocked = true
					}
					goto clientCheckDone
				}
			}
		}
	}
clientCheckDone:

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

		// Increment violation count.
		u.BlockViolationCount++
		u.DisableDetail = fmt.Sprintf("last blocked client: %s", detected.ClientName)

		// Check if auto-disable is enabled and threshold reached.
		if settings.SubBlockAutoDisable && u.BlockViolationCount >= settings.SubBlockAutoDisableCount {
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

		// Save updated violation count.
		if err := h.users.Update(c.Request.Context(), u); err != nil {
			log.Warn("failed to update violation count", "user_id", u.ID, "err", err)
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

	// Log access (best-effort). Record original UA without modification.
	_ = h.subLogs.Insert(c.Request.Context(), &domain.SubLog{
		UserID:     u.ID,
		IP:         c.ClientIP(),
		UA:         ua,
		ClientType: detected.ClientName,
		AccessedAt: time.Now(),
	})

	for k, v := range out.Headers {
		c.Header(k, v)
	}
	c.Data(http.StatusOK, out.ContentType, out.Body)
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
