package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

// AdminTransitHandler serves the settings of the tunnels between Passwall Node
// servers. A tunnel belongs to an ordered pair of servers (entry, exit) and
// carries every node of the entry whose traffic leaves from the exit. Its
// credentials never leave the panel.
type AdminTransitHandler struct {
	links *transit.Service
}

func NewAdminTransitHandler(links *transit.Service) *AdminTransitHandler {
	return &AdminTransitHandler{links: links}
}

type transitLinkDTO struct {
	Exists            bool   `json:"exists"`
	Protocol          string `json:"protocol"`
	ExitAddress       string `json:"exit_address"`
	RealityServerName string `json:"reality_server_name"`
	RealityTarget     string `json:"reality_target"`
	Port              int    `json:"port,omitempty"`
	ServerAddress     string `json:"server_address"`
	UpBytes           int64  `json:"up_bytes"`
	DownBytes         int64  `json:"down_bytes"`
}

type updateTransitLinkRequest struct {
	Protocol          string `json:"protocol" binding:"required"`
	ExitAddress       string `json:"exit_address"`
	RealityServerName string `json:"reality_server_name"`
	RealityTarget     string `json:"reality_target"`
}

func linkDTO(view transit.LinkView) transitLinkDTO {
	return transitLinkDTO{
		Exists: view.Exists, Protocol: view.Protocol, ExitAddress: view.ExitAddress,
		RealityServerName: view.RealityServerName, RealityTarget: view.RealityTarget,
		Port: view.Port, ServerAddress: view.ServerAddress, UpBytes: view.UpBytes, DownBytes: view.DownBytes,
	}
}

func linkPair(c *gin.Context) (int64, int64, bool) {
	entry, err1 := strconv.ParseInt(c.Param("entry"), 10, 64)
	exit, err2 := strconv.ParseInt(c.Param("exit"), 10, 64)
	if err1 != nil || err2 != nil || entry <= 0 || exit <= 0 || entry == exit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a tunnel joins two different servers"})
		return 0, 0, false
	}
	return entry, exit, true
}

// GetLink returns the tunnel from one server to another, or the defaults a new
// one would start from.
func (h *AdminTransitHandler) GetLink(c *gin.Context) {
	entry, exit, ok := linkPair(c)
	if !ok {
		return
	}
	view, err := h.links.Link(c.Request.Context(), entry, exit)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, linkDTO(view))
}

// UpdateLink changes a tunnel's protocol, the address the entry dials, and the
// REALITY camouflage site.
func (h *AdminTransitHandler) UpdateLink(c *gin.Context) {
	entry, exit, ok := linkPair(c)
	if !ok {
		return
	}
	var req updateTransitLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	view, err := h.links.UpdateLink(c.Request.Context(), entry, exit, domain.TransitLinkSettings{
		Protocol: req.Protocol, ExitAddress: req.ExitAddress,
		RealityServerName: req.RealityServerName, RealityTarget: req.RealityTarget,
	})
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, linkDTO(view))
}
