package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// UpgradeOptions answers, per component, whether THIS instance may upgrade it —
// and when it may not, why not.
//
// WHY PER-COMPONENT AND NOT ONE BOOLEAN. The three components fail for
// unrelated reasons. Apanel's upgrade may be possible but unpinnable; a core's
// may be pinnable and simply not offered for this version; an agent's depends on
// a verified edge that has nothing to do with either. Collapsing them into
// "upgradable" would make the honest answer for one component hide behind the
// confident answer for another, and the caller could not tell "no" from "not
// this way".
//
// THE ANSWERS ARE COMPUTED HERE, NOT IN THE CLIENT. A UI that decided for itself
// would be a second decision source, and the one the operator sees would not be
// the one the service enforces. The endpoint reports; the write paths enforce.
type upgradeOptionState string

const (
	// upgradeReady: this instance can be told to upgrade this component now.
	upgradeReady upgradeOptionState = "ready"
	// upgradeManualOnly: an upgrade exists but cannot be performed the managed
	// way — the operator does it themselves. Distinct from unsupported, which
	// means there is nothing to do here at all.
	upgradeManualOnly upgradeOptionState = "manual_only"
	// upgradeUnsupported: this backend has no such capability.
	upgradeUnsupported upgradeOptionState = "unsupported"
	// upgradeBlocked: the capability exists and the request is currently refused.
	upgradeBlocked upgradeOptionState = "blocked"
)

type upgradeOption struct {
	Component      string             `json:"component"`
	State          upgradeOptionState `json:"state"`
	CurrentVersion string             `json:"current_version,omitempty"`
	TargetVersion  string             `json:"target_version,omitempty"`
	// TargetPinnable is false when the executor cannot be held to the version
	// shown. It is reported even for a ready answer, because "you may do this"
	// and "this will reach a known version" are different promises.
	TargetPinnable bool     `json:"target_pinnable"`
	ReasonCodes    []string `json:"reason_codes"`
}

// decidePanelUpgrade answers for the 3X-UI panel component.
//
// 3X-UI's /updatePanel takes no version argument, so the target is not pinnable
// and the managed path is the operator choosing to pull latest rather than an
// upgrade to a version. That makes it manual_only rather than ready: the plan's
// rule is that a strict managed upgrade needs a pinnable executor, and this one
// cannot be pinned.
func decidePanelUpgrade(hasUpdater bool, info *ports.PanelUpdateInfo, compat version.CompatStatus) upgradeOption {
	if !hasUpdater {
		return upgradeOption{Component: "panel", State: upgradeUnsupported, ReasonCodes: []string{"capability_missing"}}
	}
	if info == nil {
		return upgradeOption{Component: "panel", State: upgradeBlocked, ReasonCodes: []string{"target_unknown"}}
	}
	option := upgradeOption{
		Component:      "panel",
		CurrentVersion: info.CurrentVersion,
		TargetVersion:  info.LatestVersion,
	}
	if !info.UpdateAvailable {
		option.State = upgradeReady
		option.TargetPinnable = true
		option.ReasonCodes = []string{"already_latest"}
		return option
	}
	// The target cannot be pinned regardless of what the compatibility range
	// says, and reporting readiness would promise a version the panel is not
	// held to.
	option.State = upgradeManualOnly
	option.ReasonCodes = []string{"target_not_pinnable"}
	if compat == version.CompatTooOld || compat == version.CompatUnknown {
		option.ReasonCodes = append(option.ReasonCodes, "target_"+compat.String())
	}
	return option
}

// decideCoreUpgrade answers for the proxy-core component.
//
// Different from the panel in the way that matters: the core can be told an
// exact version, so this is a managed path and its failures are refusals rather
// than a hand-off.
func decideCoreUpgrade(hasUpdater bool, currentVersion string) upgradeOption {
	if !hasUpdater {
		return upgradeOption{Component: "core", State: upgradeUnsupported, ReasonCodes: []string{"capability_missing"}}
	}
	return upgradeOption{
		Component:      "core",
		State:          upgradeReady,
		CurrentVersion: currentVersion,
		TargetPinnable: true,
		ReasonCodes:    []string{"target_pinnable"},
	}
}

// decideAgentUpgrade answers for the native-agent component from the policy in
// force and the node's own identity.
//
// A release being listed is not a path to it having been checked, so this needs
// both: a policy that offers a target, and a verified edge from where the node
// is now. Without a policy the panel is in its pre-policy state and the answer
// is computed from the manifest edges, which is what it always was.
func decideAgentUpgrade(currentVersion string, offersTarget, edgeVerified bool) upgradeOption {
	option := upgradeOption{Component: "agent", CurrentVersion: currentVersion}
	switch {
	case currentVersion == "":
		option.State = upgradeBlocked
		option.ReasonCodes = []string{"identity_unknown"}
	case !offersTarget:
		option.State = upgradeBlocked
		option.ReasonCodes = []string{"no_offered_target"}
	case !edgeVerified:
		// Compatible, but nobody walked this path. The refusal names the PAIR.
		option.State = upgradeBlocked
		option.ReasonCodes = []string{"upgrade_edge_unverified"}
	default:
		option.State = upgradeReady
		option.TargetPinnable = true
		option.ReasonCodes = []string{"edge_verified"}
	}
	return option
}

// UpgradeOptions is the read-only per-component plan for one server.
func (h *AdminServersHandler) UpgradeOptions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	component := c.Query("component")
	if component != "panel" && component != "core" && component != "agent" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "component must be one of panel, core, agent",
			// The three are separate questions; a default would answer one while
			// the caller asked another.
			"reason": "component_required",
		})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}

	var option upgradeOption
	switch component {
	case "panel":
		var info *ports.PanelUpdateInfo
		if updater, ok := client.(ports.PanelUpdater); ok {
			probed, perr := updater.GetPanelUpdateInfo(c.Request.Context())
			if perr != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to query update info: " + perr.Error()})
				return
			}
			info = probed
		}
		_, hasUpdater := client.(ports.PanelUpdater)
		compat := version.CompatUnknown
		if info != nil {
			compat = version.CheckXUI(info.LatestVersion)
		}
		option = decidePanelUpgrade(hasUpdater, info, compat)
	case "core":
		// The capability is checked BEFORE the status probe. Asking a backend for
		// a core version it has no concept of is a call whose answer cannot be
		// used, and the refusal does not depend on what the probe would have
		// said — the operation does not exist either way.
		if _, hasUpdater := client.(ports.CoreUpdater); !hasUpdater {
			option = decideCoreUpgrade(false, "")
			break
		}
		current := ""
		if status, serr := client.GetServerStatus(c.Request.Context()); serr == nil {
			current = status.XrayVersion
		}
		option = decideCoreUpgrade(true, current)
	case "agent":
		option = h.decideAgentOption(c, id)
	}
	c.JSON(http.StatusOK, option)
}

func (h *AdminServersHandler) decideAgentOption(c *gin.Context, panelID int64) upgradeOption {
	// The node's own reported version lands on the panel row: the daemon writes
	// its official tag there when it checks in, which is what the update hint
	// already reads. The agent record carries protocol and capability
	// observations, not a release version.
	current := ""
	if h.repo != nil {
		if panel, err := h.repo.GetByID(c.Request.Context(), panelID); err == nil && panel != nil {
			current = panel.PanelVersion
		}
	}
	// Before a policy exists the panel is in its pre-policy state, where the
	// manifest's edges are the only source and there is no offered-target list
	// to consult. Once one is in force it answers both questions.
	targetOffered := !version.PolicyInForce() || (current != "" && version.PolicyOffersRelease(current))
	edgeVerified := current != "" && version.HasUpgradeEdgeFrom(current)
	return decideAgentUpgrade(current, targetOffered, edgeVerified)
}
