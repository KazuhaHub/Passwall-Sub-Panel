package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func milli(v int) *int { return &v }

// An editor that does not send factors keeps them; one that does sets them,
// within range.
func TestUpdateMetadataKeepsOmittedLineFactors(t *testing.T) {
	repo := &labelNodeRepo{stored: domain.Node{ID: 1, Region: "HK", TrafficFactorMilli: milli(2000),
		Relays: []domain.RelayLine{{ID: "a", Address: "a.example", Region: "HK", TrafficFactorMilli: milli(0)}}}}
	svc := &Service{nodes: repo}
	next := &domain.Node{ID: 1, Region: "HK", Relays: []domain.RelayLine{
		{ID: "a", Address: "a.example", Region: "HK"},
		{Address: "b.example", Region: "HK"},
	}}
	if err := svc.UpdateMetadata(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	got := repo.written
	if got.EffectiveTrafficFactor() != 2000 || got.Relays[0].EffectiveTrafficFactor() != 0 || got.Relays[1].EffectiveTrafficFactor() != 1000 {
		t.Fatalf("factors = %d / %d / %d", got.EffectiveTrafficFactor(), got.Relays[0].EffectiveTrafficFactor(), got.Relays[1].EffectiveTrafficFactor())
	}
	bad := &domain.Node{ID: 1, Region: "HK", TrafficFactorMilli: milli(100001)}
	if err := svc.UpdateMetadata(context.Background(), bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("out-of-range factor = %v", err)
	}
}

type factorNodes struct {
	fakeNodeRepo
	nodes   map[int64]*domain.Node
	written []*domain.Node
}

func (r *factorNodes) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copy := *n
	copy.Relays = append([]domain.RelayLine(nil), n.Relays...)
	return &copy, nil
}

func (r *factorNodes) UpdateMetadata(_ context.Context, n *domain.Node) error {
	copy := *n
	r.written = append(r.written, &copy)
	return nil
}

func TestSetLineFactors(t *testing.T) {
	repo := &factorNodes{nodes: map[int64]*domain.Node{
		1: {ID: 1, PanelID: 10, DisplayName: "hk", Relays: []domain.RelayLine{{ID: "r", TrafficFactorMilli: milli(500)}}},
		2: {ID: 2, PanelID: 11, DisplayName: "jp"},
	}}
	svc := &Service{nodes: repo}
	ctx := context.Background()

	applied, err := svc.SetLineFactors(ctx, 10, []LineFactorChange{
		{Line: domain.DirectLine(1), FactorMilli: 1500},
		{Line: domain.LineKey{NodeID: 1, LineID: "r"}, FactorMilli: 500}, // unchanged: no audit entry
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0].From != 1000 || applied[0].To != 1500 || len(repo.written) != 1 ||
		repo.written[0].EffectiveTrafficFactor() != 1500 || repo.written[0].Relays[0].EffectiveTrafficFactor() != 500 {
		t.Fatalf("applied %+v written %+v", applied, repo.written)
	}
	for name, changes := range map[string][]LineFactorChange{
		"another server's node": {{Line: domain.DirectLine(2), FactorMilli: 0}},
		"unknown line":          {{Line: domain.LineKey{NodeID: 1, LineID: "nope"}, FactorMilli: 0}},
		"listed twice":          {{Line: domain.DirectLine(1), FactorMilli: 0}, {Line: domain.DirectLine(1), FactorMilli: 0}},
		"out of range":          {{Line: domain.DirectLine(1), FactorMilli: -1}},
	} {
		repo.written = nil
		if _, err := svc.SetLineFactors(ctx, 10, changes); !errors.Is(err, domain.ErrValidation) || len(repo.written) != 0 {
			t.Errorf("%s: err %v, written %d — want a validation error and nothing written", name, err, len(repo.written))
		}
	}
}

func TestChangedLineFactors(t *testing.T) {
	before := &domain.Node{ID: 1, Relays: []domain.RelayLine{{ID: "a"}, {ID: "b", TrafficFactorMilli: milli(2000)}}}
	after := &domain.Node{ID: 1, TrafficFactorMilli: milli(0), Relays: []domain.RelayLine{{ID: "a"}, {ID: "b", TrafficFactorMilli: milli(2000)}, {ID: "c", TrafficFactorMilli: milli(3000)}}}
	got := ChangedLineFactors(before, after)
	if len(got) != 2 || got[0].Line != domain.DirectLine(1) || got[0].To != 0 || got[1].Line.LineID != "c" || got[1].From != 1000 {
		t.Fatalf("changes = %+v", got)
	}
}

type failingFactorNodes struct {
	factorNodes
	failOn int64
}

func (r *failingFactorNodes) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	if n.ID == r.failOn && n.EffectiveTrafficFactor() == 0 {
		return errors.New("db down")
	}
	return r.factorNodes.UpdateMetadata(ctx, n)
}

// A failed write puts the nodes already written back: all or nothing.
func TestSetLineFactorsRollsBackOnFailure(t *testing.T) {
	repo := &failingFactorNodes{factorNodes: factorNodes{nodes: map[int64]*domain.Node{
		1: {ID: 1, PanelID: 10}, 2: {ID: 2, PanelID: 10},
	}}, failOn: 2}
	_, err := (&Service{nodes: repo}).SetLineFactors(context.Background(), 10, []LineFactorChange{
		{Line: domain.DirectLine(1), FactorMilli: 0}, {Line: domain.DirectLine(2), FactorMilli: 0},
	})
	if err == nil {
		t.Fatal("failure not reported")
	}
	last := repo.written[len(repo.written)-1]
	if last.ID != 1 || last.EffectiveTrafficFactor() != 1000 {
		t.Fatalf("node 1 not rolled back: %+v", repo.written)
	}
}
