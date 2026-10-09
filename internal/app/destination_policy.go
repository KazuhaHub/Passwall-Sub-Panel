package app

import (
	"context"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

func destinationPolicyMinSeconds(ctx context.Context, settings ports.SettingsRepo) (int, error) {
	if settings == nil {
		return 0, domain.ErrUnavailable
	}
	stored, err := settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return 0, err
	}
	return stored.DestinationSettings().Effective().PolicyApplyMinSeconds, nil
}

// Invalidation precedes asynchronous membership work. The callback can run
// under an agent's Sync lock, so it never waits for a member's panel call.
// Failed reads preserve the existing plan; periodic heal provides recovery.
func destinationAllowlistResyncer(groups *group.Service, users *user.Service, eligibility ports.DestinationEligibilityRepo, invalidateRender func(), run func(string, func(context.Context))) func(context.Context, int64) {
	return func(_ context.Context, panelID int64) {
		groups.InvalidateEligibility(panelID)
		invalidateRender()
		run("destination.allowlist-members", func(ctx context.Context) {
			members, err := groups.TagMatchedMembers(ctx, panelID)
			if err != nil {
				log.Warn("destination allowlist membership read failed", "panel_id", panelID)
				return
			}
			ids := make([]int64, 0, len(members))
			for id := range members {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			// Resolve every mode before enqueuing any changes. A storage error
			// is never converted into a permissive or restrictive verdict.
			var selected []int64
			for _, id := range ids {
				mode, err := eligibility.GroupEligibilityMode(ctx, id)
				if err != nil {
					log.Warn("destination allowlist mode read failed", "group_id", id)
					return
				}
				if mode == "allowlist" {
					selected = append(selected, id)
				}
			}
			for _, id := range selected {
				users.ResyncGroupMembersInBackground(id)
			}
		})
	}
}
