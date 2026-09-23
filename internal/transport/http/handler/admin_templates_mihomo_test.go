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

func TestAdminTemplatesSaveValidatesMihomoSubRulePlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	rules, err := yamladapter.NewRuleSetRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := yamladapter.NewTemplateRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.Save(context.Background(), &domain.RuleSet{
		Slug: "advanced", Name: "Advanced", Enabled: true,
		MihomoSubRules: []domain.MihomoSubRule{{Name: "ai-rules", Content: "- MATCH,DIRECT"}},
	}); err != nil {
		t.Fatal(err)
	}

	h := NewAdminTemplatesHandler(templates, root, rules)
	body := templateDTO{
		Slug: "mihomo", Name: "Mihomo", ClientType: string(domain.ClientMihomo),
		RuleSets: []string{"advanced"}, Content: "rules:\n  {{ rules_common }}",
	}
	w := performTemplateSave(t, h, body)
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("missing_sub_rules_placeholder")) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	body.Content = "{{ mihomo_sub_rules }}\nrules:\n  {{ rules_common }}"
	w = performTemplateSave(t, h, body)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got, err := templates.GetBySlug(context.Background(), body.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != body.Content {
		t.Fatalf("content=%q", got.Content)
	}
}

func performTemplateSave(t *testing.T, h *AdminTemplatesHandler, body templateDTO) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/templates/"+body.Slug, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Save(c)
	c.Writer.WriteHeaderNow()
	return w
}
