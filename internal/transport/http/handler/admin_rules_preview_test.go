package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type previewAdminRepo struct {
	ports.UserRepo
	groupID int64
	calls   int
}

func (r *previewAdminRepo) GetByID(_ context.Context, id int64) (*domain.User, error) {
	r.calls++
	return &domain.User{ID: id, GroupID: r.groupID}, nil
}

type previewCompiler struct {
	groupID int64
	draft   *domain.RuleSet
	err     error
}

func (p *previewCompiler) PreviewMihomoGroup(_ context.Context, groupID int64, draft *domain.RuleSet) (*render.GroupSubscriptionPreview, error) {
	p.groupID, p.draft = groupID, draft
	if p.err != nil {
		return nil, p.err
	}
	return &render.GroupSubscriptionPreview{GroupID: groupID, Status: "ready", Groups: []render.GroupSubscriptionPreviewGroup{{Name: "Pick", Members: []string{"only"}}}}, nil
}

func TestAdminGroupPreviewDefaultsAndReadOnlyDraft(t *testing.T) {
	for _, scenario := range []string{"default", "explicit", "missing_group", "negative", "unauthorized", "operator", "invalid_group"} {
		t.Run(scenario, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			users := &previewAdminRepo{groupID: 71}
			compiler := &previewCompiler{}
			h := &AdminRuleSetsHandler{previewUsers: users, previewRenderer: compiler}
			req := map[string]any{"draft": ruleSetDTO{Slug: "renamed", Enabled: true, Content: "- MATCH,Pick", ProxyGroupOrder: []string{"Pick"}, ProxyGroupMembers: map[string][]domain.ProxyGroupMember{"Pick": {{Kind: "builtin", Value: "DIRECT"}}}}}
			want := http.StatusOK
			switch scenario {
			case "explicit":
				req["preview_group_id"] = 98
			case "negative":
				req["preview_group_id"] = -1
				want = 400
			case "missing_group":
				users.groupID = 0
				want = 400
			case "unauthorized":
				want = 401
			case "operator":
				want = 403
			case "invalid_group":
				req["preview_group_id"] = 999
				compiler.err = domain.ErrNotFound
				want = 404
			}
			body, _ := json.Marshal(req)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/rules/preview-proxy-groups", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if scenario != "unauthorized" {
				role := domain.RoleAdmin
				if scenario == "operator" {
					role = domain.RoleOperator
				}
				c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 5, Role: role})
			}
			h.PreviewProxyGroups(c)
			if w.Code != want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if want == 200 {
				expected := int64(71)
				if scenario == "explicit" {
					expected = 98
				}
				if compiler.groupID != expected || compiler.draft.Slug != "renamed" || len(compiler.draft.ProxyGroupOrder) != 1 {
					t.Fatalf("context not preserved: %+v", compiler)
				}
				if scenario == "explicit" && users.calls != 0 {
					t.Fatal("explicit preview read a user")
				}
			}
		})
	}
}
