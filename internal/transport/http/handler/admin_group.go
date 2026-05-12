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
	ID        int64         `json:"id"`
	Slug      string        `json:"slug"`
	Name      string        `json:"name"`
	TagFilter tagFilterDTO  `json:"tag_filter"`
	Layout    domain.Layout `json:"layout"`
	Remark    string        `json:"remark,omitempty"`
	Members   int64         `json:"members"`
}

type tagFilterDTO struct {
	All  bool     `json:"all"`
	Tags []string `json:"tags"`
}

type createGroupRequest struct {
	Slug      string        `json:"slug" binding:"required"`
	Name      string        `json:"name" binding:"required"`
	TagFilter tagFilterDTO  `json:"tag_filter"`
	Layout    domain.Layout `json:"layout"`
	Remark    string        `json:"remark"`
}

type updateGroupRequest struct {
	Name      *string       `json:"name,omitempty"`
	TagFilter *tagFilterDTO `json:"tag_filter,omitempty"`
	Remark    *string       `json:"remark,omitempty"`
}

type updateLayoutRequest struct {
	Layout domain.Layout `json:"layout"`
}

// ---- Handlers ----

func (h *AdminGroupHandler) List(c *gin.Context) {
	groups, err := h.group.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]groupDTO, len(groups))
	for i, g := range groups {
		out[i] = toGroupDTO(g)
		if cnt, err := h.group.CountMembers(c.Request.Context(), g.ID); err == nil {
			out[i].Members = cnt
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *AdminGroupHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
		Slug:      req.Slug,
		Name:      req.Name,
		TagFilter: domain.TagFilter{All: req.TagFilter.All, Tags: req.TagFilter.Tags},
		Layout:    req.Layout,
		Remark:    req.Remark,
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
		g.TagFilter = domain.TagFilter{All: req.TagFilter.All, Tags: req.TagFilter.Tags}
		filterChanged = true
	}
	if req.Remark != nil {
		g.Remark = *req.Remark
	}
	if err := h.group.Update(c.Request.Context(), g); err != nil {
		mapGroupServiceError(c, err)
		return
	}

	resyncErrors := []string{}
	if filterChanged {
		members, err := h.users.ListByGroup(c.Request.Context(), id)
		if err != nil {
			resyncErrors = append(resyncErrors, "list members: "+err.Error())
		}
		for _, m := range members {
			if err := h.user.ResyncMembership(c.Request.Context(), m.ID); err != nil {
				resyncErrors = append(resyncErrors, m.Username+": "+err.Error())
			}
		}
	}
	resp := gin.H{"group": toGroupDTO(g)}
	if len(resyncErrors) > 0 {
		resp["resync_errors"] = resyncErrors
	}
	c.JSON(http.StatusOK, resp)
}

func (h *AdminGroupHandler) UpdateLayout(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
	return groupDTO{
		ID:        g.ID,
		Slug:      g.Slug,
		Name:      g.Name,
		TagFilter: tagFilterDTO{All: g.TagFilter.All, Tags: g.TagFilter.Tags},
		Layout:    g.Layout,
		Remark:    g.Remark,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
