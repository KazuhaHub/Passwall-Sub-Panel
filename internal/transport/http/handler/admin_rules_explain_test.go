package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type fakeExplainer struct {
	got render.ExplainRequest
	err error
}

func (f *fakeExplainer) ExplainRoute(_ context.Context, req render.ExplainRequest) (*render.ExplainResponse, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return &render.ExplainResponse{ClientType: "mihomo", TemplateSlug: req.TemplateSlug, Result: render.ExplainResult{State: render.ExplainNoMatch, Certain: true}}, nil
}

func TestAdminRuleExplainStatuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		role   domain.Role
		noAuth bool
		err    error
		body   string
		want   int
		code   string
	}{
		{name: "ok", role: domain.RoleAdmin, want: http.StatusOK},
		{name: "unauthenticated", noAuth: true, want: http.StatusUnauthorized},
		{name: "operator", role: domain.RoleOperator, want: http.StatusForbidden},
		{name: "bad json", role: domain.RoleAdmin, body: `{`, want: http.StatusBadRequest},
		{name: "negative user", role: domain.RoleAdmin, body: `{"template_slug":"t","user_id":-1}`, want: http.StatusBadRequest},
		{name: "sing-box", role: domain.RoleAdmin, err: fmt.Errorf("%w: template", render.ErrExplainUnsupportedClient), want: http.StatusUnprocessableEntity, code: "explain_unsupported_client"},
		{name: "bad probe", role: domain.RoleAdmin, err: &domain.CodedValidationError{Code: "explain_address_invalid", Msg: "x"}, want: http.StatusBadRequest, code: "explain_address_invalid"},
		{name: "unknown template", role: domain.RoleAdmin, err: domain.ErrNotFound, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeExplainer{err: tc.err}
			body := tc.body
			if body == "" {
				body = `{"template_slug":"default-mihomo","user_id":4,"probe":{"host":"claude.ai","port":443,"network":"udp"},"draft":{"slug":"d","enabled":true,"content":"- MATCH,Pick","proxy_group_members":{"Pick":[{"kind":"builtin","value":"DIRECT"}]}}}`
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/rules/explain", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if !tc.noAuth {
				c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: tc.role})
			}
			NewAdminRuleExplainHandler(f).Explain(c)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.code != "" {
				var out map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				if out["code"] != tc.code {
					t.Fatalf("body %s", w.Body.String())
				}
			}
			if tc.want == http.StatusOK {
				if f.got.TemplateSlug != "default-mihomo" || f.got.UserID != 4 || f.got.Probe.Host != "claude.ai" || f.got.Probe.Port != 443 || f.got.Probe.Network != "udp" {
					t.Fatalf("request not passed through: %+v", f.got)
				}
				if f.got.Draft == nil || f.got.Draft.Slug != "d" || f.got.Draft.Content != "- MATCH,Pick" || len(f.got.Draft.ProxyGroupMembers["Pick"]) != 1 {
					t.Fatalf("draft not passed through: %+v", f.got.Draft)
				}
			}
		})
	}
}
