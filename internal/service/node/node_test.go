package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeNodeRepo satisfies ports.NodeRepo for Reorder tests. Only the
// BatchUpdateSortOrder hook is meaningful — all other methods are stubs.
type fakeNodeRepo struct {
	got []ports.NodeSortUpdate
	err error
}

func (r *fakeNodeRepo) Create(ctx context.Context, n *domain.Node) error { return nil }
func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.Node) error { return nil }
func (r *fakeNodeRepo) UpdatePanelName(ctx context.Context, panelID int64, panelName string) error {
	return nil
}
func (r *fakeNodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	if r.err != nil {
		return r.err
	}
	r.got = append([]ports.NodeSortUpdate(nil), updates...)
	return nil
}
func (r *fakeNodeRepo) Delete(ctx context.Context, id int64) error { return nil }
func (r *fakeNodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeNodeRepo) GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeNodeRepo) List(ctx context.Context) ([]*domain.Node, error)        { return nil, nil }
func (r *fakeNodeRepo) ListEnabled(ctx context.Context) ([]*domain.Node, error) { return nil, nil }

func newReorderSvc(repo ports.NodeRepo) *Service {
	return &Service{nodes: repo}
}

func TestReorder_HappyPath(t *testing.T) {
	repo := &fakeNodeRepo{}
	svc := newReorderSvc(repo)
	in := []ports.NodeSortUpdate{
		{NodeID: 1, SortOrder: 10},
		{NodeID: 2, SortOrder: 20},
		{NodeID: 3, SortOrder: 30},
	}
	if err := svc.Reorder(context.Background(), in); err != nil {
		t.Fatalf("Reorder = %v, want nil", err)
	}
	if len(repo.got) != 3 {
		t.Fatalf("repo received %d updates, want 3", len(repo.got))
	}
	for i := range in {
		if repo.got[i] != in[i] {
			t.Fatalf("update[%d] = %+v, want %+v", i, repo.got[i], in[i])
		}
	}
}

func TestReorder_EmptyRejected(t *testing.T) {
	repo := &fakeNodeRepo{}
	svc := newReorderSvc(repo)
	err := svc.Reorder(context.Background(), nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty Reorder err = %v, want ErrValidation", err)
	}
	if repo.got != nil {
		t.Fatalf("repo must not be touched on validation failure, got %+v", repo.got)
	}
}

func TestReorder_DuplicateNodeIDRejected(t *testing.T) {
	repo := &fakeNodeRepo{}
	svc := newReorderSvc(repo)
	err := svc.Reorder(context.Background(), []ports.NodeSortUpdate{
		{NodeID: 1, SortOrder: 10},
		{NodeID: 1, SortOrder: 20},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("duplicate Reorder err = %v, want ErrValidation", err)
	}
	if repo.got != nil {
		t.Fatalf("repo must not be touched on validation failure")
	}
}

func TestReorder_NonPositiveNodeIDRejected(t *testing.T) {
	repo := &fakeNodeRepo{}
	svc := newReorderSvc(repo)
	err := svc.Reorder(context.Background(), []ports.NodeSortUpdate{
		{NodeID: 0, SortOrder: 10},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("zero NodeID err = %v, want ErrValidation", err)
	}

	err = svc.Reorder(context.Background(), []ports.NodeSortUpdate{
		{NodeID: -5, SortOrder: 10},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("negative NodeID err = %v, want ErrValidation", err)
	}
}

func TestReorder_RepoErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	repo := &fakeNodeRepo{err: want}
	svc := newReorderSvc(repo)
	err := svc.Reorder(context.Background(), []ports.NodeSortUpdate{
		{NodeID: 1, SortOrder: 10},
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v wrapped", err, want)
	}
}

// captureNodeRepo records Node creates so CreateSeparator tests can
// inspect the row that would have been written. Inherits fakeNodeRepo
// stubs for the rest of the NodeRepo surface.
type captureNodeRepo struct {
	fakeNodeRepo
	created []*domain.Node
}

func (r *captureNodeRepo) Create(_ context.Context, n *domain.Node) error {
	cp := *n
	r.created = append(r.created, &cp)
	n.ID = int64(len(r.created))
	return nil
}

func TestCreateSeparator_StampsKindAndUniqueInboundID(t *testing.T) {
	repo := &captureNodeRepo{}
	svc := &Service{nodes: repo}
	n := &domain.Node{DisplayName: "---- Taiwan HiNet ----", Region: "TW", SortOrder: 50}
	if err := svc.CreateSeparator(context.Background(), n); err != nil {
		t.Fatalf("CreateSeparator = %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("got %d Create calls, want 1", len(repo.created))
	}
	got := repo.created[0]
	if got.Kind != domain.NodeKindSeparator {
		t.Errorf("Kind = %q, want separator", got.Kind)
	}
	if got.PanelID != 0 {
		t.Errorf("PanelID = %d, want 0 (separator has no panel binding)", got.PanelID)
	}
	if got.InboundID >= 0 {
		t.Errorf("InboundID = %d, want a negative value so the (panel_id, inbound_id) uniqueIndex never collides across separators", got.InboundID)
	}
	if !got.Enabled {
		t.Errorf("new separator should default to enabled=true so it shows up in subscriptions immediately")
	}
	if got.DisplayName != "---- Taiwan HiNet ----" {
		t.Errorf("DisplayName = %q, want preserved verbatim", got.DisplayName)
	}
	if got.SortOrder != 50 {
		t.Errorf("SortOrder = %d, want admin-supplied value preserved", got.SortOrder)
	}
}

func TestCreateSeparator_RejectsBlankDisplayName(t *testing.T) {
	cases := []struct {
		name string
		in   *domain.Node
	}{
		{"nil node", nil},
		{"empty display_name", &domain.Node{DisplayName: ""}},
		{"whitespace display_name", &domain.Node{DisplayName: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &captureNodeRepo{}
			svc := &Service{nodes: repo}
			err := svc.CreateSeparator(context.Background(), tc.in)
			if !errors.Is(err, domain.ErrValidation) {
				t.Errorf("err = %v, want ErrValidation", err)
			}
			if len(repo.created) != 0 {
				t.Errorf("repo Create touched on validation failure (got %d calls)", len(repo.created))
			}
		})
	}
}
