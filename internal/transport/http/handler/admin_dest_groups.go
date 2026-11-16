package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destgroup"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// ---- the allowlist groups ----
//
// A group's allowlist is a state (off, report, on), the lists it is made of and
// two lists the group owns. The routes read every group with the nodes that can
// enforce it, set the state and lists in one write, preview that write, and
// report what a report-only group would have denied. All of them are the
// administrator's: the lists and the destinations are in them.

const (
	codeGroupNotFound = "dest_group_not_found"
	codeGroupStale    = "dest_group_stale"
	codeListNotReady  = "dest_list_not_ready"
)

var destGroupCodes = errCodes{stale: codeGroupStale}

func (h *AdminDestHandler) groupsReady(c *gin.Context) bool {
	return h.ready(c, h.w.AllowlistGroups != nil)
}

// groupError answers an error of the group routes: a group that is not there is
// 404 dest_group_not_found, the rest is destError's.
func (h *AdminDestHandler) groupError(c *gin.Context, err error) {
	if errors.Is(err, destgroup.ErrGroupNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such group", "code": codeGroupNotFound})
		return
	}
	h.destError(c, err, destGroupCodes)
}

// ListGroups answers GET /api/admin/dest/groups: every group, the ones with no
// allowlist as off.
func (h *AdminDestHandler) ListGroups(c *gin.Context) {
	if !h.groupsReady(c) {
		return
	}
	items, err := h.w.AllowlistGroups.List(c.Request.Context())
	if err != nil {
		h.groupError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GetGroup answers GET /api/admin/dest/groups/:group_id: the group as the list
// shows it, with the readiness the turn-on dialog lists.
func (h *AdminDestHandler) GetGroup(c *gin.Context) {
	if !h.groupsReady(c) {
		return
	}
	gid, ok := pathID(c, "group_id")
	if !ok {
		return
	}
	d, err := h.w.AllowlistGroups.Get(c.Request.Context(), gid)
	if err != nil {
		h.groupError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

type destGroupRequest struct {
	State   domain.DestState `json:"state"`
	ListIDs []int64          `json:"list_ids"`
	// BaseHosts is a pointer: absent is not the same as empty (a group's first
	// allowlist needs it, an existing one refuses it).
	BaseHosts *[]string `json:"base_hosts"`
	UpdatedAt *int64    `json:"updated_at"`
}

// PutGroup answers PUT /api/admin/dest/groups/:group_id: the group's state and
// lists, from any state to any other. A group's first allowlist carries
// base_hosts (it may be empty) and is created with its two own lists in one
// transaction; an existing one carries the updated_at it was read at (409
// dest_group_stale when it moved). 409 dest_list_not_ready says which lists are
// not ready, and is only for "on". The answer is the group as the list shows it.
func (h *AdminDestHandler) PutGroup(c *gin.Context) {
	if !h.groupsReady(c) {
		return
	}
	gid, ok := pathID(c, "group_id")
	if !ok {
		return
	}
	var req destGroupRequest
	if !bindDestJSON(c, &req) {
		return
	}
	v, err := h.w.AllowlistGroups.Put(c.Request.Context(), gid, destgroup.PutInput{
		State: req.State, ListIDs: req.ListIDs, BaseHosts: req.BaseHosts, UpdatedAt: timeFromMS(req.UpdatedAt),
	})
	if err != nil {
		h.groupError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// PreviewGroup answers GET /api/admin/dest/groups/:group_id/preview?state=&list_ids=:
// what a save would do, written nowhere. list_ids is comma separated; the state
// defaults to report.
func (h *AdminDestHandler) PreviewGroup(c *gin.Context) {
	if !h.groupsReady(c) {
		return
	}
	gid, ok := pathID(c, "group_id")
	if !ok {
		return
	}
	in := destgroup.PreviewInput{State: domain.DestState(c.DefaultQuery("state", string(domain.DestStateReport)))}
	for _, part := range strings.Split(c.Query("list_ids"), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "list_ids are list ids", "code": destpolicy.CodePolicyInvalid, "field": "list_ids", "bad": []string{part}})
			return
		}
		in.ListIDs = append(in.ListIDs, id)
	}
	p, err := h.w.AllowlistGroups.Preview(c.Request.Context(), gid, in)
	if err != nil {
		h.groupError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// GroupReport answers GET /api/admin/dest/groups/:group_id/report?days=: the
// destinations the group's members would have been denied, by site.
func (h *AdminDestHandler) GroupReport(c *gin.Context) {
	if !h.groupsReady(c) {
		return
	}
	gid, ok := pathID(c, "group_id")
	if !ok {
		return
	}
	days := 0
	if raw := c.Query("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "days is a number of days", "code": destpolicy.CodePolicyInvalid, "field": "days", "bad": []string{raw}})
			return
		}
		days = n
	}
	r, err := h.w.AllowlistGroups.Report(c.Request.Context(), gid, days)
	if err != nil {
		h.groupError(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

// groupStateSummaries are the groups in force, for the policy list.
func (h *AdminDestHandler) groupStateSummaries(c *gin.Context) []destgroup.Summary {
	if h.w.AllowlistGroups == nil {
		return []destgroup.Summary{}
	}
	out, err := h.w.AllowlistGroups.Summaries(c.Request.Context())
	if err != nil {
		return []destgroup.Summary{}
	}
	return out
}

// listNotReadyBody names the lists of a dest_list_not_ready.
func (h *AdminDestHandler) listNotReadyBody(c *gin.Context, ids []int64) gin.H {
	refs := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		ref := gin.H{"id": id, "name": ""}
		if h.w.Lists != nil {
			if l, err := h.w.Lists.GetList(c.Request.Context(), id); err == nil {
				ref["name"] = l.Name
			}
		}
		refs = append(refs, ref)
	}
	return gin.H{"not_ready_lists": refs}
}

// groupRefs names allowlist groups by id, for a 409 that says who holds a list.
func (h *AdminDestHandler) groupRefs(c *gin.Context, ids []int64) []gin.H {
	out := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		ref := gin.H{"kind": "group", "id": id, "name": ""}
		if h.w.Groups != nil {
			if g, err := h.w.Groups.GetByID(c.Request.Context(), id); err == nil {
				ref["name"] = g.Name
			}
		}
		out = append(out, ref)
	}
	return out
}
