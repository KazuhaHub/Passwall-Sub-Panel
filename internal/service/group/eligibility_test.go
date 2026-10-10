package group

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type eligibilityFacts struct {
	mu                    sync.Mutex
	mode                  string
	panels                map[int64]ports.DestinationPanelEligibility
	modeCalls, panelCalls int
	err                   error
	panelLoad             func(context.Context, int64, ports.DestinationPanelEligibility) (ports.DestinationPanelEligibility, error)
}

func (r *eligibilityFacts) GroupEligibilityMode(context.Context, int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modeCalls++
	return r.mode, r.err
}
func (r *eligibilityFacts) PanelDestinationEligibility(ctx context.Context, id int64) (ports.DestinationPanelEligibility, error) {
	r.mu.Lock()
	r.panelCalls++
	value, err, load := r.panels[id], r.err, r.panelLoad
	r.mu.Unlock()
	if err != nil {
		return value, err
	}
	if load != nil {
		return load(ctx, id, value)
	}
	return value, nil
}

func TestEligibleEnforcesDestinationFactsAndKeepsLegacyMatching(t *testing.T) {
	facts := &eligibilityFacts{mode: "allowlist", panels: map[int64]ports.DestinationPanelEligibility{
		81: {Native: true, PolicyCapable: true}, 82: {PolicyCapable: true}, 83: {Native: true},
		84: {Native: true, PolicyCapable: true, FallbackBlocked: true},
	}}
	s := New(nil, nil, nil)
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	node := &domain.Node{PanelID: 81}
	if ok, err := s.Eligible(t.Context(), node, g); err != nil || !ok {
		t.Fatalf("nil destination dependency changed legacy matching: %t / %v", ok, err)
	}
	s.SetDestinationEligibilityRepo(facts)
	for _, id := range []int64{81, 82, 83, 84} {
		node.PanelID = id
		ok, err := s.Eligible(t.Context(), node, g)
		if err != nil || ok != (id == 81) {
			t.Fatalf("panel %d eligibility: %t / %v", id, ok, err)
		}
	}
	facts.mode = "open"
	s.InvalidateEligibility(0)
	if ok, err := s.Eligible(t.Context(), node, g); err != nil || !ok {
		t.Fatalf("open mode no longer matches: %t / %v", ok, err)
	}
	g.TagFilter = domain.TagFilter{Tags: []string{"region:TW"}}
	if ok, err := s.Eligible(t.Context(), node, g); err != nil || ok {
		t.Fatalf("tag mismatch became eligible: %t / %v", ok, err)
	}
}

func TestNodesForFiltersAllShortcutAndCachesModeAndPanelFacts(t *testing.T) {
	facts := &eligibilityFacts{mode: "allowlist", panels: map[int64]ports.DestinationPanelEligibility{81: {Native: true, PolicyCapable: true}, 82: {Native: true, PolicyCapable: true, FallbackBlocked: true}}}
	nodes := destMemberNodes{nodes: []*domain.Node{{ID: 1, PanelID: 81}, {ID: 2, PanelID: 82}, {ID: 3, PanelID: 81}}}
	s := New(nil, nodes, nil)
	s.SetDestinationEligibilityRepo(facts)
	now := time.Unix(1791000000, 0)
	s.now = func() time.Time { return now }
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	for range 2 {
		got, err := s.NodesFor(t.Context(), g)
		if err != nil || !reflect.DeepEqual(got, []*domain.Node{nodes.nodes[0], nodes.nodes[2]}) {
			t.Fatalf("all shortcut bypassed enforcement: %+v / %v", got, err)
		}
	}
	if facts.modeCalls != 1 || facts.panelCalls != 2 {
		t.Fatalf("hot selection reread facts: modes=%d panels=%d", facts.modeCalls, facts.panelCalls)
	}
	facts.panels[81] = ports.DestinationPanelEligibility{Native: true, PolicyCapable: true, FallbackBlocked: true}
	s.InvalidateEligibility(81)
	got, err := s.NodesFor(t.Context(), g)
	if err != nil || len(got) != 0 || facts.modeCalls != 1 || facts.panelCalls != 3 {
		t.Fatalf("targeted invalidation: nodes=%d modes=%d panels=%d / %v", len(got), facts.modeCalls, facts.panelCalls, err)
	}
	now = now.Add(31 * time.Second)
	if _, err := s.NodesFor(t.Context(), g); err != nil || facts.modeCalls != 2 || facts.panelCalls != 5 {
		t.Fatalf("30-second expiry: modes=%d panels=%d / %v", facts.modeCalls, facts.panelCalls, err)
	}
}

func TestEligibilityReadErrorsAreNotVerdictsOrCached(t *testing.T) {
	failure := errors.New("destination facts unavailable")
	facts := &eligibilityFacts{mode: "allowlist", panels: map[int64]ports.DestinationPanelEligibility{81: {Native: true, PolicyCapable: true}}, err: failure}
	s := New(nil, destMemberNodes{nodes: []*domain.Node{{PanelID: 81}}}, nil)
	s.SetDestinationEligibilityRepo(facts)
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	if nodes, err := s.NodesFor(t.Context(), g); !errors.Is(err, failure) || nodes != nil {
		t.Fatalf("failed read became a partial membership verdict: %+v / %v", nodes, err)
	}
	facts.err = nil
	if nodes, err := s.NodesFor(t.Context(), g); err != nil || len(nodes) != 1 {
		t.Fatalf("read failure poisoned cache: %+v / %v", nodes, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Eligible(ctx, &domain.Node{PanelID: 81}, g); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request reused eligibility: %v", err)
	}
}

func TestEligibilityInvalidationDiscardsInFlightPanelFacts(t *testing.T) {
	facts := &eligibilityFacts{mode: "allowlist", panels: map[int64]ports.DestinationPanelEligibility{81: {Native: true, PolicyCapable: true}}}
	started, release := make(chan struct{}), make(chan struct{})
	first := true
	facts.panelLoad = func(ctx context.Context, _ int64, value ports.DestinationPanelEligibility) (ports.DestinationPanelEligibility, error) {
		if first {
			first = false
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return value, ctx.Err()
			}
		}
		return value, nil
	}
	s := New(nil, nil, nil)
	s.SetDestinationEligibilityRepo(facts)
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	done := make(chan bool, 1)
	go func() {
		ok, err := s.Eligible(t.Context(), &domain.Node{PanelID: 81}, g)
		if err != nil {
			t.Errorf("invalidated read: %v", err)
		}
		done <- ok
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("eligibility did not read panel facts")
	}
	facts.mu.Lock()
	facts.panels[81] = ports.DestinationPanelEligibility{Native: true, PolicyCapable: true, FallbackBlocked: true}
	facts.mu.Unlock()
	s.InvalidateEligibility(81)
	close(release)
	if <-done {
		t.Fatal("in-flight pre-invalidation facts admitted the node")
	}
	if facts.panelCalls != 2 {
		t.Fatalf("invalidation did not read current facts: %d", facts.panelCalls)
	}
}

func TestNodesForPanelReadFailureNeverReturnsPartialMembership(t *testing.T) {
	facts := &eligibilityFacts{mode: "allowlist", panels: map[int64]ports.DestinationPanelEligibility{81: {Native: true, PolicyCapable: true}, 82: {Native: true, PolicyCapable: true}}}
	failure := errors.New("second panel unavailable")
	facts.panelLoad = func(_ context.Context, id int64, value ports.DestinationPanelEligibility) (ports.DestinationPanelEligibility, error) {
		if id == 82 {
			return value, failure
		}
		return value, nil
	}
	s := New(nil, destMemberNodes{nodes: []*domain.Node{{ID: 1, PanelID: 81}, {ID: 2, PanelID: 82}}}, nil)
	s.SetDestinationEligibilityRepo(facts)
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	if nodes, err := s.NodesFor(t.Context(), g); nodes != nil || !errors.Is(err, failure) {
		t.Fatalf("partial membership returned: %+v / %v", nodes, err)
	}
	facts.panelLoad = nil
	if nodes, err := s.NodesFor(t.Context(), g); err != nil || len(nodes) != 2 || facts.modeCalls != 1 || facts.panelCalls != 3 {
		t.Fatalf("panel failure retained or unrelated facts lost: nodes=%d mode=%d panel=%d / %v", len(nodes), facts.modeCalls, facts.panelCalls, err)
	}
}

func TestEligibilitySharesConcurrentColdLoadsAndRejectsCorruptModes(t *testing.T) {
	facts := &eligibilityFacts{mode: "broken", panels: map[int64]ports.DestinationPanelEligibility{81: {Native: true, PolicyCapable: true}}}
	s := New(nil, nil, nil)
	s.SetDestinationEligibilityRepo(facts)
	node, g := &domain.Node{PanelID: 81}, &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	if _, err := s.Eligible(t.Context(), node, g); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("corrupt mode admitted: %v", err)
	}
	facts.mode = "allowlist"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if ok, err := s.Eligible(t.Context(), node, g); err != nil || !ok {
				t.Errorf("concurrent facts: %t / %v", ok, err)
			}
		})
	}
	wg.Wait()
	if facts.modeCalls != 2 || facts.panelCalls != 1 {
		t.Fatalf("corrupt mode cached or duplicate cold loads: modes=%d panels=%d", facts.modeCalls, facts.panelCalls)
	}
}
