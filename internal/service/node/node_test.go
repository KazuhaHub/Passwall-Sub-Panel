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
func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.Node) error                 { return nil }
func (r *fakeNodeRepo) UpdateMetadata(ctx context.Context, n *domain.Node) error         { return nil }
func (r *fakeNodeRepo) UpdateTrafficCounters(ctx context.Context, n *domain.Node) error  { return nil }
func (r *fakeNodeRepo) UpdateHealth(ctx context.Context, n *domain.Node) error           { return nil }
func (r *fakeNodeRepo) UpdateInboundConfig(ctx context.Context, n *domain.Node) error    { return nil }
func (r *fakeNodeRepo) UpdateEnabled(ctx context.Context, id int64, enabled bool) error  { return nil }
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
func (r *fakeNodeRepo) ListPaged(ctx context.Context, _ ports.Pagination) ([]*domain.Node, int64, error) {
	return nil, 0, nil
}

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

// captureSeparatorRepo records SeparatorEntry creates so CreateSeparator
// tests can inspect the row that would have been written. Implements the
// SeparatorRepo surface with no-op stubs for the methods we don't drive.
type captureSeparatorRepo struct {
	created []*domain.SeparatorEntry
}

func (r *captureSeparatorRepo) Create(_ context.Context, s *domain.SeparatorEntry) error {
	cp := *s
	r.created = append(r.created, &cp)
	s.ID = int64(len(r.created))
	return nil
}
func (r *captureSeparatorRepo) Update(context.Context, *domain.SeparatorEntry) error { return nil }
func (r *captureSeparatorRepo) Delete(context.Context, int64) error                  { return nil }
func (r *captureSeparatorRepo) GetByID(context.Context, int64) (*domain.SeparatorEntry, error) {
	return nil, domain.ErrNotFound
}
func (r *captureSeparatorRepo) List(context.Context) ([]*domain.SeparatorEntry, error) {
	return nil, nil
}
func (r *captureSeparatorRepo) ListPaged(context.Context, ports.Pagination) ([]*domain.SeparatorEntry, int64, error) {
	return nil, 0, nil
}
func (r *captureSeparatorRepo) ListEnabled(context.Context) ([]*domain.SeparatorEntry, error) {
	return nil, nil
}
func (r *captureSeparatorRepo) BatchUpdateSortOrder(context.Context, []ports.SeparatorSortUpdate) error {
	return nil
}

func TestCreateSeparator_StoresEntry(t *testing.T) {
	repo := &captureSeparatorRepo{}
	svc := &Service{separators: repo}
	e := &domain.SeparatorEntry{
		DisplayName: "  ---- Taiwan HiNet ----  ",
		SortOrder:   50,
		Enabled:     true,
		Mode:        domain.SeparatorModeNodeBound,
		NodeIDs:     []int64{1, 3},
	}
	if err := svc.CreateSeparator(context.Background(), e); err != nil {
		t.Fatalf("CreateSeparator = %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("got %d Create calls, want 1", len(repo.created))
	}
	got := repo.created[0]
	if got.DisplayName != "---- Taiwan HiNet ----" {
		t.Errorf("DisplayName = %q, want surrounding whitespace trimmed", got.DisplayName)
	}
	if got.SortOrder != 50 {
		t.Errorf("SortOrder = %d, want 50", got.SortOrder)
	}
	if got.Mode != domain.SeparatorModeNodeBound {
		t.Errorf("Mode = %q, want node_bound", got.Mode)
	}
	if len(got.NodeIDs) != 2 || got.NodeIDs[0] != 1 || got.NodeIDs[1] != 3 {
		t.Errorf("NodeIDs = %v, want [1 3]", got.NodeIDs)
	}
}

func TestCreateSeparator_RejectsBlankDisplayName(t *testing.T) {
	cases := []struct {
		name string
		in   *domain.SeparatorEntry
	}{
		{"nil entry", nil},
		{"empty display_name", &domain.SeparatorEntry{DisplayName: ""}},
		{"whitespace display_name", &domain.SeparatorEntry{DisplayName: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &captureSeparatorRepo{}
			svc := &Service{separators: repo}
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
