package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type retirementProvisioner struct {
	retired domain.SharedClientRetirements
	err     error
	calls   int
}

func (p *retirementProvisioner) SyncUserRetirements(context.Context, int64, string, domain.EmailRules, []*domain.Node) (domain.SharedClientRetirements, error) {
	p.calls++
	return p.retired, p.err
}

type retirementMigrator struct {
	resyncMigrator
	err error
}

func (m *retirementMigrator) ProvisionUser(context.Context, int64) error { return m.err }

type retirementClient struct {
	ports.XUIClient
	err     error
	deleted []string
}

func (c *retirementClient) DelClientByEmail(_ context.Context, email string) error {
	c.deleted = append(c.deleted, email)
	return c.err
}

type retirementPool struct {
	ports.XUIPool
	clients map[int64]*retirementClient
}

func (p retirementPool) Get(id int64) (ports.XUIClient, error) {
	if c := p.clients[id]; c != nil {
		return c, nil
	}
	return nil, domain.ErrNotFound
}

type retirementSelector struct {
	nodes []*domain.Node
	err   error
}

func (s retirementSelector) NodesFor(context.Context, *domain.Group) ([]*domain.Node, error) {
	return s.nodes, s.err
}

func TestAllowlistResyncRetiresRemovedPanelIndependentlyAndRetriesDeletion(t *testing.T) {
	otherPanelDown := errors.New("native desired panel offline")
	deleteFailed := errors.New("retired panel deletion failed")
	readFailed := errors.New("eligibility read failed")
	for _, tc := range []struct {
		name                                            string
		provisionErr, retireErr, selectorErr, deleteErr error
		lifeFails                                       bool
		wantRemoved, wantReplaced                       int
		wantErr                                         error
	}{
		{name: "other native panel offline", provisionErr: otherPanelDown, wantRemoved: 1, wantErr: otherPanelDown},
		{name: "other lifecycle push fails", lifeFails: true, wantRemoved: 1},
		{name: "partial local write failure", retireErr: otherPanelDown, wantRemoved: 1, wantErr: otherPanelDown},
		{name: "replacement ready", wantRemoved: 1, wantReplaced: 1},
		{name: "deletion error surfaces", deleteErr: deleteFailed, wantRemoved: 1, wantReplaced: 1, wantErr: deleteFailed},
		{name: "deletion error joins other failure", provisionErr: otherPanelDown, deleteErr: deleteFailed, wantRemoved: 1, wantErr: deleteFailed},
		{name: "eligibility failure deletes nothing", selectorErr: readFailed, wantErr: readFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed, replaced := &retirementClient{err: tc.deleteErr}, &retirementClient{}
			psp := &retirementProvisioner{retired: domain.SharedClientRetirements{81: {Emails: []string{"replacement-old"}}, 82: {Emails: []string{"removed-old"}, PanelRemoved: true}}, err: tc.retireErr}
			s := &Service{
				users:    &memoryUserRepo{byID: map[int64]*domain.User{7: {ID: 7, GroupID: 1, Enabled: true}}},
				groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
				selector: retirementSelector{nodes: []*domain.Node{{ID: 10, PanelID: 81, DesiredProtocol: "vless"}}, err: tc.selectorErr},
				settings: bfSettings{},
				pool:     retirementPool{clients: map[int64]*retirementClient{81: replaced, 82: removed}},
			}
			s.SetPSPProvisioner(psp)
			s.SetSharedMigrator(&retirementMigrator{err: tc.provisionErr})
			s.SetSharedLifecycleSyncer(&failingSharedLife{fail: tc.lifeFails})
			err := s.ResyncMembership(t.Context(), 7)
			if len(removed.deleted) != tc.wantRemoved || len(replaced.deleted) != tc.wantReplaced {
				t.Fatalf("retirement coupled to unrelated writes: removed=%v replaced=%v", removed.deleted, replaced.deleted)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) || tc.lifeFails && err == nil || tc.wantErr == nil && !tc.lifeFails && err != nil {
				t.Fatalf("retirement failure did not reach retry caller: %v", err)
			}
			if tc.selectorErr != nil && psp.calls != 0 {
				t.Fatal("eligibility read failure mutated client plans")
			}
		})
	}
}
