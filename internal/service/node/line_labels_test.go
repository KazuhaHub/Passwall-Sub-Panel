package node

import (
	"context"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type labelNodeRepo struct {
	fakeNodeRepo
	stored  domain.Node
	written *domain.Node
}

func (r *labelNodeRepo) GetByID(context.Context, int64) (*domain.Node, error) {
	n := r.stored
	return &n, nil
}

func (r *labelNodeRepo) UpdateMetadata(_ context.Context, n *domain.Node) error {
	if err := domain.CheckRelayLabelsResolved(n.Relays); err != nil {
		return err
	}
	copy := *n
	r.written = &copy
	return nil
}

// ADR 0040: an omitted label keeps the saved line value even when the node's
// labels change in the same save; a new line takes the node's new value.
func TestUpdateMetadataResolvesOmittedRelayLabels(t *testing.T) {
	repo := &labelNodeRepo{stored: domain.Node{ID: 1, Region: "HK", Tags: []string{"old"},
		Relays: []domain.RelayLine{{ID: "a", Address: "a.example", Region: "JP", Tags: []string{"cmcc"}}}}}
	svc := &Service{nodes: repo}
	next := &domain.Node{ID: 1, Region: "US", Tags: []string{"edge"}, Relays: []domain.RelayLine{
		{ID: "a", Address: "a.example", RegionUnresolved: true, TagsUnresolved: true},
		{Address: "new.example", RegionUnresolved: true, TagsUnresolved: true},
	}}
	if err := svc.UpdateMetadata(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	got := repo.written.Relays
	if got[0].Region != "JP" || !reflect.DeepEqual(got[0].Tags, []string{"cmcc"}) {
		t.Fatalf("existing line followed the node: %+v", got[0])
	}
	if got[1].Region != "US" || !reflect.DeepEqual(got[1].Tags, []string{"edge"}) {
		t.Fatalf("new line did not take the node's saved value: %+v", got[1])
	}
}

// A legacy client that omits relay IDs is matched by endpoint; its omitted
// labels must still resolve against the matched saved line.
func TestUpdateMetadataResolvesLabelsAfterLegacyIDMatching(t *testing.T) {
	repo := &labelNodeRepo{stored: domain.Node{ID: 1, Region: "HK",
		Relays: []domain.RelayLine{{ID: "a", Address: "a.example", Port: 1, Region: "JP", Tags: []string{"cmcc"}}}}}
	svc := &Service{nodes: repo}
	next := &domain.Node{ID: 1, Region: "US", Relays: []domain.RelayLine{{Address: "a.example", Port: 1, RegionUnresolved: true, TagsUnresolved: true}}}
	if err := svc.UpdateMetadata(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if got := repo.written.Relays[0]; got.ID != "a" || got.Region != "JP" || !reflect.DeepEqual(got.Tags, []string{"cmcc"}) {
		t.Fatalf("legacy save cleared or replaced labels: %+v", got)
	}
}
