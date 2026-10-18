package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/vlessroute"
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

type detachPanelRepo struct{ ports.XUIPanelRepo }

func (detachPanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: 10, Kind: domain.PanelKind3XUI, URL: "https://offline.example"}, nil
}

type detachPanelClient struct {
	ports.PanelClient
	state       string
	statusErr   error
	statusReads int
}

func (c *detachPanelClient) GetServerStatus(context.Context) (*ports.ServerStatus, error) {
	c.statusReads++
	return &ports.ServerStatus{XrayState: c.state}, c.statusErr
}

func (*detachPanelClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return nil, domain.ErrNotFound
}

func (*detachPanelClient) ReadRouteTemplate(context.Context) (ports.RouteTemplate, error) {
	panic("unavailable backend must not read routing template")
}

func (*detachPanelClient) WriteRouteTemplate(context.Context, ports.RouteTemplate) error {
	panic("unavailable backend must not write routing template")
}

type detachPanelPool struct {
	ports.XUIPool
	client *detachPanelClient
}

func (p detachPanelPool) Get(int64) (ports.PanelClient, error) { return p.client, nil }

type detachRouteStore struct{ ports.NodeRouteStore }

func detachFixture() (*Service, *detachNodeRepo, *detachOwnershipCleaner) {
	repo := &detachNodeRepo{captureNodeRepo: &captureNodeRepo{node: &domain.Node{
		ID: 7, PanelID: 10, InboundID: 5, Enabled: true, DesiredProtocol: "vless", DesiredPort: 443,
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{{ID: "route", Route: 1, OutboundTag: "warp", Enabled: true}}},
	}}}
	cleaner := &detachOwnershipCleaner{}
	return &Service{nodes: repo, cleaner: cleaner}, repo, cleaner
}

func TestDetachWithRouteLinesSucceedsWhenPanelIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name, state string
		err         error
	}{
		{name: "offline", err: errors.New("panel unreachable")},
		{name: "core_stopped", state: "stopped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, repo, cleaner := detachFixture()
			client := &detachPanelClient{state: test.state, statusErr: test.err}
			routes := vlessroute.New(ports.Repos{Node: repo, NodeRoute: detachRouteStore{}, XUIPanel: detachPanelRepo{}}, detachPanelPool{client: client}, nil)
			svc.SetRouteCleaner(routes.RemoveForNode)
			invalidations := 0
			svc.SetSubscriptionInvalidator(func() { invalidations++ })
			if err := svc.DetachAndSync(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
			if client.statusReads != 1 || !cleaner.unclaimed || cleaner.panelID != 10 || cleaner.inboundID != 5 || !repo.deleted || invalidations != 1 {
				t.Fatalf("local detach incomplete: status_reads=%d ownership=%+v deleted=%v invalidations=%d", client.statusReads, cleaner, repo.deleted, invalidations)
			}
			if _, err := repo.GetByID(context.Background(), 7); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("node still exists: %v", err)
			}
		})
	}
}

func TestDetachStillStopsOnLocalOwnershipFailure(t *testing.T) {
	svc, repo, cleaner := detachFixture()
	want := errors.New("ownership database unavailable")
	cleaner.err = want
	svc.SetRouteCleaner(func(context.Context, int64) error { return domain.ErrUnavailable })
	if err := svc.DetachAndSync(context.Background(), 7); !errors.Is(err, want) {
		t.Fatalf("local failure swallowed: %v", err)
	}
	if repo.deleted {
		t.Fatal("node deleted before local ownership cleanup succeeded")
	}
}

func TestDestructiveNodeOperationsStillRequireRouteCleanup(t *testing.T) {
	for _, operation := range []string{"delete", "recreate", "port", "protocol"} {
		t.Run(operation, func(t *testing.T) {
			svc, repo, cleaner := detachFixture()
			svc.pool = detachPanelPool{client: &detachPanelClient{}}
			want, calls := errors.New("route cleanup unavailable"), 0
			svc.SetRouteCleaner(func(_ context.Context, id int64) error {
				if id != 7 {
					t.Fatalf("unexpected cleanup target: %d", id)
				}
				calls++
				return want
			})
			var err error
			switch operation {
			case "delete":
				err = svc.runNodeTask(context.Background(), &domain.SyncTask{Type: domain.SyncTaskNodeDelete, TargetID: 7})
			case "recreate":
				err = svc.RecreateInboundOnServer(context.Background(), 7)
			case "port":
				err = svc.UpdateInboundConfig(context.Background(), 7, ports.InboundSpec{Protocol: "vless", Port: 8443})
			case "protocol":
				err = svc.UpdateInboundConfig(context.Background(), 7, ports.InboundSpec{Protocol: "trojan", Port: 443})
			}
			if !errors.Is(err, want) || calls != 1 || repo.deleted || cleaner.unclaimed || repo.updateCfg != nil || repo.update != nil {
				t.Fatalf("cleanup failure bypassed for %s: err=%v calls=%d deleted=%v", operation, err, calls, repo.deleted)
			}
		})
	}
}
