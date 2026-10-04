package destpolicy

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"golang.org/x/sync/singleflight"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type TagMatchedMemberReader interface {
	TagMatchedMembers(context.Context, int64) (map[int64][]int64, error)
}
type Inputs struct {
	panels               ports.PanelAuditSettingsRepo
	users                ports.UserMembershipRepo
	groups               TagMatchedMemberReader
	membershipGeneration func() uint64
	membershipCache      *membershipInputCache
	membershipFlights    singleflight.Group
}

func NewInputs(panels ports.PanelAuditSettingsRepo, users ports.UserMembershipRepo, groups TagMatchedMemberReader) (*Inputs, error) {
	if panels == nil || users == nil || groups == nil {
		return nil, domain.ErrValidation
	}
	return &Inputs{panels: panels, users: users, groups: groups, membershipCache: newMembershipInputCache()}, nil
}

// SetMembershipGeneration enables membership caching. Wire before serving:
// the counter must advance after committed user, group-filter and node-selection
// changes. Nil reads fresh each time rather than assuming invalidation exists.
func (i *Inputs) SetMembershipGeneration(generation func() uint64) {
	i.membershipGeneration = generation
	i.membershipCache = newMembershipInputCache()
}

// ForNode keeps roster scope separate from full tag-matched quota membership.
// Collection-only paths avoid all membership reads.
func (i *Inputs) ForNode(ctx context.Context, panelID int64, snapshot *ports.NativeDesiredSnapshot, includeMembers bool) (RosterInput, RosterInput, error) {
	if panelID <= 0 || snapshot == nil {
		return RosterInput{}, RosterInput{}, domain.ErrValidation
	}
	if i == nil || i.panels == nil || i.users == nil || i.groups == nil {
		return RosterInput{}, RosterInput{}, domain.ErrUnavailable
	}
	settings, err := i.panels.GetAuditSettings(ctx, panelID)
	if err != nil {
		return RosterInput{}, RosterInput{}, err
	}
	if !settings.Collect.Valid() || settings.Revision == 0 {
		return RosterInput{}, RosterInput{}, fmt.Errorf("%w: invalid native audit control", domain.ErrUnavailable)
	}
	roster := RosterInput{Collect: settings.Collect, CollectRevision: settings.Revision, UserGroups: map[int64]int64{}}
	quota := RosterInput{Collect: settings.Collect, CollectRevision: settings.Revision, UserGroups: map[int64]int64{}}
	if !includeMembers {
		return roster, quota, nil
	}
	for _, client := range snapshot.Clients {
		if client.Client == nil || client.Client.UserID <= 0 {
			return RosterInput{}, RosterInput{}, fmt.Errorf("%w: invalid native policy roster", domain.ErrUnavailable)
		}
		roster.UserIDs = append(roster.UserIDs, client.Client.UserID)
	}
	slices.Sort(roster.UserIDs)
	roster.UserIDs = slices.Compact(roster.UserIDs)
	members, err := i.memberInputs(ctx, panelID, roster.UserIDs)
	if err != nil {
		return RosterInput{}, RosterInput{}, err
	}
	roster, quota = cloneMembershipInput(members.roster), cloneMembershipInput(members.quota)
	roster.Collect, quota.Collect = settings.Collect, settings.Collect
	roster.CollectRevision, quota.CollectRevision = settings.Revision, settings.Revision
	return roster, quota, nil
}

func cloneMembershipInput(input RosterInput) RosterInput {
	input.UserIDs = slices.Clone(input.UserIDs)
	input.UserGroups = maps.Clone(input.UserGroups)
	return input
}

func (i *Inputs) readMemberInputs(ctx context.Context, panelID int64, ids []int64) (membershipInputs, error) {
	roster := RosterInput{UserIDs: slices.Clone(ids), UserGroups: map[int64]int64{}}
	quota := RosterInput{UserGroups: map[int64]int64{}}
	if len(roster.UserIDs) > 0 {
		groups, err := i.users.GroupIDsByIDs(ctx, roster.UserIDs)
		if err != nil {
			return membershipInputs{}, err
		}
		for _, id := range roster.UserIDs {
			// A user removed after the roster snapshot has no current group.
			// Ignore unrequested rows and never reuse a stale client group ID.
			if groupID, found := groups[id]; found {
				if groupID < 0 {
					return membershipInputs{}, fmt.Errorf("%w: invalid roster group identity", domain.ErrUnavailable)
				}
				roster.UserGroups[id] = groupID
			}
		}
	}
	members, err := i.groups.TagMatchedMembers(ctx, panelID)
	if err != nil {
		return membershipInputs{}, err
	}
	for groupID, ids := range members {
		if groupID <= 0 {
			return membershipInputs{}, fmt.Errorf("%w: invalid quota group identity", domain.ErrUnavailable)
		}
		for _, id := range ids {
			if id <= 0 {
				return membershipInputs{}, fmt.Errorf("%w: invalid quota member identity", domain.ErrUnavailable)
			}
			if previous, found := quota.UserGroups[id]; found && previous != groupID {
				return membershipInputs{}, fmt.Errorf("%w: conflicting quota membership", domain.ErrUnavailable)
			}
			quota.UserGroups[id] = groupID
			quota.UserIDs = append(quota.UserIDs, id)
		}
	}
	slices.Sort(quota.UserIDs)
	quota.UserIDs = slices.Compact(quota.UserIDs)
	return membershipInputs{roster: roster, quota: quota}, nil
}

var _ CompilerInputs = (*Inputs)(nil)
