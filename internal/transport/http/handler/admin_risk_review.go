package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskreview"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// RiskReviewService is the risk center's review actions as the handler uses
// them (riskreview.Service).
type RiskReviewService interface {
	Dismiss(ctx context.Context, userID int64, by riskreview.Actor, note string, expected domain.AttentionLevels) (domain.RiskReview, error)
	Undismiss(ctx context.Context, userID int64, by riskreview.Actor) (domain.RiskReview, error)
	Trust(ctx context.Context, userID int64, by riskreview.Actor, resume bool) (riskreview.TrustResult, error)
	Untrust(ctx context.Context, userID int64, by riskreview.Actor) (domain.RiskReview, error)
}

// AdminRiskReviewHandler serves the risk center's review actions: dismiss
// an account's current signals (and withdraw that), trust an account (and
// withdraw that). adminGroup only, like every risk-center route. Each is a
// POST or DELETE under /api/admin, so AuditWrites keeps one audit row per
// request — the only place the admin's note is kept besides the review row
// itself; the flag record the service writes is name- and note-free.
type AdminRiskReviewHandler struct{ svc RiskReviewService }

// NewAdminRiskReviewHandler wraps svc. Nil is a deployment that did not wire
// the review actions: every route answers 503.
func NewAdminRiskReviewHandler(svc RiskReviewService) *AdminRiskReviewHandler {
	return &AdminRiskReviewHandler{svc: svc}
}

// begin answers the refusals every action shares — unwired (503), a bad id
// (400), no admin behind the request (401: every review names its admin) —
// and otherwise returns the account id and the acting admin.
func (h *AdminRiskReviewHandler) begin(c *gin.Context) (int64, riskreview.Actor, bool) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "the risk review is not wired in this deployment"})
		return 0, riskreview.Actor{}, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return 0, riskreview.Actor{}, false
	}
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return 0, riskreview.Actor{}, false
	}
	return id, riskreview.Actor{ID: claims.UserID, UPN: claims.UPN}, true
}

// bindOptional decodes an optional JSON body: no body at all is {}, since
// the SPA sends none when it has nothing to say; anything that does not
// decode is a 400 and nothing is done, so no request is ever half-applied.
func bindOptional(c *gin.Context, into any) bool {
	if err := c.ShouldBindJSON(into); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	return true
}

// reviewConflictCodes are the refusals the SPA tells apart: each is a
// stale view (a second admin, an old tab) or an escalation the admin never
// saw, and the SPA refreshes and says which.
var reviewConflictCodes = []struct {
	err  error
	code string
	msg  string
}{
	{riskreview.ErrNothingToDismiss, "nothing_to_dismiss", "Nothing to dismiss"},
	{riskreview.ErrAlreadyDismissed, "already_dismissed", "Already dismissed"},
	{riskreview.ErrNotDismissed, "not_dismissed", "Not dismissed"},
	{riskreview.ErrAlreadyTrusted, "already_trusted", "Already trusted"},
	{riskreview.ErrNotTrusted, "not_trusted", "Not trusted"},
	{riskreview.ErrChanged, "changed", "The account's signals changed since they were shown"},
}

// respondReviewError maps the service's refusals to their codes: a note
// over the limit is a 400 naming the limit, each conflict a 409 naming what
// changed. Anything else — a missing account (404), a malformed
// expectation (400), a store failure — goes through the shared mapping.
func respondReviewError(c *gin.Context, err error) {
	if errors.Is(err, riskreview.ErrNoteTooLong) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Note too long", "code": "note_too_long"})
		return
	}
	for _, rc := range reviewConflictCodes {
		if errors.Is(err, rc.err) {
			c.JSON(http.StatusConflict, gin.H{"error": rc.msg, "code": rc.code})
			return
		}
	}
	respondError(c, err)
}

// storedReviewDTO is the review row as the action stored it, in the
// drawer's review shape. The admins are named by UPN only where the acting
// admin is the one named — anyone else by id alone, which the SPA shows as
// #id — so the answer costs no read. What the reopen rule decides now
// (reopened, lapsed, escalated) is not evaluated here: it is the drawer's,
// which the SPA re-reads after every action, and reads as not reopened.
func storedReviewDTO(rev domain.RiskReview, by riskreview.Actor) riskReviewDTO {
	nameOf := func(id int64) string {
		if id != 0 && id == by.ID {
			return by.UPN
		}
		return ""
	}
	return reviewDTOOf(domain.AccountAttention{Review: rev, HasReview: rev.Dismissed() || rev.Trusted},
		nameOf(rev.DismissedBy), nameOf(rev.TrustedBy))
}

type dismissRequest struct {
	Note string `json:"note"`
	// Expected is the levels the admin was shown; absent (or null) sends
	// none, and the service then checks nothing (older SPAs).
	Expected domain.AttentionLevels `json:"expected"`
}

// Dismiss serves POST /risk-center/users/:id/dismiss with an optional body
// {note, expected} → 200 {"review": …}.
func (h *AdminRiskReviewHandler) Dismiss(c *gin.Context) {
	id, by, ok := h.begin(c)
	if !ok {
		return
	}
	var req dismissRequest
	if !bindOptional(c, &req) {
		return
	}
	rev, err := h.svc.Dismiss(c.Request.Context(), id, by, strings.TrimSpace(req.Note), req.Expected)
	if err != nil {
		respondReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": storedReviewDTO(rev, by)})
}

// Undismiss serves DELETE /risk-center/users/:id/dismiss → 200 {"review": …}.
func (h *AdminRiskReviewHandler) Undismiss(c *gin.Context) {
	id, by, ok := h.begin(c)
	if !ok {
		return
	}
	rev, err := h.svc.Undismiss(c.Request.Context(), id, by)
	if err != nil {
		respondReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": storedReviewDTO(rev, by)})
}

type trustRequest struct {
	// ResumeService also lifts the location detector's own suspension.
	ResumeService bool `json:"resume_service"`
}

type trustResponse struct {
	Review  riskReviewDTO `json:"review"`
	Resumed bool          `json:"resumed"`
	// ResumeWarning: the hold was lifted, but the push to the panels failed
	// and could not be queued.
	ResumeWarning string `json:"resume_warning,omitempty"`
	// ResumeError: the hold was not lifted. The trust stands either way.
	ResumeError string `json:"resume_error,omitempty"`
}

// Trust serves POST /risk-center/users/:id/trust with an optional body
// {resume_service} → 200 {"review": …, "resumed": …} plus resume_warning or
// resume_error when the lift went wrong. A failed lift is not a failed
// request: the trust was saved, and the SPA says what is left to do.
func (h *AdminRiskReviewHandler) Trust(c *gin.Context) {
	id, by, ok := h.begin(c)
	if !ok {
		return
	}
	var req trustRequest
	if !bindOptional(c, &req) {
		return
	}
	res, err := h.svc.Trust(c.Request.Context(), id, by, req.ResumeService)
	if err != nil {
		respondReviewError(c, err)
		return
	}
	out := trustResponse{Review: storedReviewDTO(res.Review, by), Resumed: res.Resumed}
	if res.ResumeErr != nil {
		if res.Resumed {
			out.ResumeWarning = res.ResumeErr.Error()
		} else {
			out.ResumeError = res.ResumeErr.Error()
		}
	}
	c.JSON(http.StatusOK, out)
}

// Untrust serves DELETE /risk-center/users/:id/trust → 200 {"review": …}.
func (h *AdminRiskReviewHandler) Untrust(c *gin.Context) {
	id, by, ok := h.begin(c)
	if !ok {
		return
	}
	rev, err := h.svc.Untrust(c.Request.Context(), id, by)
	if err != nil {
		respondReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"review": storedReviewDTO(rev, by)})
}
