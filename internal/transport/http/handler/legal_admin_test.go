package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

func TestLegalAdminHTTP_PublicationActorHistoryAndImpact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repos := legalHTTPRepos(t)
	actor := legalHTTPUser(t, repos, domain.RoleAdmin, 1)
	legalHTTPUser(t, repos, domain.RoleUser, 2)
	legalHTTPUser(t, repos, domain.RoleOperator, 3)
	h := NewLegalAdminHandler(repos.Legal)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: actor.ID}); c.Next() })
	r.GET("/legal/affected-users", h.AffectedUsers)
	r.GET("/legal/:kind", h.History)
	r.POST("/legal/:kind", h.Publish)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		body    string
		version int64
	}{
		{`{"locale":"en-US","content":"first","published_by":999999}`, 1},
		{`{"locale":"zh-CN","content":"major","consent_bump":true,"published_by":999999}`, 2},
	} {
		w := request(http.MethodPost, "/legal/terms", tc.body)
		var p domain.LegalPublication
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || p.Document.PublishedBy != actor.ID || p.ConsentVersion != tc.version {
			t.Fatalf("publish %d %+v", w.Code, p)
		}
	}
	w := request(http.MethodGet, "/legal/terms?limit=1", "")
	var page struct {
		Items []domain.LegalDocument `json:"items"`
		Next  int64                  `json:"next_before_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].Locale != "zh-CN" || page.Next != page.Items[0].ID {
		t.Fatalf("history %d %+v", w.Code, page)
	}
	w = request(http.MethodGet, fmt.Sprintf("/legal/terms?before_id=%d&limit=1", page.Next), "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].Locale != "en-US" {
		t.Fatalf("next history %d %+v", w.Code, page)
	}
	for _, path := range []string{"/legal/terms?limit=51", "/legal/terms?before_id=-1", "/legal/terms?limit=x", "/legal/unknown"} {
		if w := request(http.MethodGet, path, ""); w.Code != 400 {
			t.Fatalf("invalid %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = request(http.MethodGet, "/legal/affected-users", "")
	var impact struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &impact); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || impact.Count != 1 {
		t.Fatalf("impact %d %+v", w.Code, impact)
	}
	large, err := json.Marshal(map[string]any{"locale": "en-US", "content": strings.Repeat("中", 20001)})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(http.MethodPost, "/legal/privacy", string(large)); w.Code != 400 || decodeErrBody(t, w) != "legal_content_too_large" {
		t.Fatalf("large body %d %s", w.Code, w.Body.String())
	}
}

func TestLegalAdminHTTP_PublishRequiresActor(t *testing.T) {
	h := NewLegalAdminHandler(legalHTTPRepos(t).Legal)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/legal/terms", strings.NewReader(`{"locale":"en-US","content":"text"}`))
	h.Publish(c)
	if w.Code != 401 {
		t.Fatalf("unauthenticated publish %d %s", w.Code, w.Body.String())
	}
}

func TestLegalAdminHTTP_CollectionBeforeEnablement(t *testing.T) {
	repos := legalHTTPRepos(t)
	settings := ports.UISettings{LegalEnabled: false, AuthEventRetentionDays: 30, RiskRefreshIntervalMinutes: 25, RiskHWIDCaptureOff: true}
	if err := repos.Settings.Save(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/legal/data-collection", NewLegalAdminHandler(repos.Legal).Collection)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/legal/data-collection", nil))
	var data domain.LegalDataCollection
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || data.AuthEventRetentionDays != 30 || data.RiskAssessmentRefreshMinutes != 25 || data.ConnectionRetentionDays != 7 || data.HWIDCaptured || data.Access == nil {
		t.Fatalf("disabled administrator disclosure %d %+v", w.Code, data)
	}
}

func TestLegalAdminHTTP_LatestIsIndependentOfHistoryPageAndEnablement(t *testing.T) {
	repos := legalHTTPRepos(t)
	ctx := context.Background()
	for _, draft := range []domain.LegalDraft{
		{Kind: "terms", Locale: "zh-CN", Content: "中文", PublishedBy: 1},
		{Kind: "terms", Locale: "en-US", Content: "English", PublishedBy: 1},
		{Kind: "terms", Locale: "en-US", Content: "English updated", PublishedBy: 1},
	} {
		if _, err := repos.Legal.Publish(ctx, draft); err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.GET("/legal/:kind/latest", NewLegalAdminHandler(repos.Legal).Latest)
	for _, tc := range []struct {
		query   string
		status  int
		content string
		version int64
	}{
		{"", 200, "中文", 1},
		{"?lang=en-US", 200, "English updated", 2},
		{"?lang=fr-FR", 404, "", 0},
		{"?lang=../bad", 400, "", 0},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/legal/terms/latest"+tc.query, nil))
		if w.Code != tc.status {
			t.Fatalf("latest %q status %d", tc.query, w.Code)
		}
		if tc.status == 200 {
			var doc domain.LegalDocument
			if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Content != tc.content || doc.Version != tc.version {
				t.Fatalf("latest %q => %+v", tc.query, doc)
			}
		}
	}
}
