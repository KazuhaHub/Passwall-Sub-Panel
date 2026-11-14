package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fixedEligibility struct {
	ok  bool
	err error
}

func (f fixedEligibility) Eligible(context.Context, *domain.Node, *domain.Group) (bool, error) {
	return f.ok, f.err
}

// scanMissing runs the missing-client heal for a user who holds one legacy
// ownership row, against one enabled node the group's tags select that the
// user does not own, and reports how many missing clients it found.
func scanMissing(t *testing.T, e GroupEligibility) int {
	t.Helper()
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: true}
	u := &domain.User{ID: 7, UUID: "uuid-7", GroupID: 9, Enabled: true}
	other := &domain.XUIClientEntry{ID: 21, UserID: u.ID, PanelID: 2, InboundID: 8, ClientEmail: "u7-n2@psp.local"}
	g := &domain.Group{ID: 9, TagFilter: domain.TagFilter{All: true}}
	svc := &Service{
		ownership: &recOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{u.ID: {other}}},
		pool:      recPool{c: &recClient{}}, // no inbound 3: the heal stops after counting it
	}
	if e != nil {
		svc.SetGroupEligibility(e)
	}
	report := &Report{}
	svc.checkMissingOwnershipsWithCtx(context.Background(), u, domain.UserLifecycle{Enable: true}, report,
		map[inboundCacheKey]*inboundCacheEntry{}, domain.EmailRules{}, []*domain.Node{node}, g,
		[]*domain.XUIClientEntry{other}, true)
	return report.Scanned
}

// The legacy missing-client heal must not re-create a client for a member on a
// node that cannot enforce the group's allowlist.
func TestMissingClientHealUsesEligibility(t *testing.T) {
	if got := scanMissing(t, nil); got != 1 {
		t.Fatalf("unwired (tags only): found %d missing, want 1", got)
	}
	if got := scanMissing(t, fixedEligibility{ok: true}); got != 1 {
		t.Fatalf("eligible: found %d missing, want 1", got)
	}
	if got := scanMissing(t, fixedEligibility{ok: false}); got != 0 {
		t.Fatalf("not eligible: found %d missing, want 0", got)
	}
	// Unknown is not a yes: skip the group this round.
	if got := scanMissing(t, fixedEligibility{err: errors.New("db down")}); got != 0 {
		t.Fatalf("unreadable: found %d missing, want 0", got)
	}
}
