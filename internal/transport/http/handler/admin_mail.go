package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
)

type AdminMailHandler struct {
	mail *mailer.Service
}

func NewAdminMailHandler(mailSvc *mailer.Service) *AdminMailHandler {
	return &AdminMailHandler{mail: mailSvc}
}

// mailSettingsDTO mirrors domain.MailSettings (SMTP connection only).
// Notify thresholds (expire_before_days / traffic_remain_percent) moved to
// the unified settings KV (type='notify') in v3 — admins edit them through
// the global settings page now, not the mail page.
type mailSettingsDTO struct {
	Enabled         bool   `json:"enabled"`
	SMTPHost        string `json:"smtp_host"`
	SMTPPort        int    `json:"smtp_port"`
	SMTPUsername    string `json:"smtp_username"`
	SMTPPassword    string `json:"smtp_password,omitempty"`
	HasSMTPPassword bool   `json:"has_smtp_password"`
	FromEmail       string `json:"from_email"`
	FromName        string `json:"from_name"`
	Encryption      string `json:"encryption"`
}

type mailTemplateDTO struct {
	Kind    domain.MailReminderKind `json:"kind"`
	Subject string                  `json:"subject"`
	Body    string                  `json:"body"`
	Enabled bool                    `json:"enabled"`
}

func (h *AdminMailHandler) Get(c *gin.Context) {
	settings, err := h.mail.LoadSettings(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	templates, err := h.mail.ListTemplates(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	outTemplates := make([]mailTemplateDTO, len(templates))
	for i, tpl := range templates {
		outTemplates[i] = mailTemplateDTO{
			Kind:    tpl.Kind,
			Subject: tpl.Subject,
			Body:    tpl.Body,
			Enabled: tpl.Enabled,
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"settings":  toMailSettingsDTO(settings),
		"templates": outTemplates,
	})
}

func (h *AdminMailHandler) PutSettings(c *gin.Context) {
	current, err := h.mail.LoadSettings(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	var req mailSettingsDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	password := req.SMTPPassword
	if password == "" {
		password = current.SMTPPassword
	}
	settings := domain.MailSettings{
		Enabled:      req.Enabled,
		SMTPHost:     req.SMTPHost,
		SMTPPort:     req.SMTPPort,
		SMTPUsername: req.SMTPUsername,
		SMTPPassword: password,
		FromEmail:    req.FromEmail,
		FromName:     req.FromName,
		Encryption:   req.Encryption,
	}
	if err := h.mail.SaveSettings(c.Request.Context(), settings); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	saved, _ := h.mail.LoadSettings(c.Request.Context())
	c.JSON(http.StatusOK, toMailSettingsDTO(saved))
}

func (h *AdminMailHandler) PutTemplate(c *gin.Context) {
	kind := domain.MailReminderKind(c.Param("kind"))
	var req mailTemplateDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tpl := &domain.MailTemplate{
		Kind:    kind,
		Subject: req.Subject,
		Body:    req.Body,
		Enabled: req.Enabled,
	}
	if err := h.mail.SaveTemplate(c.Request.Context(), tpl); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, mailTemplateDTO{
		Kind:    tpl.Kind,
		Subject: tpl.Subject,
		Body:    tpl.Body,
		Enabled: tpl.Enabled,
	})
}

func (h *AdminMailHandler) PreviewTemplate(c *gin.Context) {
	kind := domain.MailReminderKind(c.Param("kind"))
	var req mailTemplateDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := h.mail.PreviewTemplate(c.Request.Context(), &domain.MailTemplate{
		Kind:    kind,
		Subject: req.Subject,
		Body:    req.Body,
		Enabled: req.Enabled,
	})
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

// ResetTemplate replaces the stored template body+subject for the given kind
// with the in-code default. Used by the "重置为默认" button so admins who
// saved a template under an older panel version can pull in newer copy /
// structure (e.g., when we add a new metric row) without manual editing.
//
// Returns the freshly-restored template DTO so the editor can replace its
// form state in one round-trip.
func (h *AdminMailHandler) ResetTemplate(c *gin.Context) {
	kind := domain.MailReminderKind(c.Param("kind"))
	tpl, err := h.mail.ResetTemplate(c.Request.Context(), kind)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, mailTemplateDTO{
		Kind:    tpl.Kind,
		Subject: tpl.Subject,
		Body:    tpl.Body,
		Enabled: tpl.Enabled,
	})
}

type mailTestRequest struct {
	To string `json:"to" binding:"required"`
}

func (h *AdminMailHandler) Test(c *gin.Context) {
	var req mailTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.mail.SendTest(c.Request.Context(), req.To); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

type mailAnnouncementRequest struct {
	Subject     string `json:"subject" binding:"required"`
	Body        string `json:"body" binding:"required"`
	OnlyEnabled bool   `json:"only_enabled"`
}

func (h *AdminMailHandler) Announcement(c *gin.Context) {
	var req mailAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.mail.SendAnnouncement(c.Request.Context(), mailer.AnnouncementInput{
		Subject:     req.Subject,
		Body:        req.Body,
		OnlyEnabled: req.OnlyEnabled,
	})
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func toMailSettingsDTO(s domain.MailSettings) mailSettingsDTO {
	return mailSettingsDTO{
		Enabled:         s.Enabled,
		SMTPHost:        s.SMTPHost,
		SMTPPort:        s.SMTPPort,
		SMTPUsername:    s.SMTPUsername,
		HasSMTPPassword: s.SMTPPassword != "",
		FromEmail:       s.FromEmail,
		FromName:        s.FromName,
		Encryption:      s.Encryption,
	}
}
