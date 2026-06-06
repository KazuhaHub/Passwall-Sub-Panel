package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AdminGroupHandler exposes group CRUD under /api/admin/groups.
// When a group's tag_filter changes, every member is re-synced against the
// new definition through user.ResyncMembership.
type AdminGroupHandler struct {
	group *group.Service
	user  *user.Service
	users ports.UserRepo
}

func NewAdminGroupHandler(groupSvc *group.Service, userSvc *user.Service, users ports.UserRepo) *AdminGroupHandler {
	return &AdminGroupHandler{group: groupSvc, user: userSvc, users: users}
}

// ---- DTOs ----

type groupDTO struct {
	ID         int64         `json:"id"`
	Slug       string        `json:"slug"`
	Name       string        `json:"name"`
	TagFilter  tagFilterDTO  `json:"tag_filter"`
	Layout     domain.Layout `json:"layout"`
	Remark     string        `json:"remark,omitempty"`
	Require2FA bool          `json:"require_2fa"`
	Members    int64         `json:"members"`
}

type tagFilterDTO struct {
	All  bool     `json:"all"`
	Tags []string `json:"tags"`
	// Mode controls how Tags are combined. "" or "all" → AND (every cond
	// must match); "any" → OR (at least one match). Empty serializes back
	// as omitted on rows persisted before OR support was added.
	Mode string `json:"mode,omitempty"`
}

type createGroupRequest struct {
	Slug       string        `json:"slug" binding:"required"`
	Name       string        `json:"name" binding:"required"`
	TagFilter  tagFilterDTO  `json:"tag_filter"`
	Layout     domain.Layout `json:"layout"`
	Remark     string        `json:"remark"`
	Require2FA bool          `json:"require_2fa"`
}

type updateGroupRequest struct {
	Name       *string       `json:"name,omitempty"`
	TagFilter  *tagFilterDTO `json:"tag_filter,omitempty"`
	Remark     *string       `json:"remark,omitempty"`
	Require2FA *bool         `json:"require_2fa,omitempty"`
}

type updateLayoutRequest struct {
	Layout domain.Layout `json:"layout"`
}

// ---- Handlers ----

func (h *AdminGroupHandler) List(c *gin.Context) {
	p := parsePagination(c)
	groups, total, err := h.group.ListPaged(c.Request.Context(), p)
	if err != nil {
		respondError(c, err)
		return
	}
	// Batch the member-count fetch — pre-fix the loop issued one
	// SELECT COUNT(*) per row. page_size=25 → 26 queries per /groups
	// load; CountMembersByGroups collapses to one GROUP BY.
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	counts, _ := h.group.CountMembersByGroups(c.Request.Context(), ids)
	out := make([]groupDTO, len(groups))
	for i, g := range groups {
		out[i] = toGroupDTO(g)
		out[i].Members = counts[g.ID]
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminGroupHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	g, err := h.group.Get(c.Request.Context(), id)
	if err != nil {
		mapGroupServiceError(c, err)
		return
	}
	dto := toGroupDTO(g)
	if cnt, err := h.group.CountMembers(c.Request.Context(), id); err == nil {
		dto.Members = cnt
	}
	c.JSON(http.StatusOK, dto)
}

func (h *AdminGroupHandler) Create(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	g := &domain.Group{
		Slug:       req.Slug,
		Name:       req.Name,
		TagFilter:  domain.TagFilter{All: req.TagFilter.All, Tags: req.TagFilter.Tags, Mode: req.TagFilter.Mode},
		Layout:     req.Layout,
		Remark:     req.Remark,
		Require2FA: req.Require2FA,
	}
	if err := h.group.Create(c.Request.Context(), g); err != nil {
		mapGroupServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toGroupDTO(g))
}

// Update applies partial changes. If tag_filter changed, every member is
// re-synced against the new definition. Resync errors are surfaced but
// don't block the response — leftover drift is healed by reconciliation.
func (h *AdminGroupHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	g, err := h.group.Get(c.Request.Context(), id)
	if err != nil {
		mapGroupServiceError(c, err)
		return
	}
	var req updateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filterChanged := false
	if req.Name != nil {
		g.Name = *req.Name
	}
	if req.TagFilter != nil {
		g.TagFilter = domain.TagFilter{All: req.TagFilter.All, Tags: req.TagFilter.Tags, Mode: req.TagFilter.Mode}
		filterChanged = true
	}
	if req.Remark != nil {
		g.Remark = *req.Remark
	}
	if req.Require2FA != nil {
		g.Require2FA = *req.Require2FA
	}
	if err := h.group.Update(c.Request.Context(), g); err != nil {
		mapGroupServiceError(c, err)
		return
	}

	// On a filter change every member's 3X-UI memberships must be recomputed.
	// Run it immediately but OFF the request thread (sync-first, async fallback
	// per member) so a populous group / slow panel doesn't block the save on N
	// sequential 3X-UI round-trips. The save returns at once; reconcile heals
	// anything the background pass can't finish.
	if filterChanged {
		h.user.ResyncGroupMembersInBackground(id)
	}
	c.JSON(http.StatusOK, gin.H{"group": toGroupDTO(g)})
}

func (h *AdminGroupHandler) UpdateLayout(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req updateLayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	g, err := h.group.Get(c.Request.Context(), id)
	if err != nil {
		mapGroupServiceError(c, err)
		return
	}
	g.Layout = req.Layout
	if err := h.group.Update(c.Request.Context(), g); err != nil {
		mapGroupServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toGroupDTO(g))
}

func (h *AdminGroupHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.group.Delete(c.Request.Context(), id); err != nil {
		mapGroupServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- helpers ----

func toGroupDTO(g *domain.Group) groupDTO {
	tags := g.TagFilter.Tags
	if tags == nil {
		tags = []string{}
	}
	return groupDTO{
		ID:         g.ID,
		Slug:       g.Slug,
		Name:       g.Name,
		TagFilter:  tagFilterDTO{All: g.TagFilter.All, Tags: tags, Mode: g.TagFilter.Mode},
		Layout:     g.Layout,
		Remark:     g.Remark,
		Require2FA: g.Require2FA,
	}
}

func mapGroupServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		respondError(c, err)
	}
}
