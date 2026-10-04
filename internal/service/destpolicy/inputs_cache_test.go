package destpolicy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type cachedInputPanels struct {
	mode     domain.AuditCollect
	revision uint64
}

func (p *cachedInputPanels) GetAuditSettings(context.Context, int64) (ports.PanelAuditSettings, error) {
	return ports.PanelAuditSettings{Collect: p.mode, Revision: p.revision}, nil
}

type cachedInputMembers struct {
	ports.UserMembershipRepo
	calls     atomic.Int64
	group     atomic.Int64
	afterRead func()
	err       error
}

func (m *cachedInputMembers) GroupIDsByIDs(context.Context, []int64) (map[int64]int64, error) {
	m.calls.Add(1)
	group := m.group.Load()
	if m.afterRead != nil {
		m.afterRead()
	}
	return map[int64]int64{2: group}, m.err
}

type cachedInputGroups struct{ calls atomic.Int64 }

func (g *cachedInputGroups) TagMatchedMembers(context.Context, int64) (map[int64][]int64, error) {
	g.calls.Add(1)
	return map[int64][]int64{8: {2, 3, 4}}, nil
}
func inputCacheFixture(t *testing.T) (*Inputs, *cachedInputMembers, *cachedInputGroups, *cachedInputPanels, *ports.NativeDesiredSnapshot) {
	t.Helper()
	members, groups := &cachedInputMembers{}, &cachedInputGroups{}
	members.group.Store(8)
	panels := &cachedInputPanels{mode: domain.AuditCollectHits, revision: 1}
	inputs, err := NewInputs(panels, members, groups)
	if err != nil {
		t.Fatal(err)
	}
	return inputs, members, groups, panels, &ports.NativeDesiredSnapshot{Clients: []ports.NativeDesiredClient{{Client: &domain.PSPClient{UserID: 2}}}}
}

func TestMembershipInputCacheTracksGenerationRosterAndLiveCollection(t *testing.T) {
	inputs, members, groups, panels, snapshot := inputCacheFixture(t)
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	roster, quota, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	roster.UserGroups[2], quota.UserGroups[3] = 999, 999
	quota.UserIDs[0] = 999
	panels.mode, panels.revision = domain.AuditCollectOff, 2
	again, full, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if err != nil || members.calls.Load() != 1 || groups.calls.Load() != 1 || again.UserGroups[2] != 8 || full.UserGroups[3] != 8 || full.UserIDs[0] != 2 || again.Collect != domain.AuditCollectOff || full.CollectRevision != 2 {
		t.Fatalf("idle cache/control/copy: roster=%+v quota=%+v queries=%d/%d / %v", again, full, members.calls.Load(), groups.calls.Load(), err)
	}
	members.group.Store(9)
	generation.Add(1)
	changed, _, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if err != nil || changed.UserGroups[2] != 9 || members.calls.Load() != 2 || groups.calls.Load() != 2 {
		t.Fatalf("same-roster group move not visible: %+v / %v", changed, err)
	}
	snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: &domain.PSPClient{UserID: 5}})
	if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, true); err != nil || members.calls.Load() != 3 {
		t.Fatalf("roster change reused stale input: %v", err)
	}
	if _, _, err := inputs.ForNode(t.Context(), 82, snapshot, true); err != nil || groups.calls.Load() != 4 {
		t.Fatalf("panel scope leaked: %v", err)
	}
}

func TestMembershipInputCacheRetriesInterleavedChangeAndDoesNotCacheErrors(t *testing.T) {
	inputs, members, groups, _, snapshot := inputCacheFixture(t)
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	members.afterRead = func() {
		if members.calls.Load() == 1 {
			members.group.Store(9)
			generation.Add(1)
		}
	}
	roster, _, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if err != nil || roster.UserGroups[2] != 9 || members.calls.Load() != 2 {
		t.Fatalf("interleaved stale mapping escaped: %+v queries=%d / %v", roster, members.calls.Load(), err)
	}
	members.afterRead = nil
	generation.Add(1)
	members.err = domain.ErrUnavailable
	if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, true); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("load failure hidden: %v", err)
	}
	members.err = nil
	if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, true); err != nil || members.calls.Load() != 4 || groups.calls.Load() != 3 {
		t.Fatalf("failure poisoned cache: %v", err)
	}
}

func TestMembershipInputCacheColdLoadSharedAndNilClockReadsFresh(t *testing.T) {
	inputs, members, groups, _, snapshot := inputCacheFixture(t)
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if members.calls.Load() != 1 || groups.calls.Load() != 1 {
		t.Fatalf("cold membership loads duplicated: %d/%d", members.calls.Load(), groups.calls.Load())
	}
	inputs.SetMembershipGeneration(nil)
	for range 2 {
		if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, true); err != nil {
			t.Fatal(err)
		}
	}
	if members.calls.Load() != 3 || groups.calls.Load() != 3 {
		t.Fatalf("nil clock reused cached memberships: %d/%d", members.calls.Load(), groups.calls.Load())
	}
}

func TestMembershipInputCacheBoundsEvictionAndPreservesOversizedInputs(t *testing.T) {
	inputs, members, _, _, snapshot := inputCacheFixture(t)
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	for panel := int64(1); panel <= membershipCacheEntries+1; panel++ {
		if _, _, err := inputs.ForNode(t.Context(), panel, snapshot, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(inputs.membershipCache.items) != membershipCacheEntries || inputs.membershipCache.weight > membershipCacheBudget {
		t.Fatal("membership cache grew beyond its budget")
	}
	if _, _, err := inputs.ForNode(t.Context(), 1, snapshot, true); err != nil || members.calls.Load() != membershipCacheEntries+2 {
		t.Fatalf("evicted panel did not reload: queries=%d / %v", members.calls.Load(), err)
	}
	large := membershipInputs{quota: RosterInput{UserGroups: map[int64]int64{}}}
	for id := int64(1); id <= membershipCacheBudget/64+1; id++ {
		large.quota.UserGroups[id] = 8
	}
	cache := newMembershipInputCache()
	cache.put("too-large", large)
	if _, found := cache.get("too-large"); found || len(large.quota.UserGroups) != membershipCacheBudget/64+1 {
		t.Fatal("oversized memberships were retained or truncated")
	}
}

func TestMembershipInputCacheCancellationAndUnsettledChangesReturnNoInputs(t *testing.T) {
	inputs, members, groups, _, snapshot := inputCacheFixture(t)
	var generation atomic.Uint64
	inputs.SetMembershipGeneration(generation.Load)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := inputs.ForNode(ctx, 81, snapshot, true); !errors.Is(err, context.Canceled) || members.calls.Load() != 0 || groups.calls.Load() != 0 {
		t.Fatalf("canceled input read: %v", err)
	}
	members.afterRead = func() { generation.Add(1) }
	roster, quota, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if !errors.Is(err, domain.ErrUnavailable) || len(roster.UserIDs) != 0 || len(quota.UserIDs) != 0 || members.calls.Load() != 3 || len(inputs.membershipCache.items) != 0 {
		t.Fatalf("unsettled inputs escaped or retried indefinitely: roster=%+v quota=%+v queries=%d / %v", roster, quota, members.calls.Load(), err)
	}
}
