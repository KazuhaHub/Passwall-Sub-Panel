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
	settings ports.ScopedSettings
	users    ports.UserRepo
	mailer   *mailer.Service
	async    AsyncDispatcher
}

func NewSubHandler(userSvc *user.Service, renderSvc *render.Service, subLogs ports.SubLogRepo, settings ports.ScopedSettings, users ports.UserRepo, mailerSvc *mailer.Service, async AsyncDispatcher) *SubHandler {
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
	// Past this point the user (hence their group) is known, so the
	// subscription-POLICY settings resolve to this user's effective values
	// (global ⊕ group overrides): the emergency-quota gate below, the
	// blocked-client auto-disable thresholds, and the block-notify toggle. The
	// pre-identity client-detection reads above (SubClients / SubClientFilterMode)
	// stay global — they run before the token is resolved.
	eff, err := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
	if err != nil {
		log.Warn("sub: load per-user settings failed", "user_id", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	now := time.Now()
	// "Valid token but the subscription can't be served right now"
	// (disabled / expired / emergency-window-expired) is collapsed to
	// the same opaque 404 + empty body the unknown-token branch above
	// uses. Pre-v3.6.1-beta.3 each reason returned a distinct 403 + body
	// — an unauthenticated probe could enumerate valid tokens by status
	// alone (404 = no such token; 403 = real user, just suspended). The
	// real reason is preserved in the log line so admin can still
	// diagnose, and the legitimate user's proxy client surfaces
	// "subscription unavailable" generic enough that they'll contact
	// admin (who has the audit / users page for the real cause).
	switch {
	case u.EmergencyUntil != nil && now.After(*u.EmergencyUntil) && u.ServiceDisabledReason == domain.DisabledTrafficExceeded:
		log.Info("sub: blocked", "user_id", u.ID, "reason", "emergency_expired")
		c.String(http.StatusNotFound, "")
		return
	case u.EmergencyQuotaExhausted(int64(eff.EmergencyAccessQuotaGB*1024*1024*1024), now):
		// Emergency window still time-valid, but the per-window quota is spent.
		// The traffic poll only tears the window down on its next cycle (up to
		// minutes away); without this gate /sub keeps serving a working config
		// in that gap. The predicate mirrors user.emergencyFloor / the poll
		// teardown so the two enforcement layers can't drift.
		log.Info("sub: blocked", "user_id", u.ID, "reason", "emergency_quota_exhausted")
		c.String(http.StatusNotFound, "")
		return
	case !u.Enabled:
		log.Info("sub: blocked", "user_id", u.ID, "reason", "disabled",
			"auto_reason", string(u.AutoDisabledReason))
		c.String(http.StatusNotFound, "")
		return
	case !u.ProxyAccessEnabled(now):
		log.Info("sub: blocked", "user_id", u.ID, "reason", string(u.ServiceStatus(now)),
			"service_reason", string(u.ServiceDisabledReason))
		c.String(http.StatusNotFound, "")
		return
	}

	// If client is blocked, handle violation tracking and potential auto-disable.
	if clientBlocked {
		// Log the violation off the hot path. sub_logs is the
		// highest-write-rate table on the public endpoint — every
		// active client polls every few minutes; with N users a
		// synchronous INSERT here means N×(fsync wall-clock) added to
		// the request budget. async.Go defers the write to a tracked
		// background goroutine and returns the request right away;
		// drops on shutdown are acceptable (best-effort log).
		h.logSubAsync(u.ID, c.ClientIP(), ua, detected.ClientName)

		// Advance the violation count atomically, with the dedup window gated
		// INSIDE the UPDATE: only one request per window advances, so concurrent
		// /sub fetches can't lose an increment or double-fire auto-disable.
		// advanced=false means we're inside the window (or the row vanished) —
		// the UPDATE wrote nothing, so no hot-path churn and no threshold check.
		detail := fmt.Sprintf("last blocked client: %s", detected.ClientName)
		count, advanced, err := h.users.AdvanceBlockViolation(c.Request.Context(), u.ID,
			now.Add(-blockViolationDedupWindow), now, detail)
		if err != nil {
			log.Warn("failed to advance blocked-client violation count", "user_id", u.ID, "err", err)
		}

		// Auto-disable when this newly-counted violation crosses the threshold.
		if advanced && eff.SubBlockAutoDisable && count >= eff.SubBlockAutoDisableCount {
			disableDetail := fmt.Sprintf("auto-disabled after %d violations, last client: %s", count, detected.ClientName)
			// SetServiceSuspendedAndSync now emails the user (async, carrying the
			// blocked-client reason) from its single chokepoint — so no separate
			// send here. The old send double-fired against that path and used
			// account-disable wording ("账号已被停用") for a service-only suspension
			// the user can still log in through.
			if err := h.user.SetServiceSuspendedAndSync(c.Request.Context(), u.ID, domain.DisabledBlockedClient, disableDetail); err != nil {
				log.Warn("failed to auto-suspend user service", "user_id", u.ID, "err", err)
			}

			// Same 404-collapse as the other "account exists but service
			// unavailable" branches above — once auto-suspend fires, the
			// response shouldn't leak that distinction either.
			log.Info("sub: blocked", "user_id", u.ID, "reason", "auto_suspended_blocked_client", "violations", count)
			c.String(http.StatusNotFound, "")
			return
		}

		// Soft notice (async): tell the user they used a blocked client, before
		// they hit the auto-disable threshold. Gate on the already-loaded
		// setting here so we don't spawn a goroutine (and its DB reads) when
		// the feature is off — the per-day cap inside the mailer handles the
		// rest. Pass the loaded UI settings through to avoid re-reading them.
		if eff.SubBlockNotifyUser && h.mailer != nil && h.async != nil {
			userCopy := u
			clientName := detected.ClientName
			uiCfg := eff
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

	// Render subscription. Cached for a short TTL (subRenderCacheTTL): the
	// polling fleet re-fetches an unchanged config on a timer, so repeat polls
	// within the window skip the full render + group/node/separator/traffic
	// reads. Staleness is bounded by the TTL (accepted for sub delivery); the
	// access log, blocked-client check and userinfo headers below still run on
	// every fetch.
	out, err := h.render.RenderForUserCached(c.Request.Context(), u, ct)
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

	// Log access off the hot path (best-effort). Record original UA
	// without modification. 304 still counts as a fetch — admins
	// reading sub_logs would otherwise see an active polling client
	// appear dormant.
	h.logSubAsync(u.ID, c.ClientIP(), ua, detected.ClientName)

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

// logSubAsync defers the sub_logs INSERT off the request thread via
// the async dispatcher. Pre-v3.6.1-beta.3 this was a synchronous Insert
// inside the request — every active proxy client polls every few
// minutes so the table's write rate is the highest on the public
// endpoint, and an fsync-bound INSERT on the hot path dominated the
// per-request budget. Best-effort: when async or the repo is nil (test
// harness) the call no-ops; failed inserts log Warn server-side and
// are swallowed for the caller. Values are captured at call time
// (NOT inside the goroutine) so a request-context cancel can't race a
// gin.Context method call after the request returned.
func (h *SubHandler) logSubAsync(userID int64, ip, ua, clientType string) {
	if h.subLogs == nil {
		return
	}
	entry := &domain.SubLog{
		UserID:     userID,
		IP:         ip,
		UA:         ua,
		ClientType: clientType,
		AccessedAt: time.Now(),
	}
	if h.async == nil {
		// Test harness or other no-async wiring: fall back to a
		// best-effort synchronous write so the row still lands.
		_ = h.subLogs.Insert(context.Background(), entry)
		return
	}
	h.async.Go("sub.log-insert", func(ctx context.Context) {
		if err := h.subLogs.Insert(ctx, entry); err != nil {
			log.Warn("sub: log insert failed", "user_id", userID, "err", err)
		}
	})
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
