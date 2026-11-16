package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
)

type previewGroupRepo struct {
	ports.GroupRepo
	groups  map[int64]*domain.Group
	members int64
	err     error
}

func (r previewGroupRepo) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	if g, ok := r.groups[id]; ok {
		copied := *g
		return &copied, nil
	}
	return nil, domain.ErrNotFound
}
func (r previewGroupRepo) CountMembers(context.Context, int64) (int64, error) {
	return r.members, r.err
}

type previewNodeRepo struct {
	ports.NodeRepo
	nodes []*domain.Node
	err   error
}

func (r previewNodeRepo) ListEnabled(context.Context) ([]*domain.Node, error) { return r.nodes, r.err }

func groupPreviewNodes() []*domain.Node {
	return []*domain.Node{
		{ID: 1, Enabled: true, DisplayName: "HK-1", Region: "HK", Tags: []string{"premium"},
			Relays: []domain.RelayLine{{ID: "a", Region: "HK", Tags: []string{"premium"}}, {ID: "b", Region: "CN", Tags: []string{}}}},
		{ID: 2, Enabled: true, DisplayName: "JP-1", Region: "JP", Tags: []string{"premium"}},
		{ID: 3, Enabled: true, DisplayName: "US-1", Region: "US"},
	}
}

func postGroupPreview(t *testing.T, h *AdminGroupHandler, path, body string) (*httptest.ResponseRecorder, groupNodePreviewResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/groups/node-preview", h.PreviewNodes)
	r.POST("/groups/:id/node-preview", h.PreviewNodes)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
	var out groupNodePreviewResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return w, out
}

func TestGroupNodePreviewForANewGroup(t *testing.T) {
	svc := group.New(previewGroupRepo{}, previewNodeRepo{nodes: groupPreviewNodes()}, nil)
	h := NewAdminGroupHandler(svc, nil, nil)
	w, got := postGroupPreview(t, h, "/groups/node-preview", `{"tag_filter":{"tags":["region:HK"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// The CN relay fails region:HK, so HK-1 shows 1 of 2 lines (ADR 0041).
	want := []groupPreviewNodeDTO{{ID: 1, Name: "HK-1", Region: "HK", VisibleLines: 1, TotalLines: 2}}
	if !reflect.DeepEqual(got.Nodes, want) || len(got.Added) != 0 || len(got.Removed) != 0 || got.Members != 0 {
		t.Fatalf("new group preview %+v", got)
	}
}

func TestGroupNodePreviewComparesWithTheSavedGroup(t *testing.T) {
	saved := &domain.Group{ID: 7, Name: "VIP", TagFilter: domain.TagFilter{Tags: []string{"region:HK"}}}
	svc := group.New(previewGroupRepo{groups: map[int64]*domain.Group{7: saved}, members: 12}, previewNodeRepo{nodes: groupPreviewNodes()}, nil)
	h := NewAdminGroupHandler(svc, nil, nil)
	w, got := postGroupPreview(t, h, "/groups/7/node-preview", `{"tag_filter":{"tags":["premium"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(got.Nodes) != 2 || got.Nodes[0].ID != 1 || got.Nodes[1].ID != 2 {
		t.Fatalf("nodes %+v", got.Nodes)
	}
	if !reflect.DeepEqual(got.Added, []int64{2}) || !reflect.DeepEqual(got.Removed, []int64{}) || got.Members != 12 {
		t.Fatalf("change %+v / %+v, members %d", got.Added, got.Removed, got.Members)
	}
	_, got = postGroupPreview(t, h, "/groups/7/node-preview", `{"tag_filter":{"tags":["region:US"]}}`)
	if !reflect.DeepEqual(got.Added, []int64{3}) || !reflect.DeepEqual(got.Removed, []int64{1}) {
		t.Fatalf("change %+v / %+v", got.Added, got.Removed)
	}
}

// A group with an allowlist keeps it in the draft: matching nodes it cannot
// enforce on are listed as excluded, with a reason, not silently dropped.
func TestGroupNodePreviewListsAllowlistExclusions(t *testing.T) {
	saved := &domain.Group{ID: 7, Name: "Strict", TagFilter: domain.TagFilter{All: true}, DestAllowlist: domain.DestAllowlist{State: domain.DestStateOn}}
	svc := group.New(previewGroupRepo{groups: map[int64]*domain.Group{7: saved}}, previewNodeRepo{nodes: groupPreviewNodes()}, nil)
	svc.SetEligibilityDeps(group.EligibilityDeps{Panels: unknownPanels{}, Agents: unknownAgents{}, Policies: unknownPolicies{}})
	h := NewAdminGroupHandler(svc, nil, nil)
	w, got := postGroupPreview(t, h, "/groups/7/node-preview", `{"tag_filter":{"all":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(got.Nodes) != 0 || len(got.Excluded) != 3 || got.Excluded[0].Reason != group.ExcludedAllowlist {
		t.Fatalf("allowlist preview %+v", got)
	}
}

type unknownPanels struct{}
type unknownAgents struct{}
type unknownPolicies struct{}

func (unknownAgents) GetByPanelID(context.Context, int64) (*domain.NodeAgent, error) {
	return nil, domain.ErrNotFound
}
func (unknownPolicies) GetMeta(context.Context, string) (*domain.DestAgentPolicy, error) {
	return nil, domain.ErrNotFound
}

func (unknownPanels) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return nil, domain.ErrNotFound
}

func TestGroupNodePreviewFailuresAreNotAnswers(t *testing.T) {
	boom := errors.New("db down")
	h := NewAdminGroupHandler(group.New(previewGroupRepo{}, previewNodeRepo{err: boom}, nil), nil, nil)
	if w, _ := postGroupPreview(t, h, "/groups/node-preview", `{"tag_filter":{"all":true}}`); w.Code < 500 {
		t.Fatalf("a failed node read must not look like an answer: %d", w.Code)
	}
	h = NewAdminGroupHandler(group.New(previewGroupRepo{}, previewNodeRepo{nodes: groupPreviewNodes()}, nil), nil, nil)
	if w, _ := postGroupPreview(t, h, "/groups/99/node-preview", `{"tag_filter":{"all":true}}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown group: %d", w.Code)
	}
	saved := &domain.Group{ID: 7, TagFilter: domain.TagFilter{All: true}}
	h = NewAdminGroupHandler(group.New(previewGroupRepo{groups: map[int64]*domain.Group{7: saved}, err: boom}, previewNodeRepo{nodes: groupPreviewNodes()}, nil), nil, nil)
	if w, _ := postGroupPreview(t, h, "/groups/7/node-preview", `{"tag_filter":{"all":true}}`); w.Code < 500 {
		t.Fatalf("a failed member count must not look like an answer: %d", w.Code)
	}
}
