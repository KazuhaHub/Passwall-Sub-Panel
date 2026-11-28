package nodesync

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// batchReadUsers is a user repository that counts how it is read: by one
// account at a time, or by a list of accounts.
type batchReadUsers struct {
	ports.UserRepo
	byID       map[int64]*domain.User
	pointReads int
	listReads  [][]int64
}

func (r *batchReadUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	r.pointReads++
	if user := r.byID[id]; user != nil {
		return user, nil
	}
	return nil, domain.ErrNotFound
}

// ListByIDs answers like the real repository: the accounts that exist, in id
// order, a missing one simply absent.
func (r *batchReadUsers) ListByIDs(_ context.Context, ids []int64) ([]*domain.User, error) {
	r.listReads = append(r.listReads, slices.Clone(ids))
	var out []*domain.User
	for _, id := range slices.Compact(slices.Sorted(slices.Values(ids))) {
		if user := r.byID[id]; user != nil {
			out = append(out, user)
		}
	}
	return out, nil
}

type directiveClientList struct {
	ports.PSPClientRepo
	clients []*domain.PSPClient
}

func (r directiveClientList) ListAll(context.Context) ([]*domain.PSPClient, error) {
	return r.clients, nil
}

func directiveUserFixture(users *batchReadUsers, clients []*domain.PSPClient) (*Service, *domain.NodeAgent, *ports.NativeDesiredSnapshot) {
	agent := &domain.NodeAgent{AgentID: "agt_users", PanelID: 9}
	snapshot := &ports.NativeDesiredSnapshot{}
	for _, client := range clients {
		snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: client})
	}
	return &Service{
		users:    users,
		clients:  directiveClientList{clients: clients},
		agents:   emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{},
		reports:  make(map[string]receivedFullReport),
		anchors:  make(map[int64]nodeprotocol.ClientCounters),
		grants:   make(map[string]map[nodeprotocol.ClientKey]int64),
	}, agent, snapshot
}

// The accounts of a roster are read in one list, not one statement each: a
// sync of a server with U accounts used to cost U point reads every poll.
func TestBuildDirectivesReadsRosterUsersInOneList(t *testing.T) {
	users := &batchReadUsers{byID: map[int64]*domain.User{
		5: {ID: 5, IPLimit: 2},
		7: {ID: 7, IPLimit: 3, TrafficLimitBytes: 1 << 30},
	}}
	// Snapshot order is not user order, and one account holds two clients.
	service, agent, snapshot := directiveUserFixture(users, []*domain.PSPClient{
		{ID: 11, UserID: 7, PanelID: 9},
		{ID: 12, UserID: 5, PanelID: 9},
		{ID: 13, UserID: 7, PanelID: 9},
	})
	body, _, err := service.buildDirectives(t.Context(), agent, snapshot,
		nodeprotocol.NodeReport{}, nodeprotocol.Version{Epoch: 1, Version: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if users.pointReads != 0 {
		t.Fatalf("point reads = %d, want none", users.pointReads)
	}
	if len(users.listReads) != 1 || !slices.Equal(slices.Sorted(slices.Values(users.listReads[0])), []int64{5, 7}) {
		t.Fatalf("list reads = %v, want one read of accounts 5 and 7", users.listReads)
	}
	// What is sent is what the point reads produced: a quota entry per client
	// in roster order, and the IP limits in account order.
	var quota []nodeprotocol.ClientKey
	for _, entry := range body.Quota {
		quota = append(quota, entry.Client)
	}
	if want := []nodeprotocol.ClientKey{nodeprotocol.NewClientKey(11), nodeprotocol.NewClientKey(12), nodeprotocol.NewClientKey(13)}; !slices.Equal(quota, want) {
		t.Fatalf("quota order = %v, want %v", quota, want)
	}
	if body.Quota[0].HeadroomBytes == nil || body.Quota[1].HeadroomBytes != nil || body.Quota[2].HeadroomBytes == nil {
		t.Fatalf("limited account 7 must be gated and unlimited account 5 must not: %+v", body.Quota)
	}
	want := []nodeprotocol.IPShadowEntry{
		{Subject: nodeprotocol.NewSubjectKey(5), IPLimit: 2},
		{Subject: nodeprotocol.NewSubjectKey(7), IPLimit: 3},
	}
	if !slices.Equal(body.IPShadow, want) {
		t.Fatalf("ip shadow = %+v, want %+v", body.IPShadow, want)
	}
}

// A list read leaves a deleted account out instead of failing. The directives
// must still fail as the point read did: quota built without the account would
// send its client no gate at all.
func TestBuildDirectivesFailsOnRosterUserThatNoLongerExists(t *testing.T) {
	users := &batchReadUsers{byID: map[int64]*domain.User{5: {ID: 5}}}
	service, agent, snapshot := directiveUserFixture(users, []*domain.PSPClient{
		{ID: 12, UserID: 5, PanelID: 9},
		{ID: 11, UserID: 8, PanelID: 9},
	})
	body, _, err := service.buildDirectives(t.Context(), agent, snapshot,
		nodeprotocol.NodeReport{}, nodeprotocol.Version{Epoch: 1, Version: 1}, time.Now())
	if !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "load user 8") {
		t.Fatalf("error = %v, want not-found naming user 8", err)
	}
	if len(body.Quota) != 0 {
		t.Fatalf("a missing account emitted quota directives: %+v", body.Quota)
	}
}
