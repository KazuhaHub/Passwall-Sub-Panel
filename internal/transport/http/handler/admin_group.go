package handler

import (
	"errors"
	"fmt"
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
	// The group's entitlement policy every member inherits unless they
	// override it. null = the group states nothing, which resolves to
	// unlimited — distinct from 0, which states "uncapped" explicitly. The
	// UI needs the difference to render "not set" against "unlimited".
	TrafficLimitGB *float64 `json:"traffic_limit_gb"`
	IPLimit        *int     `json:"ip_limit"`
	DeviceLimit    *int     `json:"device_limit"`
	Members        int64    `json:"members"`
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
	// Omitted (or null) leaves the group stating nothing.
	TrafficLimitGB *float64 `json:"traffic_limit_gb,omitempty"`
	IPLimit        *int     `json:"ip_limit,omitempty"`
	DeviceLimit    *int     `json:"device_limit,omitempty"`
}

type updateGroupRequest struct {
	Name       *string       `json:"name,omitempty"`
	TagFilter  *tagFilterDTO `json:"tag_filter,omitempty"`
	Remark     *string       `json:"remark,omitempty"`
	Require2FA *bool         `json:"require_2fa,omitempty"`
	// A nil limit means "leave it alone", so clearing a policy back to "states
	// nothing" needs its own signal. Same shape as the user side's Inherit*
	// flags, and for the same reason a sparse DTO always needs one. A clear
	// flag wins over its value if both arrive.
	TrafficLimitGB    *float64 `json:"traffic_limit_gb,omitempty"`
	IPLimit           *int     `json:"ip_limit,omitempty"`
	DeviceLimit       *int     `json:"device_limit,omitempty"`
	ClearTrafficLimit bool     `json:"clear_traffic_limit,omitempty"`
	ClearIPLimit      bool     `json:"clear_ip_limit,omitempty"`
	ClearDeviceLimit  bool     `json:"clear_device_limit,omitempty"`
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
		Limits: domain.GroupLimits{
			TrafficLimitBytes: gbToBytesPtr(req.TrafficLimitGB),
			IPLimit:           req.IPLimit,
			DeviceLimit:       req.DeviceLimit,
		},
	}
	if err := validateGroupLimits(g.Limits); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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
	before := g.Limits
	if req.ClearTrafficLimit {
		g.Limits.TrafficLimitBytes = nil
	} else if req.TrafficLimitGB != nil {
		g.Limits.TrafficLimitBytes = gbToBytesPtr(req.TrafficLimitGB)
	}
	if req.ClearIPLimit {
		g.Limits.IPLimit = nil
	} else if req.IPLimit != nil {
		g.Limits.IPLimit = req.IPLimit
	}
	if req.ClearDeviceLimit {
		g.Limits.DeviceLimit = nil
	} else if req.DeviceLimit != nil {
		g.Limits.DeviceLimit = req.DeviceLimit
	}
	if err := validateGroupLimits(g.Limits); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// A limit change is panel-enforced, so it is not real until it reaches the
	// panels. Nothing else would carry it: an idle member generates no traffic,
	// so the poll never pushes them, and they would keep the old cap
	// indefinitely.
	limitsChanged := !sameGroupLimits(before, g.Limits)
	if err := h.group.Update(c.Request.Context(), g); err != nil {
		mapGroupServiceError(c, err)
		return
	}

	// On a filter change every member's 3X-UI memberships must be recomputed.
	// Run it immediately but OFF the request thread (sync-first, async fallback
	// per member) so a populous group / slow panel doesn't block the save on N
	// sequential 3X-UI round-trips. The save returns at once; reconcile heals
	// anything the background pass can't finish.
	if filterChanged || limitsChanged {
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
		ID:             g.ID,
		Slug:           g.Slug,
		Name:           g.Name,
		TagFilter:      tagFilterDTO{All: g.TagFilter.All, Tags: tags, Mode: g.TagFilter.Mode},
		Layout:         g.Layout,
		Remark:         g.Remark,
		Require2FA:     g.Require2FA,
		TrafficLimitGB: bytesToGBPtr(g.Limits.TrafficLimitBytes),
		IPLimit:        g.Limits.IPLimit,
		DeviceLimit:    g.Limits.DeviceLimit,
	}
}

// bytesToGBPtr renders a byte quota as GB for the API, preserving the
// null-vs-zero distinction the whole feature rests on.
func bytesToGBPtr(b *int64) *float64 {
	if b == nil {
		return nil
	}
	gb := float64(*b) / (1024 * 1024 * 1024)
	return &gb
}

// gbToBytesPtr is its inverse.
func gbToBytesPtr(gb *float64) *int64 {
	if gb == nil {
		return nil
	}
	b := int64(*gb * 1024 * 1024 * 1024)
	return &b
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

// validateGroupLimits rejects a policy a user endpoint would refuse.
//
// It runs on create as well as update: a negative cap slipped in at creation
// would be inherited by every member, pushed verbatim as the panel's LimitIP,
// and — because the capability-gap warning only fires for a positive limit —
// would not even show up as unenforceable.
//
// The user endpoints answer 400 for all three now. They did NOT when this
// comment first claimed they did: the two connection caps were guarded from
// the start, traffic was not, and asserting otherwise here was a fact never
// checked. The gap mattered — a negative quota stores as an explicit override
// and then reads as "unlimited" everywhere, because trafficFloor,
// PanelQuotaCap and the traffic-exceeded check all test `> 0`.
func validateGroupLimits(l domain.GroupLimits) error {
	if l.TrafficLimitBytes != nil && *l.TrafficLimitBytes < 0 {
		return fmt.Errorf("%w: traffic_limit_gb must be >= 0", domain.ErrValidation)
	}
	if l.IPLimit != nil && *l.IPLimit < 0 {
		return fmt.Errorf("%w: ip_limit must be >= 0", domain.ErrValidation)
	}
	if l.DeviceLimit != nil && *l.DeviceLimit < 0 {
		return fmt.Errorf("%w: device_limit must be >= 0", domain.ErrValidation)
	}
	return nil
}

// sameGroupLimits compares two policies, treating nil (states nothing) as
// distinct from any value — clearing a policy changes what members enforce
// just as much as setting one, so it has to count as a change.
func sameGroupLimits(a, b domain.GroupLimits) bool {
	return sameInt64Ptr(a.TrafficLimitBytes, b.TrafficLimitBytes) &&
		sameIntPtr(a.IPLimit, b.IPLimit) &&
		sameIntPtr(a.DeviceLimit, b.DeviceLimit)
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
