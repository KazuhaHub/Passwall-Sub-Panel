package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type reconcileEligibility struct{ failSecond bool }

func (e reconcileEligibility) Eligible(_ context.Context, n *domain.Node, _ *domain.Group) (bool, error) {
	if n.PanelID == 82 {
		if e.failSecond {
			return false, errors.New("eligibility unavailable")
		}
		return false, nil
	}
	return true, nil
}

func TestReconcileMemberAdditionChecksAllCandidateEligibilityBeforeWrites(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked candidate", true: "later candidate read fails"}[failure], func(t *testing.T) {
			syncer := &capturingSyncer{}
			s := &Service{syncer: syncer, pool: recPool{}}
			s.SetGroupEligibility(reconcileEligibility{failSecond: failure})
			nodes := []*domain.Node{{ID: 1, PanelID: 81, InboundID: 20, Enabled: true}, {ID: 2, PanelID: 82, InboundID: 21, Enabled: true}}
			cache := map[inboundCacheKey]*inboundCacheEntry{{panelID: 81, inboundID: 20}: {inbound: &ports.Inbound{ID: 20, Protocol: "vless"}}, {panelID: 82, inboundID: 21}: {inbound: &ports.Inbound{ID: 21, Protocol: "vless"}}}
			entries := []*domain.XUIClientEntry{{PanelID: 99, InboundID: 1}}
			s.checkMissingOwnershipsWithCtx(t.Context(), &domain.User{ID: 7, UUID: "user-7", GroupID: 8}, domain.UserLifecycle{Enable: true}, &Report{}, cache, domain.EmailRules{}, nodes, &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}, entries, true)
			want := 1
			if failure {
				want = 0
			}
			if len(syncer.added) != want {
				t.Fatalf("unsafe/partial group addition: writes=%d want=%d", len(syncer.added), want)
			}
		})
	}
}
