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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type previewUsers struct {
	ports.UserRepo
	users map[int64]*domain.User
}

func (r previewUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if u, ok := r.users[id]; ok {
		copied := *u
		return &copied, nil
	}
	return nil, domain.ErrNotFound
}

type previewGroupList struct {
	previewGroupRepo
	list []*domain.Group
	err  error
}

func (r previewGroupList) List(context.Context) ([]*domain.Group, error) { return r.list, r.err }

func previewGroups() map[int64]*domain.Group {
	return map[int64]*domain.Group{
		1: {ID: 1, Name: "HK", TagFilter: domain.TagFilter{Tags: []string{"region:HK"}}},
		2: {ID: 2, Name: "Premium", TagFilter: domain.TagFilter{Tags: []string{"premium"}}},
	}
}

func servePreview(t *testing.T, role domain.Role, method, route, path string, h gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, route, func(c *gin.Context) {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{Role: role, UPN: "tester"})
		c.Next()
	}, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return w
}

func userPreviewHandler(t *testing.T, users map[int64]*domain.User, groups previewGroupRepo, nodes previewNodeRepo) *AdminUserHandler {
	t.Helper()
	repo := previewUsers{users: users}
	h := NewAdminUserHandler(user.New(repo, nil, nil, nil, nil, nil, nil, nil), nil, nil, nil, nil, nil)
	return h.WithMembershipPreview(group.New(groups, nodes, nil))
}

// ADR 0042 §3: what moving a user to another group would change.
func TestUserGroupChangePreview(t *testing.T) {
	users := map[int64]*domain.User{5: {ID: 5, Role: domain.RoleUser, GroupID: 1}, 6: {ID: 6, Role: domain.RoleAdmin, GroupID: 1}}
	h := userPreviewHandler(t, users, previewGroupRepo{groups: previewGroups()}, previewNodeRepo{nodes: groupPreviewNodes()})
	w := servePreview(t, domain.RoleOperator, http.MethodPost, "/users/:id/group-preview", "/users/5/group-preview", h.PreviewGroupChange, `{"group_id":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got groupChangePreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// HK keeps HK-1; Premium adds JP-1. HK-1's lines are counted under
	// Premium in "kept", which only counts.
	if got.Kept != 1 || len(got.Removed) != 0 || !reflect.DeepEqual(got.Added, []groupChangeNodeDTO{{ID: 2, Name: "JP-1", Region: "JP"}}) {
		t.Fatalf("preview %+v", got)
	}
	// The operator guard of the write applies to its preview.
	if w := servePreview(t, domain.RoleOperator, http.MethodPost, "/users/:id/group-preview", "/users/6/group-preview", h.PreviewGroupChange, `{"group_id":2}`); w.Code != http.StatusForbidden {
		t.Fatalf("operator previewed an admin's group change: %d", w.Code)
	}
	if w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/users/:id/group-preview", "/users/5/group-preview", h.PreviewGroupChange, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing group_id: %d", w.Code)
	}
	if w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/users/:id/group-preview", "/users/5/group-preview", h.PreviewGroupChange, `{"group_id":99}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown target group: %d", w.Code)
	}
	broken := userPreviewHandler(t, users, previewGroupRepo{groups: previewGroups()}, previewNodeRepo{err: errors.New("db down")})
	if w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/users/:id/group-preview", "/users/5/group-preview", broken.PreviewGroupChange, `{"group_id":2}`); w.Code < 500 {
		t.Fatalf("a failed read must not look like an answer: %d", w.Code)
	}
	unwired := NewAdminUserHandler(user.New(previewUsers{users: users}, nil, nil, nil, nil, nil, nil, nil), nil, nil, nil, nil, nil)
	if w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/users/:id/group-preview", "/users/5/group-preview", unwired.PreviewGroupChange, `{"group_id":2}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired preview: %d", w.Code)
	}
}

// ADR 0042 §4: which groups would show a node-bound separator.
func TestSeparatorGroupsPreview(t *testing.T) {
	groups := previewGroups()
	list := previewGroupList{previewGroupRepo: previewGroupRepo{groups: groups}, list: []*domain.Group{groups[1], groups[2]}}
	nodesSvc := node.New(visibilityNodes{}, nil, nil, nil, nil, nil, nil)
	h := NewAdminNodeHandler(nodesSvc, nil, nil, nil, nil, nil, nil).WithLineVisibility(group.New(list, previewNodeRepo{nodes: groupPreviewNodes()}, nil))
	post := func(body string) (*httptest.ResponseRecorder, separatorGroupsResponse) {
		w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/nodes/separator/group-preview", "/nodes/separator/group-preview", h.PreviewSeparatorGroups, body)
		var out separatorGroupsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	// JP-1 (node 2) is only in Premium.
	if w, got := post(`{"mode":"node_bound","node_ids":[2]}`); w.Code != http.StatusOK || !reflect.DeepEqual(got.Visible, []int64{2}) || len(got.Groups) != 2 {
		t.Fatalf("node-bound: %d %+v", w.Code, got)
	}
	if _, got := post(`{"mode":"node_bound","node_ids":[]}`); got.Visible == nil || len(got.Visible) != 0 {
		t.Fatalf("no nodes must be an empty, non-nil answer: %+v", got)
	}
	if _, got := post(`{"mode":"global"}`); !reflect.DeepEqual(got.Visible, []int64{1, 2}) {
		t.Fatalf("global: %+v", got)
	}
	if w, _ := post(`{"mode":"sometimes"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad mode: %d", w.Code)
	}
	failing := NewAdminNodeHandler(nodesSvc, nil, nil, nil, nil, nil, nil).WithLineVisibility(group.New(previewGroupList{list: nil, err: errors.New("db down")}, previewNodeRepo{}, nil))
	w := servePreview(t, domain.RoleAdmin, http.MethodPost, "/nodes/separator/group-preview", "/nodes/separator/group-preview", failing.PreviewSeparatorGroups, `{"mode":"global"}`)
	if w.Code < 500 {
		t.Fatalf("a failed read must not look like an answer: %d", w.Code)
	}
}
