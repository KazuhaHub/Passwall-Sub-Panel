package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
)

// seededRuleSetHandler is the handler over a config dir holding the real seeded
// rule sets (default_rules is ~450 KB), the case the list payload is about.
func seededRuleSetHandler(t *testing.T) (*AdminRuleSetsHandler, *yamladapter.RuleSetRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := seed.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	repo, err := yamladapter.NewRuleSetRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	return NewAdminRuleSetsHandler(repo, staticRuleNodes{}, nil, dir), repo
}

func listRuleSets(t *testing.T, h *AdminRuleSetsHandler) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	r := gin.New()
	r.GET("/api/admin/rules", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/rules?page=1&page_size=200", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return w, env.Items
}

// Cost test: the list used to carry the whole 450 KB body of the seeded rule set.
func TestAdminRuleSetsListIsASmallSummary(t *testing.T) {
	h, repo := seededRuleSetHandler(t)
	w, items := listRuleSets(t, h)
	if w.Body.Len() > 4<<10 {
		t.Fatalf("list response is %d bytes, want under 4 KiB", w.Body.Len())
	}
	if len(items) != 1 {
		t.Fatalf("items=%d", len(items))
	}
	it := items[0]
	for _, banned := range []string{"content", "mihomo_sub_rules", "mihomo_rematch_outbounds", "proxy_group_members", "proxy_group_options"} {
		if _, ok := it[banned]; ok {
			t.Errorf("summary still carries %q", banned)
		}
	}
	full, err := repo.GetBySlug(context.Background(), "default_rules")
	if err != nil {
		t.Fatal(err)
	}
	if it["slug"] != "default_rules" || it["name"] != full.Name || it["enabled"] != full.Enabled || it["revision"] != full.Revision || full.Revision == "" {
		t.Errorf("identity fields differ from the stored rule set: %v", it)
	}
	if int(it["size_bytes"].(float64)) != len(full.Content) {
		t.Errorf("size_bytes=%v want %d", it["size_bytes"], len(full.Content))
	}
	lines := 0
	for _, l := range strings.Split(full.Content, "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if int(it["line_count"].(float64)) != lines || lines == 0 {
		t.Errorf("line_count=%v want %d", it["line_count"], lines)
	}
}

// The targets are the backend's own reading of the rules. The fixture is what the
// browser's former line-splitting function (templates/analysis.ts extractRuleTargets)
// produced for the same seeded file, so the move to the server changed no answer.
func TestAdminRuleSetsListTargetsMatchTheFormerBrowserAnswer(t *testing.T) {
	h, _ := seededRuleSetHandler(t)
	_, items := listRuleSets(t, h)
	raw, err := os.ReadFile("testdata/default_rules_targets.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range items[0]["targets"].([]any) {
		got = append(got, v.(string))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets=%v\nwant=%v", got, want)
	}
}

// The derived summary is computed once per revision, and recomputed when the file changes.
func TestAdminRuleSetsSummaryIsCachedPerRevision(t *testing.T) {
	h, repo := seededRuleSetHandler(t)
	listRuleSets(t, h)
	first := h.summaries["default_rules"]
	if first.revision == "" || len(first.targets) == 0 {
		t.Fatalf("nothing cached: %#v", first)
	}
	listRuleSets(t, h)
	// Same backing array: the second list reused the cached slice rather than parsing again.
	if &h.summaries["default_rules"].targets[0] != &first.targets[0] {
		t.Fatal("second list re-derived an unchanged rule set")
	}
	rs := &domain.RuleSet{Slug: "extra", Name: "Extra", Enabled: true, Content: "- DOMAIN,a.example,Alpha\n\n- MATCH,Beta"}
	if err := repo.Save(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	_, items := listRuleSets(t, h)
	var extra map[string]any
	for _, it := range items {
		if it["slug"] == "extra" {
			extra = it
		}
	}
	if extra == nil || int(extra["line_count"].(float64)) != 2 || !reflect.DeepEqual(extra["targets"], []any{"Alpha", "Beta"}) {
		t.Fatalf("extra=%v", extra)
	}
	rs.Content = "- MATCH,Gamma"
	if err := repo.Save(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	_, items = listRuleSets(t, h)
	for _, it := range items {
		if it["slug"] == "extra" && !reflect.DeepEqual(it["targets"], []any{"Gamma"}) {
			t.Fatalf("stale summary after an edit: %v", it)
		}
	}
}

// GET /rules/:slug keeps returning the whole document (the editor and the conflict dialog read it).
func TestAdminRuleSetsGetStillReturnsContent(t *testing.T) {
	h, _ := seededRuleSetHandler(t)
	r := gin.New()
	r.GET("/api/admin/rules/:slug", h.Get)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/rules/default_rules", nil))
	var got ruleSetDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Content) < 100000 || got.Revision == "" {
		t.Fatalf("detail lost its body: content=%d revision=%q", len(got.Content), got.Revision)
	}
}
