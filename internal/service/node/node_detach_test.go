package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type detachNodeRepo struct {
	*captureNodeRepo
	deleted bool
}

func (r *detachNodeRepo) Delete(_ context.Context, id int64) error {
	if r.node == nil || r.node.ID != id {
		return domain.ErrNotFound
	}
	r.deleted, r.node = true, nil
	return nil
}

type detachOwnershipCleaner struct {
	InboundCleaner
	unclaimed bool
	err       error
	panelID   int64
	inboundID int
}

func (c *detachOwnershipCleaner) UnclaimAllForInbound(_ context.Context, panelID int64, inboundID int) error {
	if c.err != nil {
		return c.err
	}
	c.unclaimed, c.panelID, c.inboundID = true, panelID, inboundID
	return nil
}

func detachFixture() (*Service, *detachNodeRepo, *detachOwnershipCleaner) {
	repo := &detachNodeRepo{captureNodeRepo: &captureNodeRepo{node: &domain.Node{
		ID: 7, PanelID: 10, InboundID: 5, Enabled: true, DesiredProtocol: "vless", DesiredPort: 443,
	}}}
	cleaner := &detachOwnershipCleaner{}
	return &Service{nodes: repo, cleaner: cleaner}, repo, cleaner
}

func TestDetachDropsTheNodeLocally(t *testing.T) {
	svc, repo, cleaner := detachFixture()
	invalidations := 0
	svc.SetSubscriptionInvalidator(func() { invalidations++ })
	if err := svc.DetachAndSync(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if !cleaner.unclaimed || cleaner.panelID != 10 || cleaner.inboundID != 5 || !repo.deleted || invalidations != 1 {
		t.Fatalf("local detach incomplete: ownership=%+v deleted=%v invalidations=%d", cleaner, repo.deleted, invalidations)
	}
	if _, err := repo.GetByID(context.Background(), 7); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("node still exists: %v", err)
	}
}

func TestDetachStillStopsOnLocalOwnershipFailure(t *testing.T) {
	svc, repo, cleaner := detachFixture()
	want := errors.New("ownership database unavailable")
	cleaner.err = want
	if err := svc.DetachAndSync(context.Background(), 7); !errors.Is(err, want) {
		t.Fatalf("local failure swallowed: %v", err)
	}
	if repo.deleted {
		t.Fatal("node deleted before local ownership cleanup succeeded")
	}
}
