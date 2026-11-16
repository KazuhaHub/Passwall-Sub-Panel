package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/legal"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminLegalStore is what the administration of the documents needs of the
// store (sqlstore.LegalRepo).
type AdminLegalStore interface {
	PublishWithConsent(ctx context.Context, d domain.LegalDocument, bump bool) (*domain.LegalDocument, int, bool, error)
	LatestPerLocale(ctx context.Context, kind domain.LegalKind) ([]*domain.LegalDocument, error)
	History(ctx context.Context, kind domain.LegalKind) ([]domain.LegalDocumentMeta, error)
	CountAffectedUsers(ctx context.Context, version int) (affected, accepted int64, err error)
}

// LegalWiring is the legal feature's wiring. A nil Store answers the admin
// routes with 503; a nil Service the public ones with 404.
type LegalWiring struct {
	Service *legal.Service
	Store   AdminLegalStore
}

// AdminLegalHandler serves /api/admin/legal. adminGroup only: publishing the
// text every user is shown, and moving the consent version, is the owner's
// call. Its audit rows are NOT in adminOnlyAuditTargets: the published text is
// public by design, and the audit middleware keeps only the first 8192
// characters of the content in the row, which is expected.
//
// It is the ONLY writer of legal_consent_version. The store inserts the
// document and moves the version in one transaction; because the settings store
// saves whole records, Publish holds the same lock as the system settings PUT
// (uiSettingsWriteMu) so a record loaded before the bump is never saved over it,
// and drops the settings cache afterwards.
type AdminLegalHandler struct {
	w        LegalWiring
	settings ports.SettingsRepo
	defaults func() ports.UISettings
}

// NewAdminLegalHandler builds the handler.
func NewAdminLegalHandler(w LegalWiring, settings ports.SettingsRepo) *AdminLegalHandler {
	return &AdminLegalHandler{w: w, settings: settings, defaults: (&AdminSettingsHandler{}).defaults}
}

func (h *AdminLegalHandler) unavailable(c *gin.Context) bool {
	if h.w.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Legal documents are not available"})
		return true
	}
	return false
}

type legalDocumentDTO struct {
	ID          int64     `json:"id"`
	Locale      string    `json:"locale"`
	Version     int       `json:"version"`
	Content     string    `json:"content"`
	ConsentBump bool      `json:"consent_bump"`
	PublishedAt time.Time `json:"published_at"`
	PublishedBy int64     `json:"published_by"`
}

type legalVersionDTO struct {
	ID          int64     `json:"id"`
	Locale      string    `json:"locale"`
	Version     int       `json:"version"`
	ConsentBump bool      `json:"consent_bump"`
	PublishedAt time.Time `json:"published_at"`
	PublishedBy int64     `json:"published_by"`
}

// Get answers the newest version of the kind in every locale (with its text,
// for the editor) and the history of every version (without).
func (h *AdminLegalHandler) Get(c *gin.Context) {
	kind, ok := domain.ParseLegalKind(c.Param("kind"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	if h.unavailable(c) {
		return
	}
	ctx := c.Request.Context()
	set, err := h.settings.Load(ctx, h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	latest, err := h.w.Store.LatestPerLocale(ctx, kind)
	if err != nil {
		respondError(c, err)
		return
	}
	hist, err := h.w.Store.History(ctx, kind)
	if err != nil {
		respondError(c, err)
		return
	}
	docs := make([]legalDocumentDTO, len(latest))
	for i, d := range latest {
		docs[i] = legalDocumentDTO{ID: d.ID, Locale: d.Locale, Version: d.Version, Content: d.Content, ConsentBump: d.ConsentBump, PublishedAt: d.PublishedAt, PublishedBy: d.PublishedBy}
	}
	vers := make([]legalVersionDTO, len(hist))
	for i, m := range hist {
		vers[i] = legalVersionDTO{ID: m.ID, Locale: m.Locale, Version: m.Version, ConsentBump: m.ConsentBump, PublishedAt: m.PublishedAt, PublishedBy: m.PublishedBy}
	}
	out := gin.H{
		"kind": kind, "enabled": set.LegalEnabled, "consent_version": set.LegalConsentVersion,
		"documents": docs, "versions": vers,
	}
	// The editor's preview of the generated list. A failure leaves it out: the
	// page still edits and publishes, the preview says it has nothing to show.
	if h.w.Service != nil {
		if dc, err := h.w.Service.Collection(ctx); err == nil {
			out["data_collection"] = dc
		}
	}
	c.JSON(http.StatusOK, out)
}

type publishLegalRequest struct {
	Locale      string `json:"locale"`
	Content     string `json:"content"`
	ConsentBump bool   `json:"consent_bump"`
}

// Publish stores the next version of a document and, when this is the first
// publication or it is marked as a material change, moves the consent version:
// to 1 on the first (whether or not it is marked), by one on a marked one,
// never on an unmarked later one. Both happen in one transaction
// (LegalRepo.PublishWithConsent): a published text never exists without the
// version that announces it.
func (h *AdminLegalHandler) Publish(c *gin.Context) {
	kind, ok := domain.ParseLegalKind(c.Param("kind"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	if h.unavailable(c) {
		return
	}
	var req publishLegalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	locale, ok := domain.NormalizeLegalLocale(req.Locale)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "locale must be a language tag such as zh-CN", "field": "locale", "code": "legal_locale_invalid"})
		return
	}
	if err := domain.ValidateLegalContent(req.Content); err != nil {
		h.contentError(c, err)
		return
	}
	var by int64
	if claims := middleware.ClaimsFrom(c); claims != nil {
		by = claims.UserID
	}
	ctx := c.Request.Context()

	uiSettingsWriteMu.Lock()
	defer uiSettingsWriteMu.Unlock()

	doc, version, bumped, err := h.w.Store.PublishWithConsent(ctx, domain.LegalDocument{Kind: kind, Locale: locale, Content: req.Content, ConsentBump: req.ConsentBump, PublishedBy: by}, req.ConsentBump)
	if err != nil {
		h.contentError(c, err)
		return
	}
	// The version was written behind the settings store's back.
	if inv, ok := h.settings.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
	if h.w.Service != nil {
		h.w.Service.InvalidateReachable()
	}
	c.JSON(http.StatusOK, gin.H{
		"document":        legalVersionDTO{ID: doc.ID, Locale: doc.Locale, Version: doc.Version, ConsentBump: doc.ConsentBump, PublishedAt: doc.PublishedAt, PublishedBy: doc.PublishedBy},
		"consent_version": version,
		"consent_bumped":  bumped,
	})
}

func (h *AdminLegalHandler) contentError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrLegalContentTooLarge) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "field": "content", "code": "legal_content_too_large", "max_bytes": domain.LegalContentMaxBytes})
		return
	}
	respondError(c, err)
}

// AffectedUsers answers how many accounts a material change would ask to
// consent again: every account of role user (staff are never asked), and how
// many of them already accepted the current version.
func (h *AdminLegalHandler) AffectedUsers(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	ctx := c.Request.Context()
	set, err := h.settings.Load(ctx, h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	affected, accepted, err := h.w.Store.CountAffectedUsers(ctx, set.LegalConsentVersion)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"consent_version": set.LegalConsentVersion, "affected_users": affected, "accepted_current": accepted})
}
