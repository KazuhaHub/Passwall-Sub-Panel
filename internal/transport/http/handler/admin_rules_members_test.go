package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type staticRuleNodes struct{ nodes []*domain.Node }

func (s staticRuleNodes) List(context.Context) ([]*domain.Node, error) { return s.nodes, nil }

func TestAdminRuleSetsSavePersistsMembersAndOptionsAndInvalidatesRenderCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{nodes: []*domain.Node{{ID: 42, DisplayName: "China", Enabled: true}}}, nil, func() { invalidations++ }, t.TempDir())

	body := ruleSetDTO{
		Slug: "custom", Name: "Custom", Enabled: true, Content: "- MATCH,🇨🇳 中国大陆",
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"🇨🇳 中国大陆": {{Kind: "node", NodeID: 42}, {Kind: "builtin", Value: "DIRECT"}, {Kind: "node_set", Value: "remaining"}},
		},
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"🇨🇳 中国大陆": {Type: "load-balance", Strategy: "consistent-hashing"},
		},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if invalidations != 1 {
		t.Fatalf("invalidations=%d", invalidations)
	}
	got, err := repo.GetBySlug(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if members := got.ProxyGroupMembers["🇨🇳 中国大陆"]; len(members) != 3 || members[0].NodeID != 42 {
		t.Fatalf("members=%#v", members)
	}
	options := got.ProxyGroupOptions["🇨🇳 中国大陆"]
	if options.Type != "load-balance" || options.Strategy != "consistent-hashing" || options.URL == "" || options.Interval == nil || options.Lazy == nil || options.Timeout == nil {
		t.Fatalf("options were not normalized and persisted: %#v", options)
	}
}

func TestAdminRuleSetsSaveRejectsInvalidProxyGroupOptions(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, nil, t.TempDir())
	negative, zero := -1, 0
	tests := []domain.ProxyGroupOptions{
		{Type: "random"},
		{Type: "url-test", URL: "ftp://example.com", Interval: &negative},
		{Type: "fallback", Timeout: &zero},
		{Type: "load-balance", Strategy: "random"},
	}
	for _, options := range tests {
		body := ruleSetDTO{
			Slug: "bad", Name: "Bad", Enabled: true, Content: "- MATCH,Auto",
			ProxyGroupOptions: map[string]domain.ProxyGroupOptions{"Auto": options},
		}
		w := performRuleSave(t, h, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("options=%#v status=%d body=%s", options, w.Code, w.Body.String())
		}
	}
}

func TestAdminRuleSetsInspectReturnsEffectiveOptionsForAllTypes(t *testing.T) {
	h := NewAdminRuleSetsHandler(nil, staticRuleNodes{nodes: []*domain.Node{
		{ID: 1, DisplayName: "A", Enabled: true}, {ID: 2, DisplayName: "B", Enabled: true},
	}}, nil, nil, t.TempDir())
	req := inspectProxyGroupsRequest{
		Content: "- DOMAIN,a,Manual\n- DOMAIN,b,Auto\n- DOMAIN,c,Failover\n- MATCH,Balanced",
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"Manual":   {Type: "select"},
			"Auto":     {Type: "url-test"},
			"Failover": {Type: "fallback"},
			"Balanced": {Type: "load-balance"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/rules/inspect-proxy-groups", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	h.InspectProxyGroups(c)
	c.Writer.WriteHeaderNow()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Groups []struct {
			Name    string                   `json:"name"`
			Options domain.ProxyGroupOptions `json:"options"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	byName := map[string]domain.ProxyGroupOptions{}
	for _, group := range response.Groups {
		byName[group.Name] = group.Options
	}
	if byName["Manual"].Type != "select" || byName["Auto"].Type != "url-test" || byName["Failover"].Type != "fallback" || byName["Balanced"].Type != "load-balance" {
		t.Fatalf("unexpected effective options: %#v", byName)
	}
	if byName["Auto"].Tolerance == nil || byName["Balanced"].Strategy != "consistent-hashing" {
		t.Fatalf("type defaults missing: %#v", byName)
	}
}

func TestAdminRuleSetsSaveRejectsMemberCycleWithoutInvalidating(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, func() { invalidations++ }, t.TempDir())
	body := ruleSetDTO{
		Slug: "bad", Name: "Bad", Enabled: true, Content: "- DOMAIN,a,A\n- MATCH,B",
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"A": {{Kind: "proxy_group", Value: "B"}},
			"B": {{Kind: "proxy_group", Value: "A"}},
		},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if invalidations != 0 {
		t.Fatalf("invalidations=%d", invalidations)
	}
}

func performRuleSave(t *testing.T, h *AdminRuleSetsHandler, body ruleSetDTO) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/rules/"+body.Slug, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Save(c)
	c.Writer.WriteHeaderNow()
	return w
}
