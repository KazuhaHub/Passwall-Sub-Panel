package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		Slug: "custom", Name: "Custom", Enabled: true, DirectSubscriptionDomain: true, Content: "- MATCH,🇨🇳 中国大陆",
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			// A load-balance group may only health-check real endpoints, so its
			// members are proxy-side (a specific node, the node selector, and the
			// remaining nodes) — never DIRECT/REJECT built-in exits.
			"🇨🇳 中国大陆": {{Kind: "node", NodeID: 42}, {Kind: "proxy_group", Value: "🚀 节点选择"}, {Kind: "node_set", Value: "remaining"}},
		},
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"🇨🇳 中国大陆": {Type: "load-balance", Strategy: "consistent-hashing"},
		},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("mihomo_rules")) {
		t.Fatalf("legacy field leaked into normalized response: %s", w.Body.String())
	}
	if invalidations != 1 {
		t.Fatalf("invalidations=%d", invalidations)
	}
	got, err := repo.GetBySlug(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if !got.DirectSubscriptionDomain {
		t.Fatal("direct subscription domain option was not persisted")
	}
	if members := got.ProxyGroupMembers["🇨🇳 中国大陆"]; len(members) != 3 || members[0].NodeID != 42 {
		t.Fatalf("members=%#v", members)
	}
	options := got.ProxyGroupOptions["🇨🇳 中国大陆"]
	if options.Type != "load-balance" || options.Strategy != "consistent-hashing" || options.URL == "" || options.Interval == nil || options.Lazy == nil || options.Timeout == nil {
		t.Fatalf("options were not normalized and persisted: %#v", options)
	}
}

func TestAdminRuleSetsSaveMigratesLegacyMihomoRulesIntoContent(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, nil, t.TempDir())
	body := ruleSetDTO{
		Slug: "advanced", Name: "Advanced", Enabled: true, Content: "- MATCH,DIRECT",
		MihomoRules:            "- DOMAIN-SUFFIX,openai.com,use-ai-rules",
		MihomoSubRules:         []domain.MihomoSubRule{{Name: "ai-rules", Content: "- MATCH,DIRECT"}},
		MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "use-ai-rules", TargetSubRule: "ai-rules"}},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("mihomo_rules")) || !bytes.Contains(w.Body.Bytes(), []byte("DOMAIN-SUFFIX,openai.com")) {
		t.Fatalf("legacy rules were not normalized in the response: %s", w.Body.String())
	}
	got, err := repo.GetBySlug(context.Background(), "advanced")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != body.MihomoRules+"\n- MATCH,DIRECT" || len(got.MihomoSubRules) != 1 || len(got.MihomoRematchOutbounds) != 1 {
		t.Fatalf("advanced fields not persisted: %#v", got)
	}
}

func TestAdminRuleSetsInspectMergesLegacyMihomoRulesIntoContent(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, nil, t.TempDir())
	raw, err := json.Marshal(inspectProxyGroupsRequest{
		Content:     "- MATCH,Current",
		MihomoRules: "- MATCH,Legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/rules/inspect-proxy-groups", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	h.InspectProxyGroups(c)
	c.Writer.WriteHeaderNow()
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"Legacy"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"Current"`)) {
		t.Fatalf("legacy and unified rules were not both inspected: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminRuleSetsSaveRejectsBoundSubRulesWithoutTemplatePlaceholder(t *testing.T) {
	root := t.TempDir()
	rules, err := yamladapter.NewRuleSetRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := yamladapter.NewTemplateRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := templates.Save(context.Background(), &domain.Template{
		Slug: "mihomo", Name: "Mihomo", ClientType: domain.ClientMihomo, RuleSets: []string{"advanced"}, Content: "rules:\n  {{ rules_common }}",
	}); err != nil {
		t.Fatal(err)
	}
	h := NewAdminRuleSetsHandler(rules, staticRuleNodes{}, nil, nil, root, templates)
	w := performRuleSave(t, h, ruleSetDTO{
		Slug: "advanced", Name: "Advanced", Enabled: true, Content: "- MATCH,DIRECT",
		MihomoSubRules: []domain.MihomoSubRule{{Name: "ai-rules", Content: "- MATCH,DIRECT"}},
	})
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("missing_sub_rules_placeholder")) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminRuleSetsSavePrunesRemovedGroupMetadataAndReturnsPersistedRuleSet(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, func() { invalidations++ }, t.TempDir())
	body := ruleSetDTO{
		Slug: "custom", Name: "Custom", Enabled: true,
		Content:         "- MATCH,🐟 漏网之鱼",
		ProxyGroupOrder: []string{"📣 谷歌FCM", "🐟 漏网之鱼"},
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"📣 谷歌FCM": {{Kind: "builtin", Value: "DIRECT"}},
			"🐟 漏网之鱼":  {{Kind: "node_set", Value: "remaining"}},
		},
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"📣 谷歌FCM": {Type: "fallback"},
		},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if invalidations != 1 {
		t.Fatalf("invalidations=%d", invalidations)
	}
	var response ruleSetDTO
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response.ProxyGroupOrder, []string{"🐟 漏网之鱼"}) || response.ProxyGroupMembers["📣 谷歌FCM"] != nil {
		t.Fatalf("response was not normalized: %#v", response)
	}
	if len(response.ProxyGroupOptions) != 0 {
		t.Fatalf("orphan options survived in response: %#v", response.ProxyGroupOptions)
	}
	got, err := repo.GetBySlug(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ProxyGroupOrder, response.ProxyGroupOrder) || !reflect.DeepEqual(got.ProxyGroupMembers, response.ProxyGroupMembers) || !reflect.DeepEqual(got.ProxyGroupOptions, response.ProxyGroupOptions) {
		t.Fatalf("response and repository differ: response=%#v stored=%#v", response, got)
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

func TestAdminRuleSetsSaveStillRejectsSurvivingReferenceToRemovedGroup(t *testing.T) {
	repo, err := yamladapter.NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	h := NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, func() { invalidations++ }, t.TempDir())
	body := ruleSetDTO{
		Slug: "bad", Name: "Bad", Enabled: true, Content: "- MATCH,Keep",
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"Keep": {{Kind: "proxy_group", Value: "Removed"}},
		},
	}
	w := performRuleSave(t, h, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if invalidations != 0 {
		t.Fatalf("invalidations=%d", invalidations)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"code":"missing_group"`)) {
		t.Fatalf("missing_group issue not returned: %s", w.Body.String())
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
