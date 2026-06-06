package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/passkey"
)

// AuthPasskeyHandler exposes the usernameless (discoverable) passkey login:
// /auth/passkey/begin issues request options, /auth/passkey/finish verifies the
// assertion and mints a session (honoring the same disable / local-login-lock /
// 2FA gates as password login).
type AuthPasskeyHandler struct {
	passkey    *passkey.Service
	auth       *auth.Service
	settings   ports.SettingsRepo
	authEvents ports.AuthEventRepo
}

func NewAuthPasskeyHandler(passkeySvc *passkey.Service, authSvc *auth.Service, settings ports.SettingsRepo, authEvents ports.AuthEventRepo) *AuthPasskeyHandler {
	return &AuthPasskeyHandler{passkey: passkeySvc, auth: authSvc, settings: settings, authEvents: authEvents}
}

// LoginBegin returns discoverable-credential request options + a session id. It
// reveals nothing about which accounts exist (no allow-list, no user lookup).
func (h *AuthPasskeyHandler) LoginBegin(c *gin.Context) {
	opts, sessionID, err := h.passkey.BeginLogin(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "publicKey": opts})
}

// LoginFinish verifies the assertion (request body) against the stashed session
// (?session=...) and, on success, mints a session — re-checking the live account
// the same way password login does.
func (h *AuthPasskeyHandler) LoginFinish(c *gin.Context) {
	sessionID := c.Query("session")
	u, err := h.passkey.FinishLogin(c.Request.Context(), sessionID, c.Request)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, 0, "", "passkey_invalid")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Passkey authentication failed"})
		return
	}
	// Disable gate — a passkey for a disabled account must not mint a session
	// (self-service disable reasons stay loginable, matching password login).
	if !u.Enabled && !domain.SelfServiceDisableReason(u.AutoDisabledReason) {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, u.ID, u.UPN, "disabled:"+string(u.AutoDisabledReason))
		c.JSON(http.StatusForbidden, gin.H{"error": disabledReasonMessage(u.AutoDisabledReason), "reason": string(u.AutoDisabledReason)})
		return
	}
	// Honor the non-admin local-login lock: a passkey is a local-account
	// credential, so allowing it when the admin forced SSO for users would be a
	// bypass. Admins keep it as a break-glass path. Load settings once (also reused
	// for the 2FA method list below).
	s, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if u.Role != domain.RoleAdmin && s.DisallowUserLocalLogin {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, u.ID, u.UPN, "local_login_disallowed")
		c.JSON(http.StatusForbidden, gin.H{"error": "Local login is restricted to administrators; please use SSO"})
		return
	}
	// 2FA gate — a passkey proves possession; if the user also enrolled TOTP,
	// still require it (defense in depth), mirroring password login. The first
	// factor is recorded as "passkey" so the challenge won't offer passkey again.
	if u.TOTPEnabled {
		pending, perr := h.auth.IssuePending(u, jwtutil.FirstFactorPasskey)
		if perr != nil {
			recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
			respondError(c, perr)
			return
		}
		methods := availableTwoFAMethods(c.Request.Context(), u, jwtutil.FirstFactorPasskey, s, h.passkey)
		c.JSON(http.StatusOK, gin.H{"status": "2fa_required", "pending_token": pending, "methods": methods})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
		respondError(c, err)
		return
	}
	recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeSuccess, u.ID, u.UPN, "passkey")
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}
