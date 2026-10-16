package clientprov

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
)

type countingUserClientRepo struct {
	*fakePSPClientRepo
	reads, rows int
}

func (r *countingUserClientRepo) ListByUser(ctx context.Context, user int64) ([]*domain.PSPClient, error) {
	rows, err := r.fakePSPClientRepo.ListByUser(ctx, user)
	r.reads++
	r.rows += len(rows)
	return rows, err
}

type countingPanelClientRepo struct {
	*countingUserClientRepo
	panels    []int64
	rows      int
	failPanel int64
}

func (r *countingPanelClientRepo) ListByUserAndPanel(_ context.Context, user, panel int64) ([]*domain.PSPClient, error) {
	r.panels = append(r.panels, panel)
	if panel == r.failPanel {
		return nil, domain.ErrUnavailable
	}
	var out []*domain.PSPClient
	for _, c := range r.clients {
		if c.UserID == user && c.PanelID == panel {
			copy := *c
			out = append(out, &copy)
		}
	}
	r.rows += len(out)
	return out, nil
}

func panelScopeFixture(t *testing.T) (*countingUserClientRepo, []*domain.Node) {
	t.Helper()
	base := newFakeRepo()
	svc := New(base)
	var nodes []*domain.Node
	for panel := int64(1); panel <= 66; panel++ {
		if _, err := svc.Sync(t.Context(), 42, "stable-user-uuid", panel, rules, []clientplan.NodeCred{{NodeID: panel, Protocol: "vless"}}); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, &domain.Node{ID: panel, PanelID: panel, DesiredProtocol: "vless"})
	}
	if _, err := svc.Sync(t.Context(), 99, "foreign-user-uuid", 1, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: "vless"}}); err != nil {
		t.Fatal(err)
	}
	for id, c := range base.clients {
		c.LifetimeTotalBytes, c.LastRawTotalBytes, c.LastCounterEpoch = id*11, id*7, uint64(id+100)
		c.SetDesiredLifecycle(domain.UserLifecycle{Enable: id%2 == 0, QuotaHeadroom: id * 3})
	}
	return &countingUserClientRepo{fakePSPClientRepo: base}, nodes
}

func TestUserRetirementsPanelReadsKeepLegacyPlanningAndStableState(t *testing.T) {
	legacy, nodes := panelScopeFixture(t)
	scopedBase, _ := panelScopeFixture(t)
	scoped := &countingPanelClientRepo{countingUserClientRepo: scopedBase}
	oldRetired, oldErr := New(legacy).SyncUserRetirements(t.Context(), 42, "stable-user-uuid", rules, nodes[:65])
	newRetired, newErr := New(scoped).SyncUserRetirements(t.Context(), 42, "stable-user-uuid", rules, nodes[:65])
	if oldErr != nil || newErr != nil || !reflect.DeepEqual(oldRetired, newRetired) || len(newRetired) != 1 || !newRetired[66].PanelRemoved {
		t.Fatal("panel scoping changed the removed-panel retirement or its reason")
	}
	if !reflect.DeepEqual(legacy.clients, scoped.clients) || !reflect.DeepEqual(legacy.inbounds, scoped.inbounds) {
		t.Fatal("panel scoping changed stable IDs, definitions, counters, lifecycle, attachments or another user")
	}
	seen := map[int64]bool{}
	for _, panel := range scoped.panels {
		if seen[panel] {
			t.Fatal("a panel was planned twice")
		}
		seen[panel] = true
	}
	if len(seen) != 66 || scopedBase.reads != 1 || scopedBase.rows != 66 || scoped.rows != 66 || legacy.reads != 67 || legacy.rows != 66*67 {
		t.Fatal("membership did not retain one complete panel-union read and independent current panel reads")
	}
}

func TestUserRetirementsPanelReadErrorsKeepIndependentCommittedRemovals(t *testing.T) {
	base, nodes := panelScopeFixture(t)
	scoped := &countingPanelClientRepo{countingUserClientRepo: base, failPanel: 17}
	before, err := base.GetByID(t.Context(), 17)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := New(scoped).SyncUserRetirements(t.Context(), 42, "changed-user-uuid", rules, nodes[:65])
	if !errors.Is(err, domain.ErrUnavailable) || len(retired) != 1 || !retired[66].PanelRemoved || len(retired[66].Emails) != 1 {
		t.Fatal("one panel read error discarded an independent committed removal")
	}
	after, err := base.GetByID(t.Context(), 17)
	if err != nil || !reflect.DeepEqual(before, after) || len(base.inbounds[17]) != 1 || base.reads != 1 {
		t.Fatal("a failed current panel read wrote a plan or silently fell back to a wider read")
	}
	// A later pass must observe repaired current state, not a retained error or
	// an earlier union snapshot. It now applies the new credential on panel 17.
	scoped.failPanel = 0
	if _, err := New(scoped).SyncUserRetirements(t.Context(), 42, "changed-user-uuid", rules, nodes[:65]); err != nil {
		t.Fatal(err)
	}
	after, err = base.GetByID(t.Context(), 17)
	if err != nil || after.UUID == before.UUID || after.LastCounterEpoch != before.LastCounterEpoch || after.LifetimeTotalBytes != before.LifetimeTotalBytes {
		t.Fatal("recovered panel read failed to replan current credentials while preserving counters")
	}
}
