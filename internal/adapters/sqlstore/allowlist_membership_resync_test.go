package sqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/clientprov"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"gorm.io/gorm"
)

type removedPanelClient struct {
	ports.XUIClient
	err   error
	calls int
}

func (c *removedPanelClient) DelClientByEmail(context.Context, string) error { c.calls++; return c.err }

type removedPanelPool struct {
	ports.XUIPool
	client *removedPanelClient
}

func (p removedPanelPool) Get(id int64) (ports.XUIClient, error) {
	if id != 82 {
		return nil, domain.ErrUnavailable
	}
	return p.client, nil
}

type offlineDesiredPanel struct{}

func (offlineDesiredPanel) ProvisionUser(context.Context, int64) error       { return domain.ErrUnavailable }
func (offlineDesiredPanel) DeleteLegacyForUser(context.Context, int64) error { return nil }
func (offlineDesiredPanel) ReconcileOrphans(context.Context, int64) error    { return nil }
func (offlineDesiredPanel) DeleteSharedForUser(context.Context, int64) error { return nil }
func (offlineDesiredPanel) BulkProvisionNodeInbound(context.Context, *domain.Node, []int64) error {
	return nil
}

func TestAllowlistMembershipSQLRemovalSurvivesOtherPanelOutageAndRetries(t *testing.T) {
	db, agent, _, _, now := policyMintFixture(t)
	ctx := t.Context()
	repos := NewRepos(db)
	g := &domain.Group{ID: 8, Slug: "allowlist", Name: "allowlist", TagFilter: domain.TagFilter{All: true}}
	if err := repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xuiPanelRow{ID: 81, Name: "native", Kind: "psp", URL: "psp://agt_policy_mint"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xuiPanelRow{ID: 82, Name: "legacy", Kind: "3xui", URL: "https://legacy.invalid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateProtocolObservation(ctx, "agt_policy_mint", protocol.ProtocolVersion1, []string{protocol.CapabilityDestinationPolicy}, now); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{UPN: "allowlist@example.test", Email: "allowlist@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "allowlist@example.test", UUID: "88888888-8888-4888-8888-000000000001", SubToken: strings.Repeat("s", 32), GroupID: g.ID, Enabled: true}
	if err := repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	var nodes []*domain.Node
	for _, panelID := range []int64{81, 82} {
		n := &domain.Node{PanelID: panelID, InboundID: 1, DisplayName: "membership", DesiredProtocol: "vless", Enabled: true}
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	provisioner := clientprov.New(repos.PSPClient)
	if _, err := provisioner.SyncUserRetirements(ctx, u.ID, u.UUID, domain.EmailRules{Domain: "psp.local"}, nodes); err != nil {
		t.Fatal(err)
	}
	clients, err := repos.PSPClient.ListByUser(ctx, u.ID)
	if err != nil || len(clients) != 2 {
		t.Fatalf("initial memberships: %d / %v", len(clients), err)
	}
	var retiredID int64
	for _, c := range clients {
		if c.PanelID == 82 {
			retiredID = c.ID
		}
	}
	if retiredID == 0 {
		t.Fatal("missing legacy membership")
	}
	if err := db.Create(&destGroupModeRow{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}).Error; err != nil {
		t.Fatal(err)
	}
	selector := group.New(repos.Group, repos.Node, nil)
	selector.SetDestinationEligibilityRepo(repos.DestinationEligibility)
	deletionFailure := errors.New("legacy panel deletion unavailable")
	client := &removedPanelClient{err: deletionFailure}
	pool := removedPanelPool{client: client}
	s := user.New(repos.User, repos.Group, repos.Ownership, repos.SyncTask, selector, syncsvc.New(pool, repos.Ownership), pool, repos.ScopedSettings)
	s.SetPSPProvisioner(provisioner)
	s.SetSharedMigrator(offlineDesiredPanel{})
	if err := s.ResyncMembership(ctx, u.ID); !errors.Is(err, deletionFailure) || client.calls != 1 {
		t.Fatalf("removed panel coupled to native outage: calls=%d / %v", client.calls, err)
	}
	if inbounds, err := repos.PSPClient.ListInbounds(ctx, retiredID); err != nil || len(inbounds) != 0 {
		t.Fatalf("retired local attachments: %d / %v", len(inbounds), err)
	}
	if _, err := repos.PSPClient.GetByID(ctx, retiredID); err != nil {
		t.Fatalf("retirement erased retry/counter identity: %v", err)
	}
	if err := s.ResyncMembershipOrEnqueue(ctx, u.ID, "retry membership deletion"); err != nil {
		t.Fatal(err)
	}
	tasks, _, err := repos.SyncTask.List(ctx, ports.SyncTaskFilter{})
	if err != nil || len(tasks) != 1 || tasks[0].Type != domain.SyncTaskUserResync {
		t.Fatalf("deletion did not persist retry: tasks=%d / %v", len(tasks), err)
	}
	client.err = nil
	if err := s.ResyncMembership(ctx, u.ID); !errors.Is(err, domain.ErrUnavailable) || client.calls != 3 {
		t.Fatalf("retained empty row did not retry deletion despite native outage: calls=%d / %v", client.calls, err)
	}
	// The current eligibility read fails before client-plan writes or deletions.
	selector.InvalidateEligibility(0)
	readFailure := errors.New("eligibility database read failed")
	db.Callback().Query().Before("gorm:query").Register("failed-membership-eligibility", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_group_modes" {
			tx.AddError(readFailure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Query().Remove("failed-membership-eligibility") })
	if err := s.ResyncMembership(ctx, u.ID); !errors.Is(err, readFailure) || client.calls != 3 {
		t.Fatalf("read failure deleted membership: calls=%d / %v", client.calls, err)
	}
}
