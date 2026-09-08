package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sharedclient"
)

// LimitEnforcementReader is the sharedclient half of the answer: which panels
// this user has clients on, and whether each can STORE the connection caps.
// An interface rather than the concrete service so this endpoint can be tested
// without a panel pool.
type LimitEnforcementReader interface {
	LimitEnforcement(ctx context.Context, userID int64) ([]sharedclient.PanelLimitFacts, error)
}

// userLimitEnforcementDTO answers, for one user, what the caps an admin typed
// will actually do.
//
// It exists because the two numbers on the edit form are per USER while both
// caps are enforced per (panel, client email), and nothing on that form said
// so. An admin typing "2 concurrent IPs" was reading a promise the system does
// not make: with clients on three enforcing panels the user can hold six, and
// with one panel unable to enforce they can hold as many as they like.
//
// Facts only, no verdict. The client derives the wording so the rules live in
// one tested place rather than being split across two languages.
type userLimitEnforcementDTO struct {
	// The RESOLVED caps, repeated here so the client never has to correlate
	// this response with a separately-fetched user row to say what it means.
	// 0 = unlimited.
	IPLimit     int `json:"ip_limit"`
	DeviceLimit int `json:"device_limit"`

	// DeviceLimitEnforcedAnywhere is a constant false today, and is sent as a
	// field rather than assumed by the client so the day it becomes true is a
	// server change rather than a hunt through the UI.
	//
	// 3X-UI enforces limitHwid in exactly one place — its own subscription
	// controller, where a client app's fetch registers a device — and PSP
	// serves its own subscriptions, so that gate never fires for a PSP user on
	// any panel version. Storing the field is a capability; acting on it is
	// not, and no panel acts on it. See internal/adapters/xui/client.go and
	// docs/connection-limits.md §4.1.
	DeviceLimitEnforcedAnywhere bool `json:"device_limit_enforced_anywhere"`

	Panels []userLimitPanelDTO `json:"panels"`
}

type userLimitPanelDTO struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind,omitempty"`
	// Clients is how many panel-side rows this user has here. Each row carries
	// its own copy of the caps, so this is a multiplier on this panel alone.
	Clients        int  `json:"clients"`
	CanStoreIP     bool `json:"can_store_ip"`
	CanStoreDevice bool `json:"can_store_device"`
	// PanelUnreachable / PanelMissing separate two unknowns from each other and
	// both from a working panel. An unknown must never render as enforcing.
	PanelUnreachable bool `json:"panel_unreachable,omitempty"`
	PanelMissing     bool `json:"panel_missing,omitempty"`
	// IPLimitEnforcement is read from the panel ROW, never from the pool: the
	// fail2ban probe writes only the row, so a pool-cached copy would report
	// the state as of process start and could say "enforced" long after the
	// probe stopped agreeing. Always sent, "unknown" included.
	IPLimitEnforcement string     `json:"ip_limit_enforcement"`
	IPLimitProbedAt    *time.Time `json:"ip_limit_probed_at,omitempty"`
}

// LimitEnforcement serves GET /api/admin/users/:id/limit-enforcement.
func (h *AdminUserHandler) LimitEnforcement(c *gin.Context) {
	if h.shared == nil || h.panels == nil {
		// 503 rather than an empty list: "this build cannot answer" and "this
		// user's caps are fine everywhere" must not look the same.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "limit enforcement reporting is not wired in this deployment"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	facts, err := h.shared.LimitEnforcement(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}

	out := buildLimitEnforcementDTO(u.IPLimit, u.DeviceLimit, facts, func(panelID int64) *domain.XUIPanel {
		p, perr := h.panels.GetByID(c.Request.Context(), panelID)
		if perr != nil {
			return nil
		}
		return p
	})
	c.JSON(http.StatusOK, out)
}

// compile-time reminder that the capability constants this endpoint reports on
// are the same two the push path gates writes with. If either is renamed or
// retired, this stops compiling here as well as there.
var _ = [...]ports.PanelCapability{ports.CapabilityClientIPLimit, ports.CapabilityClientDeviceLimit}

// buildLimitEnforcementDTO joins the two halves of the answer.
//
// lookup returns nil for a panel that cannot be read, and the two halves are
// deliberately sourced differently: capabilities come from the pool's adapter,
// while the fail2ban verdict comes from the panel ROW. The probe writes only
// the row (UpdateIPLimitEnforcement), so the pool's cached copy is frozen at
// the moment the panel was registered — reading enforcement from there would
// report "enforced" for a node whose probe has since said otherwise, which is
// the exact failure this endpoint exists to make visible.
func buildLimitEnforcementDTO(
	ipLimit, deviceLimit int,
	facts []sharedclient.PanelLimitFacts,
	lookup func(panelID int64) *domain.XUIPanel,
) userLimitEnforcementDTO {
	out := userLimitEnforcementDTO{
		IPLimit:     ipLimit,
		DeviceLimit: deviceLimit,
		Panels:      make([]userLimitPanelDTO, 0, len(facts)),
	}
	for _, f := range facts {
		row := userLimitPanelDTO{
			PanelID:          f.PanelID,
			Clients:          f.Clients,
			CanStoreIP:       f.CanStoreIP,
			CanStoreDevice:   f.CanStoreDevice,
			PanelUnreachable: f.PanelUnreachable,
			// Defaulted, not omitted. An absent verdict and an "unknown" one
			// mean the same thing and must render the same way; leaving the
			// field empty would let a client treat it as "nothing to worry
			// about".
			IPLimitEnforcement: string(domain.IPLimitEnforcementUnknown),
		}
		if p := lookup(f.PanelID); p != nil {
			row.Name = p.Name
			row.Kind = string(p.Kind)
			row.IPLimitProbedAt = p.IPLimitProbedAt
			if p.IPLimitEnforcement != "" {
				row.IPLimitEnforcement = string(p.IPLimitEnforcement)
			}
		} else {
			// A client whose panel row is gone. Reported rather than dropped:
			// silently shrinking the list would hide the one panel nobody can
			// vouch for.
			row.PanelMissing = true
		}
		out.Panels = append(out.Panels, row)
	}
	return out
}
