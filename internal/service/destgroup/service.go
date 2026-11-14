// Package destgroup is the admin side of the destination allowlist groups: what
// the allowlist tab and the group's own tab read (every group with its state,
// lists and the nodes that can enforce it), the one write that sets a group's
// state and lists, the preview of that write, the report of what a report-only
// group would have denied, and the exceptions an admin adds from that report.
//
// A group's allowlist is a property of the group (domain.Group.DestAllowlist,
// nullable columns on groups_) written through sqlstore.DestGroupAllowlistRepo
// together with the destination generation. Which nodes the group's members may
// have a client on is group.Service.Eligible; this package reports it, and tells
// the member sync to re-derive it after every write.
package destgroup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// ErrGroupNotFound is a group that does not exist (HTTP 404 dest_group_not_found).
var ErrGroupNotFound = fmt.Errorf("%w: no such group", domain.ErrNotFound)

// The reads and writes the service is built from, each narrow and satisfied by
// the concrete repos and services.
type (
	// AllowlistStore is the group allowlist columns (sqlstore.DestGroupAllowlistRepo).
	AllowlistStore interface {
		Get(ctx context.Context, groupID int64) (*domain.DestAllowlist, error)
		Save(ctx context.Context, groupID int64, a *domain.DestAllowlist, ifUpdatedAt *time.Time) error
		CreateWithOwnedLists(ctx context.Context, groupID int64, a *domain.DestAllowlist, base, extra *domain.DestList) error
		List(ctx context.Context) ([]*domain.DestGroupAllowlistEntry, error)
	}
	// GroupStore reads groups (ports.GroupRepo).
	GroupStore interface {
		GetByID(ctx context.Context, id int64) (*domain.Group, error)
		List(ctx context.Context) ([]*domain.Group, error)
		CountMembersByGroups(ctx context.Context, ids []int64) (map[int64]int64, error)
	}
	// ListStore reads lists (sqlstore.DestListRepo).
	ListStore interface {
		GetByID(ctx context.Context, id int64) (*domain.DestList, error)
	}
	// ListEditor appends to a custom list (destlist.Service).
	ListEditor interface {
		AddEntries(ctx context.Context, id int64, add, remove []string) (*domain.DestList, *destlist.ParseReport, error)
	}
	// Budgeter judges a save against the limits (destpolicy.Admin).
	Budgeter interface {
		CheckGroupBudget(ctx context.Context, h destpolicy.GroupHypo) error
	}
	// Eligibility is what group.Service knows about which panels can enforce an
	// allowlist.
	Eligibility interface {
		PanelsFor(ctx context.Context, g *domain.Group) ([]int64, error)
		PanelEnforceReason(ctx context.Context, panelID int64) (string, error)
		InvalidateEligibility(panelID int64)
	}
	// Resyncer re-derives the node memberships of a group's members (user.Service).
	Resyncer interface {
		ResyncGroupMembersInBackground(groupID int64)
	}
	// PanelReader reads a panel (ports.XUIPanelRepo).
	PanelReader interface {
		GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
	}
	// AgentReader reads a native panel's agent (ports.NodeAgentRepo).
	AgentReader interface {
		GetByPanelID(ctx context.Context, panelID int64) (*domain.NodeAgent, error)
	}
	// PolicyMeta reads an agent's policy delivery state (sqlstore.DestAgentPolicyRepo).
	PolicyMeta interface {
		GetMeta(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error)
	}
	// HitReader reads the report-only rows of the groups (sqlstore.DestHitRepo).
	HitReader interface {
		TrialSites(ctx context.Context, source string, sinceMS int64) ([]domain.DestTrialSite, error)
		TrialDropped(ctx context.Context, sinceMS, untilMS int64) (int64, error)
		SourceCounts(ctx context.Context, sinceMS int64) ([]domain.DestSourceCount, error)
	}
	// SettingsReader is the settings store (for the retention of trial rows).
	SettingsReader interface {
		Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
	}
)

// Deps is everything the service is built from. Allowlists, Groups, Lists,
// Eligibility, Panels, Agents and Policies are required; the others may be nil
// and what they serve then degrades (no Budget: a save is not measured; no
// Resync: the heal sweep converges the members; no Hits: no report or denial
// counts; no BaseHosts: no suggestion; no Editor: no group exceptions).
type Deps struct {
	Allowlists  AllowlistStore
	Groups      GroupStore
	Lists       ListStore
	Editor      ListEditor
	Budget      Budgeter
	Eligibility Eligibility
	Resync      Resyncer
	Panels      PanelReader
	Agents      AgentReader
	Policies    PolicyMeta
	Hits        HitReader
	Settings    SettingsReader
	// BaseHosts are the hosts a new group's base allowance is suggested from
	// (the current subscription templates' DNS section).
	BaseHosts func(ctx context.Context) ([]string, error)
	// Changed is called after a write has told the eligibility and the member
	// sync (the rendered subscriptions drop what they cached).
	Changed func(ctx context.Context)
	Now     func() time.Time
}

// Service is the admin side of the allowlist groups.
type Service struct {
	Deps
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Allowlists == nil || d.Groups == nil || d.Lists == nil || d.Eligibility == nil || d.Panels == nil || d.Agents == nil || d.Policies == nil {
		return nil, errors.New("destgroup: allowlists, groups, lists, eligibility, panels, agents and policies are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{Deps: d}, nil
}

// ---- views ----

// ListView is a list an allowlist names. State is ready, not_ready or empty.
type ListView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	EntryCount int    `json:"entry_count"`
	State      string `json:"state"`
}

// ListRef names a list.
type ListRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// PanelRef names a panel (a node, as the admin knows it).
type PanelRef struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
}

// Ineligible is a node that cannot enforce the allowlist, with why: xui, sui,
// capability, over_limit, rejected or sniffing.
type Ineligible struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
}

// WouldDeny is what a report-only group would have denied lately.
type WouldDeny struct {
	Days  int   `json:"days"`
	Sites int   `json:"sites"`
	Count int64 `json:"count"`
}

// View is one group with its allowlist: GET /dest/groups.
type View struct {
	GroupID        int64        `json:"group_id"`
	GroupName      string       `json:"group_name"`
	Members        int64        `json:"members"`
	State          string       `json:"state"`
	StateChangedAt *int64       `json:"state_changed_at"`
	UpdatedAt      *int64       `json:"updated_at"`
	ListIDs        []int64      `json:"list_ids"`
	Lists          []ListView   `json:"lists"`
	BaseListID     int64        `json:"base_list_id"`
	ExtraListID    int64        `json:"extra_list_id"`
	BaseCount      int          `json:"base_count"`
	EligibleNodes  int          `json:"eligible_nodes"`
	TotalNodes     int          `json:"total_nodes"`
	Ineligible     []Ineligible `json:"ineligible"`
	AppliedNodes   int          `json:"applied_nodes"`
	PendingNodes   []PanelRef   `json:"pending_nodes"`
	NotReadyLists  []ListRef    `json:"not_ready_lists"`
	WouldDeny      *WouldDeny   `json:"would_deny_recent"`
	Denied24h      *int64       `json:"denied_24h"`
}

// SiteCount is a destination and how often it was seen.
type SiteCount struct {
	Site  string `json:"site"`
	Count int64  `json:"count"`
	Added bool   `json:"added"`
}

// Readiness is what the turn-on dialog lists beside the view.
type Readiness struct {
	ListsReady        bool        `json:"lists_ready"`
	NotReadyLists     []ListRef   `json:"not_ready_lists"`
	UnappliedNodes    int         `json:"unapplied_nodes"`
	BaseNonempty      bool        `json:"base_nonempty"`
	RemainingSites24h int         `json:"remaining_sites_24h"`
	TopSites          []SiteCount `json:"top_sites"`
}

// Detail is GET /dest/groups/:group_id.
type Detail struct {
	*View
	Readiness Readiness `json:"readiness"`
}

// PutInput is PUT /dest/groups/:group_id.
type PutInput struct {
	State     domain.DestState
	ListIDs   []int64
	BaseHosts *[]string
	UpdatedAt *time.Time
}

// PreviewInput is GET /dest/groups/:group_id/preview.
type PreviewInput struct {
	State   domain.DestState
	ListIDs []int64
}

// WouldDenyTop is the preview's would-deny figure with its biggest sites.
type WouldDenyTop struct {
	WouldDeny
	Top []SiteCount `json:"top"`
}

// Preview is what a save would do, without saving.
type Preview struct {
	Members           int64         `json:"members"`
	EligibleNodes     int           `json:"eligible_nodes"`
	TotalNodes        int           `json:"total_nodes"`
	Ineligible        []Ineligible  `json:"ineligible"`
	NodesLost         []Ineligible  `json:"nodes_lost"`
	NonRecordingNodes int           `json:"non_recording_nodes"`
	NotReadyLists     []ListRef     `json:"not_ready_lists"`
	BaseHosts         []string      `json:"base_hosts"`
	BaseHostsSource   string        `json:"base_hosts_source"`
	WouldDeny         *WouldDenyTop `json:"would_deny_recent"`
}

// ReportItem is one destination of the report.
type ReportItem struct {
	Site   string `json:"site"`
	Count  int64  `json:"count"`
	LastAt int64  `json:"last_at"`
	Added  bool   `json:"added"`
}

// Report is GET /dest/groups/:group_id/report.
type Report struct {
	EffectiveDays  int          `json:"effective_days"`
	State          string       `json:"state"`
	SinceAt        *int64       `json:"since_at"`
	Items          []ReportItem `json:"items"`
	DroppedInRange int64        `json:"dropped_in_range"`
}

// Summary is a group in force, as the policy list names it.
type Summary struct {
	GroupID   int64  `json:"group_id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	StateDays int    `json:"state_days"`
}

// ExceptionResult is an entry added to a group's extra list.
type ExceptionResult struct {
	Entry   string `json:"entry"`
	GroupID int64  `json:"group_id"`
	ListID  int64  `json:"list_id"`
}

// Wiring says which optional collaborators the service was built with. It exists
// for the application's wiring test: a collaborator left out degrades without a
// word.
type Wiring struct{ Budget, Resync, Changed, Hits, BaseHosts, Editor, Settings bool }

// Wired reports what the service has.
func (s *Service) Wired() Wiring {
	return Wiring{
		Budget: s.Budget != nil, Resync: s.Resync != nil, Changed: s.Changed != nil, Hits: s.Hits != nil,
		BaseHosts: s.BaseHosts != nil, Editor: s.Editor != nil, Settings: s.Settings != nil,
	}
}
