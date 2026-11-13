package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destcatalog"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// The access-control API (/api/admin/dest/...). EVERY route is adminGroup:
// access control decides what proxy users may reach and what a pasted list
// carries, and its audit rows are admin-only (adminOnlyAuditTargets).
//
// Times are UTC milliseconds throughout. A 400 is {error, code, field?, bad?};
// the codes are the API's contract with the page (see destError).

// DestExemptionStore is dest_exemptions (sqlstore.DestExemptionRepo).
type DestExemptionStore interface {
	Upsert(ctx context.Context, e *domain.DestExemption) error
	Get(ctx context.Context, userID int64) (*domain.DestExemption, error)
	List(ctx context.Context) ([]*domain.DestExemption, error)
	Delete(ctx context.Context, userID int64) error
}

// DestSourceRuleStore is dest_source_rules (sqlstore.DestSourceRuleRepo).
type DestSourceRuleStore interface {
	Upsert(ctx context.Context, rule *domain.DestSourceRule, ifUpdatedAt *time.Time) error
	Get(ctx context.Context, nodeID int64) (*domain.DestSourceRule, error)
	List(ctx context.Context) ([]*domain.DestSourceRule, error)
}

// DestWiring is everything the access-control routes need. A field left nil
// makes the routes that need it answer 503: "this build cannot" stays apart
// from "nothing there" (TestBuildWiresTheAccessControlAPI guards Build).
type DestWiring struct {
	Lists       *destlist.Service
	Catalog     *destcatalog.Service
	Policy      *destpolicy.Service
	Admin       *destpolicy.Admin
	Policies    destpolicy.PolicyStore
	Exemptions  DestExemptionStore
	SourceRules DestSourceRuleStore
	Users       ports.UserRepo
	Groups      ports.GroupRepo
	Nodes       ports.NodeRepo
	Panels      ports.XUIPanelRepo
	// Async runs a list refresh in the background. Nil runs it inline.
	Async interface {
		Go(name string, fn func(ctx context.Context))
	}
}

// AdminDestHandler serves the access-control API.
type AdminDestHandler struct{ w DestWiring }

// NewAdminDestHandler builds the handler.
func NewAdminDestHandler(w DestWiring) *AdminDestHandler { return &AdminDestHandler{w: w} }

func (h *AdminDestHandler) ready(c *gin.Context, ok bool) bool {
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "access control is not available in this build", "code": "dest_unavailable"})
	}
	return ok
}

func msOf(t time.Time) int64 { return t.UTC().UnixMilli() }

func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UTC().UnixMilli()
	return &v
}

func timeFromMS(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad " + name, "code": "dest_bad_id", "field": name})
		return 0, false
	}
	return id, true
}

// errCodes names the codes of the conflicts a route can answer: the stale
// precondition (the row changed since the page read it) and the duplicate.
type errCodes struct{ stale, exists string }

// destError answers an error with the API's status and code.
//
//	400 dest_list_parse_failed {lines}        a line that cannot be used
//	400 dest_list_too_large {kind,used,limit}
//	400 dest_list_too_broad {entry}
//	400 dest_list_insecure_url
//	400 dest_policy_invalid / _no_match / _over_limit {kind,used,limit} / dest_policy_state_unavailable
//	409 dest_list_in_use {used_by}            deleting a list a policy uses
//	409 <stale>                               the precondition failed
//	409 <exists>                              a duplicate (name, exemption)
//	409 dest_geosite_busy / dest_geosite_update_rejected {rejection}
//	409 dest_source_rule_not_native
//	409 dest_policy_over_limit {publish_error} a publish the validation refused
//	503 dest_geoip_unavailable / dest_geosite_unavailable
func (h *AdminDestHandler) destError(c *gin.Context, err error, codes errCodes) {
	var (
		verr   *destpolicy.ValidationError
		parse  *destlist.ParseError
		large  *destlist.TooLargeError
		broad  *destlist.BroadEntryError
		inUse  *destlist.ListInUseError
		reject *destlist.UpdateRejectedError
		pub    *destpolicy.PublishRefusedError
	)
	body := func(status int, code, msg string, extra gin.H) {
		out := gin.H{"error": msg, "code": code}
		for k, v := range extra {
			out[k] = v
		}
		c.JSON(status, out)
	}
	switch {
	case errors.As(err, &verr):
		extra := gin.H{}
		if verr.Field != "" {
			extra["field"] = verr.Field
		}
		if len(verr.Bad) > 0 {
			extra["bad"] = verr.Bad
		}
		if verr.Code == destpolicy.CodePolicyOverLimit {
			extra["kind"], extra["used"], extra["limit"] = verr.Kind, verr.Used, verr.Limit
		}
		msg := verr.Msg
		if msg == "" {
			msg = verr.Code
		}
		body(http.StatusBadRequest, verr.Code, msg, extra)
	case errors.As(err, &parse):
		body(http.StatusBadRequest, "dest_list_parse_failed", parse.Error(), gin.H{"lines": parse.Lines})
	case errors.As(err, &large):
		body(http.StatusBadRequest, "dest_list_too_large", large.Error(), gin.H{"kind": large.Kind, "used": large.Used, "limit": large.Limit})
	case errors.As(err, &broad):
		body(http.StatusBadRequest, "dest_list_too_broad", broad.Error(), gin.H{"entry": broad.Entry, "reason": broad.Reason})
	case errors.Is(err, destlist.ErrInsecureURL):
		body(http.StatusBadRequest, "dest_list_insecure_url", "a list URL must be https", nil)
	case errors.As(err, &inUse):
		body(http.StatusConflict, "dest_list_in_use", "a policy uses this list", gin.H{"used_by": h.policyRefs(c.Request.Context(), inUse.PolicyIDs)})
	case errors.As(err, &reject):
		body(http.StatusConflict, "dest_geosite_update_rejected", "the update would break a policy in force", gin.H{"rejection": reject.UpdateRejection})
	case errors.Is(err, destlist.ErrGeositeBusy):
		body(http.StatusConflict, "dest_geosite_busy", "a geosite update is in progress", nil)
	case errors.Is(err, destpolicy.ErrSourceRuleNotNative):
		body(http.StatusConflict, destpolicy.CodeSourceRuleNotNative, "a source rule needs a native node", nil)
	case errors.As(err, &pub) && pub.Detail.Kind == "invalid":
		body(http.StatusBadRequest, destpolicy.CodePolicyInvalid, pub.Error(), gin.H{"field": pub.Detail.Field, "publish_error": pub.Detail})
	case errors.As(err, &pub):
		body(http.StatusConflict, destpolicy.CodePolicyOverLimit, pub.Error(), gin.H{"publish_error": pub.Detail})
	case errors.Is(err, destlist.ErrGeoIPUnavailable):
		body(http.StatusServiceUnavailable, "dest_geoip_unavailable", "there is no GeoIP database", nil)
	case errors.Is(err, destlist.ErrGeositeUnavailable):
		body(http.StatusServiceUnavailable, "dest_geosite_unavailable", "there is no geosite data", nil)
	case errors.Is(err, destcatalog.ErrTemplateUnavailable):
		body(http.StatusBadRequest, "dest_template_unavailable", "this group has no generic category", nil)
	case errors.Is(err, domain.ErrAlreadyExists) && codes.exists != "":
		body(http.StatusConflict, codes.exists, "already exists", nil)
	case errors.Is(err, domain.ErrConflict) && codes.stale != "":
		body(http.StatusConflict, codes.stale, "it changed since it was read", nil)
	default:
		respondError(c, err)
	}
}

// policyRefs names policies by id, for a 409 that says who holds a list.
func (h *AdminDestHandler) policyRefs(ctx context.Context, ids []int64) []destlist.PolicyRef {
	out := make([]destlist.PolicyRef, 0, len(ids))
	for _, id := range ids {
		ref := destlist.PolicyRef{Kind: "policy", ID: id}
		if h.w.Policies != nil {
			if p, err := h.w.Policies.GetByID(ctx, id); err == nil {
				ref.Name = p.Name
			}
		}
		out = append(out, ref)
	}
	return out
}

// bindDestJSON decodes the body. It is lenient about fields it does not know:
// the page sends back whole objects it read (ids, timestamps) on a PUT, and
// refusing those would make every edit fail on a field that means nothing here.
func bindDestJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "dest_bad_request"})
		return false
	}
	return true
}

func splitLines(b []byte, limit int) []string {
	if len(b) == 0 {
		return []string{}
	}
	// SplitN, so a list of megabytes is not split into every one of its lines
	// to show the first 50 or 200.
	if limit <= 0 {
		return strings.Split(string(b), "\n")
	}
	lines := strings.SplitN(string(b), "\n", limit+1)
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}
