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
}

func NewSubHandler(userSvc *user.Service, renderSvc *render.Service, subLogs ports.SubLogRepo, settings ports.SettingsRepo, users ports.UserRepo, mailerSvc *mailer.Service) *SubHandler {
	return &SubHandler{user: userSvc, render: renderSvc, subLogs: subLogs, settings: settings, users: users, mailer: mailerSvc}
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Detect client from User-Agent (primary, for access control).
	ua := c.GetHeader("User-Agent")
	detected := clientdetect.Detect(ua, settings.SubClientRules)

	// Check if the detected client is allowed.
	clientBlocked := false
	if detected.Matched {
		// Find the matched rule to check if it's enabled.
		for _, rule := range settings.SubClientRules {
			for _, kw := range rule.Keywords {
				if strings.Contains(strings.ToLower(ua), strings.ToLower(kw)) {
					if !rule.Enabled {
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !u.Enabled {
		c.String(http.StatusForbidden, "disabled")
		return
	}
	if u.IsExpired(time.Now()) {
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
		u.AutoDisabledReason = domain.DisabledBlockedClient
		u.DisableDetail = fmt.Sprintf("blocked client: %s", detected.ClientName)

		// Check if auto-disable is enabled and threshold reached.
		if settings.SubBlockAutoDisable && u.BlockViolationCount >= settings.SubBlockAutoDisableCount {
			u.Enabled = false
			u.AutoDisabledReason = domain.DisabledBlockedClient
			u.DisableDetail = fmt.Sprintf("auto-disabled after %d violations, last client: %s", u.BlockViolationCount, detected.ClientName)

			// Save user with disabled status.
			if err := h.users.Update(c.Request.Context(), u); err != nil {
				log.Warn("failed to auto-disable user", "user_id", u.ID, "err", err)
			}

			// Send account disabled notification email (async).
			if h.mailer != nil {
				go func(userCopy *domain.User) {
					ctx := context.Background()
					if err := h.mailer.SendAccountDisabledNotification(ctx, userCopy, "使用被禁止的客户端", userCopy.DisableDetail); err != nil {
						log.Warn("failed to send disable notification", "user_id", userCopy.ID, "err", err)
					}
				}(u)
			}

			c.String(http.StatusForbidden, "account disabled due to repeated use of blocked client")
			return
		}

		// Save updated violation count.
		if err := h.users.Update(c.Request.Context(), u); err != nil {
			fmt.Printf("failed to update violation count for user %d: %v\n", u.ID, err)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
