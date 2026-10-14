package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fleetInputMembers struct {
	ports.UserMembershipRepo
	calls      int
	groupCalls int
	group      int64
}

func (m *fleetInputMembers) GroupIDsByIDs(_ context.Context, ids []int64) (map[int64]int64, error) {
	m.calls++
	groups := make(map[int64]int64, len(ids))
	for _, id := range ids {
		groups[id] = m.group
	}
	return groups, nil
}

func (m *fleetInputMembers) TagMatchedMembers(context.Context, int64) (map[int64][]int64, error) {
	m.groupCalls++
	ids := make([]int64, 200)
	for n := range ids {
		ids[n] = int64(n + 1)
	}
	return map[int64][]int64{m.group: ids}, nil
}

func TestMembershipInputCacheKeeps65Node200UserFleetWarm(t *testing.T) {
	inputs, _, _, panels, _ := inputCacheFixture(t)
	members := &fleetInputMembers{group: 8}
	inputs.users = members
	inputs.groups = members
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	snapshot := &ports.NativeDesiredSnapshot{}
	for id := int64(1); id <= 200; id++ {
		snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: &domain.PSPClient{UserID: id}})
	}
	for epoch := range 2 {
		for round := range 3 {
			// Collection settings remain live even when membership stays warm.
			panels.revision++
			for panel := int64(1); panel <= 65; panel++ {
				roster, quota, err := inputs.ForNode(t.Context(), panel, snapshot, true)
				if err != nil || len(roster.UserIDs) != 200 || len(roster.UserGroups) != 200 || roster.UserGroups[200] != members.group || len(quota.UserIDs) != 200 || len(quota.UserGroups) != 200 || quota.UserGroups[2] != members.group || roster.CollectRevision != panels.revision || quota.CollectRevision != panels.revision {
					t.Fatalf("fleet membership epoch=%d round=%d panel=%d: %v", epoch, round, panel, err)
				}
				roster.UserGroups[200] = -1
				roster.UserIDs[0] = -1
				quota.UserGroups[2] = -1
			}
		}
		want := 65 * (epoch + 1)
		if members.calls != want || members.groupCalls != want {
			t.Fatalf("stable fleet repeatedly loaded membership: user=%d group=%d want=%d", members.calls, members.groupCalls, want)
		}
		members.group++
		generation.Add(1)
	}
}

type fleetCacheRuntime struct {
	ports.DestAgentPolicyRepo
	owners map[string]*cacheCompilerRuntime
}

func (r *fleetCacheRuntime) Update(ctx context.Context, agent string, now time.Time, fn func(*domain.DestAgentPolicy, func() error) (bool, error)) (bool, error) {
	return r.owners[agent].Update(ctx, agent, now, fn)
}

func TestCompilerResultCacheKeeps65IndependentNodesWarmAndChecksEveryOwner(t *testing.T) {
	c, inputs, _, _, snapshot, base := cachedCompilerFixture(t)
	runtime := &fleetCacheRuntime{owners: map[string]*cacheCompilerRuntime{}}
	c.runtime = runtime
	prepares, selects := 0, 0
	c.prepare = func(d domain.DestDefinitions, r, q RosterInput, b protocol.ConfigBody) (*PreparedCandidate, error) {
		prepares++
		return PrepareCandidate(d, r, q, b)
	}
	c.selectCandidate = func(p *PreparedCandidate, state *domain.DestAgentPolicy) (ports.DestPolicyCandidate, bool, error) {
		selects++
		return SelectCandidate(p, state)
	}
	for panel := int64(1); panel <= 65; panel++ {
		runtime.owners[fmt.Sprint(panel)] = &cacheCompilerRuntime{}
	}
	for epoch := range 2 {
		for round := range 3 {
			for panel := int64(1); panel <= 65; panel++ {
				agent := &domain.NodeAgent{AgentID: fmt.Sprint(panel), PanelID: panel}
				candidate, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
				if err != nil || candidate.Policy == nil || candidate.Policy.Rules[0].Ports != "443" {
					t.Fatalf("fleet compile epoch=%d round=%d panel=%d: %v", epoch, round, panel, err)
				}
				candidate.Policy.Rules[0].Ports = "80"
			}
		}
		want := 65 * (epoch + 1)
		if prepares != want || selects != want {
			t.Fatalf("stable fleet rebuilt: prepares=%d selects=%d want=%d", prepares, selects, want)
		}
		for agent, owner := range runtime.owners {
			if owner.calls != 3*(epoch+1) {
				t.Fatalf("cached compilation skipped owner %s runtime: %d", agent, owner.calls)
			}
		}
		if epoch == 0 {
			inputs.generation++
		}
	}
	runtime.owners["1"].fail = true
	if _, err := c.Compile(t.Context(), &domain.NodeAgent{AgentID: "1", PanelID: 1}, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("warm fleet hid owner persistence failure: %v", err)
	}
}
