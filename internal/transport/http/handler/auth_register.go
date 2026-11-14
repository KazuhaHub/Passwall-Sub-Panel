package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/legal"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/registration"
)

// AuthRegisterHandler exposes the public self-registration endpoints. Both sit
// behind the login rate limiter (wired in the router).
type AuthRegisterHandler struct {
	reg      registrar
	settings ports.SettingsRepo
	captcha  *captcha.Service
	// legal is optional: unset, registration asks for no consent.
	legal *legal.Service
}

// registrar is what the handler needs of registration.Service.
type registrar interface {
	Register(ctx context.Context, in registration.RegisterInput) (*registration.RegisterResult, error)
	ResendVerification(ctx context.Context, email string)
	Verify(ctx context.Context, in registration.VerifyInput) error
}

// SetLegal makes registration check and record consent to the published terms.
func (h *AuthRegisterHandler) SetLegal(svc *legal.Service) { h.legal = svc }

func NewAuthRegisterHandler(reg *registration.Service, settings ports.SettingsRepo, captchaSvc *captcha.Service) *AuthRegisterHandler {
	return &AuthRegisterHandler{reg: reg, settings: settings, captcha: captchaSvc}
}

type registerRequest struct {
	Email       string `json:"email" binding:"required"`
	Password    string `json:"password" binding:"required"`
	DisplayName string `json:"display_name"`
	// Optional captcha proof (image: id+answer; token providers: token), checked
	// when the admin enabled captcha for the registration context.
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
	CaptchaToken  string `json:"captcha_token"`
	// AcceptedConsentVersion is the consent_version the form showed (from
	// /api/auth/methods). Required while the privacy and terms feature is on and
	// something is published; a different value is a 409 legal_consent_outdated
	// and the page reloads and asks again.
	AcceptedConsentVersion *int `json:"accepted_consent_version"`
}

// Register creates a new local account. On success returns whether the account
// still needs email verification. Maps validation / disabled / taken-email to
// the right status (taken-email IS revealed — expected signup UX).
func (h *AuthRegisterHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Before the captcha: a challenge answered for a form that is refused for
	// its consent version would be spent, and the retry would need a new one.
	consentRequired := false
	if h.legal != nil {
		var cerr error
		consentRequired, cerr = h.legal.CheckRegistration(c.Request.Context(), req.AcceptedConsentVersion)
		if errors.Is(cerr, domain.ErrLegalConsentOutdated) {
			cur, _ := h.legal.Current(c.Request.Context())
			c.JSON(http.StatusConflict, gin.H{"error": "The terms changed; reload and accept again", "code": "legal_consent_outdated", "consent_version": cur.ConsentVersion})
			return
		}
		if cerr != nil {
			respondPublicError(c, cerr)
			return
		}
	}
	s, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if !requireCaptcha(c, h.captcha, s, s.CaptchaRegisterEnabled, req.CaptchaID, req.CaptchaAnswer, req.CaptchaToken) {
		return
	}
	res, err := h.reg.Register(c.Request.Context(), registration.RegisterInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		respondPublicError(c, err)
		return
	}
	if consentRequired && res.UserID != 0 {
		// The account exists whether or not this write lands: a failure only
		// leaves it pending, so its first sign-in asks (legal_pending).
		if err := h.legal.RecordRegistration(c.Request.Context(), res.UserID, *req.AcceptedConsentVersion); err != nil {
			log.Warn("record registration consent", "user_id", res.UserID, "err", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "requires_verification": res.RequiresVerification})
}

type resendVerificationRequest struct {
	Email string `json:"email" binding:"required"`
	// Same optional captcha proof as Register — resend sends an email, so it's
	// gated by the registration captcha toggle too.
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
	CaptchaToken  string `json:"captcha_token"`
}

// ResendVerification re-sends the verification code/link for a pending account.
// ALWAYS responds 200 regardless of whether the email exists or its state — the
// response must not let a caller enumerate accounts. The send is cooldown-gated
// in the service (anti email-bomb).
func (h *AuthRegisterHandler) ResendVerification(c *gin.Context) {
	var req resendVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if !requireCaptcha(c, h.captcha, s, s.CaptchaRegisterEnabled, req.CaptchaID, req.CaptchaAnswer, req.CaptchaToken) {
		return
	}
	h.reg.ResendVerification(c.Request.Context(), req.Email)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type verifyEmailRequest struct {
	Token string `json:"token"`
	Ident string `json:"ident"`
	Code  string `json:"code"`
}

// VerifyEmail confirms the email and activates the account. 401 on a bad/expired
// token (deliberately generic).
func (h *AuthRegisterHandler) VerifyEmail(c *gin.Context) {
	var req verifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.reg.Verify(c.Request.Context(), registration.VerifyInput{
		Token: req.Token,
		Ident: req.Ident,
		Code:  req.Code,
	}); err != nil {
		respondPublicError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
